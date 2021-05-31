// Copyright 2020 Northern.tech AS
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

package system

import (
	"os"
	"os/exec"
	"time"

	"github.com/pkg/errors"
)

type SystemRebootCmd struct {
	command Commander
}

func NewSystemRebootCmd(command Commander) *SystemRebootCmd {
	return &SystemRebootCmd{
		command: command,
	}
}

func (s *SystemRebootCmd) Reboot() error {
	s.command.Command("reboot").Run()

	// Wait up to ten minutes for reboot to kill the client, otherwise the
	// client may mistake a successful return code as "reboot is complete,
	// continue". *Any* return from this function is an error.
	time.Sleep(10 * time.Minute)
	return errors.New("System did not reboot, even though 'reboot' call succeeded.")
}

type Commander interface {
	Command(name string, arg ...string) *exec.Cmd
}

type StatCommander interface {
	Stat(string) (os.FileInfo, error)
	Commander
}

// we need real OS implementation
type OsCalls struct {
}

func (OsCalls) Command(name string, arg ...string) *exec.Cmd {
	return exec.Command(name, arg...)
}

func (OsCalls) Stat(name string) (os.FileInfo, error) {
	return os.Stat(name)
}
