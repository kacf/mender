// Copyright 2019 Northern.tech AS
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
	"os"

	"github.com/mendersoftware/log"
	"github.com/mendersoftware/mender-artifact/areader"
	"github.com/mendersoftware/mender-artifact/artifact"
	"github.com/mendersoftware/mender-artifact/handlers"
	"github.com/mendersoftware/mender/statescript"
	"github.com/pkg/errors"
)

type Rebooter interface {
	Reboot() error
}

type PayloadInstaller interface {
	Rebooter
	handlers.UpdateStorer
	InstallUpdate() error
	NeedsReboot() (bool, error)
	CommitUpdate() error
	SupportsRollback() (bool, error)
	Rollback() error
	// Verify that rebooting into the new update worked.
	VerifyReboot() error
	RollbackReboot() error
	// Verify that rebooting into the old update worked.
	VerifyRollbackReboot() error
	Failure() error
	Cleanup() error

	GetType() string
}

type UpdateStorerProducers struct {
	DualRootfs handlers.UpdateStorerProducer
	Modules    *ModuleInstallerFactory
}

type ArtifactInfoGetter interface {
	GetCurrentArtifactName() (string, error)
	GetCurrentArtifactGroup() (string, error)
}

type DeviceInfoGetter interface {
	GetDeviceType() (string, error)
}

type Installer struct {
	ar *areader.Reader
}

func ReadHeaders(art io.ReadCloser, dt string, key []byte, scrDir string,
	inst *UpdateStorerProducers) (*Installer, []PayloadInstaller, error) {

	var ar *areader.Reader
	var installers []PayloadInstaller
	var err error

	// if there is a verification key artifact must be signed
	if key != nil {
		ar = areader.NewReaderSigned(art)
	} else {
		ar = areader.NewReader(art)
		log.Info("no public key was provided for authenticating the artifact")
	}

	// Important for the client to forbid artifacts types we don't know.
	ar.ForbidUnknownHandlers = true

	if err = registerHandlers(ar, inst); err != nil {
		return nil, installers, err
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
	if err = scr.Clear(); err != nil {
		log.Errorf("installer: error initializing directory for scripts [%s]: %v",
			scrDir, err)
		return nil, installers, errors.Wrap(err, "installer: error initializing directory for scripts")
	}

	// All the scripts that are part of the artifact will be processed here.
	ar.ScriptsReadCallback = func(r io.Reader, fi os.FileInfo) error {
		log.Debugf("installer: processing script: %s", fi.Name())
		return scr.StoreScript(r, fi.Name())
	}

	// read the artifact
	if err = ar.ReadArtifactHeaders(); err != nil {
		return nil, installers, errors.Wrap(err, "installer: failed to read Artifact")
	}

	if err = scr.Finalize(ar.GetInfo().Version); err != nil {
		return nil, installers, errors.Wrap(err, "installer: error finalizing writing scripts")
	}

	updateStorers, err := ar.GetUpdateStorers()
	if err != nil {
		return nil, installers, err
	}
	installers, err = getInstallerList(updateStorers)
	if err != nil {
		return nil, installers, err
	}

	log.Debugf(
		"installer: successfully read artifact [name: %v; version: %v; compatible devices: %v]",
		ar.GetArtifactName(), ar.GetInfo().Version, ar.GetCompatibleDevices())

	return &Installer{ar}, installers, nil
}

func (i *Installer) StorePayloads() error {
	return i.ar.ReadArtifactData()
}

func registerHandlers(ar *areader.Reader, inst *UpdateStorerProducers) error {

	// Built-in rootfs handler.
	rootfs := handlers.NewRootfsInstaller()
	rootfs.SetUpdateStorerProducer(inst.DualRootfs)
	if err := ar.RegisterHandler(rootfs); err != nil {
		return errors.Wrap(err, "failed to register rootfs install handler")
	}

	// Update modules.
	updateTypes := inst.Modules.GetModuleTypes()
	for _, updateType := range updateTypes {
		if updateType == "rootfs-image" {
			log.Errorf("Found update module called %s, which "+
				"cannot be overriden. Ignoring.", updateType)
			continue
		}
		moduleImage := handlers.NewModuleImage(updateType)
		moduleImage.SetUpdateStorerProducer(inst.Modules)
		if err := ar.RegisterHandler(moduleImage); err != nil {
			return errors.Wrapf(err, "failed to register '%s' install handler",
				updateType)
		}
	}

	return nil
}

func getInstallerList(updateStorers []handlers.UpdateStorer) ([]PayloadInstaller, error) {
	empty := []PayloadInstaller{}

	list := make([]PayloadInstaller, len(updateStorers))
	for i, us := range updateStorers {
		installer, ok := us.(PayloadInstaller)
		if !ok {
			// If you got this error unexpectedly after working on
			// some code, check if your installer still implements
			// PayloadInstaller.
			return empty, errors.New("Artifact reader returned an unknown installer type")
		}
		list[i] = installer
	}

	return list, nil
}

func CreateInstallersFromList(inst *UpdateStorerProducers,
	desiredTypes []string) ([]PayloadInstaller, error) {

	payloadStorers := make([]handlers.UpdateStorer, len(desiredTypes))
	typesFromDisk := inst.Modules.GetModuleTypes()

	for n, desired := range desiredTypes {
		var err error
		if desired == "rootfs-image" {
			payloadStorers[n], err = inst.DualRootfs.NewUpdateStorer(desired, n)
			if err != nil {
				return nil, err
			}
			continue
		}

		found := false
		for _, fromDisk := range typesFromDisk {
			if fromDisk == desired {
				found = true
				break
			}
		}
		if !found {
			log.Errorf("Update module %s not found when assembling list of "+
				"update modules. Recovery may fail.", desired)
		}
		// Even if we don't find the update module. Construct it
		// unconditionally. It will fail all over the place, but at
		// least we won't get nil pointers, and it allows other existing
		// modules to run.
		payloadStorers[n], err = inst.Modules.NewUpdateStorer(desired, n)
		if err != nil {
			return nil, err
		}
	}

	return getInstallerList(payloadStorers)
}
