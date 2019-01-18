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
	"io/ioutil"
	"os"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/require"
)

type testStreamsTreeInfo struct {
}

func (i *testStreamsTreeInfo) GetCurrentArtifactName() (string, error) {
	return "test-name", nil
}

func (i *testStreamsTreeInfo) GetCurrentArtifactGroup() (string, error) {
	return "test-group", nil
}

func (i *testStreamsTreeInfo) GetDeviceType() (string, error) {
	return "test-device", nil
}

func (i *testStreamsTreeInfo) GetVersion() int {
	return 3
}

func (i *testStreamsTreeInfo) GetUpdateType() string {
	return "test-type"
}

func (i *testStreamsTreeInfo) GetUpdateOriginalType() string {
	return "test-type"
}

func (i *testStreamsTreeInfo) GetUpdateDepends() (*artifact.TypeInfoDepends, error) {
	return i.GetUpdateOriginalDepends(), nil
}
func (i *testStreamsTreeInfo) GetUpdateProvides() (*artifact.TypeInfoProvides, error) {
	return i.GetUpdateOriginalProvides(), nil
}
func (i *testStreamsTreeInfo) GetUpdateMetaData() (map[string]interface{}, error) {
	return i.GetUpdateOriginalMetaData(), nil
}

func (i *testStreamsTreeInfo) GetUpdateOriginalDepends() *artifact.TypeInfoDepends {
	return &artifact.TypeInfoDepends{}
}
func (i *testStreamsTreeInfo) GetUpdateOriginalProvides() *artifact.TypeInfoProvides {
	return &artifact.TypeInfoProvides{}
}
func (i *testStreamsTreeInfo) GetUpdateOriginalMetaData() map[string]interface{} {
	return map[string]interface{}{
		"testKey": "testValue",
	}
}

func (i *testStreamsTreeInfo) GetUpdateAugmentDepends() *artifact.TypeInfoDepends {
	return nil
}
func (i *testStreamsTreeInfo) GetUpdateAugmentProvides() *artifact.TypeInfoProvides {
	return nil
}
func (i *testStreamsTreeInfo) GetUpdateAugmentMetaData() map[string]interface{} {
	return map[string]interface{}{}
}

func (i *testStreamsTreeInfo) GetUpdateOriginalTypeInfoWriter() io.Writer {
}
func (i *testStreamsTreeInfo) GetUpdateAugmentTypeInfoWriter() io.Writer {
}


func TestStreamsTree(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "")
	require.NoError(t, err)
	defer os.RemoveAll(tmpdir)

	i := testStreamsTreeInfo{}

	mod := ModuleInstaller{
		payloadIndex: 0,
		modulesWorkPath: tmpdir,
		updateType: "test-type",
		artifactInfo: &i,
		deviceInfo: &i,
	}

	headerInfo := artifact.HeaderInfo{
		ArtifactName: "new-artifact",
		Updates: []UpdateType{
			UpdateType{
				"test-type",
			},
		},
		CompatibleDevices: []string{
			"test-device",
		},
	}

	err = mod.buildStreamsTree(&headerInfo, nil, )
	require.NoError(t, err)

	output, _ := exec.Command("find", tmpdir).Output()
	t.Log(string(output))
}
