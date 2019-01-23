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
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"runtime"
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

	downloader      *moduleDownload
	processKiller   *delayKiller
}

type delayKiller struct {
	proc       *os.Process
	killer     *time.Timer
	hardKiller *time.Timer
}

// kill9After is time after killAfter as expired, not total time.
func newDelayKiller(proc *os.Process, killAfter, kill9After time.Duration) *delayKiller {
	k := &delayKiller{
		proc: proc,
	}
	k.killer = time.AfterFunc(killAfter, func(){
		log.Errorf("Process %d timed out. Sending SIGTERM", k.proc.Pid)
		k.proc.Kill()
	})
	k.hardKiller = time.AfterFunc(killAfter + kill9After, func(){
		log.Errorf("Process %d timed out. Sending SIGKILL", k.proc.Pid)
		k.proc.Signal(os.Kill)
	})
	return k
}

func (k *delayKiller) Stop() {
	k.killer.Stop()
	k.hardKiller.Stop()
}

func (mod *ModuleInstaller) callModule(state string, capture bool) (string, error) {
	log.Infof("Calling module: %s %s", mod.programPath, state)
	cmd := exec.Command(mod.programPath, state)
	cmd.Dir = mod.payloadPath()
	outputPipe, err := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err != nil {
		return "", err
	}
	err = cmd.Start()
	if err != nil {
		log.Errorf("Could not execute update module: %s", err.Error())
		return "", err
	}

	log.Errorln("TODO, FIX THIS INTERVAL")
	killer := newDelayKiller(cmd.Process, 5 * time.Second, 2 * 5 * time.Second)
	defer killer.Stop()

	output, err := mod.readAndLog(outputPipe, capture)
	if err != nil {
		return output, err
	}

	err = cmd.Wait()
	if err != nil {
		log.Errorf("Update module returned error: %s", err.Error())
	}
	return output, err
}

func (mod *ModuleInstaller) readAndLog(r io.ReadCloser, capture bool) (string, error) {
	var output string
	lineReader := bufio.NewReader(r)
	defer r.Close()

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
	for _, dir := range []string{"header", "tmp", "streams"} {
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

	headerInfoJson, err := json.MarshalIndent(artifactHeaders, "", "  ")
	if err != nil {
		return err
	}

	typeInfo, err := json.MarshalIndent(payloadHeaders.GetUpdateOriginalTypeInfoWriter(), "", "  ")
	if err != nil {
		return err
	}

	metaData, err := json.MarshalIndent(payloadHeaders.GetUpdateOriginalMetaData(), "", "  ")
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

	// Create FIFO for next stream, but don't write anything to it yet.
	err = syscall.Mkfifo(path.Join(workPath, "stream-next"), 0600)
	if err != nil {
		return err
	}

	// Make sure everything is synced to disk in case we need to pick up
	// from where we left after a spontaneous reboot.
	syscall.Sync()

	return nil
}

type oneStream struct {
	r         io.Reader
	name      string
	openFlags int
	status    chan error
}

func newOneStream(r io.Reader, name string, openFlags int) *oneStream {
	return &oneStream{
		r: r,
		name: name,
		openFlags: openFlags,
	}
}

func (s *oneStream) startStream() {
	s.status = make(chan error)
	runtime.SetFinalizer(s, func(s *oneStream){
		s.cancelStream()
	})
	// Use function arguments so that garbage collector can destroy outer
	// object, and invoke our finalizer.
	go func(r io.Reader, name string, openFlags int, status chan error) {
		defer close(status)

		fd, err := os.OpenFile(name, openFlags, 0600)
		if err != nil {
			status <- errors.Wrapf(err, "Unable to open %s", name)
			return
		}
		defer fd.Close()

		_, err = io.Copy(fd, r)
		if err != nil {
			status <- errors.Wrapf(err, "Unable to stream into %s", name)
			return
		}

		status <- nil
	}(s.r, s.name, s.openFlags, s.status)
}

func (s *oneStream) cancelStream() {
	// Open and immediately close the pipe to shake loose the download
	// process. We use the non-blocking flag so that we ourselves do not get
	// stuck.

	for {
		select {
		case _, _ = <-s.status:
			// Go routine has returned, or channel is closed.
			return
		default:
			cancel, err := os.OpenFile(s.name, os.O_RDONLY | syscall.O_NONBLOCK, 0600)
			if err == nil {
				cancel.Close()
			}
			// Yield so that other routine finishes quickly.
			runtime.Gosched()
		}
	}
}

func (s *oneStream) streamStatusChannel() chan error {
	return s.status
}

const (
	unknownDownloader = 0
	moduleDownloader = 1
	menderDownloader = 2
)

type readerAndNamePair struct {
	r    io.Reader
	name string
}

type moduleDownload struct {
	payloadPath string
	proc        *exec.Cmd

	// Channel for supplying new payload files while the download loop is
	// running
	nextArtifactStream chan *readerAndNamePair

	// Status return to calling function
	status        chan error

	finishChannel chan bool

	////////////////////////////////////////////////////////////////////////
	// Status variables for mail loop.
	////////////////////////////////////////////////////////////////////////

	// Status channel for the module process
	cmdErr         chan error

	// Used to keep track of whether we are letting the module or the client
	// do the streaming. It starts out as unknownDownloader, and switches to
	// moduleDownloader or menderDownloader once we know.
	downloaderType int

	// The current stream, read from nextArtifactStream
	currentStream  *readerAndNamePair
	finishFlag     bool
	// The streaming object for the "stream-next" file
	streamNext     *oneStream
	// The streaming object for the stream itself
	stream         *oneStream

	////////////////////////////////////////////////////////////////////////
	// End of status variables
	////////////////////////////////////////////////////////////////////////
}

func newModuleDownload(payloadPath string, proc *exec.Cmd) *moduleDownload {
	return &moduleDownload{
		payloadPath: payloadPath,
		proc: proc,
		nextArtifactStream: make(chan *readerAndNamePair),
		status: make(chan error),
		finishChannel: make(chan bool),
		cmdErr: make(chan error),
	}
}

// Should be called in a subroutine.
func (d *moduleDownload) detachedDownloadProcess() {
	err := d.downloadProcessLoop()
	d.status <- err
}

func (d *moduleDownload) waitForProcessStatus(original error) error {
	cErr := <-d.cmdErr
	if cErr != nil {
		return errors.Wrap(original, cErr.Error())
	} else {
		return original
	}
}

func (d *moduleDownload) handleCmdErr(err error) (bool, error) {
	d.proc = nil
	d.cmdErr = nil

	if err != nil {
		// Command error: Always an error.
		return false, errors.Wrap(err, "Update module terminated abnormally")

	} else if d.finishFlag {
		// Process terminated, we are done!
		return true, nil

	} else if d.downloaderType == unknownDownloader {

		d.downloaderType = menderDownloader

		// We could still be trying to write to the "stream-next" file
		// in a go routine, so cancel that.
		if d.streamNext != nil {
			d.streamNext.cancelStream()
			d.streamNext = nil
		}

		err = d.initializeMenderDownload()
		if err != nil {
			return false, err
		}

		if d.currentStream != nil {
			// We may have gotten a stream already. Start
			// downloading it straight into "files" directory.
			filePath := path.Join(d.payloadPath, "files", d.currentStream.name)
			d.stream = newOneStream(d.currentStream.r, filePath,
				os.O_WRONLY | os.O_CREATE | os.O_EXCL)
			d.stream.startStream()
		}

	} else if d.downloaderType == moduleDownloader {
		// Should always get finishFlag before this happens.
		return false, errors.New("Update module terminated in the middle of the download")
	}

	return false, nil
}

func (d *moduleDownload) handleNextArtifactStream() error {
	if d.downloaderType == menderDownloader {
		// Download new stream straight to "files".
		filePath := path.Join(d.payloadPath, "files", d.currentStream.name)
		d.stream = newOneStream(d.currentStream.r, filePath,
			os.O_WRONLY | os.O_CREATE | os.O_EXCL)
		d.stream.startStream()
	} else {
		// Download new stream to update module using "stream-next" and
		// "streams" directory.
		var err error
		d.streamNext, err = d.publishNameInStreamNext(d.currentStream.name)
		if err != nil {
			return d.waitForProcessStatus(err)
		}
		d.streamNext.startStream()
	}

	return nil
}

func (d *moduleDownload) handleStreamNextChannel(err error) error {
	d.streamNext = nil

	if d.downloaderType == menderDownloader {
		// We don't care about this status if we have switched to the
		// Mender downloader.
		return nil
	}

	if err != nil {
		return d.waitForProcessStatus(err)
	}

	if d.downloaderType == unknownDownloader {
		d.downloaderType = moduleDownloader
	}

	if d.finishFlag {
		// Streaming is finished. Return and wait for
		// process to terminate.
		return nil
	}

	// Process has read from "stream-next", now stream into
	// the file in the "streams" directory.
	filePath := path.Join(d.payloadPath, "streams", d.currentStream.name)
	d.stream = newOneStream(d.currentStream.r, filePath,
		os.O_WRONLY)
	d.stream.startStream()

	return nil
}

func (d *moduleDownload) handleStreamChannel(err error) error {
	d.stream = nil

	// Process has finished streaming, give back status.
	if err != nil {
		// If error, bail.
		return d.waitForProcessStatus(err)
	} else {
		// If successful, stay in loop.
		d.status <- err
		return nil
	}
}

func (d *moduleDownload) handleFinishChannel() (bool, error) {
	d.finishFlag = true

	if d.downloaderType != menderDownloader {
		// Publish empty entry to signal end of streams.
		var err error
		d.streamNext, err = d.publishNameInStreamNext("")
		if err != nil {
			return false, d.waitForProcessStatus(err)
		}
		d.streamNext.startStream()

	} else if d.proc == nil {
		// Finish has been requested, and the process
		// has already terminated. We are done.
		return true, nil
	}

	return false, nil
}

// Loop to receive new stream requests and process them. It is essentially an
// event loop that handles input from several sources:
//
// 1. The update module process. Only one of these processes will run for all
//    the downloads, since each state is only invoked once.
//
// 2. nextArtifactStream: The channel which the client uses to deliver new
//    payload files while parsing the artifact
//
// 3. streamNextChannel: The channel that contains the error status of the
//    latest write to the "stream-next" file
//
// 4. streamChannel: The channel that contains the error status of the latest
//    write of the payload file, whether that is to a FIFO in the "streams"
//    directory or a file in the "files" directory
//
// 5. finishChannel: Used by the client to signal that all payload files have
//    been read, IOW to terminate the loop
func (d *moduleDownload) downloadProcessLoop() error {
	go func() {
		err := d.proc.Wait()
		d.cmdErr <- err
	}()

	// Corresponds to "stream-next" file and actual streaming file.
	defer func(){
		if d.streamNext != nil {
			d.streamNext.cancelStream()
		}
		if d.stream != nil {
			d.stream.cancelStream()
		}
	}()

	for {
		var streamNextChannel chan error
		if d.streamNext != nil {
			streamNextChannel = d.streamNext.streamStatusChannel()
		}
		var streamChannel chan error
		if d.stream != nil {
			streamChannel = d.stream.streamStatusChannel()
		}

		var finished bool
		var err error

		select {
		case err = <-d.cmdErr:
			finished, err = d.handleCmdErr(err)

		case d.currentStream = <-d.nextArtifactStream:
			err = d.handleNextArtifactStream()

		case err = <-streamNextChannel:
			err = d.handleStreamNextChannel(err)

		case err = <-streamChannel:
			err = d.handleStreamChannel(err)

		case <-d.finishChannel:
			finished, err = d.handleFinishChannel()
		}

		if err != nil {
			return err
		} else if finished {
			return nil
		}
	}
}

func (d *moduleDownload) publishNameInStreamNext(name string) (*oneStream, error) {
	if name != "" {
		streamName := path.Join(d.payloadPath, "streams", name)
		err := syscall.Mkfifo(streamName, 0600)
		if err != nil {
			return nil, errors.Wrapf(err, "Unable to create %s", streamName)
		}
	}

	var streamNextStr string
	if name == "" {
		streamNextStr = ""
	} else {
		streamNextStr = fmt.Sprintf("streams/%s\n", name)
	}
	buf := bytes.NewBuffer([]byte(streamNextStr))

	streamPath := path.Join(d.payloadPath, "stream-next")
	stream := newOneStream(buf, streamPath, os.O_WRONLY)

	return stream, nil
}

func (d *moduleDownload) initializeMenderDownload() error {
	err := os.RemoveAll(path.Join(d.payloadPath, "streams"))
	if err != nil {
		return err
	}
	err = os.Remove(path.Join(d.payloadPath, "stream-next"))
	if err != nil {
		return err
	}

	err = os.Mkdir(path.Join(d.payloadPath, "files"), 0700)
	return err
}

// If this returns any errors, the download should be considered cancelled, and
// no further functions should be called.
func (d *moduleDownload) downloadStream(r io.Reader, name string) error {
	d.nextArtifactStream <- &readerAndNamePair{r, name}
	err := <-d.status
	return err
}

// This function should only be called if downloadStream() did not return any
// errors, otherwise the program may hang.
func (d *moduleDownload) finishDownloadProcess() error {
	d.finishChannel <- true
	err := <-d.status
	return err
}

func (mod *ModuleInstaller) PrepareStoreUpdate(artifactHeaders,
	artifactAugmentedHeaders artifact.HeaderInfoer,
	payloadHeaders handlers.ArtifactUpdateHeaders) error {

	log.Debug("Executing ModuleInstaller.PrepareStoreUpdate")

	if mod.downloader != nil {
		return errors.New("Internal error: PrepareStoreUpdate() called when download is already active")
	}

	if artifactAugmentedHeaders != nil {
		msg := "Augmented artifacts are not supported yet"
		log.Error(msg)
		return errors.New(msg)
	}

	err := mod.buildStreamsTree(artifactHeaders, artifactAugmentedHeaders, payloadHeaders)
	if err != nil {
		return err
	}

	storeUpdateCmd := exec.Command(mod.programPath, "Download")
	storeUpdateCmd.Dir = mod.payloadPath()
	output, err := storeUpdateCmd.StdoutPipe()
	if err != nil {
		return err
	}
	storeUpdateCmd.Stderr = storeUpdateCmd.Stdout

	go func() {
		_, err := mod.readAndLog(output, false)
		if err != nil {
			log.Error(err.Error())
		}
	}()

	err = storeUpdateCmd.Start()
	if err != nil {
		log.Errorf("Module could not be executed: %s", err.Error())
		return errors.Wrap(err, "Module could not be executed")
	}

	log.Error("TODO: FIX TIMER HERE!")
	mod.processKiller = newDelayKiller(storeUpdateCmd.Process, 5 * time.Second, 5 * time.Second)
	mod.downloader = newModuleDownload(mod.payloadPath(), storeUpdateCmd)

	go mod.downloader.detachedDownloadProcess()

	return nil
}

func (mod *ModuleInstaller) StoreUpdate(r io.Reader, info os.FileInfo) error {
	log.Debug("Executing ModuleInstaller.StoreUpdate")

	if mod.downloader == nil {
		return errors.New("Internal error: StoreUpdate() called when download is inactive")
	}

	return mod.downloader.downloadStream(r, info.Name())
}

func (mod *ModuleInstaller) FinishStoreUpdate() error {
	log.Debug("Executing ModuleInstaller.FinishStoreUpdate")

	if mod.downloader == nil {
		return errors.New("Internal error: FinishStoreUpdate() called when download is inactive")
	}

	err := mod.downloader.finishDownloadProcess()
	mod.processKiller.Stop()

	mod.downloader = nil
	mod.processKiller = nil

	return err
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
	_, err := mod.callModule("ArtifactReboot", false)
	return err
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
