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
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"github.com/pkg/errors"
	"github.com/mendersoftware/log"
	"github.com/mendersoftware/mender-artifact/handlers"
)

type ModuleInstaller struct {
	payloadIndex    int
	modulesPath     string
	modulesWorkPath string
	updateType      string
	workDir         string
	programPath     string
}

func (mod *ModuleInstaller) callModule(state string, capture bool) (string, error) {
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

func (mod *ModuleInstaller) StoreUpdate(r io.Reader, info os.FileInfo) error {
	return nil
}

func (mod *ModuleInstaller) InstallUpdate() error {
	_, err := mod.callModule("ArtifactInstall", false)
	return err
}

func (mod *ModuleInstaller) Reboot() error {
	return nil
}

func (mod *ModuleInstaller) CommitUpdate() error {
	return nil
}

func (mod *ModuleInstaller) Rollback() error {
	return nil
}

func (mod *ModuleInstaller) VerifyReboot() error {
	return nil
}

func (mod *ModuleInstaller) VerifyRollbackReboot() error {
	return nil
}

func (mod *ModuleInstaller) Failure() error {
	return nil
}

func (mod *ModuleInstaller) Cleanup() error {
	return nil
}

type ModuleInstallerFactory struct {
	modulesPath     string
	modulesWorkPath string
}

func NewModuleInstallerFactory(modulesPath, modulesWorkPath string) *ModuleInstallerFactory {
	return &ModuleInstallerFactory{
		modulesPath: modulesPath,
		modulesWorkPath: modulesWorkPath,
	}
}

func (mf *ModuleInstallerFactory) NewUpdateStorer(updateType string, payloadNum int) (handlers.UpdateStorer, error) {
	numStr := fmt.Sprintf("%04d", payloadNum)
	mod := &ModuleInstaller{
		payloadIndex:     payloadNum,
		modulesPath:      mf.modulesPath,
		modulesWorkPath:  mf.modulesWorkPath,
		workDir:          path.Join(mf.modulesWorkPath, numStr),
		updateType:       updateType,
		programPath:      path.Join(mf.modulesPath, updateType),
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
