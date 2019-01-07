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

	"github.com/mendersoftware/mender-artifact/handlers"
)

type ModuleInstaller struct {
	payloadIndex    int
	modulesPath     string
	modulesWorkPath string
}

func (mod *ModuleInstaller) StoreUpdate(r io.Reader, info os.FileInfo) error {
	return nil
}

func (mod *ModuleInstaller) InstallUpdate() error {
	return nil
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

type ModuleInstallerFactory struct {
	modulesPath     string
	modulesWorkPath string
	installers      []*ModuleInstaller
}

func NewModuleInstallerFactory(modulesPath, modulesWorkPath string) *ModuleInstallerFactory {
	return &ModuleInstallerFactory{
		modulesPath: modulesPath,
		modulesWorkPath: modulesWorkPath,
	}
}

func (mf *ModuleInstallerFactory) NewUpdateStorer(payloadNum int) (handlers.UpdateStorer, error) {
	mod := &ModuleInstaller{
		payloadIndex:     payloadNum,
		modulesPath:      mf.modulesPath,
		modulesWorkPath:  mf.modulesWorkPath,
	}
	mf.installers = append(mf.installers, mod)
	return mod, nil
}

func (mf *ModuleInstallerFactory) GetProducedInstallers() []*ModuleInstaller {
	return mf.installers
}

func (mf *ModuleInstallerFactory) ClearProducedInstallers() {
	mf.installers = []*ModuleInstaller{}
}
