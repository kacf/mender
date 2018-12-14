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
	// "io"
	// "os"

	"github.com/mendersoftware/mender-artifact/handlers"
)

type ModuleInstaller struct {
	handlers.ModuleImage

	modulesPath     string
	modulesWorkPath string
}

func NewModuleInstaller(updateType string) *ModuleInstaller {
	module := &ModuleInstaller{
		ModuleImage: *handlers.NewModuleImage(updateType),
	}
	return module
}

// func (mod *ModuleInstaller) Install(r io.Reader, info *os.FileInfo) error {
// }
