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

package app

import (
	"github.com/godbus/dbus/v5"
)

// Some usage examples using command line:
//
// Monitor signals from client:
// dbus-monitor --system type=signal,sender=com.mender.MenderClient
//
// Execute various commands:
// dbus-send --system --print-reply --dest=com.mender.MenderClient /com/mender/MenderClient com.mender.Deployments.CheckForDeployment
// dbus-send --system --print-reply --dest=com.mender.MenderClient /com/mender/MenderClient com.mender.Deployments.WaitAtArtifactReboot boolean:true
// dbus-send --system --print-reply --dest=com.mender.MenderClient /com/mender/MenderClient com.mender.Deployments.ResumeArtifactReboot
// dbus-send --system --print-reply --dest=com.mender.MenderClient /com/mender/MenderClient com.mender.Inventory.UpdateInventory

var DbusConn *dbus.Conn
var WaitWithReboot bool
var GoForReboot chan bool

type MenderDbusDeploymentsInterface struct {
	d *MenderDaemon
}

func (m *MenderDbusDeploymentsInterface) CheckForDeployment() *dbus.Error {
	m.d.ForceToState <- States.UpdateCheck
	m.d.Sctx.WakeupChan <- true
	return nil
}

func (m *MenderDbusDeploymentsInterface) WaitAtArtifactReboot(wait bool) *dbus.Error {
	GoForReboot = make(chan bool)
	WaitWithReboot = wait
	return nil
}

func (m *MenderDbusDeploymentsInterface) ResumeArtifactReboot() *dbus.Error {
	WaitWithReboot = false
	GoForReboot <- true
	return nil
}

type MenderDbusInventoryInterface struct {
	d *MenderDaemon
}

func (m *MenderDbusInventoryInterface) UpdateInventory() *dbus.Error {
	m.d.ForceToState <- States.InventoryUpdate
	m.d.Sctx.WakeupChan <- true
	return nil
}
