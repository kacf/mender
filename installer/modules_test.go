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
	"bytes"
	"encoding/json"
	"io"
	"io/ioutil"
	"os"
	"os/exec"
	"path"
	"reflect"
	"syscall"
	"testing"

	"github.com/mendersoftware/mender-artifact/artifact"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testStreamsTreeInfo struct {
	typeInfo artifact.TypeInfoV3
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
	return i.typeInfo.ArtifactDepends
}
func (i *testStreamsTreeInfo) GetUpdateOriginalProvides() *artifact.TypeInfoProvides {
	return i.typeInfo.ArtifactProvides
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
	return &i.typeInfo
}
func (i *testStreamsTreeInfo) GetUpdateAugmentTypeInfoWriter() io.Writer {
	return nil
}

func verifyFileContent(t *testing.T, path, content string) {
	fd, err := os.Open(path)
	require.NoError(t, err)
	defer fd.Close()

	stat, err := fd.Stat()
	require.NoError(t, err)

	buf := make([]byte, stat.Size())
	n, err := fd.Read(buf)
	require.NoError(t, err)
	assert.Equal(t, len(content), n)

	assert.Equal(t, content, string(buf))
}

func verifyFileJSON(t *testing.T, path, content string) {
	fd, err := os.Open(path)
	require.NoError(t, err)
	defer fd.Close()

	stat, err := fd.Stat()
	require.NoError(t, err)

	buf := make([]byte, stat.Size())
	_, err = fd.Read(buf)
	require.NoError(t, err)

	var decoded interface{}
	err = json.Unmarshal(buf, &decoded)
	assert.NoError(t, err)

	var expected interface{}
	err = json.Unmarshal([]byte(content), &expected)
	require.NoError(t, err)

	assert.Truef(t, reflect.DeepEqual(expected, decoded),
		"Expected: '%s'\nActual: '%s'", string(content), string(buf))
}

func TestStreamsTree(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "")
	require.NoError(t, err)
	defer os.RemoveAll(tmpdir)

	treedir := path.Join(tmpdir, "payloads", "0000", "tree")

	prov := artifact.ArtifactProvides{
		ArtifactName: "new-artifact",
		ArtifactGroup: "new-group",
	}
	dep := artifact.ArtifactDepends{
		ArtifactName: []string{"name1", "name2"},
		CompatibleDevices: []string{"test-device"},
		ArtifactGroup: []string{"existing-group"},
	}

	headerInfo := artifact.NewHeaderInfoV3([]artifact.UpdateType{
		artifact.UpdateType{
			"test-type",
		},
	}, &prov, &dep)

	i := testStreamsTreeInfo{
		typeInfo: artifact.TypeInfoV3{
			Type: "test-type",
			ArtifactDepends: &artifact.TypeInfoDepends{
				"test-depend-key": "test-depend-value",
			},
			ArtifactProvides: &artifact.TypeInfoProvides{
				"test-provide-key": "test-provide-value",
			},
		},
	}

	mod := ModuleInstaller{
		payloadIndex: 0,
		modulesWorkPath: tmpdir,
		updateType: "test-type",
		artifactInfo: &i,
		deviceInfo: &i,
	}

	err = mod.buildStreamsTree(headerInfo, nil, &i)
	require.NoError(t, err)

	verifyFileContent(t, path.Join(treedir, "version"), "3")
	verifyFileContent(t, path.Join(treedir, "current_artifact_group"), "test-group")
	verifyFileContent(t, path.Join(treedir, "current_artifact_name"), "test-name")
	verifyFileContent(t, path.Join(treedir, "current_device_type"), "test-device")

	verifyFileContent(t, path.Join(treedir, "header", "artifact_group"),
		"new-group")
	verifyFileContent(t, path.Join(treedir, "header", "artifact_name"),
		"new-artifact")
	verifyFileContent(t, path.Join(treedir, "header", "payload_type"),
		"test-type")

	verifyFileJSON(t, path.Join(treedir, "header", "header-info"), `{
  "payloads": [
    {
      "type": "test-type"
    }
  ],
  "artifact_provides": {
    "artifact_name": "new-artifact",
    "artifact_group": "new-group"
  },
  "artifact_depends": {
    "artifact_name": [
      "name1",
      "name2"
    ],
    "device_type": [
      "test-device"
    ],
    "artifact_group": [
      "existing-group"
    ]
  }
}`)
	verifyFileJSON(t, path.Join(treedir, "header", "type-info"), `{
  "type": "test-type",
  "artifact_provides": {
    "test-provide-key": "test-provide-value"
  },
  "artifact_depends": {
    "test-depend-key": "test-depend-value"
  }
}`)
	verifyFileJSON(t, path.Join(treedir, "header", "meta-data"), `{
  "testKey": "testValue"
}`)

	stat, err := os.Stat(path.Join(treedir, "tmp"))
	require.NoError(t, err)
	assert.True(t, stat.IsDir())

	dirlist, err := ioutil.ReadDir(path.Join(treedir, "tmp"))
	require.NoError(t, err)
	assert.Equal(t, 0, len(dirlist))
}

func moduleDownloadSetup(t *testing.T, tmpdir, helperArg string) *moduleDownload {
	require.NoError(t, os.MkdirAll(path.Join(tmpdir, "streams"), 0700))
	require.NoError(t, os.MkdirAll(path.Join(tmpdir, "tmp"), 0700))
	require.NoError(t, syscall.Mkfifo(path.Join(tmpdir, "stream-next"), 0600))

	cwd, err := os.Getwd()
	require.NoError(t, err)

	cmd := exec.Command(path.Join(cwd, "modules_test_helper.sh"), helperArg)
	cmd.Dir = tmpdir
	require.NoError(t, cmd.Start())

	download := newModuleDownload(tmpdir, cmd)
	go download.detachedDownloadProcess()

	return download
}

func TestModulesMenderDownload(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "TestModuleDownload")
	require.NoError(t, err)
	defer os.RemoveAll(tmpdir)

	download := moduleDownloadSetup(t, tmpdir, "menderDownload")

	buf := bytes.NewBuffer([]byte("Test content"))
	require.NoError(t, download.downloadStream(buf, "test-name"))

	require.NoError(t, download.finishDownloadProcess())

	verifyFileContent(t, path.Join(tmpdir, "files", "test-name"), "Test content")
}

func TestModulesModuleDownload(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "TestModuleDownload")
	require.NoError(t, err)
	defer os.RemoveAll(tmpdir)

	download := moduleDownloadSetup(t, tmpdir, "moduleDownload")

	buf := bytes.NewBuffer([]byte("Test content"))
	require.NoError(t, download.downloadStream(buf, "test-name"))

	require.NoError(t, download.finishDownloadProcess())

	verifyFileContent(t, path.Join(tmpdir, "tmp", "module-downloaded-file"), "Test content")
	// Check that Mender doesn't also create the file.
	_, err = os.Stat(path.Join(tmpdir, "files", "test-name"))
	assert.True(t, os.IsNotExist(err))
}

func TestModulesModuleDownloadFailure(t *testing.T) {
	tmpdir, err := ioutil.TempDir("", "TestModuleDownload")
	require.NoError(t, err)
	defer os.RemoveAll(tmpdir)

	download := moduleDownloadSetup(t, tmpdir, "moduleDownloadFailure")

	buf := bytes.NewBuffer([]byte("Test content"))
	require.NoError(t, download.downloadStream(buf, "test-name"))

	panic("HERE")
	require.NoError(t, download.finishDownloadProcess())

	verifyFileContent(t, path.Join(tmpdir, "tmp", "module-downloaded-file"), "Test content")
	// Check that Mender doesn't also create the file.
	_, err = os.Stat(path.Join(tmpdir, "files", "test-name"))
	assert.True(t, os.IsNotExist(err))
}
