// Copyright 2018 Northern.tech AS
//
//    Licensed under the Apache License, Version 2.0 (the "License");
//    you may not use this file except in compliance with the License.
//    You may obtain a copy of the License at
//
//        http://www.apache.org/licenses/LICENSE-2.0
//
//    Unless required by applicable law or agreed to in writing, software
//    distributed under the License is distributed on an "AS IS" BASIS,
//    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
//    See the License for the specific language governing permissions and
//    limitations under the License.

package installer

import (
	"io"
	"io/ioutil"
	"os"

	"github.com/mendersoftware/log"
	"github.com/mendersoftware/mender-artifact/areader"
	"github.com/mendersoftware/mender-artifact/artifact"
	"github.com/mendersoftware/mender-artifact/handlers"
	"github.com/mendersoftware/mender/statescript"
	"github.com/pkg/errors"
)

type UInstaller interface {
	StoreUpdate(io.ReadCloser, int64) error
	InstallUpdate() error
}

type Rebooter interface {
	Reboot() error
}

type UInstallCommitRebooter interface {
	UInstaller
	Rebooter
	CommitUpdate() error
	Rollback() error
	// Verify that rebooting into the new update worked.
	VerifyReboot() error
	// Verify that rebooting into the old update worked.
	VerifyRollbackReboot() error
}

type ArtifactPayloadInstaller interface {
	UInstallCommitRebooter
	NewInstance(bucketNum int) (ArtifactPayloadInstaller, error)
}

func Install(art io.ReadCloser, dt string, key []byte,
	scrDir, modulesDir, modulesWorkDir string,
	dualRootfsDevice UInstaller, acceptStateScripts bool) error {

	var ar *areader.Reader
	// if there is a verification key artifact must be signed
	if key != nil {
		ar = areader.NewReaderSigned(art)
	} else {
		ar = areader.NewReader(art)
		log.Info("no public key was provided for authenticating the artifact")
	}

	// Important for the client to forbid artifacts types we don't know.
	ar.ForbidUnknownHandlers = true

	if err := registerHandlers(ar, dualRootfsDevice); err != nil {
		return err
	}

	ar.CompatibleDevicesCallback = func(devices []string) error {
		log.Debugf("checking if device [%s] is on compatibile device list: %v\n",
			dt, devices)
		if dt == "" {
			log.Errorf("Unknown device_type. Continuing with update")
			return nil
		}
		for _, dev := range devices {
			if dev == dt {
				return nil
			}
		}
		return errors.Errorf("installer: image (device types %v) not compatible with device %v",
			devices, dt)
	}

	// VerifySignatureCallback needs to be registered both for
	// NewReader and NewReaderSigned to print a warning if artifact is signed
	// but no verification key is provided.
	ar.VerifySignatureCallback = func(message, sig []byte) error {
		// MEN-1196 skip verification of the signature if there is no key
		// provided. This means signed artifact will be installed on all
		// devices having no key specified.
		if key == nil {
			log.Warn("installer: installing signed artifact without verification " +
				"as verification key is missing")
			return nil
		}

		// Do the verification only if the key is provided.
		s := artifact.NewVerifier(key)
		err := s.Verify(message, sig)
		if err == nil {
			// MEN-2152 Provide confirmation in log that digital signature was authenticated.
			log.Info("installer: authenticated digital signature of artifact")
		}
		return err
	}

	scr := statescript.NewStore(scrDir)
	// we need to wipe out the scripts directory first
	if err := scr.Clear(); err != nil {
		log.Errorf("installer: error initializing directory for scripts [%s]: %v",
			scrDir, err)
		return errors.Wrap(err, "installer: error initializing directory for scripts")
	}

	if acceptStateScripts {
		// All the scripts that are part of the artifact will be processed here.
		ar.ScriptsReadCallback = func(r io.Reader, fi os.FileInfo) error {
			log.Debugf("installer: processing script: %s", fi.Name())
			return scr.StoreScript(r, fi.Name())
		}
	} else {
		ar.ScriptsReadCallback = func(r io.Reader, fi os.FileInfo) error {
			errMsg := "will not install artifact with state-scripts when installing from cmd-line. Use -f to override"
			return errors.New(errMsg)
		}
	}

	// read the artifact
	if err := ar.ReadArtifact(); err != nil {
		return errors.Wrap(err, "installer: failed to read and install update")
	}

	if err := scr.Finalize(ar.GetInfo().Version); err != nil {
		return errors.Wrap(err, "installer: error finalizing writing scripts")
	}

	log.Debugf(
		"installer: successfully read artifact [name: %v; version: %v; compatible devices: %v]",
		ar.GetArtifactName(), ar.GetInfo().Version, ar.GetCompatibleDevices())

	return nil
}

func registerHandlers(ar *areader.Reader, dualRootfsDevice UInstaller) error {
	// Built-in rootfs handler.
	rootfs := handlers.NewRootfsInstaller()
	rootfs.SetInstallHandler(func(r io.Reader, info os.FileInfo) error {
		if dualRootfsDevice == nil {
			return errors.New("No dual rootfs configuration present: Cannot install update")
		}
		log.Debugf("installing update %v of size %v", info.Name(), info.Size())
		err := dualRootfsDevice.StoreUpdate(ioutil.NopCloser(r), info.Size())
		if err != nil {
			log.Errorf("update image installation failed: %v", err)
			return err
		}
		return nil
	})

	if err := ar.RegisterHandler(rootfs); err != nil {
		return errors.Wrap(err, "failed to register rootfs install handler")
	}

	// Update modules.
	updateTypes := []string{"test-type"}
	for _, updateType := range updateTypes {
		moduleImage := handlers.NewModuleImage(updateType)
		moduleImage.SetInstallHandler(NewModuleInstaller())
		if err := ar.RegisterHandler(moduleImage); err != nil {
			return errors.Wrapf(err, "failed to register '%s' install handler",
				updateType)
		}
	}

	return nil
}
