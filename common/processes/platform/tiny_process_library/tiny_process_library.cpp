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

#include <common/processes.hpp>

#include <string>
#include <string_view>

#include <common/events_io.hpp>
#include <common/io.hpp>

using namespace std;

namespace mender::common::processes {

namespace io = mender::common::io;

class ProcessReaderFunctor {
public:
	void operator()(const char *bytes, size_t n);

	io::WriterPtr writer_;
};

Process::~Process() {
	// TODO
}

error::Error Process::Start() {
	proc_ = make_unique<tpl::Process>(args_, "");

	if (proc_->get_id() == -1) {
		return MakeError(
			ProcessesErrorCode::SpawnError,
			"Failed to spawn '" + (args_.size() >= 1 ? args_[0] : "<null>") + "'");
	}

	return error::NoError;
}

int Process::Wait() {
	if (proc_) {
		exit_status_ = proc_->get_exit_status();
		proc_.reset();
	}
	return exit_status_;
}

static void CollectLineData(
	string &trailing_line, vector<string> &lines, const char *bytes, size_t len) {
	auto bytes_view = string_view(bytes, len);
	size_t line_start_idx = 0;
	size_t line_end_idx = bytes_view.find("\n", 0);
	if ((trailing_line != "") && (line_end_idx != string_view::npos)) {
		lines.push_back(trailing_line + string(bytes_view, 0, line_end_idx));
		line_start_idx = line_end_idx + 1;
		line_end_idx = bytes_view.find("\n", line_start_idx);
		trailing_line = "";
	}

	while ((line_start_idx < (len - 1)) && (line_end_idx != string_view::npos)) {
		lines.push_back(string(bytes_view, line_start_idx, (line_end_idx - line_start_idx)));
		line_start_idx = line_end_idx + 1;
		line_end_idx = bytes_view.find("\n", line_start_idx);
	}

	if ((line_end_idx == string_view::npos) && (line_start_idx != (len - 1))) {
		trailing_line += string(bytes_view, line_start_idx, (len - line_start_idx));
	}
}

ExpectedLineData Process::GenerateLineData() {
	if (args_.size() == 0) {
		return expected::unexpected(MakeError(
			ProcessesErrorCode::SpawnError, "No arguments given, cannot spawn a process"));
	}

	string trailing_line;
	vector<string> ret;
	proc_ =
		make_unique<tpl::Process>(args_, "", [&trailing_line, &ret](const char *bytes, size_t len) {
			CollectLineData(trailing_line, ret, bytes, len);
		});

	auto id = proc_->get_id();

	// waits for the process to finish
	// TODO: log exit status != 0? Or error?
	Wait();

	if (trailing_line != "") {
		ret.push_back(trailing_line);
	}

	if (id == -1) {
		return expected::unexpected(MakeError(
			ProcessesErrorCode::SpawnError,
			"Failed to spawn '" + (args_.size() >= 1 ? args_[0] : "<null>") + "'"));
	}

	return ExpectedLineData(ret);
}

expected::expected<pair<io::WriterPtr, io::AsyncReaderPtr>, error::Error> Process::GetProcessReader(events::EventLoop &loop) {
	if (proc_) {
		return expected::unexpected(MakeError(ProcessAlreadyStartedError, "Cannot get process output"));
	}

	int fds[2];
	int ret = pipe(fds);
	if (ret < 0) {
		int err = errno;
		return expected::unexpected(error::Error(generic_category().default_error_condition(err), "Could not get process stdout reader"));
	}

	return pair<io::WriterPtr, io::AsyncReaderPtr> {
		make_shared<io::FileDescriptorWriter>(fds[1]),
		make_shared<events::io::AsyncFileDescriptorReader>(loop, fds[0])
	};
}

io::ExpectedAsyncReaderPtr Process::GetAsyncStdoutReader(events::EventLoop &loop) {
	return GetProcessReader(loop).and_then([this](pair<io::WriterPtr, io::AsyncReaderPtr> result) -> io::ExpectedAsyncReaderPtr {
		stdout_pipe_ = result.first;
		return result.second;
	});
}

io::ExpectedAsyncReaderPtr Process::GetAsyncStderrReader(events::EventLoop &loop) {
	return GetProcessReader(loop).and_then([this](pair<io::WriterPtr, io::AsyncReaderPtr> result) -> io::ExpectedAsyncReaderPtr {
		stderr_pipe_ = result.first;
		return result.second;
	});
}

void Process::Terminate() {
	if (proc_) {
		proc_->kill(false);
	}
}

void Process::Kill() {
	if (proc_) {
		proc_->kill(true);
	}
}

void ProcessReaderFunctor::operator()(const char *bytes, size_t n) {
	if (!writer_) {
		return;
	}

	// TODO: ???
	auto result = writer_->Write(bytes, bytes + n);
}

} // namespace mender::common::processes
