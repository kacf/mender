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
	"errors"
	"os"
	"testing"
	"time"

	"github.com/mendersoftware/mender/installer"
	"github.com/stretchr/testify/assert"
)

// implements BootEnvReadWriter
type fakeBootEnv struct {
	readVars  BootVars
	readErr   error
	writeVars BootVars
	writeErr  error
}

func (f *fakeBootEnv) ReadEnv(...string) (BootVars, error) {
	return f.readVars, f.readErr
}

func (f *fakeBootEnv) WriteEnv(w BootVars) error {
	f.writeVars = w
	return f.writeErr
}

func Test_commitUpdate(t *testing.T) {
	dualRootfsDevice := dualRootfsDeviceImpl{}

	dualRootfsDevice.BootEnvReadWriter = &fakeBootEnv{
		readVars: BootVars{
			"upgrade_available": "1",
		},
	}

	if err := dualRootfsDevice.CommitUpdate(); err != nil {
		t.FailNow()
	}

	dualRootfsDevice.BootEnvReadWriter = &fakeBootEnv{
		readVars: BootVars{
			"upgrade_available": "0",
		},
	}

	if err := dualRootfsDevice.CommitUpdate(); err != installer.ErrorNothingToCommit {
		t.FailNow()
	}

	dualRootfsDevice.BootEnvReadWriter = &fakeBootEnv{
		readVars: BootVars{
			"upgrade_available": "1",
		},
		readErr: errors.New("IO error"),
	}

	if err := dualRootfsDevice.CommitUpdate(); err == nil {
		t.FailNow()
	}
}

func Test_enableUpdatedPartition_wrongPartitinNumber_fails(t *testing.T) {
	runner := newTestOSCalls("", 0)
	fakeEnv := uBootEnv{&runner}

	testPart := partitions{}
	testPart.inactive = "inactive"

	testDevice := dualRootfsDeviceImpl{}
	testDevice.partitions = &testPart
	testDevice.BootEnvReadWriter = &fakeEnv

	if err := testDevice.InstallUpdate(); err == nil {
		t.FailNow()
	}
}

func Test_enableUpdatedPartition_correctPartitinNumber(t *testing.T) {
	runner := newTestOSCalls("", 0)
	fakeEnv := uBootEnv{&runner}

	testPart := partitions{}
	testPart.inactive = "inactive2"

	testDevice := dualRootfsDeviceImpl{}
	testDevice.partitions = &testPart
	testDevice.BootEnvReadWriter = &fakeEnv

	if err := testDevice.InstallUpdate(); err != nil {
		t.FailNow()
	}

	runner = newTestOSCalls("", 1)
	if err := testDevice.InstallUpdate(); err == nil {
		t.FailNow()
	}
}

type sizeOnlyFileInfo struct {
	size int64
}

func (s *sizeOnlyFileInfo) Name() string {
	return ""
}
func (s *sizeOnlyFileInfo) Size() int64 {
	return s.size
}
func (s *sizeOnlyFileInfo) Mode() os.FileMode {
	return 0444
}
func (s *sizeOnlyFileInfo) ModTime() time.Time {
	return time.Time{}
}
func (s *sizeOnlyFileInfo) IsDir() bool {
	return false
}
func (s *sizeOnlyFileInfo) Sys() interface{} {
	return nil
}

func Test_installUpdate_existingAndNonInactivePartition(t *testing.T) {
	testDevice := dualRootfsDeviceImpl{}

	fakePartitions := partitions{}
	fakePartitions.inactive = "/non/existing"
	testDevice.partitions = &fakePartitions

	if err := testDevice.StoreUpdate(nil, &sizeOnlyFileInfo{0}); err == nil {
		t.FailNow()
	}

	os.Create("inactivePart")
	fakePartitions.inactive = "inactivePart"
	defer os.Remove("inactivePart")

	image, _ := os.Create("imageFile")
	defer os.Remove("imageFile")

	imageContent := "test content"
	image.WriteString(imageContent)
	// rewind to the beginning of file
	image.Seek(0, 0)

	old := BlockDeviceGetSizeOf
	oldSectorSizeOf := BlockDeviceGetSectorSizeOf
	BlockDeviceGetSizeOf = func(file *os.File) (uint64, error) { return uint64(len(imageContent)), nil }
	BlockDeviceGetSectorSizeOf = func(file *os.File) (int, error) { return int(len(imageContent)), nil }

	if err := testDevice.StoreUpdate(image, &sizeOnlyFileInfo{int64(len(imageContent))}); err != nil {
		t.FailNow()
	}

	BlockDeviceGetSizeOf = func(file *os.File) (uint64, error) { return 0, errors.New("") }
	if err := testDevice.StoreUpdate(image, &sizeOnlyFileInfo{int64(len(imageContent))}); err == nil {
		t.FailNow()
	}
	BlockDeviceGetSizeOf = old
	BlockDeviceGetSectorSizeOf = oldSectorSizeOf
}

func Test_FetchUpdate_existingAndNonExistingUpdateFile(t *testing.T) {
	image, _ := os.Create("imageFile")
	imageContent := "test content"
	image.WriteString(imageContent)
	file, size, err := FetchUpdateFromFile("imageFile")
	if file == nil || size != int64(len(imageContent)) || err != nil {
		t.FailNow()
	}

	file, _, err = FetchUpdateFromFile("non-existing")
	if file != nil || err == nil {
		t.FailNow()
	}
}

func Test_Rollback_OK(t *testing.T) {
	runner := newTestOSCalls("", 0)
	fakeEnv := uBootEnv{&runner}

	testPart := partitions{}
	testPart.inactive = "part2"

	testDevice := dualRootfsDeviceImpl{}
	testDevice.partitions = &testPart
	testDevice.BootEnvReadWriter = &fakeEnv

	if err := testDevice.Rollback(); err != nil {
		t.FailNow()
	}
}

func TestDeviceVerifyReboot(t *testing.T) {
	config := dualRootfsDeviceConfig{
		"part1",
		"part2",
	}

	runner := newTestOSCalls("", 255)
	testDevice := NewDualRootfsDevice(
		&uBootEnv{&runner},
		nil,
		config)
	err := testDevice.VerifyReboot()
	assert.EqualError(t, err, "failed to read environment variable: exit status 255")

	runner = newTestOSCalls("upgrade_available=0", 0)
	testDevice = NewDualRootfsDevice(
		&uBootEnv{&runner},
		nil,
		config)
	err = testDevice.VerifyReboot()
	assert.EqualError(t, err, "Reboot to new update failed. Expected \"upgrade_available\" flag to be true but it was false")

	runner = newTestOSCalls("upgrade_available=1", 0)
	testDevice = NewDualRootfsDevice(
		&uBootEnv{&runner},
		nil,
		config)
	err = testDevice.VerifyReboot()
	assert.NoError(t, err)
}
