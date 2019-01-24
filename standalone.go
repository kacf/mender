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
package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"strings"

	"github.com/mendersoftware/log"
	"github.com/mendersoftware/mender/client"
	"github.com/mendersoftware/mender/installer"
	"github.com/mendersoftware/mender/statescript"
	"github.com/mendersoftware/mender/utils"
	"github.com/mendersoftware/mender-artifact/handlers"
	"github.com/pkg/errors"
)

// This will be run manually from command line ONLY
func doStandaloneInstall(dualRootfsDevice handlers.UpdateStorerProducer, args runOptionsType, dt string,
	vKey []byte, config *menderConfig, stateExec statescript.Executor) error {
	var image io.ReadCloser
	var imageSize int64
	var err error
	var upclient client.Updater

	if args == (runOptionsType{}) {
		return errors.New("rootfs called without needed parameters")
	}

	log.Debug("Starting device update.")

	updateLocation := *args.imageFile
	if strings.HasPrefix(updateLocation, "http:") ||
		strings.HasPrefix(updateLocation, "https:") {
		log.Infof("Performing remote update from: [%s].", updateLocation)

		var ac *client.ApiClient
		// we are having remote update
		ac, err = client.New(args.Config)
		if err != nil {
			return errors.New("Can not initialize client for performing network update.")
		}
		upclient = client.NewUpdate()

		log.Debug("Client initialized. Start downloading image.")

		image, imageSize, err = upclient.FetchUpdate(ac, updateLocation, 0)
		log.Debugf("Image downloaded: %d [%v] [%v]", imageSize, image, err)
	} else {
		// perform update from local file
		log.Infof("Start updating from local image file: [%s]", updateLocation)
		image, imageSize, err = FetchUpdateFromFile(updateLocation)

		log.Debugf("Fetching update from file results: [%v], %d, %v", image, imageSize, err)
	}

	if image == nil || err != nil {
		return errors.Wrapf(err, "rootfs: error while updating image from command line")
	}
	defer image.Close()

	fmt.Fprintf(os.Stdout, "Installing update from the artifact of size %d\n", imageSize)
	p := &utils.ProgressWriter{
		Out: os.Stdout,
		N:   imageSize,
	}
	tr := io.TeeReader(image, p)

	installerFactories := installer.UpdateStorerProducers{
		DualRootfs: dualRootfsDevice,
		// Modules:    installer.NewModuleInstallerFactory(config.ModulesPath,
		// 	config.ModulesWorkPath, config),
	}

	return doStandaloneInstallStates(ioutil.NopCloser(tr), dt, vKey, &installerFactories, stateExec)
}

func doStandaloneInstallStates(art io.ReadCloser, dt string, key []byte,
	installerFactories *installer.UpdateStorerProducers, stateExec statescript.Executor) error {

	// Download state
	err := stateExec.ExecuteAll("Download", "Enter", false, nil)
	if err != nil {
		log.Errorf("Download_Enter script failed: %s", err.Error())
		// No doStandaloneInstallFailure here, since we have not done anything yet.
		return err
	}
	installer, installers, err := installer.ReadHeaders(art, dt, key, "", installerFactories)
	if err != nil {
		log.Errorf("Reading headers failed: %s", err.Error())
		doStandaloneInstallFailure(installers, stateExec, true)
		return err
	}
	err = installer.StorePayloads()
	if err != nil {
		log.Errorf("Download failed: %s", err.Error())
		doStandaloneInstallFailure(installers, stateExec, true)
		return err
	}
	err = stateExec.ExecuteAll("Download", "Leave", false, nil)
	if err != nil {
		log.Errorf("Download_Leave script failed: %s", err.Error())
		doStandaloneInstallFailure(installers, stateExec, true)
		return err
	}

	// ArtifactInstall state
	err = stateExec.ExecuteAll("ArtifactInstall", "Enter", false, nil)
	if err != nil {
		log.Errorf("ArtifactInstall_Enter script failed: %s", err.Error())
		doStandaloneInstallFailure(installers, stateExec, false)
		return err
	}
	for _, inst := range installers {
		err = inst.InstallUpdate()
		if err != nil {
			log.Errorf("Installation failed: %s", err.Error())
			doStandaloneInstallFailure(installers, stateExec, false)
			return err
		}
	}
	err = stateExec.ExecuteAll("ArtifactInstall", "Leave", false, nil)
	if err != nil {
		log.Errorf("ArtifactInstall_Leave script failed: %s", err.Error())
		doStandaloneInstallFailure(installers, stateExec, false)
		return err
	}

	return nil
}

func doStandaloneInstallFailure(installers []installer.PayloadInstaller, stateExec statescript.Executor, onlyCleanup bool) {
	var err error

	if !onlyCleanup {
		err = stateExec.ExecuteAll("ArtifactFailure", "Enter", true, nil)
		if err != nil {
			log.Errorf("Error when executing ArtifactFailure_Enter scripts: %s", err.Error())
		}
		for _, inst := range installers {
			err = inst.Failure()
			if err != nil {
				log.Errorf("Error when executing ArtifactFailure state: %s", err. Error())
			}
		}
		err = stateExec.ExecuteAll("ArtifactFailure", "Leave", true, nil)
		if err != nil {
			log.Errorf("Error when executing ArtifactFailure_Leave scripts: %s", err.Error())
		}
	}

	for _, inst := range installers {
		err = inst.Cleanup()
		if err != nil {
			log.Errorf("Error when executing Cleanup state: %s", err. Error())
		}
	}
}

// FetchUpdateFromFile returns a byte stream of the given file, size of the file
// and an error if one occurred.
func FetchUpdateFromFile(file string) (io.ReadCloser, int64, error) {
	fd, err := os.Open(file)
	if err != nil {
		return nil, 0, fmt.Errorf("Not able to open image file: %s: %s\n",
			file, err.Error())
	}

	imageInfo, err := fd.Stat()
	if err != nil {
		return nil, 0, fmt.Errorf("Unable to stat() file: %s: %s\n", file, err.Error())
	}

	return fd, imageInfo.Size(), nil
}
