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
	"github.com/mendersoftware/mender/datastore"
	"github.com/mendersoftware/mender/store"
	"github.com/mendersoftware/mender/system"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"

	"github.com/godbus/dbus/v5"
)

// Config section

type MenderDaemon struct {
	Mender       Controller
	Sctx         StateContext
	Store        store.Store
	ForceToState chan State
	stop         bool
}

func NewDaemon(mender Controller, store store.Store) *MenderDaemon {

	daemon := MenderDaemon{
		Mender: mender,
		Sctx: StateContext{
			Store:      store,
			Rebooter:   system.NewSystemRebootCmd(system.OsCalls{}),
			WakeupChan: make(chan bool, 1),
		},
		Store:        store,
		ForceToState: make(chan State, 1),
	}
	return &daemon
}

func (d *MenderDaemon) StopDaemon() {
	d.stop = true
}

func (d *MenderDaemon) Cleanup() {
	if d.Store != nil {
		if err := d.Store.Close(); err != nil {
			log.Errorf("Failed to close data store: %v", err)
		}
		d.Store = nil
	}
}

func (d *MenderDaemon) shouldStop() bool {
	return d.stop
}

func (d *MenderDaemon) Run() error {
	var err error
	DbusConn, err = dbus.SystemBus()
	if err != nil {
		return errors.Wrap(err, "Could not connect to system D-Bus")
	}

	rnr, err := DbusConn.RequestName("com.mender.MenderClient", 0)
	if err != nil {
		return errors.Wrap(err, "Could not request D-Bus name")
	} else if rnr != dbus.RequestNameReplyPrimaryOwner {
		return errors.Errorf("Could not request D-Bus name: %d", rnr)
	}

	err = DbusConn.Export(&MenderDbusDeploymentsInterface{d}, "/com/mender/MenderClient", "com.mender.Deployments")
	if err != nil {
		return errors.Wrap(err, "Could not export to system D-Bus")
	}

	err = DbusConn.Export(&MenderDbusInventoryInterface{d}, "/com/mender/MenderClient", "com.mender.Inventory")
	if err != nil {
		return errors.Wrap(err, "Could not export to system D-Bus")
	}

	// set the first state transition
	var toState State = d.Mender.GetCurrentState()
	cancelled := false
	for {
		// If signal SIGUSR1 or SIGUSR2 is received, force the state-machine to the correct state.
		select {
		case nState := <-d.ForceToState:
			switch toState.(type) {
			case *idleState,
				*checkWaitState,
				*updateCheckState,
				*inventoryUpdateState:
				log.Infof("Forcing state machine to: %s", nState)
				toState = nState
			default:
				log.Errorf("Cannot check update or update inventory while in %s state", toState)
			}

		default:
			// Identity op - do nothing.
		}
		toState, cancelled = d.Mender.TransitionState(toState, &d.Sctx)
		if toState.Id() == datastore.MenderStateError {
			es, ok := toState.(*errorState)
			if ok {
				if es.IsFatal() {
					return es.cause
				}
			} else {
				return errors.New("failed")
			}
		}
		if cancelled || toState.Id() == datastore.MenderStateDone {
			break
		}
		if d.shouldStop() {
			return nil
		}
	}
	return nil
}
