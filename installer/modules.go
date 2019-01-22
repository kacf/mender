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
		k.proc.Kill()
	})
	k.hardKiller = time.AfterFunc(killAfter + kill9After, func(){
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

	nextStream       chan readerAndNamePair
	streamNext       *os.File
	streamNextStatus chan error

	status     chan error
	finishChan chan bool
	finishFlag bool
}

func newModuleDownload(payloadPath string, proc *exec.Cmd) *moduleDownload {
	return &moduleDownload{
		payloadPath,
		proc,
		make(chan readerAndNamePair),
		nil,
		make(chan error),
		make(chan error),
		make(chan bool),
		false,
	}
}

// Should be called in a subroutine.
func (d *moduleDownload) detachedDownloadProcess() {
	err := d.downloadProcessLoop()
	log.Error("OUT")
	d.status <- err
}

// Loop to receive new stream requests and process them. It gets the download
// reader via the nextStream channel, and then publishes this in the streamNext
// stream (which is the "stream-next" FIFO), and gets the status from this
// publication in streamNextStatus. It then performs the download either by
// piping to a streams FIFO or a file, depending on whether the update module
// consumed the entry from "stream-next".
func (d *moduleDownload) downloadProcessLoop() error {
	cmdErr := make(chan error)
	go func() {
		err := d.proc.Wait()
		log.Errorf("HERE2, %p, %s", &cmdErr, err.Error())
		cmdErr <- err
		log.Errorf("HERE4, %p", &cmdErr)
	}()

	downloaderType := unknownDownloader
	var currentStream *readerAndNamePair
	var err error

	for {
		log.Errorf("HERE, %p", &cmdErr)
		select {
		case err = <-cmdErr:
		log.Errorf("HERE3, %p", &cmdErr)
			if err != nil {
				return err
			} else if d.finishFlag {
				return nil
			} else if downloaderType != unknownDownloader {
				return errors.New("Module process terminated in the middle of download")
			}

			// Process terminated without doing any
			// downloading. This means Mender should download.
			downloaderType = menderDownloader

			err = d.initializeMenderDownload()
			if err != nil {
				return err
			}

		case stream := <-d.nextStream:
			if downloaderType != menderDownloader {
				d.publishNameInStreamNext(stream.name)
			}
			currentStream = &stream

		case err = <-d.streamNextStatus:
			if err != nil {
				if downloaderType != menderDownloader {
					return err
				}
				// We don't care what happens to the pipe if Mender downloads.
			} else if downloaderType == unknownDownloader {
				// File name was published successfully in
				// "stream-next". This means the module is the
				// downloader.
				downloaderType = moduleDownloader
			}

		case d.finishFlag = <-d.finishChan:
			if downloaderType == menderDownloader {
				return nil
			} else {
				// Signal to module that we are done by
				// returning zero length read.
				d.publishNameInStreamNext("")
			}
		}
		log.Errorf("SCOPE, %p", &cmdErr)

		if currentStream != nil {
			if downloaderType == menderDownloader {
				err = d.downloadStreamInDir(currentStream, "files",
					os.O_CREATE | os.O_EXCL | os.O_WRONLY)
				d.status <- err
				currentStream = nil
			} else if downloaderType == moduleDownloader {
				err = d.downloadStreamInDir(currentStream, "streams",
					os.O_WRONLY)
				d.status <- err
				currentStream = nil
			}
			// } else {
			// 	We need to wait for a decision on
			// 	downloaderType first.
			// }
		}
	}
}

func (d *moduleDownload) publishNameInStreamNext(name string) error {
	if name != "" {
		log.Error("TODO: Consider doing character check here")
		streamName := path.Join(d.payloadPath, "streams", name)
		err := syscall.Mkfifo(streamName, 0600)
		if err != nil {
			return err
		}
	}

	// Finished setup, but final status will be available in
	// streamNextStatus, since it depends on what the module does.

	go func() {
		err := func() error {
			var err error
			d.streamNext, err = os.OpenFile(path.Join(d.payloadPath, "stream-next"), os.O_WRONLY, 0)
			if err != nil {
				return err
			}

			var streamNextStr string
			if name == "" {
				streamNextStr = ""
			} else {
				streamNextStr = fmt.Sprintf("streams/%s\n", name)
			}

			n, err := d.streamNext.Write([]byte(streamNextStr))
			// Important to close, so that the module gets EOF and carries
			// on.
			d.streamNext.Close()
			if err == nil && n != len(streamNextStr) {
				return errors.New("Unable to write entire entry to stream-next")
			} else {
				return err
			}
		}()
		d.streamNextStatus <- err
	}()

	return nil
}

func (d *moduleDownload) initializeMenderDownload() error {
	// We could still be trying to write to the "stream-next" file in a go
	// routine, so close that to avoid leaking memory, and to delete it.
	d.streamNext.Close()

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

func (d *moduleDownload) downloadStreamInDir(stream *readerAndNamePair,
	subdir string, openFlags int) error {

	filePath := path.Join(d.payloadPath, subdir, stream.name)
	fd, err := os.OpenFile(filePath, openFlags, 0600)
	if err != nil {
		return err
	}
	defer fd.Close()

	_, err = io.Copy(fd, stream.r)
	return err
}

func (d *moduleDownload) downloadStream(r io.Reader, name string) error {
	d.nextStream <- readerAndNamePair{r, name}
	err := <-d.status
	return err
}

func (d *moduleDownload) finishDownloadProcess() error {
	d.finishChan <- true
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
