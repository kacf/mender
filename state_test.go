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
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mendersoftware/log"
	"github.com/mendersoftware/mender/client"
	"github.com/mendersoftware/mender/datastore"
	"github.com/mendersoftware/mender/installer"
	"github.com/mendersoftware/mender/statescript"
	"github.com/mendersoftware/mender/store"
	"github.com/mendersoftware/mender-artifact/artifact"
	"github.com/mendersoftware/mender-artifact/awriter"
	"github.com/mendersoftware/mender-artifact/handlers"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type stateTestController struct {
	fakeDevice
	updater         fakeUpdater
	artifactName    string
	pollIntvl       time.Duration
	retryIntvl      time.Duration
	state           State
	updateResp      *datastore.UpdateInfo
	updateRespErr   menderError
	authorized      bool
	authorizeErr    menderError
	reportError     menderError
	logSendingError menderError
	reportStatus    string
	reportUpdate    datastore.UpdateInfo
	logUpdate       datastore.UpdateInfo
	logs            []byte
	inventoryErr    error
}

func (s *stateTestController) GetCurrentArtifactName() (string, error) {
	if s.artifactName == "" {
		return "", errors.New("open ..., no such file or directory")
	}
	return s.artifactName, nil
}

func (s *stateTestController) GetUpdatePollInterval() time.Duration {
	return s.pollIntvl
}

func (s *stateTestController) GetInventoryPollInterval() time.Duration {
	return s.pollIntvl
}

func (s *stateTestController) GetRetryPollInterval() time.Duration {
	return s.retryIntvl
}

func (s *stateTestController) CheckUpdate() (*datastore.UpdateInfo, menderError) {
	return s.updateResp, s.updateRespErr
}

func (s *stateTestController) FetchUpdate(url string) (io.ReadCloser, int64, error) {
	return s.updater.FetchUpdate(nil, url)
}

func (s *stateTestController) GetCurrentState() State {
	return s.state
}

func (s *stateTestController) SetNextState(state State) {
	s.state = state
}

func (s *stateTestController) TransitionState(next State, ctx *StateContext) (State, bool) {
	next, cancel := s.state.Handle(ctx, s)
	s.state = next
	return next, cancel
}

func (s *stateTestController) Authorize() menderError {
	return s.authorizeErr
}

func (s *stateTestController) IsAuthorized() bool {
	return s.authorized
}

func (s *stateTestController) ReportUpdateStatus(update *datastore.UpdateInfo, status string) menderError {
	s.reportUpdate = *update
	s.reportStatus = status
	return s.reportError
}

func (s *stateTestController) UploadLog(update *datastore.UpdateInfo, logs []byte) menderError {
	s.logUpdate = *update
	s.logs = logs
	return s.logSendingError
}

func (s *stateTestController) InventoryRefresh() error {
	return s.inventoryErr
}

func (s *stateTestController) CheckScriptsCompatibility() error {
	return nil
}

func (s *stateTestController) ReadArtifactHeaders(from io.ReadCloser) (*installer.Installer, error) {
	installerFactories := installer.UpdateStorerProducers{
		DualRootfs: s.fakeDevice,
	}

	installer, _, err := installer.ReadHeaders(from,
		"vexpress-qemu",
		nil,
		"",
		&installerFactories)
	return installer, err
}

func (s *stateTestController) GetInstallers() []installer.PayloadInstaller {
	return []installer.PayloadInstaller{s.fakeDevice}
}

func (s *stateTestController) RestoreInstallersFromTypeList(payloadTypes []string) error {
	return nil
}

func (s *stateTestController) NewStatusReportWrapper(updateId string,
	stateId datastore.MenderState) *client.StatusReportWrapper {

	return nil
}

func (s *stateTestController) GetScriptExecutor() statescript.Executor {
	return nil
}

type waitStateTest struct {
	baseState
}

func (c *waitStateTest) Wait(next, same State, wait time.Duration) (State, bool) {
	log.Debugf("Fake waiting for %f seconds, going from state %s to state %s",
		wait.Seconds(), same.Id(), next.Id())
	return next, false
}

func (c *waitStateTest) Wake() bool {
	return true // Dummy.
}

func (c *waitStateTest) Stop() {
	// Noop for now.
}

func (c *waitStateTest) Handle(*StateContext, Controller) (State, bool) {
	return c, false
}

func TestStateBase(t *testing.T) {
	bs := baseState{
		id: datastore.MenderStateInit,
	}

	assert.Equal(t, datastore.MenderStateInit, bs.Id())
	assert.False(t, bs.Cancel())
}

func TestStateWait(t *testing.T) {
	cs := NewWaitState(datastore.MenderStateAuthorizeWait, ToNone)

	assert.Equal(t, datastore.MenderStateAuthorizeWait, cs.Id())

	var s State
	var c bool

	// no update
	var tstart, tend time.Time

	tstart = time.Now()
	s, c = cs.Wait(authorizeState, authorizeWaitState, 100*time.Millisecond)
	tend = time.Now()
	// not cancelled should return the 'next' state
	assert.Equal(t, authorizeState, s)
	assert.False(t, c)
	assert.WithinDuration(t, tend, tstart, 105*time.Millisecond)

	// asynchronously cancel state operation
	go func() {
		ch := cs.Cancel()
		assert.True(t, ch)
	}()
	// should finish right away
	tstart = time.Now()
	s, c = cs.Wait(authorizeState, authorizeWaitState, 100*time.Millisecond)
	tend = time.Now()
	// canceled should return the same state
	assert.Equal(t, authorizeWaitState, s)
	assert.True(t, c)
	assert.WithinDuration(t, tend, tstart, 5*time.Millisecond)
	// Force wake from sleep and continue execution.
	s, c = cs.Wait(authorizeState, authorizeWaitState, 10*time.Second)
	go func() {
		assert.False(t, cs.Wake())
	}()
	// Wake should return the next state
	assert.Equal(t, authorizeState, s)
	assert.False(t, c)
}

func TestStateError(t *testing.T) {

	fooerr := NewTransientError(errors.New("foo"))

	es := NewErrorState(fooerr)
	assert.Equal(t, datastore.MenderStateError, es.Id())
	assert.IsType(t, &ErrorState{}, es)
	errstate, _ := es.(*ErrorState)
	assert.NotNil(t, errstate)
	assert.Equal(t, fooerr, errstate.cause)
	s, c := es.Handle(nil, &stateTestController{})
	assert.IsType(t, &IdleState{}, s)
	assert.False(t, c)

	es = NewErrorState(nil)
	errstate, _ = es.(*ErrorState)
	assert.NotNil(t, errstate)
	assert.Contains(t, errstate.cause.Error(), "general error")
	s, c = es.Handle(nil, &stateTestController{})
	assert.IsType(t, &FinalState{}, s)
	assert.False(t, c)
}

func TestStateUpdateError(t *testing.T) {

	update := &datastore.UpdateInfo{
		ID: "foobar",
	}
	fooerr := NewTransientError(errors.New("foo"))

	es := NewUpdateErrorState(fooerr, update)
	assert.Equal(t, datastore.MenderStateUpdateError, es.Id())
	assert.IsType(t, &UpdateErrorState{}, es)
	errstate, _ := es.(*UpdateErrorState)
	assert.NotNil(t, errstate)
	assert.Equal(t, fooerr, errstate.cause)

	ms := store.NewMemStore()
	ctx := StateContext{
		store: ms,
	}
	sc := &stateTestController{}
	es = NewUpdateErrorState(fooerr, update)
	s, _ := es.Handle(&ctx, sc)
	assert.IsType(t, &UpdateStatusReportState{}, s)
	// verify that update status report state data is correct
	usr, _ := s.(*UpdateStatusReportState)
	assert.Equal(t, client.StatusFailure, usr.status)
	assert.Equal(t, *update, *usr.Update())
}

func TestStateUpdateReportStatus(t *testing.T) {
	update := &datastore.UpdateInfo{
		ID: "foobar",
	}

	ms := store.NewMemStore()
	ctx := StateContext{
		store: ms,
	}
	sc := &stateTestController{}

	// create directory for storing deployments logs
	tempDir, _ := ioutil.TempDir("", "logs")
	defer os.RemoveAll(tempDir)

	openLogFileWithContent(path.Join(tempDir, "deployments.0001.foobar.log"),
		`{ "time": "12:12:12", "level": "error", "msg": "log foo" }`)
	DeploymentLogger = NewDeploymentLogManager(tempDir)

	usr := NewUpdateStatusReportState(update, client.StatusFailure)
	usr.Handle(&ctx, sc)
	assert.Equal(t, client.StatusFailure, sc.reportStatus)
	assert.Equal(t, *update, sc.reportUpdate)

	assert.NotEmpty(t, sc.logs)
	assert.JSONEq(t, `{
	  "messages": [
	      {
	          "time": "12:12:12",
	          "level": "error",
	          "msg": "log foo"
	      }
	   ]}`, string(sc.logs))

	sc = &stateTestController{}
	usr = NewUpdateStatusReportState(update, client.StatusSuccess)
	usr.Handle(&ctx, sc)
	assert.Equal(t, client.StatusSuccess, sc.reportStatus)
	assert.Equal(t, *update, sc.reportUpdate)

	// cancelled state should not wipe state data, for this pretend the reporting
	// fails and cancel
	sc = &stateTestController{
		reportError: NewTransientError(errors.New("report failed")),
	}
	usr = NewUpdateStatusReportState(update, client.StatusSuccess)
	s, c := usr.Handle(&ctx, sc)
	// there was an error while reporting status; we should retry
	assert.IsType(t, s, &UpdateStatusReportRetryState{})
	assert.False(t, c)
	sd, err := LoadStateData(ms)
	assert.NoError(t, err)
	assert.Equal(t, *update, sd.UpdateInfo)

	poll := 5 * time.Millisecond
	retry := 1 * time.Millisecond
	// error sending status
	sc = &stateTestController{
		pollIntvl:   poll,
		retryIntvl:  retry,
		reportError: NewTransientError(errors.New("test error sending status")),
	}

	shouldTry := maxSendingAttempts(poll, retry, minReportSendRetries)
	s = NewUpdateStatusReportState(update, client.StatusSuccess)

	now := time.Now()

	for i := 0; i < shouldTry; i++ {
		s, c = s.Handle(&ctx, sc)
		assert.IsType(t, &UpdateStatusReportRetryState{}, s)
		assert.False(t, c)

		s, c = s.Handle(&ctx, sc)
		assert.IsType(t, &UpdateStatusReportState{}, s)
		assert.False(t, c)
	}
	assert.WithinDuration(t, now, time.Now(), time.Duration(int64(shouldTry)*int64(retry))+time.Millisecond*10)

	// next attempt should return an error
	s, c = s.Handle(&ctx, sc)
	assert.IsType(t, &UpdateStatusReportRetryState{}, s)
	s, c = s.Handle(&ctx, sc)
	assert.IsType(t, &ReportErrorState{}, s)
	assert.False(t, c)

	// error sending logs
	sc = &stateTestController{
		pollIntvl:       poll,
		retryIntvl:      retry,
		logSendingError: NewTransientError(errors.New("test error sending logs")),
	}
	s = NewUpdateStatusReportState(update, client.StatusFailure)

	now = time.Now()
	for i := 0; i < shouldTry; i++ {
		s, c = s.Handle(&ctx, sc)
		assert.IsType(t, &UpdateStatusReportRetryState{}, s)
		assert.False(t, c)

		s, c = s.Handle(&ctx, sc)
		assert.IsType(t, &UpdateStatusReportState{}, s)
		assert.True(t, s.(*UpdateStatusReportState).reportSent)
		assert.False(t, c)
	}
	assert.WithinDuration(t, now, time.Now(), time.Duration(int64(shouldTry)*int64(retry))+time.Millisecond*10)

	s, c = s.Handle(&ctx, sc)
	assert.IsType(t, &UpdateStatusReportRetryState{}, s)
	s, c = s.Handle(&ctx, sc)
	assert.IsType(t, s, &ReportErrorState{})
	assert.False(t, c)

	// pretend update was aborted at the backend, but was applied
	// successfully on the device
	usr = NewUpdateStatusReportState(update, client.StatusSuccess)
	sc = &stateTestController{
		reportError: NewFatalError(client.ErrDeploymentAborted),
	}
	s, c = usr.Handle(&ctx, sc)
	assert.IsType(t, &ReportErrorState{}, s)

	// pretend update was aborted at the backend, along with local failure
	usr = NewUpdateStatusReportState(update, client.StatusFailure)
	sc = &stateTestController{
		reportError: NewFatalError(client.ErrDeploymentAborted),
	}
	s, c = usr.Handle(&ctx, sc)
	assert.IsType(t, &ReportErrorState{}, s)
}

func TestStateIdle(t *testing.T) {
	i := IdleState{}

	s, c := i.Handle(&StateContext{}, &stateTestController{
		authorized: false,
	})
	assert.IsType(t, &AuthorizeState{}, s)
	assert.False(t, c)

	s, c = i.Handle(&StateContext{}, &stateTestController{
		authorized: true,
	})
	assert.IsType(t, &CheckWaitState{}, s)
	assert.False(t, c)
}

func TestStateInit(t *testing.T) {
	// create directory for storing deployments logs
	tempDir, _ := ioutil.TempDir("", "logs")
	defer os.RemoveAll(tempDir)
	DeploymentLogger = NewDeploymentLogManager(tempDir)

	i := InitState{}

	var s State
	var c bool

	ms := store.NewMemStore()
	ctx := StateContext{
		store: ms,
	}
	s, c = i.Handle(&ctx, &stateTestController{
		fakeDevice: fakeDevice{
			retHasUpdate: false,
		},
	})
	assert.IsType(t, &IdleState{}, s)
	assert.False(t, c)

	// pretend we have state data
	update := &datastore.UpdateInfo{
		ID: "foobar",
	}
	update.Artifact.ArtifactName = "fakeid"

	StoreStateData(ms, datastore.StateData{
		Name:       datastore.MenderStateReboot,
		UpdateInfo: *update,
	})
	// have state data and have correct artifact name
	s, c = i.Handle(&ctx, &stateTestController{
		artifactName: "fakeid",
		fakeDevice: fakeDevice{
			retHasUpdate: true,
		},
	})
	assert.IsType(t, &UpdateAfterRebootState{}, s)
	uvs := s.(*UpdateAfterRebootState)
	assert.Equal(t, *update, *uvs.Update())
	assert.False(t, c)

	// error restoring state data
	ms.Disable(true)
	s, c = i.Handle(&ctx, &stateTestController{})
	assert.IsType(t, &UpdateErrorState{}, s)
	assert.False(t, c)
	ms.Disable(false)

	// pretend reading invalid state
	StoreStateData(ms, datastore.StateData{
		UpdateInfo: *update,
	})
	s, c = i.Handle(&ctx, &stateTestController{
		fakeDevice: fakeDevice{
			retHasUpdate: false,
		},
	})
	assert.IsType(t, &UpdateErrorState{}, s)
	use, _ := s.(*UpdateErrorState)
	assert.Equal(t, *update, use.update)

	// update-commit-leave behaviour
	StoreStateData(ms, datastore.StateData{
		UpdateInfo: *update,
		Name:       datastore.MenderStateUpdateCommit,
	})
	ctrl := &stateTestController{
		fakeDevice: fakeDevice{
			retHasUpdate: false,
		},
	}
	s, c = i.Handle(&ctx, ctrl)
	assert.IsType(t, &IdleState{}, s)
	assert.False(t, c)
	assert.IsType(t, ToArtifactCommit_Enter, ctrl.GetCurrentState().Transition())

}

func TestStateAuthorize(t *testing.T) {
	a := AuthorizeState{}
	s, c := a.Handle(nil, &stateTestController{})
	assert.IsType(t, &CheckWaitState{}, s)
	assert.False(t, c)

	s, c = a.Handle(nil, &stateTestController{
		authorizeErr: NewTransientError(errors.New("auth fail temp")),
	})
	assert.IsType(t, &AuthorizeWaitState{}, s)
	assert.False(t, c)

	s, c = a.Handle(nil, &stateTestController{
		authorizeErr: NewFatalError(errors.New("auth error")),
	})
	assert.IsType(t, &ErrorState{}, s)
	assert.False(t, c)
}

func TestStateInvetoryUpdate(t *testing.T) {
	ius := inventoryUpdateState
	ctx := new(StateContext)

	s, _ := ius.Handle(ctx, &stateTestController{
		inventoryErr: errors.New("some err"),
	})
	assert.IsType(t, &CheckWaitState{}, s)

	s, _ = ius.Handle(ctx, &stateTestController{})
	assert.IsType(t, &CheckWaitState{}, s)

	// no artifact name should fail
	s, _ = ius.Handle(ctx, &stateTestController{
		inventoryErr: errNoArtifactName,
	})

	assert.IsType(t, &ErrorState{}, s)

}

func TestStateAuthorizeWait(t *testing.T) {
	cws := NewAuthorizeWaitState()

	var s State
	var c bool
	ctx := new(StateContext)

	// no update
	var tstart, tend time.Time

	tstart = time.Now()
	s, c = cws.Handle(ctx, &stateTestController{
		retryIntvl: 100 * time.Millisecond,
	})
	tend = time.Now()
	assert.IsType(t, &AuthorizeState{}, s)
	assert.False(t, c)
	assert.WithinDuration(t, tend, tstart, 105*time.Millisecond)

	// asynchronously cancel state operation
	go func() {
		ch := cws.Cancel()
		assert.True(t, ch)
	}()
	// should finish right away
	tstart = time.Now()
	s, c = cws.Handle(ctx, &stateTestController{
		retryIntvl: 100 * time.Millisecond,
	})
	tend = time.Now()
	// canceled state should return itself
	assert.IsType(t, &AuthorizeWaitState{}, s)
	assert.True(t, c)
	assert.WithinDuration(t, tend, tstart, 5*time.Millisecond)
}

func TestStateUpdateCommit(t *testing.T) {
	// create directory for storing deployments logs
	tempDir, _ := ioutil.TempDir("", "logs")
	defer os.RemoveAll(tempDir)
	DeploymentLogger = NewDeploymentLogManager(tempDir)

	update := &datastore.UpdateInfo{
		ID: "foobar",
	}
	cs := NewUpdateCommitState(update)

	var s State
	var c bool

	ms := store.NewMemStore()
	ctx := StateContext{
		store: ms,
	}
	StoreStateData(ms, datastore.StateData{
		UpdateInfo: *update,
	})
	// commit without errors
	sc := &stateTestController{}
	s, c = cs.Handle(&ctx, sc)
	assert.IsType(t, &UpdateRollbackState{}, s)
	assert.False(t, c)
	usr, _ := s.(*UpdateRollbackState)
	assert.Equal(t, *update, *usr.Update())

	s, c = cs.Handle(&ctx, &stateTestController{
		fakeDevice: fakeDevice{
			retCommit: NewFatalError(errors.New("commit fail")),
		},
	})
	assert.IsType(t, s, &UpdateRollbackState{})
	assert.False(t, c)
	rs, _ := s.(*UpdateRollbackState)
	assert.Equal(t, *update, *rs.Update())
}

func TestStateUpdateCheckWait(t *testing.T) {
	cws := NewCheckWaitState()
	ctx := new(StateContext)

	// no iventory was sent; we should first send inventory
	var tstart, tend time.Time
	tstart = time.Now()
	s, c := cws.Handle(ctx, &stateTestController{
		pollIntvl: 10 * time.Millisecond,
	})

	tend = time.Now()
	assert.IsType(t, &InventoryUpdateState{}, s)
	assert.False(t, c)
	assert.WithinDuration(t, tend, tstart, 15*time.Millisecond)

	// now we have inventory sent; should send update request
	ctx.lastInventoryUpdate = tend
	ctx.lastUpdateCheck = tend
	tstart = time.Now()
	s, c = cws.Handle(ctx, &stateTestController{
		pollIntvl: 10 * time.Millisecond,
	})
	tend = time.Now()
	assert.IsType(t, &UpdateCheckState{}, s)
	assert.False(t, c)
	assert.WithinDuration(t, tend, tstart, 15*time.Millisecond)

	// asynchronously cancel state operation
	go func() {
		ch := cws.Cancel()
		assert.True(t, ch)
	}()
	// should finish right away
	tstart = time.Now()
	s, c = cws.Handle(ctx, &stateTestController{
		pollIntvl: 100 * time.Millisecond,
	})
	tend = time.Now()
	// canceled state should return itself
	assert.IsType(t, &CheckWaitState{}, s)
	assert.True(t, c)
	assert.WithinDuration(t, tend, tstart, 5*time.Millisecond)
}

func TestStateUpdateCheck(t *testing.T) {
	cs := UpdateCheckState{}
	ctx := new(StateContext)

	var s State
	var c bool

	// no update
	s, c = cs.Handle(ctx, &stateTestController{})
	assert.IsType(t, &CheckWaitState{}, s)
	assert.False(t, c)

	// pretend update check failed
	s, c = cs.Handle(ctx, &stateTestController{
		updateRespErr: NewTransientError(errors.New("check failed")),
	})
	assert.IsType(t, &ErrorState{}, s)
	assert.False(t, c)

	// pretend we have an update
	update := &datastore.UpdateInfo{}

	s, c = cs.Handle(ctx, &stateTestController{
		updateResp: update,
	})
	assert.IsType(t, &UpdateFetchState{}, s)
	assert.False(t, c)
	ufs, _ := s.(*UpdateFetchState)
	assert.Equal(t, *update, ufs.update)
}

func TestUpdateCheckSameImage(t *testing.T) {
	cs := UpdateCheckState{}
	ctx := new(StateContext)

	var s State
	var c bool

	// pretend we have an update
	update := &datastore.UpdateInfo{
		ID: "my-id",
	}

	s, c = cs.Handle(ctx, &stateTestController{
		updateResp:    update,
		updateRespErr: NewTransientError(os.ErrExist),
	})
	assert.IsType(t, &UpdateStatusReportState{}, s)
	assert.False(t, c)
	urs, _ := s.(*UpdateStatusReportState)
	assert.Equal(t, *update, *urs.Update())
	assert.Equal(t, client.StatusAlreadyInstalled, urs.status)
}

func TestStateUpdateFetch(t *testing.T) {
	// create directory for storing deployments logs
	tempDir, _ := ioutil.TempDir("", "logs")
	defer os.RemoveAll(tempDir)
	DeploymentLogger = NewDeploymentLogManager(tempDir)

	// pretend we have an update
	update := &datastore.UpdateInfo{
		ID: "foobar",
	}
	cs := NewUpdateFetchState(update)

	ms := store.NewMemStore()
	ctx := StateContext{
		store: ms,
	}

	// can not store state data
	ms.ReadOnly(true)
	s, c := cs.Handle(&ctx, &stateTestController{})
	assert.IsType(t, &UpdateStatusReportState{}, s)
	assert.False(t, c)
	ms.ReadOnly(false)

	data := "test"
	stream := ioutil.NopCloser(bytes.NewBufferString(data))

	sc := &stateTestController{
		updater: fakeUpdater{
			fetchUpdateReturnReadCloser: stream,
			fetchUpdateReturnSize:       int64(len(data)),
		},
	}
	s, c = cs.Handle(&ctx, sc)
	assert.IsType(t, &UpdateStoreState{}, s)
	assert.False(t, c)
	assert.Equal(t, client.StatusDownloading, sc.reportStatus)
	assert.Equal(t, *update, sc.reportUpdate)

	ud, err := LoadStateData(ms)
	assert.NoError(t, err)
	assert.Equal(t, datastore.StateData{
		Version:    datastore.StateDataVersion,
		UpdateInfo: *update,
		Name:       datastore.MenderStateUpdateFetch,
	}, ud)

	uis, _ := s.(*UpdateStoreState)
	assert.Equal(t, stream, uis.imagein)

	ms.ReadOnly(true)
	// pretend writing update state data fails
	sc = &stateTestController{}
	s, c = uis.Handle(&ctx, sc)
	assert.IsType(t, &UpdateStatusReportState{}, s)
	ms.ReadOnly(false)

	// pretend update was aborted
	sc = &stateTestController{
		reportError: NewFatalError(client.ErrDeploymentAborted),
	}
	s, c = uis.Handle(&ctx, sc)
	assert.IsType(t, &UpdateStatusReportState{}, s)
}

func TestStateUpdateFetchRetry(t *testing.T) {
	// pretend we have an update
	update := &datastore.UpdateInfo{
		ID: "foobar",
	}
	cs := NewUpdateFetchState(update)
	ms := store.NewMemStore()
	ctx := StateContext{
		store: ms,
	}
	stc := stateTestController{
		updater: fakeUpdater{
			fetchUpdateReturnError: NewTransientError(errors.New("fetch failed")),
		},
		pollIntvl: 5 * time.Minute,
	}

	// pretend update check failed
	s, c := cs.Handle(&ctx, &stc)
	assert.IsType(t, &FetchStoreRetryState{}, s)
	assert.False(t, c)

	// Test for the twelve expected attempts:
	// (1m*3) + (2m*3) + (4m*3) + (5m*3)
	for i := 0; i < 12; i++ {
		s.(*FetchStoreRetryState).WaitState = &waitStateTest{}

		s, c = s.Handle(&ctx, &stc)
		assert.IsType(t, &UpdateFetchState{}, s)
		assert.False(t, c)

		s, c = s.Handle(&ctx, &stc)
		assert.IsType(t, &FetchStoreRetryState{}, s)
		assert.False(t, c)
	}

	// Final attempt should fail completely.
	s.(*FetchStoreRetryState).WaitState = &waitStateTest{baseState{
		id: datastore.MenderStateCheckWait,
	}}

	s, c = s.Handle(&ctx, &stc)
	assert.IsType(t, &UpdateStatusReportState{}, s)
	assert.False(t, c)
}

func TestStateUpdateStore(t *testing.T) {
	// create directory for storing deployments logs
	tempDir, _ := ioutil.TempDir("", "logs")
	defer os.RemoveAll(tempDir)
	DeploymentLogger = NewDeploymentLogManager(tempDir)

	stream, err := MakeRootfsImageArtifact(3, false)
	require.NoError(t, err)

	update := &datastore.UpdateInfo{
		ID: "foo",
	}
	uis := NewUpdateStoreState(stream, update)

	ms := store.NewMemStore()
	ctx := StateContext{
		store: ms,
	}

	sc := &stateTestController{
		fakeDevice: fakeDevice{
			consumeUpdate: true,
		},
	}

	ms.ReadOnly(true)
	// pretend writing update state data fails
	s, c := uis.Handle(&ctx, sc)
	assert.IsType(t, &UpdateStatusReportState{}, s)
	ms.ReadOnly(false)

	s, c = uis.Handle(&ctx, sc)
	assert.IsType(t, &UpdateInstallState{}, s)
	assert.False(t, c)
	assert.Equal(t, client.StatusDownloading, sc.reportStatus)

	ud, err := LoadStateData(ms)
	assert.NoError(t, err)
	assert.Equal(t, datastore.StateData{
		Version:    datastore.StateDataVersion,
		UpdateInfo: *update,
		Name:       datastore.MenderStateUpdateStore,
	}, ud)

	// pretend update was aborted
	sc = &stateTestController{
		reportError: NewFatalError(client.ErrDeploymentAborted),
	}
	s, c = uis.Handle(&ctx, sc)
	assert.IsType(t, &UpdateStatusReportState{}, s)
}

func TestStateUpdateInstallRetry(t *testing.T) {
	// create directory for storing deployments logs
	tempDir, _ := ioutil.TempDir("", "logs")
	defer os.RemoveAll(tempDir)
	DeploymentLogger = NewDeploymentLogManager(tempDir)

	update := &datastore.UpdateInfo{
		ID: "foo",
	}
	data := "test"
	stream := ioutil.NopCloser(bytes.NewBufferString(data))
	uis := NewUpdateStoreState(stream, update)
	ms := store.NewMemStore()
	ctx := StateContext{
		store: ms,
	}
	stc := stateTestController{
		fakeDevice: fakeDevice{
			retStoreUpdate: NewFatalError(errors.New("install failed")),
		},
		pollIntvl: 5 * time.Minute,
	}

	// pretend update check failed
	s, c := uis.Handle(&ctx, &stc)
	assert.IsType(t, &FetchStoreRetryState{}, s)
	assert.False(t, c)

	// Test for the twelve expected attempts:
	// (1m*3) + (2m*3) + (4m*3) + (5m*3)
	for i := 0; i < 12; i++ {
		s.(*FetchStoreRetryState).WaitState = &waitStateTest{baseState{
			id: datastore.MenderStateCheckWait,
		}}

		s, c = s.Handle(&ctx, &stc)
		assert.IsType(t, &UpdateFetchState{}, s)
		assert.False(t, c)

		s, c = s.Handle(&ctx, &stc)
		assert.IsType(t, &UpdateStoreState{}, s)
		assert.False(t, c)

		// Reset data stream to something that can be closed.
		stream = ioutil.NopCloser(bytes.NewBufferString(data))
		s.(*UpdateStoreState).imagein = stream

		s, c = s.Handle(&ctx, &stc)
		assert.IsType(t, &FetchStoreRetryState{}, s)
		assert.False(t, c)
	}

	// Final attempt should fail completely.
	s.(*FetchStoreRetryState).WaitState = &waitStateTest{baseState{
		id: datastore.MenderStateCheckWait,
	}}

	s, c = s.Handle(&ctx, &stc)
	assert.IsType(t, &UpdateStatusReportState{}, s)
	assert.False(t, c)
}

func TestStateReboot(t *testing.T) {
	update := &datastore.UpdateInfo{
		ID: "foo",
	}
	rs := NewUpdateRebootState(update)

	// create directory for storing deployments logs
	tempDir, _ := ioutil.TempDir("", "logs")
	defer os.RemoveAll(tempDir)
	DeploymentLogger = NewDeploymentLogManager(tempDir)

	ctx := StateContext{}
	s, c := rs.Handle(&ctx, &stateTestController{
		fakeDevice: fakeDevice{
			retReboot: NewFatalError(errors.New("reboot failed")),
		}})
	assert.IsType(t, &UpdateRollbackState{}, s)
	assert.False(t, c)

	sc := &stateTestController{
		fakeDevice: fakeDevice{
			retHasUpdate: true,
		},
	}
	s, c = rs.Handle(&ctx, sc)
	assert.IsType(t, &UpdateAfterRebootState{}, s)
	assert.False(t, c)
	assert.Equal(t, client.StatusRebooting, sc.reportStatus)
	// reboot will be performed regardless of failures to write update state data
	s, c = s.Handle(&ctx, sc)
	assert.IsType(t, &UpdateCommitState{}, s)
	assert.False(t, c)

	// pretend update was aborted
	sc = &stateTestController{
		reportError: NewFatalError(client.ErrDeploymentAborted),
		fakeDevice: fakeDevice{
			retHasUpdate: true,
		},
	}
	s, c = rs.Handle(&ctx, sc)
	assert.IsType(t, &UpdateRollbackState{}, s)
}

func TestStateRollback(t *testing.T) {
	update := &datastore.UpdateInfo{
		ID: "foo",
	}
	rs := NewUpdateRollbackState(update)

	// create directory for storing deployments logs
	tempDir, _ := ioutil.TempDir("", "logs")
	defer os.RemoveAll(tempDir)
	DeploymentLogger = NewDeploymentLogManager(tempDir)

	s, c := rs.Handle(nil, &stateTestController{
		fakeDevice: fakeDevice{
			retRollback: NewFatalError(errors.New("rollback failed")),
		}})
	assert.IsType(t, &ErrorState{}, s)
	assert.False(t, c)

	s, c = rs.Handle(nil, &stateTestController{})
	assert.IsType(t, &UpdateErrorState{}, s)
	assert.False(t, c)
}

func TestStateFinal(t *testing.T) {
	rs := FinalState{}

	assert.Panics(t, func() {
		rs.Handle(nil, &stateTestController{})
	}, "final state Handle() should panic")
}

func TestStateData(t *testing.T) {
	ms := store.NewMemStore()
	sd := datastore.StateData{
		Version: datastore.StateDataVersion,
		Name:    datastore.MenderStateInit,
		UpdateInfo: datastore.UpdateInfo{
			ID: "foobar",
		},
	}
	err := StoreStateData(ms, sd)
	assert.NoError(t, err)
	rsd, err := LoadStateData(ms)
	assert.NoError(t, err)
	assert.Equal(t, sd, rsd)

	// test if data marshalling works fine
	data, err := ms.ReadAll(datastore.StateDataKey)
	assert.NoError(t, err)
	assert.Contains(t, string(data), `"Name":"init"`)

	sd.Version = 999
	err = StoreStateData(ms, sd)
	assert.NoError(t, err)
	rsd, err = LoadStateData(ms)
	assert.Error(t, err)
	assert.Equal(t, datastore.StateData{}, rsd)
	assert.Equal(t, sd.Version, 999)

	ms.Remove(datastore.StateDataKey)
	_, err = LoadStateData(ms)
	assert.Error(t, err)
	assert.True(t, os.IsNotExist(err))
}

func TestStateReportError(t *testing.T) {
	update := &datastore.UpdateInfo{
		ID: "foobar",
	}

	ms := store.NewMemStore()
	ctx := &StateContext{
		store: ms,
	}
	sc := &stateTestController{}

	// update succeeded, but we failed to report the status to the server,
	// rollback happens next
	res := NewReportErrorState(update, client.StatusSuccess)
	s, c := res.Handle(ctx, sc)
	assert.IsType(t, &UpdateRollbackState{}, s)
	assert.False(t, c)

	// store some state data, failing to report status with a failed update
	// will just clean that up and
	StoreStateData(ms, datastore.StateData{
		Name:       datastore.MenderStateReportStatusError,
		UpdateInfo: *update,
	})
	// update failed and we failed to report that status to the server,
	// state data should be removed and we should go back to init
	res = NewReportErrorState(update, client.StatusFailure)
	s, c = res.Handle(ctx, sc)
	assert.IsType(t, &IdleState{}, s)
	assert.False(t, c)

	_, err := LoadStateData(ms)
	assert.Equal(t, err, nil)

	// store some state data, failing to report status with an update that
	// is already installed will also clean it up
	StoreStateData(ms, datastore.StateData{
		Name:       datastore.MenderStateReportStatusError,
		UpdateInfo: *update,
	})
	// update is already installed and we failed to report that status to
	// the server, state data should be removed and we should go back to
	// init
	res = NewReportErrorState(update, client.StatusAlreadyInstalled)
	s, c = res.Handle(ctx, sc)
	assert.IsType(t, &IdleState{}, s)
	assert.False(t, c)

	_, err = LoadStateData(ms)
	assert.Equal(t, err, nil)
}

func TestMaxSendingAttempts(t *testing.T) {
	assert.Equal(t, minReportSendRetries,
		maxSendingAttempts(time.Second, 0*time.Second, minReportSendRetries))
	assert.Equal(t, minReportSendRetries,
		maxSendingAttempts(time.Second, time.Minute, minReportSendRetries))
	assert.Equal(t, 10, maxSendingAttempts(5*time.Second, time.Second, 3))
	assert.Equal(t, minReportSendRetries,
		maxSendingAttempts(time.Second, time.Second, minReportSendRetries))
}

type menderWithCustomUpdater struct {
	*mender
	updater fakeUpdater
	reportWriter io.Writer
	lastReport string
}

func (m *menderWithCustomUpdater) CheckUpdate() (*datastore.UpdateInfo, menderError) {
	update := datastore.UpdateInfo{}
	update.Artifact.CompatibleDevices = []string{"test-device"}
	update.Artifact.ArtifactName = "test-name"
	update.ID = "abcdefg"
	return &update, nil
}

func (m *menderWithCustomUpdater) ReportUpdateStatus(update *datastore.UpdateInfo, status string) menderError {
	// Don't rereport already existing status.
	if status != m.lastReport {
		_, err := m.reportWriter.Write([]byte(fmt.Sprintf("%s\n", status)))
		if err != nil {
			return NewTransientError(err)
		}
		m.lastReport = status
	}
	return nil
}

func (m *menderWithCustomUpdater) FetchUpdate(url string) (io.ReadCloser, int64, error) {
	return m.updater.FetchUpdate(nil, url)
}

func (m *menderWithCustomUpdater) UploadLog(update *datastore.UpdateInfo, logs []byte) menderError {
	return nil
}

type stateTransitionsWithUpdateModulesTestCase struct {
	caseName           string
	stateChain         []State
	artifactStateChain []string
	reportsLog         []string
	errorStates        []string
	errorForever       bool
	spontRebootStates  []string
	spontRebootForever bool
	rollbackDisabled   bool
	rebootDisabled     bool
}

var stateTransitionsWithUpdateModulesTestCases []stateTransitionsWithUpdateModulesTestCase = []stateTransitionsWithUpdateModulesTestCase{

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Normal install, no reboot, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateCommitState{},
			&UpdateAfterFirstCommitState{},
			&UpdateAfterCommitState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactCommit_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"success",
		},
		rollbackDisabled: true,
		rebootDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Normal install, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateAfterFirstCommitState{},
			&UpdateAfterCommitState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactCommit_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"success",
		},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Normal install",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateAfterFirstCommitState{},
			&UpdateAfterCommitState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactCommit_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"success",
		},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in Download state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"Download_Error_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"failure",
		},
		errorStates: []string{"Download"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in Download state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"failure",
		},
		spontRebootStates: []string{"Download"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactInstall state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"ArtifactInstall_Error_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactInstall"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactInstall state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		spontRebootStates: []string{"ArtifactInstall"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactInstall",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"ArtifactInstall_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactInstall"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactInstall",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		spontRebootStates: []string{"ArtifactInstall"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactReboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactReboot"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactReboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateAfterFirstCommitState{},
			&UpdateAfterCommitState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactCommit_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"success",
		},
		spontRebootStates: []string{"ArtifactReboot"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactVerifyReboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactVerifyReboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		spontRebootStates: []string{"ArtifactVerifyReboot"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactRollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot", "ArtifactRollback"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactRollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot"},
		spontRebootStates: []string{"ArtifactRollback"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactRollbackReboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot", "ArtifactRollbackReboot"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactRollbackReboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot"},
		spontRebootStates: []string{"ArtifactRollbackReboot"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactVerifyRollbackReboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot", "ArtifactVerifyRollbackReboot"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactVerifyRollbackReboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot"},
		spontRebootStates: []string{"ArtifactVerifyRollbackReboot"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactFailure",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"ArtifactInstall_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactInstall", "ArtifactFailure"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactFailure",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"ArtifactInstall_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactInstall"},
		spontRebootStates: []string{"ArtifactFailure"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in Cleanup",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot", "Cleanup"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in Cleanup",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot"},
		spontRebootStates: []string{"Cleanup"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactCommit",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactCommit_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactCommit"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactCommit",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		spontRebootStates: []string{"ArtifactCommit"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactCommit, no reboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateCommitState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactCommit_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactCommit"},
		rebootDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactCommit, no reboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateCommitState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		spontRebootStates: []string{"ArtifactCommit"},
		rebootDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in Download_Enter_00 state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&ErrorState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download_Error_00",
		},
		reportsLog: []string{""},
		errorStates: []string{"Download_Enter_00"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in Download_Enter_00 state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
		},
		reportsLog: []string{""},
		spontRebootStates: []string{"Download_Enter_00"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactInstall_Enter_00 state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall_Error_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"failure",
		},
		errorStates: []string{"ArtifactInstall_Enter_00"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactInstall_Enter_00 state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"failure",
		},
		spontRebootStates: []string{"ArtifactInstall_Enter_00"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactInstall_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"failure",
		},
		errorStates: []string{"ArtifactInstall_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactInstall_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"failure",
		},
		spontRebootStates: []string{"ArtifactInstall_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactReboot_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactReboot_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactReboot_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateAfterFirstCommitState{},
			&UpdateAfterCommitState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactCommit_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"success",
		},
		spontRebootStates: []string{"ArtifactReboot_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactRollback_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot", "ArtifactRollback_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactRollback_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot"},
		spontRebootStates: []string{"ArtifactRollback_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactRollbackReboot_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot", "ArtifactRollbackReboot_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactRollbackReboot_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot"},
		spontRebootStates: []string{"ArtifactRollbackReboot_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactFailure_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"ArtifactInstall_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactInstall", "ArtifactFailure_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactFailure_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"ArtifactInstall_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactInstall"},
		spontRebootStates: []string{"ArtifactFailure_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactCommit_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactCommit_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactCommit_Enter_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		spontRebootStates: []string{"ArtifactCommit_Enter_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactCommit_Enter_00, no reboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateCommitState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactCommit_Enter_00"},
		rebootDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactCommit_Enter_00, no reboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateCommitState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		spontRebootStates: []string{"ArtifactCommit_Enter_00"},
		rebootDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in Download_Leave_00 state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"Download_Error_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"failure",
		},
		errorStates: []string{"Download_Leave_00"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in Download_Leave_00 state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"failure",
		},
		spontRebootStates: []string{"Download_Leave_00"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactInstall_Leave_00 state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactInstall_Error_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactInstall_Leave_00"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactInstall_Leave_00 state, no rollback",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		spontRebootStates: []string{"ArtifactInstall_Leave_00"},
		rollbackDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactInstall_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactInstall_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactInstall_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactInstall_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		spontRebootStates: []string{"ArtifactInstall_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactReboot_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactReboot_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactReboot_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		spontRebootStates: []string{"ArtifactReboot_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactRollback_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot", "ArtifactRollback_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactRollback_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot"},
		spontRebootStates: []string{"ArtifactRollback_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactRollbackReboot_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot", "ArtifactRollbackReboot_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactRollbackReboot_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot"},
		spontRebootStates: []string{"ArtifactRollbackReboot_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactFailure_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"ArtifactInstall_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactInstall", "ArtifactFailure_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactFailure_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRollbackState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateErrorState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"ArtifactInstall_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactInstall"},
		spontRebootStates: []string{"ArtifactFailure_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactCommit_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateAfterFirstCommitState{},
			&UpdateAfterCommitState{},
			&UpdateCleanupState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactCommit_Leave_00",
			"ArtifactCommit_Error_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactCommit_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactCommit_Leave_00",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateAfterRebootState{},
			&UpdateCommitState{},
			&UpdateAfterFirstCommitState{},
			&UpdateAfterCommitState{},
			&UpdateCleanupState{},
			&UpdateAfterCommitState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactCommit_Leave_00",
			"ArtifactCommit_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"success",
		},
		spontRebootStates: []string{"ArtifactCommit_Leave_00"},
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Error in ArtifactCommit_Leave_00, no reboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateCommitState{},
			&UpdateAfterFirstCommitState{},
			&UpdateAfterCommitState{},
			&UpdateCleanupState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactCommit_Leave_00",
			"ArtifactCommit_Error_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"failure",
		},
		errorStates: []string{"ArtifactCommit_Leave_00"},
		rebootDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Killed in ArtifactCommit_Leave_00, no reboot",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateCommitState{},
			&UpdateAfterFirstCommitState{},
			&UpdateAfterCommitState{},
			&UpdateCleanupState{},
			&UpdateAfterCommitState{},
			&UpdateCleanupState{},
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactCommit_Enter_00",
			"ArtifactCommit",
			"ArtifactCommit_Leave_00",
			"ArtifactCommit_Leave_00",
			"Cleanup",
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"success",
		},
		spontRebootStates: []string{"ArtifactCommit_Leave_00"},
		rebootDisabled: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Break out of error loop",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateRollbackRebootState{},
			// Truncated after maximum number of state transitions.
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			// Truncated after maximum number of state transitions.
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot", "ArtifactVerifyRollbackReboot"},
		errorForever: true,
	},

	stateTransitionsWithUpdateModulesTestCase{
		caseName: "Break out of spontaneous reboot loop",
		stateChain: []State{
			&UpdateFetchState{},
			&UpdateStoreState{},
			&UpdateAfterStoreState{},
			&UpdateInstallState{},
			&UpdateRebootState{},
			&UpdateVerifyRebootState{},
			&UpdateRollbackState{},
			&UpdateRollbackRebootState{},
			&UpdateVerifyRollbackRebootState{},
			&UpdateAfterRollbackRebootState{},
			&UpdateErrorState{},
			&UpdateErrorState{},
			&UpdateErrorState{},
			&UpdateErrorState{},
			&UpdateErrorState{},
			&UpdateErrorState{},
			&UpdateErrorState{},
			&UpdateErrorState{},
			&UpdateErrorState{},
			// Truncated after maximum number of state transitions.
			&UpdateStatusReportState{},
			&IdleState{},
		},
		artifactStateChain: []string{
			"Download_Enter_00",
			"Download",
			"SupportsRollback",
			"Download_Leave_00",
			"ArtifactInstall_Enter_00",
			"ArtifactInstall",
			"NeedsArtifactReboot",
			"ArtifactInstall_Leave_00",
			"ArtifactReboot_Enter_00",
			"ArtifactReboot",
			"ArtifactVerifyReboot",
			"ArtifactReboot_Error_00",
			"ArtifactRollback_Enter_00",
			"ArtifactRollback",
			"ArtifactRollback_Leave_00",
			"ArtifactRollbackReboot_Enter_00",
			"ArtifactRollbackReboot",
			"ArtifactVerifyRollbackReboot",
			"ArtifactRollbackReboot_Leave_00",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			"ArtifactFailure_Enter_00",
			"ArtifactFailure",
			// Truncated after maximum number of state transitions.
		},
		reportsLog: []string{
			"downloading",
			"installing",
			"rebooting",
			"failure",
		},
		errorStates: []string{"ArtifactVerifyReboot"},
		spontRebootStates: []string{"ArtifactFailure"},
		spontRebootForever: true,
	},

}

// This test runs all state transitions for an update in a sub process,
// including state transitions that involve killing the process (which would
// happen in a spontaneous reboot situation), and tests that the transitions
// work all the way through.
func TestStateTransitionsWithUpdateModules(t *testing.T) {
	env, ok := os.LookupEnv("TestStateTransitionsWithUpdateModules")
	if ok && env == "subProcess" {
		// We are in the subprocess, run actual test case.
		for _, testCase := range stateTransitionsWithUpdateModulesTestCases {
			if os.Getenv("caseName") == testCase.caseName {
				subTestStateTransitionsWithUpdateModules(t,
					&testCase,
					os.Getenv("tmpdir"))
				return
			}
		}
		t.Errorf("Could not find test case \"%s\" in list", os.Getenv("caseName"))
	}

	// Run test in sub command so that we can use kill during the test.
	for _, c := range stateTransitionsWithUpdateModulesTestCases {
		t.Run(c.caseName, func(t *testing.T){
			tmpdir, err := ioutil.TempDir("", "TestStateTransitionsWithUpdateModules")
			require.NoError(t, err)
			defer os.RemoveAll(tmpdir)

			updateModulesSetup(t, &c, tmpdir)

			env := []string{}
			env = append(env, "TestStateTransitionsWithUpdateModules=subProcess")
			env = append(env, fmt.Sprintf("caseName=%s", c.caseName))
			env = append(env, fmt.Sprintf("tmpdir=%s", tmpdir))

			killCount := 0
			for {
				// TODO: Remove
				// {
				// 	output, _ := exec.Command("/home/kristian/code/mender-artifact/mender-artifact", "read", path.Join(tmpdir, "artifact.mender")).CombinedOutput()
				// 	t.Error(string(output))
				// }

				cmd := exec.Command(os.Args[0], os.Args[1:]...)
				cmd.Env = env
				output, err := cmd.CombinedOutput()
				t.Log(string(output))
				if err == nil {
					break
				}

				waitStatus := cmd.ProcessState.Sys().(syscall.WaitStatus)
				if waitStatus.Signal() == syscall.SIGKILL &&
					(killCount < len(c.spontRebootStates) || c.spontRebootForever) {

					t.Log("Killed as expected")
					killCount++
					continue
				}

				t.Fatal(err.Error())
			}

			logContent := make([]byte, 10000)

			log, err := os.Open(path.Join(tmpdir, "states.log"))
			require.NoError(t, err)
			n, err := log.Read(logContent)
			require.NoError(t, err)
			assert.True(t, n > 0)
			logList := strings.Split(string(bytes.TrimRight(logContent[:n], "\n")), "\n")
			assert.Equal(t, c.artifactStateChain, logList)
			log.Close()

			log, err = os.Open(path.Join(tmpdir, "reports.log"))
			require.NoError(t, err)
			n, err = log.Read(logContent)
			if err == io.EOF {
				require.Equal(t, 0, n)
			} else {
				require.NoError(t, err)
				assert.True(t, n > 0)
			}
			logList = strings.Split(string(bytes.TrimRight(logContent[:n], "\n")), "\n")
			assert.Equal(t, c.reportsLog, logList)
			log.Close()
		})
	}
}


// This entire function is executed in a sub process, so we can freely mess with
// the client state without cleaning up, and even kill it.
func subTestStateTransitionsWithUpdateModules(t *testing.T,
	c *stateTransitionsWithUpdateModulesTestCase,
	tmpdir string) {

	ctx, mender := subProcessSetup(t, tmpdir)

	var state State
	var stateIndex int

	// Since we may be killed and restarted, read where we were in the state
	// indexes.
	indexText, err := ctx.store.ReadAll("test_stateIndex")
	if err != nil {
		// Shortcut into update check state: a new update.
		ucs := *updateCheckState
		state = &ucs
	} else {
		// Start in init state, which should resume the correct state
		// after a kill/reboot.
		init := *initState
		state = &init
		stateIndex64, err := strconv.ParseInt(string(indexText), 0, 0)
		require.NoError(t, err)
		stateIndex = int(stateIndex64)
		t.Logf("Resuming from state index %d (%T)",
			stateIndex, c.stateChain[stateIndex])
	}

	// IMPORTANT: Do not use "assert.Whatever()", but only
	// "require.Whatever()" in this function. The reason is that we may get
	// killed, and then the status from asserts is lost.
	for _, expectedState := range c.stateChain[stateIndex:] {
		// Store next state index we will enter
		indexText = []byte(fmt.Sprintf("%d", stateIndex))
		require.NoError(t, ctx.store.WriteAll("test_stateIndex", indexText))

		// Now do state transition, which may kill us (part of testing
		// spontaneous reboot)
		var cancelled bool
		state, cancelled = transitionState(state, ctx, mender)
		require.False(t, cancelled)
		require.IsTypef(t, expectedState, state, "state index %d", stateIndex)

		stateIndex++
	}
}

func makeTestUpdateModule(t *testing.T, path, logPath string,
	c *stateTransitionsWithUpdateModulesTestCase) {

	fd, err := os.OpenFile(path, os.O_CREATE | os.O_TRUNC | os.O_WRONLY, 0755)
	require.NoError(t, err)
	defer fd.Close()

	fd.Write([]byte(fmt.Sprintf(`#!/bin/bash
echo "$1" >> %s
`, logPath)))

	fd.Write([]byte("if [ \"$1\" = \"SupportsRollback\" ]; then\n"))
	if c.rollbackDisabled {
		fd.Write([]byte("echo No\n"))
	} else {
		fd.Write([]byte("echo Yes\n"))
	}
	fd.Write([]byte("fi\n"))

	fd.Write([]byte("if [ \"$1\" = \"NeedsArtifactReboot\" ]; then\n"))
	if c.rebootDisabled {
		fd.Write([]byte("echo No\n"))
	} else {
		fd.Write([]byte("echo Yes\n"))
	}
	fd.Write([]byte("fi\n"))

	// Kill parent (mender) in specified state
	for _, state := range c.spontRebootStates {
		s := fmt.Sprintf("if [ \"$1\" = \"%s\" ]; then\n", state)
		fd.Write([]byte(s))

		// Prevent spontaneous rebooting forever.
		if !c.spontRebootForever {
			fd.Write([]byte("if [ ! -e \"$2/tmp/$1.already-killed\" ]; then\n"))
			fd.Write([]byte("touch \"$2/tmp/$1.already-killed\"\n"))
		}

		fd.Write([]byte("kill -9 $PPID\n"))

		if !c.spontRebootForever {
			fd.Write([]byte("fi\n"))
		}

		fd.Write([]byte("fi\n"))
	}

	// Produce error in specified state
	for _, state := range c.errorStates {
		s := fmt.Sprintf("if [ \"$1\" = \"%s\" ]; then\n", state)
		fd.Write([]byte(s))

		// Prevent returning same error forever.
		if !c.errorForever {
			fd.Write([]byte("if [ ! -e \"$2/tmp/$1.already-errored\" ]; then\n"))
			fd.Write([]byte("touch \"$2/tmp/$1.already-errored\"\n"))
		}

		fd.Write([]byte("exit 1\n"))

		if !c.errorForever {
			fd.Write([]byte("fi\n"))
		}

		fd.Write([]byte("fi\n"))
	}

	fd.Write([]byte("exit 0\n"))
}

func stateInList(state string, list []string) bool {
	for _, s := range list {
		if s == state {
			return true
		}
	}
	return false
}

func makeTestArtifactScripts(t *testing.T,
	c *stateTransitionsWithUpdateModulesTestCase,
	tmpdir, logPath string) artifact.Scripts {

	var artScripts artifact.Scripts

	stateScriptList := []string{
		"Download",
		"ArtifactInstall",
		"ArtifactReboot",
		"ArtifactCommit",
		"ArtifactRollback",
		"ArtifactRollbackReboot",
		"ArtifactFailure",
	}

	scriptsDir := path.Join(tmpdir, "scriptdir")
	require.NoError(t, os.MkdirAll(scriptsDir, 0755))
	fd, err := os.OpenFile(path.Join(scriptsDir, "version"),
		os.O_WRONLY | os.O_CREATE | os.O_TRUNC, 0644)
	require.NoError(t, err)
	fd.Write([]byte("3"))
	fd.Close()

	for _, state := range stateScriptList {
		for _, enterLeave := range []string{"Enter", "Leave", "Error"} {
			scriptFile := fmt.Sprintf("%s_%s_00", state, enterLeave)
			scriptPath := path.Join(scriptsDir, scriptFile)
			if state != "Download" {
				require.NoError(t, artScripts.Add(scriptPath))
			}

			fd, err := os.OpenFile(scriptPath, os.O_WRONLY | os.O_CREATE | os.O_TRUNC, 0755)
			require.NoError(t, err)
			defer fd.Close()

			fd.Write([]byte("#!/bin/bash\n"))
			fd.Write([]byte(fmt.Sprintf("echo %s >> %s\n",
				scriptFile, logPath)))

			if stateInList(scriptFile, c.errorStates) {
				if !c.errorForever {
					fd.Write([]byte(fmt.Sprintf("if [ ! -e \"%s/%s.already-errored\" ]; then\n",
						tmpdir, scriptFile)))
					fd.Write([]byte(fmt.Sprintf("touch \"%s/%s.already-errored\"\n",
						tmpdir, scriptFile)))
				}
				fd.Write([]byte("exit 1\n"))
				if !c.errorForever {
					fd.Write([]byte("fi\n"))
				}
			}

			if stateInList(scriptFile, c.spontRebootStates) {
				if !c.spontRebootForever {
					fd.Write([]byte(fmt.Sprintf("if [ ! -e \"%s/%s.already-killed\" ]; then\n",
						tmpdir, scriptFile)))
					fd.Write([]byte(fmt.Sprintf("touch \"%s/%s.already-killed\"\n",
						tmpdir, scriptFile)))
				}
				fd.Write([]byte("kill -9 $PPID\n"))
				if !c.spontRebootForever {
					fd.Write([]byte("fi\n"))
				}
			}

			fd.Write([]byte("exit 0\n"))
		}
	}

	return artScripts
}

func updateModulesSetup(t *testing.T,
	c *stateTransitionsWithUpdateModulesTestCase,
	tmpdir string) {

	logPath := path.Join(tmpdir, "states.log")

	require.NoError(t, os.MkdirAll(path.Join(tmpdir, "var/lib/mender"), 0755))
	require.NoError(t, os.MkdirAll(path.Join(tmpdir, "etc/mender"), 0755))

	scripts := makeTestArtifactScripts(t, c, tmpdir, logPath)

	artPath := path.Join(tmpdir, "artifact.mender")
	require.NoError(t, makeImageForUpdateModules(artPath, scripts))

	require.NoError(t, os.Mkdir(path.Join(tmpdir, "logs"), 0755))

	require.NoError(t, os.Mkdir(path.Join(tmpdir, "db"), 0755))

	modulesPath := path.Join(tmpdir, "modules")
	require.NoError(t, os.MkdirAll(modulesPath, 0755))
	makeTestUpdateModule(t, path.Join(modulesPath, "test-type"), logPath, c)

	deviceTypeFile := path.Join(tmpdir, "device_type")
	deviceTypeFd, err := os.OpenFile(deviceTypeFile, os.O_CREATE | os.O_TRUNC | os.O_WRONLY, 0644)
	require.NoError(t, err)
	defer deviceTypeFd.Close()
	deviceTypeFd.Write([]byte("device_type=test-device\n"))

	artifactInfoFd, err := os.OpenFile(path.Join(tmpdir, "artifact_info"),
		os.O_CREATE | os.O_TRUNC | os.O_WRONLY, 0644)
	require.NoError(t, err)
	defer artifactInfoFd.Close()
	artifactInfoFd.Write([]byte("artifact_name=old_name\n"))
}

func subProcessSetup(t *testing.T,
	tmpdir string) (*StateContext, *menderWithCustomUpdater) {

	store.LmdbNoSync = true
	store := store.NewDBStore(path.Join(tmpdir, "db"))

	ctx := StateContext{
		store: store,
	}
	menderPieces := MenderPieces{
		store: store,
	}

	config := menderConfig{
		menderConfigFromFile: menderConfigFromFile{
			Servers: []client.MenderServer{
				client.MenderServer{
					ServerURL: "https://not-used",
				},
			},
		},
		ModulesPath:     path.Join(tmpdir, "modules"),
		ModulesWorkPath: path.Join(tmpdir, "work"),
	}

	DeploymentLogger = NewDeploymentLogManager(path.Join(tmpdir, "logs"))

	reports, err := os.OpenFile(path.Join(tmpdir, "reports.log"),
		os.O_WRONLY | os.O_CREATE | os.O_APPEND, 0644)
	require.NoError(t, err)

	defaultArtScriptsPath = path.Join(tmpdir, "scripts")
	defaultRootfsScriptsPath = path.Join(tmpdir, "scriptdir")

	mender, err := NewMender(config, menderPieces)
	require.NoError(t, err)
	controller := menderWithCustomUpdater{
		mender: mender,
		reportWriter: reports,
	}

	controller.artifactInfoFile = path.Join(tmpdir, "artifact_info")
	controller.deviceTypeFile = path.Join(tmpdir, "device_type")
	controller.stateScriptPath = path.Join(tmpdir, "scripts")

	artPath := path.Join(tmpdir, "artifact.mender")
	updateStream, err := os.Open(artPath)
	controller.updater.fetchUpdateReturnReadCloser = updateStream

	// Avoid waiting by setting a short retry time.
	client.ExponentialBackoffSmallestUnit = time.Millisecond

	return &ctx, &controller
}

func makeImageForUpdateModules(path string, scripts artifact.Scripts) error {
	depends := artifact.ArtifactDepends{
		CompatibleDevices: []string{"test-device"},
	}
	provides := artifact.ArtifactProvides{
		ArtifactName: "artifact-name",
	}
	typeInfoV3 := artifact.TypeInfoV3{
		Type:             "test-module",
	}
	upd := awriter.Updates{
		Updates: []handlers.Composer{handlers.NewModuleImage("test-type")},
	}
	args := awriter.WriteArtifactArgs{
		Format:            "mender",
		Version:           3,
		Devices:           []string{"test-device"},
		Name:              "test-name",
		Updates:           &upd,
		Scripts:           &scripts,
		Depends:           &depends,
		Provides:          &provides,
		TypeInfoV3:        &typeInfoV3,
		MetaData:          nil,
		AugmentTypeInfoV3: nil,
		AugmentMetaData:   nil,
	}

	fd, err := os.OpenFile(path, os.O_CREATE | os.O_WRONLY | os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer fd.Close()
	writer := awriter.NewWriter(fd)

	return writer.WriteArtifact(&args)
}
