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
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/errors"
	"github.com/mendersoftware/log"
	"github.com/mendersoftware/mender-artifact/artifact"
	"github.com/mendersoftware/mender-artifact/handlers"
)

type ModuleInstaller struct {
	payloadIndex    int
	modulesPath     string
	modulesWorkPath string
	updateType      string
	programPath     string
	artifactInfo    ArtifactInfoGetter
	deviceInfo      DeviceInfoGetter

	storeUpdateCmd *exec.Cmd
}

func (mod *ModuleInstaller) callModule(state string, capture bool) (string, error) {
	log.Infof("Calling module: %s %s", mod.programPath, state)
	cmd := exec.Command(mod.programPath, state)
	cmd.Stderr = cmd.Stdout
	outputPipe, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	err = cmd.Start()
	if err != nil {
		log.Errorf("Could not execute update module: %s", err.Error())
		return "", err
	}

	log.Errorln("TODO, FIX THIS INTERVAL")
	killer := time.AfterFunc(5 * time.Second, func(){
		cmd.Process.Kill()
	})
	defer killer.Stop()
	hardKiller := time.AfterFunc(2 * 5 * time.Second, func(){
		cmd.Process.Signal(os.Kill)
	})
	defer hardKiller.Stop()

	waitChannel := make(chan error)
	go func() {
		waitChannel <- cmd.Wait()
	}()
	waitTimer := time.NewTimer(3 * 5 * time.Second)
	defer waitTimer.Stop()

	var output string
	lineReader := bufio.NewReader(outputPipe)
	for true {
		line, err := lineReader.ReadString(byte('\n'))
		if err != nil && err != io.EOF {
			log.Errorf("Reading from update module yielded error: %s", err.Error())
			return output, err
		}
		line = strings.TrimRight(line, "\n")

		if capture {
			log.Debugf("Update module output: %s", line)
			output = output + line
		} else {
			log.Infof("Update module output: %s", line)
		}

		if err == io.EOF {
			break
		}
	}

	select {
	case err := <-waitChannel:
		if err != nil {
			log.Errorf("Update module terminated abnormally: %s", err.Error())
			return output, err
		}

	case <-waitTimer.C:
		msg := "Unable to kill process"
		log.Errorln(msg)
		return output, errors.New(msg)
	}

	return output, nil
}

func (mod *ModuleInstaller) payloadPath() string {
	index := fmt.Sprintf("%04d", mod.payloadIndex)
	return path.Join(mod.modulesWorkPath, "payloads", index, "tree")
}

type fileNameAndContent struct {
	name    string
	content string
}

func (mod *ModuleInstaller) buildStreamsTree(artifactHeaders,
	artifactAugmentedHeaders artifact.HeaderInfoer,
	payloadHeaders handlers.ArtifactUpdateHeaders) error {

	workPath := mod.payloadPath()
	err := os.RemoveAll(workPath)
	if err != nil {
		return err
	}
	for _, dir := range []string{"header", "tmp"} {
		err = os.MkdirAll(path.Join(workPath, dir), 0700)
		if err != nil {
			return err
		}
	}

	currName, err := mod.artifactInfo.GetCurrentArtifactName()
	if err != nil {
		return err
	}

	currGroup, err := mod.artifactInfo.GetCurrentArtifactGroup()
	if err != nil {
		return err
	}

	deviceType, err := mod.deviceInfo.GetDeviceType()
	if err != nil {
		return err
	}

	provides := artifactHeaders.GetArtifactProvides()

	headerInfoJson, err := json.Marshal(artifactHeaders)
	if err != nil {
		return err
	}

	typeInfo, err := json.Marshal(payloadHeaders.GetUpdateOriginalTypeInfoWriter())
	if err != nil {
		return err
	}

	metaData, err := json.Marshal(payloadHeaders.GetUpdateOriginalMetaData())
	if err != nil {
		return err
	}

	filesAndContent := []fileNameAndContent{
		fileNameAndContent{
			"version",
			fmt.Sprintf("%d", payloadHeaders.GetVersion()),
		},
		fileNameAndContent{
			"current_artifact_name",
			currName,
		},
		fileNameAndContent{
			"current_artifact_group",
			currGroup,
		},
		fileNameAndContent{
			"current_device_type",
			deviceType,
		},
		fileNameAndContent{
			path.Join("header", "artifact_group"),
			provides.ArtifactGroup,
		},
		fileNameAndContent{
			path.Join("header", "artifact_name"),
			provides.ArtifactName,
		},
		fileNameAndContent{
			path.Join("header", "payload_type"),
			mod.updateType,
		},
		fileNameAndContent{
			path.Join("header", "header-info"),
			string(headerInfoJson),
		},
		fileNameAndContent{
			path.Join("header", "type-info"),
			string(typeInfo),
		},
		fileNameAndContent{
			path.Join("header", "meta-data"),
			string(metaData),
		},
	}

	for _, entry := range filesAndContent {
		fd, err := os.OpenFile(path.Join(workPath, entry.name),
			os.O_WRONLY | os.O_TRUNC | os.O_CREATE, 0600)
		if err != nil {
			return err
		}
		n, err := fd.Write([]byte(entry.content))
		if err != nil {
			return err
		}
		if n != len(entry.content) {
			return errors.New("Write returned short")
		}
	}

	// Make sure everything is synced to disk in case we need to pick up
	// from where we left after a spontaneous reboot.
	syscall.Sync()

	return nil
}

func (mod *ModuleInstaller) PrepareStoreUpdate(artifactHeaders,
	artifactAugmentedHeaders artifact.HeaderInfoer,
	payloadHeaders handlers.ArtifactUpdateHeaders) error {

	log.Debug("Executing ModuleInstaller.PrepareStoreUpdate")

	if mod.storeUpdateCmd != nil {
		return errors.New("Internal error: PrepareStoreUpdate() called when store cmd is already active")
	}

	if artifactAugmentedHeaders != nil {
		msg := "Augmented artifacts are not supported yet"
		log.Error(msg)
		return errors.New(msg)
	}

	err := mod.buildStreamsTree(artifactHeaders, artifactAugmentedHeaders, payloadHeaders)

	mod.storeUpdateCmd = exec.Command(mod.programPath, "Download")
	err = mod.storeUpdateCmd.Start()
	if err != nil {
		mod.storeUpdateCmd.Process.Kill()
		log.Errorf("Module could not be executed: %s", err.Error())
		return errors.Wrap(err, "Module could not be executed")
	}

	return nil
}

func (mod *ModuleInstaller) StoreUpdate(r io.Reader, info os.FileInfo) error {
	log.Debug("Executing ModuleInstaller.StoreUpdate")

	if mod.storeUpdateCmd == nil {
		return errors.New("Internal error: StoreUpdate() called when store cmd is inactive")
	}

	io.Copy(ioutil.Discard, r)
	_, err := mod.callModule("Download", false)
	return err
}

func (mod *ModuleInstaller) FinishStoreUpdate() error {
	log.Debug("Executing ModuleInstaller.FinishStoreUpdate")

	if mod.storeUpdateCmd == nil {
		return errors.New("Internal error: FinishStoreUpdate() called when store cmd is inactive")
	}

	return nil
}

func (mod *ModuleInstaller) InstallUpdate() error {
	log.Debug("Executing ModuleInstaller.InstallUpdate")
	_, err := mod.callModule("ArtifactInstall", false)
	return err
}

func (mod *ModuleInstaller) NeedsReboot() (bool, error) {
	log.Debug("Executing ModuleInstaller.NeedsReboot")
	output, err := mod.callModule("NeedsArtifactReboot", true)
	if err != nil {
		return false, err
	} else if output == "" || output == "No" {
		return false, nil
	} else if output == "Yes" {
		return true, nil
	} else {
		return false, fmt.Errorf("Unexpected reply from update module NeedsArtifactReboot query: %s",
			output)
	}
}

func (mod *ModuleInstaller) Reboot() error {
	log.Debug("Executing ModuleInstaller.Reboot")
	return nil
}

func (mod *ModuleInstaller) SupportsRollback() (bool, error) {
	log.Debug("Executing ModuleInstaller.SupportsRollback")
	output, err := mod.callModule("SupportsRollback", true)
	if err != nil {
		return false, err
	} else if output == "" || output == "No" {
		return false, nil
	} else if output == "Yes" {
		return true, nil
	} else {
		return false, fmt.Errorf("Unexpected reply from update module SupportsRollback query: %s",
			output)
	}
}
func (mod *ModuleInstaller) RollbackReboot() error {
	log.Debug("Executing ModuleInstaller.RollbackReboot")
	_, err := mod.callModule("ArtifactRollbackReboot", false)
	return err
}

func (mod *ModuleInstaller) CommitUpdate() error {
	log.Debug("Executing ModuleInstaller.CommitUpdate")
	_, err := mod.callModule("ArtifactCommit", false)
	return err
}

func (mod *ModuleInstaller) Rollback() error {
	log.Debug("Executing ModuleInstaller.Rollback")
	_, err := mod.callModule("ArtifactRollback", false)
	return err
}

func (mod *ModuleInstaller) VerifyReboot() error {
	log.Debug("Executing ModuleInstaller.VerifyReboot")
	_, err := mod.callModule("ArtifactVerifyReboot", false)
	return err
}

func (mod *ModuleInstaller) VerifyRollbackReboot() error {
	log.Debug("Executing ModuleInstaller.VerifyRollbackReboot")
	_, err := mod.callModule("ArtifactVerifyRollbackReboot", false)
	return err
}

func (mod *ModuleInstaller) Failure() error {
	log.Debug("Executing ModuleInstaller.Failure")
	_, err := mod.callModule("ArtifactFailure", false)
	return err
}

func (mod *ModuleInstaller) Cleanup() error {
	log.Debug("Executing ModuleInstaller.Cleanup")
	_, err := mod.callModule("Cleanup", false)
	return err
}

type ModuleInstallerFactory struct {
	modulesPath     string
	modulesWorkPath string
	artifactInfo    ArtifactInfoGetter
	deviceInfo      DeviceInfoGetter
}

func NewModuleInstallerFactory(modulesPath, modulesWorkPath string,
	artifactInfo ArtifactInfoGetter, deviceInfo DeviceInfoGetter) *ModuleInstallerFactory {

	return &ModuleInstallerFactory{
		modulesPath: modulesPath,
		modulesWorkPath: modulesWorkPath,
		artifactInfo: artifactInfo,
		deviceInfo: deviceInfo,
	}
}

func (mf *ModuleInstallerFactory) NewUpdateStorer(updateType string, payloadNum int) (handlers.UpdateStorer, error) {
	if payloadNum < 0 || payloadNum > 9999 {
		return nil, fmt.Errorf("Payload index out of range 0-9999: %d", payloadNum)
	}

	mod := &ModuleInstaller{
		payloadIndex:     payloadNum,
		modulesPath:      mf.modulesPath,
		modulesWorkPath:  mf.modulesWorkPath,
		updateType:       updateType,
		programPath:      path.Join(mf.modulesPath, updateType),
		artifactInfo:     mf.artifactInfo,
		deviceInfo:       mf.deviceInfo,
	}
	return mod, nil
}

func (mf *ModuleInstallerFactory) GetModuleTypes() []string {
	fileList, err := ioutil.ReadDir(mf.modulesPath)
	if err != nil {
		log.Infof("Update Module path \"%s\" could not be opened (%s). Update modules will not available",
			mf.modulesPath, err.Error())
		return []string{}
	}

	moduleList := make([]string, 0, len(fileList))
	for _, file := range fileList {
		if file.IsDir() {
			log.Errorf("Update module %s is a directory",
				path.Join(mf.modulesPath, file.Name()))
			continue
		}
		if (file.Mode() & 0111) == 0 {
			log.Errorf("Update module %s is not executable",
				path.Join(mf.modulesPath, file.Name()))
			continue
		}

		moduleList = append(moduleList, file.Name())
	}

	return moduleList
}
