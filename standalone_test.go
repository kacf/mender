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
	"io"
	"io/ioutil"
	"os"
	"testing"

	"github.com/mendersoftware/mender/client"
	"github.com/stretchr/testify/assert"
)

func Test_doManualUpdate_noParams_fail(t *testing.T) {
	config := menderConfig{}
	if err := doStandaloneInstall(new(dualRootfsDeviceImpl), runOptionsType{}, "", nil, &config, newStateScriptExecutor(&config)); err == nil {
		t.FailNow()
	}
}

func Test_doManualUpdate_invalidHttpsClientConfig_updateFails(t *testing.T) {
	runOptions := runOptionsType{}
	iamgeFileName := "https://update"
	runOptions.imageFile = &iamgeFileName
	runOptions.ServerCert = "non-existing"

	config := menderConfig{}
	if err := doStandaloneInstall(new(dualRootfsDeviceImpl), runOptions, "", nil, &config, newStateScriptExecutor(&config)); err == nil {
		t.FailNow()
	}
}

func Test_doManualUpdate_nonExistingFile_fail(t *testing.T) {
	fakeDevice := dualRootfsDeviceImpl{}
	fakeRunOptions := runOptionsType{}
	imageFileName := "non-existing"
	fakeRunOptions.imageFile = &imageFileName

	config := menderConfig{}
	if err := doStandaloneInstall(&fakeDevice, fakeRunOptions, "", nil, &config, newStateScriptExecutor(&config)); err == nil {
		t.FailNow()
	}
}

func Test_doManualUpdate_networkUpdateNoClient_fail(t *testing.T) {
	fakeDevice := dualRootfsDeviceImpl{}
	fakeRunOptions := runOptionsType{}
	imageFileName := "http://non-existing"
	fakeRunOptions.imageFile = &imageFileName

	config := menderConfig{}
	if err := doStandaloneInstall(&fakeDevice, fakeRunOptions, "", nil, &config, newStateScriptExecutor(&config)); err == nil {
		t.FailNow()
	}
}

func Test_doManualUpdate_networkClientExistsNoServer_fail(t *testing.T) {
	fakeDevice := dualRootfsDeviceImpl{}
	fakeRunOptions := runOptionsType{}
	imageFileName := "http://non-existing"
	fakeRunOptions.imageFile = &imageFileName

	fakeRunOptions.Config =
		client.Config{
			ServerCert: "server.crt",
			IsHttps:    true,
			NoVerify:   false,
		}

	config := menderConfig{}
	if err := doStandaloneInstall(&fakeDevice, fakeRunOptions, "", nil, &config, newStateScriptExecutor(&config)); err == nil {
		t.FailNow()
	}
}

func Test_doManualUpdate_existingFile_updateSuccess(t *testing.T) {
	// setup

	artifact, err := MakeRootfsImageArtifact(1, false)
	assert.NoError(t, err)
	assert.NotNil(t, artifact)

	f, err := ioutil.TempFile("", "update")
	assert.NoError(t, err)
	defer os.Remove(f.Name())

	_, err = io.Copy(f, artifact)
	assert.NoError(t, err)
	f.Close()

	// test

	dev := fakeDevice{consumeUpdate: true}
	fakeRunOptions := runOptionsType{}
	imageFileName := f.Name()
	fakeRunOptions.imageFile = &imageFileName

	config := menderConfig{}
	err = doStandaloneInstall(dev, fakeRunOptions, "vexpress-qemu", nil, &config, newStateScriptExecutor(&config))
	assert.NoError(t, err)
}
