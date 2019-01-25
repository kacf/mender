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
package datastore

import (
	"fmt"
	"encoding/json"

	"github.com/pkg/errors"
)

////////////////////////////////////////////////////////////////////////////////
// All the structs related to state data which is saved in the database.
////////////////////////////////////////////////////////////////////////////////

// StateData is state information that can be used for restoring state from storage
type StateData struct {
	// version is providing information about the format of the data
	Version int
	// number representing the id of the last state to execute
	Name MenderState
	// update info and reponse data for the update that was in progress
	UpdateInfo UpdateInfo
}

// current version of the format of StateData;
// incerease the version number once the format of StateData is changed
const StateDataVersion = 2

type MenderState int

const (
	// initial state
	MenderStateInit MenderState = iota
	// idle state; waiting for transition to the new state
	MenderStateIdle
	// client is bootstrapped, i.e. ready to go
	MenderStateAuthorize
	// wait before authorization attempt
	MenderStateAuthorizeWait
	// inventory update
	MenderStateInventoryUpdate
	// wait for new update or inventory sending
	MenderStateCheckWait
	// check update
	MenderStateUpdateCheck
	// update fetch
	MenderStateUpdateFetch
	// update store
	MenderStateUpdateStore
	// install update
	MenderStateUpdateInstall
	// wait before retrying fetch & install after first failing (timeout,
	// for example)
	MenderStateFetchStoreRetryWait
	// varify update
	MenderStateUpdateVerify
	// commit needed
	MenderStateUpdateCommit
	// commit is finished
	MenderStateUpdateAfterCommit
	// status report
	MenderStateUpdateStatusReport
	// wait before retrying sending either report or deployment logs
	MenderStatusReportRetryState
	// error reporting status
	MenderStateReportStatusError
	// reboot
	MenderStateReboot
	// first state after booting device after rollback reboot
	MenderStateVerifyReboot
	// state which runs the ArtifactReboot_Leave scripts
	MenderStateAfterReboot
	// rollback
	MenderStateRollback
	// reboot after rollback
	MenderStateRollbackReboot
	// first state after booting device after rollback reboot
	MenderStateVerifyRollbackReboot
	// state which runs ArtifactRollbackReboot_Leave scripts
	MenderStateAfterRollbackReboot
	// error
	MenderStateError
	// update error
	MenderStateUpdateError
	// cleanup state
	MenderStateUpdateCleanup
	// exit state
	MenderStateDone
)

var (
	stateNames = map[MenderState]string{
		MenderStateInit:                 "init",
		MenderStateIdle:                 "idle",
		MenderStateAuthorize:            "authorize",
		MenderStateAuthorizeWait:        "authorize-wait",
		MenderStateInventoryUpdate:      "inventory-update",
		MenderStateCheckWait:            "check-wait",
		MenderStateUpdateCheck:          "update-check",
		MenderStateUpdateFetch:          "update-fetch",
		MenderStateUpdateStore:          "update-store",
		MenderStateUpdateInstall:        "update-install",
		MenderStateFetchStoreRetryWait:  "fetch-install-retry-wait",
		MenderStateUpdateVerify:         "update-verify",
		MenderStateUpdateCommit:         "update-commit",
		MenderStateUpdateAfterCommit:    "update-after-commit",
		MenderStateUpdateStatusReport:   "update-status-report",
		MenderStatusReportRetryState:    "update-retry-report",
		MenderStateReportStatusError:    "status-report-error",
		MenderStateReboot:               "reboot",
		MenderStateVerifyReboot:         "verify-reboot",
		MenderStateAfterReboot:          "after-reboot",
		MenderStateRollback:             "rollback",
		MenderStateRollbackReboot:       "rollback-reboot",
		MenderStateVerifyRollbackReboot: "verify-rollback-reboot",
		MenderStateAfterRollbackReboot:  "after-rollback-reboot",
		MenderStateError:                "error",
		MenderStateUpdateError:          "update-error",
		MenderStateUpdateCleanup:        "cleanup",
		MenderStateDone:                 "finished",
	}
)

func (m MenderState) MarshalJSON() ([]byte, error) {
	n, ok := stateNames[m]
	if !ok {
		return nil, fmt.Errorf("marshal error; unknown state %v", m)
	}
	return json.Marshal(n)
}

func (m MenderState) String() string {
	return stateNames[m]
}

func (m *MenderState) UnmarshalJSON(data []byte) error {
	var s string
	err := json.Unmarshal(data, &s)
	if err != nil {
		return err
	}
	for k, v := range stateNames {
		if v == s {
			*m = k
			return nil
		}
	}
	return fmt.Errorf("unmarshal error; unknown state %s", s)
}

type SupportsRollbackType int
const (
	RollbackSupportUnknown = iota
	RollbackNotSupported
	RollbackSupported
)

func (s *SupportsRollbackType) Set(value SupportsRollbackType) error {
	if *s == RollbackSupportUnknown {
		*s = value
	} else if *s != value {
		return errors.New("Conflicting rollback support. All payloads must agree on rollback support")
	}
	return nil
}

// Info about the update in progress.
type UpdateInfo struct {
	Artifact struct {
		Source struct {
			URI    string
			Expire string
		}
		CompatibleDevices []string `json:"device_types_compatible"`
		ArtifactName      string   `json:"artifact_name"`
		PayloadTypes      []string
	}
	ID string
	// Whether the currently running update asked for reboot
	RebootRequested bool
	// Whether the currently running update supports rollback.
	SupportsRollback SupportsRollbackType
	// How many times this update's state has been stored. This is roughly,
	// but not exactly, equivalent to the number of state transitions, and
	// is used to break out of loops.
	StateDataStoreCount int
}

func (ur *UpdateInfo) CompatibleDevices() []string {
	return ur.Artifact.CompatibleDevices
}

func (ur *UpdateInfo) ArtifactName() string {
	return ur.Artifact.ArtifactName
}

func (ur *UpdateInfo) URI() string {
	return ur.Artifact.Source.URI
}
