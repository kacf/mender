// Copyright 2023 Northern.tech AS
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

#ifndef MENDER_UPDATE_UPDATE_MODULE_HPP
#define MENDER_UPDATE_UPDATE_MODULE_HPP

#include <vector>
#include <string>

#include <common/conf.hpp>
#include <common/error.hpp>
#include <common/expected.hpp>
#include <common/processes.hpp>

#include <mender-update/context.hpp>

#include <artifact/artifact.hpp>

class UpdateModuleTests;

namespace mender {
namespace update {
namespace update_module {
namespace v3 {

using namespace std;

namespace conf = mender::common::conf;
namespace context = mender::update::context;
namespace error = mender::common::error;
namespace events = mender::common::events;
namespace expected = mender::common::expected;
namespace io = mender::common::io;
namespace processes = mender::common::processes;

using context::MenderContext;
using expected::ExpectedBool;
using expected::ExpectedStringVector;
using mender::artifact::Artifact;

enum class RebootAction { No, Automatic, Yes };
enum class State {
	Download,
	ArtifactInstall,
	NeedsReboot,
	ArtifactReboot,
	ArtifactCommit,
	SupportsRollback,
	ArtifactRollback,
	ArtifactVerifyReboot,
	ArtifactRollbackReboot,
	ArtifactVerifyRollbackReboot,
	ArtifactFailure,
	Cleanup,

	LastState
};

std::string StateToString(State state);

using ExpectedRebootAction = expected::expected<RebootAction, error::Error>;

using ExpectedWriterHandler = function<void(io::ExpectedAsyncWriterPtr)>;

class UpdateModule {
public:
	UpdateModule(MenderContext &ctx, const string &payload_type);

	string GetUpdateModulePath() {
		return update_module_path_;
	}
	string GetUpdateModuleWorkDir() {
		return update_module_workdir_;
	}
	void SetUpdateModulePath(const string &path) {
		update_module_path_ = path;
	}
	void SetUpdateModuleWorkDir(const string &path) {
		update_module_workdir_ = path;
	}

	error::Error PrepareFileTreeDeviceParts(const string &path);
	error::Error CleanAndPrepareFileTree(
		const string &path, artifact::PayloadHeaderView &payload_meta_data);
	error::Error EnsureRootfsImageFileTree(const string &path);
	error::Error DeleteFileTree(const string &path);

	using DownloadFinishedHandler = function<void(error::Error)>;

	// Use same names as in Update Module specification.
	error::Error Download(artifact::Payload &payload);
	void AsyncDownload(
		events::EventLoop &event_loop, artifact::Payload &payload, DownloadFinishedHandler handler);
	error::Error ArtifactInstall();
	ExpectedRebootAction NeedsReboot();
	error::Error ArtifactReboot();
	error::Error ArtifactCommit();
	ExpectedBool SupportsRollback();
	error::Error ArtifactRollback();
	error::Error ArtifactVerifyReboot();
	error::Error ArtifactRollbackReboot();
	error::Error ArtifactVerifyRollbackReboot();
	error::Error ArtifactFailure();
	error::Error Cleanup();

	static error::Error GetProcessError(const error::Error &err);

private:
	error::Error CallState(State state, string *procOut);
	error::Error CallStateNoCapture(State state);
	string GetModulePath() const;
	string GetModulesWorkPath() const;

	error::Error PrepareStreamNextPipe();
	error::Error OpenStreamNextPipe(ExpectedWriterHandler open_handler);
	error::Error PrepareAndOpenStreamPipe(const string &path, ExpectedWriterHandler open_handler);
	error::Error PrepareDownloadDirectory(const string &path);
	error::Error DeleteStreamsFiles();

	void StartDownloadProcess();

	void StreamNextOpenHandler(io::ExpectedAsyncWriterPtr writer);
	void StreamOpenHandler(io::ExpectedAsyncWriterPtr writer);

	void StreamNextWriteHandler(size_t expected_n, io::ExpectedSize result);
	void PayloadReadHandler(io::ExpectedSize result);
	void StreamWriteHandler(size_t expected_n, io::ExpectedSize result);

	void EndStreamNext();

	void DownloadErrorHandler(const error::Error &err);
	void EndDownloadLoop(const error::Error &err);
	void DownloadTimeoutHandler();

	void ProcessEndedHandler(error::Error err);

	void StartDownloadToFile();

	context::MenderContext &ctx_;
	string update_module_path_;
	string update_module_workdir_;

	struct DownloadData {
		DownloadData(events::EventLoop &event_loop, artifact::Payload &payload);

		artifact::Payload &payload_;
		events::EventLoop &event_loop_;
		DownloadFinishedHandler download_finished_handler_;
		vector<uint8_t> buffer_;

		shared_ptr<processes::Process> proc_;
		events::Timer proc_timeout_ {event_loop_};

		string stream_next_path_;
		shared_ptr<io::Canceller> stream_next_opener_;
		io::AsyncWriterPtr stream_next_writer_;

		string current_payload_name_;
		io::AsyncReaderPtr current_payload_reader_;
		shared_ptr<io::Canceller> current_stream_opener_;
		io::AsyncWriterPtr current_stream_writer_;
		size_t written_ {0};

		bool module_has_started_download_ {false};
		bool module_has_finished_download_ {false};
		bool downloading_to_files_ {false};
	};
	unique_ptr<DownloadData> download_;

	friend class ::UpdateModuleTests;
};

ExpectedStringVector DiscoverUpdateModules(const conf::MenderConfig &config);

} // namespace v3
} // namespace update_module
} // namespace update
} // namespace mender

#endif // MENDER_UPDATE_UPDATE_MODULE_HPP
