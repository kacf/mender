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

#include <mender-update/standalone.hpp>

#include <common/common.hpp>
#include <common/events_io.hpp>
#include <common/http.hpp>
#include <common/log.hpp>
#include <common/path.hpp>

#include <artifact/v3/scripts/executor.hpp>

namespace mender {
namespace update {
namespace standalone {


namespace common = mender::common;
namespace events = mender::common::events;
namespace executor = mender::artifact::scripts::executor;
namespace http = mender::common::http;
namespace io = mender::common::io;
namespace log = mender::common::log;
namespace path = mender::common::path;

const string StateDataKeys::version {"Version"};
const string StateDataKeys::artifact_name {"ArtifactName"};
const string StateDataKeys::artifact_group {"ArtifactGroup"};
const string StateDataKeys::artifact_provides {"ArtifactTypeInfoProvides"};
const string StateDataKeys::artifact_clears_provides {"ArtifactClearsProvides"};
const string StateDataKeys::payload_types {"PayloadTypes"};
const string StateDataKeys::completed_state {"CompletedState"};

ExpectedOptionalStateData LoadStateData(database::KeyValueDatabase &db) {
	StateDataKeys keys;
	StateData dst;

	auto exp_bytes = db.Read(context::MenderContext::standalone_state_key);
	if (!exp_bytes) {
		auto &err = exp_bytes.error();
		if (err.code == database::MakeError(database::KeyError, "").code) {
			return optional<StateData>();
		} else {
			return expected::unexpected(err);
		}
	}

	auto exp_json = json::Load(common::StringFromByteVector(exp_bytes.value()));
	if (!exp_json) {
		return expected::unexpected(exp_json.error());
	}
	auto &json = exp_json.value();

	auto exp_int = json::Get<int64_t>(json, keys.version, json::MissingOk::No);
	if (!exp_int) {
		return expected::unexpected(exp_int.error());
	}
	dst.version = exp_int.value();

	if (dst.version != 1 && dst.version != context::MenderContext::standalone_data_version) {
		return expected::unexpected(error::Error(
			make_error_condition(errc::not_supported),
			"State data has a version which is not supported by this client"));
	}

	auto exp_string = json::Get<string>(json, keys.artifact_name, json::MissingOk::No);
	if (!exp_string) {
		return expected::unexpected(exp_string.error());
	}
	dst.artifact_name = exp_string.value();

	exp_string = json::Get<string>(json, keys.artifact_group, json::MissingOk::Yes);
	if (!exp_string) {
		return expected::unexpected(exp_string.error());
	}
	dst.artifact_group = exp_string.value();

	auto exp_map = json::Get<json::KeyValueMap>(json, keys.artifact_provides, json::MissingOk::No);
	if (exp_map) {
		dst.artifact_provides = exp_map.value();
	} else {
		dst.artifact_provides.reset();
	}

	auto exp_array =
		json::Get<vector<string>>(json, keys.artifact_clears_provides, json::MissingOk::No);
	if (exp_array) {
		dst.artifact_clears_provides = exp_array.value();
	} else {
		dst.artifact_clears_provides.reset();
	}

	exp_array = json::Get<vector<string>>(json, keys.payload_types, json::MissingOk::No);
	if (!exp_array) {
		return expected::unexpected(exp_array.error());
	}
	dst.payload_types = exp_array.value();

	if (dst.version == 1) {
		// In version 1, if there is any data at all, it is equivalent to this:
		dst.completed_state = "ArtifactInstall";

		// Additionally, there is never any situation where we want to save version 1 data,
		// because it only has one state: The one we just loaded in the previous
		// statement. In a rollback situation, all states are always carried out and the
		// data is removed instead. Therefore, always set it to version 2, so we can't even
		// theoretically save it wrongly (and we don't need to handle it in the saving
		// code).
		dst.version = context::MenderContext::standalone_data_version;
	} else {
		exp_string = json::Get<string>(json, keys.completed_state, json::MissingOk::No);
		if (!exp_string) {
			return expected::unexpected(exp_string.error());
		}
		dst.completed_state = exp_string.value();
	}

	if (dst.artifact_name == "") {
		return expected::unexpected(context::MakeError(
			context::DatabaseValueError, "`" + keys.artifact_name + "` is empty"));
	}

	if (dst.payload_types.size() == 0) {
		return expected::unexpected(context::MakeError(
			context::DatabaseValueError, "`" + keys.payload_types + "` is empty"));
	}
	if (dst.payload_types.size() >= 2) {
		return expected::unexpected(error::Error(
			make_error_condition(errc::not_supported),
			"`" + keys.payload_types + "` contains multiple payloads"));
	}

	return dst;
}

StateData StateDataFromPayloadHeaderView(const artifact::PayloadHeaderView &header) {
	StateData dst;
	dst.version = context::MenderContext::standalone_data_version;
	dst.artifact_name = header.header.artifact_name;
	dst.artifact_group = header.header.artifact_group;
	dst.artifact_provides = header.header.type_info.artifact_provides;
	dst.artifact_clears_provides = header.header.type_info.clears_artifact_provides;
	dst.payload_types.clear();
	dst.payload_types.push_back(header.header.payload_type);
	return dst;
}

error::Error SaveStateData(database::KeyValueDatabase &db, const StateData &data) {
	StateDataKeys keys;
	stringstream ss;
	ss << "{";
	ss << "\"" << keys.version << "\":" << data.version;

	ss << ",";
	ss << "\"" << keys.artifact_name << "\":\"" << data.artifact_name << "\"";

	ss << ",";
	ss << "\"" << keys.artifact_group << "\":\"" << data.artifact_group << "\"";

	ss << ",";
	ss << "\"" << keys.payload_types << "\": [";
	bool first = true;
	for (auto elem : data.payload_types) {
		if (!first) {
			ss << ",";
		}
		ss << "\"" << elem << "\"";
		first = false;
	}
	ss << "]";

	if (data.artifact_provides) {
		ss << ",";
		ss << "\"" << keys.artifact_provides << "\": {";
		bool first = true;
		for (auto elem : data.artifact_provides.value()) {
			if (!first) {
				ss << ",";
			}
			ss << "\"" << elem.first << "\":\"" << elem.second << "\"";
			first = false;
		}
		ss << "}";
	}

	if (data.artifact_clears_provides) {
		ss << ",";
		ss << "\"" << keys.artifact_clears_provides << "\": [";
		bool first = true;
		for (auto elem : data.artifact_clears_provides.value()) {
			if (!first) {
				ss << ",";
			}
			ss << "\"" << elem << "\"";
			first = false;
		}
		ss << "]";
	}

	ss << R"(,"CompletedState":")" << data.completed_state << R"(")";

	ss << "}";

	string strdata = ss.str();
	vector<uint8_t> bytedata(common::ByteVectorFromString(strdata));

	return db.Write(context::MenderContext::standalone_state_key, bytedata);
}

error::Error RemoveStateData(database::KeyValueDatabase &db) {
	return db.Remove(context::MenderContext::standalone_state_key);
}

StateMachine::StateMachine() :
	state_machine_ {start_state_},
	download_enter_state_ {Executor::State::Download, executor::Action::Enter, executor::OnError::Fail, Result::FailedNothingDone},
	download_leave_state_ {Executor::State::Download, executor::Action::Leave, executor::OnError::Fail, Result::FailedNothingDone},
	download_error_state_ {Executor::State::Download, executor::Action::Error, executor::OnError::Ignore, Result::NoResult},
	artifact_install_enter_state_ {Executor::State::ArtifactInstall, executor::Action::Enter, executor::OnError::Fail, Result::FailedNoRollbackAttempted},
	artifact_install_leave_state_ {Executor::State::ArtifactInstall, executor::Action::Leave, executor::OnError::Fail, Result::FailedNoRollbackAttempted},
	artifact_install_error_state_ {Executor::State::ArtifactInstall, executor::Action::Error, executor::OnError::Ignore, Result::NoResult},
	artifact_commit_enter_state_ {Executor::State::ArtifactCommit, executor::Action::Enter, executor::OnError::Fail, Result::FailedNoRollbackAttempted},
	artifact_commit_leave_state_ {Executor::State::ArtifactCommit, executor::Action::Leave, executor::OnError::Ignore, Result::InstalledButFailedInPostCommit},
	artifact_commit_error_state_ {Executor::State::ArtifactCommit, executor::Action::Error, executor::OnError::Ignore, Result::NoResult},
	artifact_rollback_enter_state_ {Executor::State::ArtifactRollback, executor::Action::Enter, executor::OnError::Ignore, Result::FailedAndRollbackFailed},
	artifact_rollback_leave_state_ {Executor::State::ArtifactRollback, executor::Action::Leave, executor::OnError::Ignore, Result::NoResult},
	artifact_failure_enter_state_ {Executor::State::ArtifactFailure, executor::Action::Enter, executor::OnError::Ignore, Result::FailedAndRollbackFailed},
	artifact_failure_leave_state_ {Executor::State::ArtifactFailure, executor::Action::Leave, executor::OnError::Ignore, Result::NoResult} {
	namespace sm = common::state_machine;
	using se = StateEvent;
	auto &s = state_machine_;

	// clang-format off
	s.AddTransition(download_enter_state_,            se::Success,          download_state_,                  sm::Immediate);
	s.AddTransition(download_enter_state_,            se::Failure,          download_error_state_,            sm::Immediate);

	s.AddTransition(download_state_,                  se::Success,          download_leave_state_,            sm::Immediate);
	s.AddTransition(download_state_,                  se::Failure,          download_error_state_,            sm::Immediate);

	s.AddTransition(download_leave_state_,            se::Success,          artifact_install_enter_state_,    sm::Immediate);
	s.AddTransition(download_leave_state_,            se::Failure,          download_error_state_,            sm::Immediate);

	s.AddTransition(download_error_state_,            se::Success,          cleanup_state_,                   sm::Immediate);
	s.AddTransition(download_error_state_,            se::Failure,          cleanup_state_,                   sm::Immediate);

	s.AddTransition(artifact_install_enter_state_,    se::Success,          artifact_install_state_,          sm::Immediate);
	s.AddTransition(artifact_install_enter_state_,    se::Failure,          artifact_install_error_state_,    sm::Immediate);

	s.AddTransition(artifact_install_state_,          se::Success,          artifact_install_leave_state_,    sm::Immediate);
	s.AddTransition(artifact_install_state_,          se::Failure,          artifact_install_error_state_,    sm::Immediate);

	s.AddTransition(artifact_install_leave_state_,    se::Success,          reboot_and_rollback_query_state_, sm::Immediate);
	s.AddTransition(artifact_install_leave_state_,    se::Failure,          artifact_install_error_state_,    sm::Immediate);

	s.AddTransition(artifact_install_error_state_,    se::Success,          rollback_query_state_,            sm::Immediate);
	s.AddTransition(artifact_install_error_state_,    se::Failure,          rollback_query_state_,            sm::Immediate);

	s.AddTransition(reboot_and_rollback_query_state_, se::Success,          artifact_commit_enter_state_,     sm::Immediate);
	s.AddTransition(reboot_and_rollback_query_state_, se::Failure,          rollback_query_state_,            sm::Immediate);
	s.AddTransition(reboot_and_rollback_query_state_, se::NeedsInteraction, exit_state_,                      sm::Immediate);

	s.AddTransition(artifact_commit_enter_state_,     se::Success,          artifact_commit_state_,           sm::Immediate);
	s.AddTransition(artifact_commit_enter_state_,     se::Failure,          artifact_commit_error_state_,     sm::Immediate);

	s.AddTransition(artifact_commit_state_,           se::Success,          artifact_commit_leave_state_,     sm::Immediate);
	s.AddTransition(artifact_commit_state_,           se::Failure,          artifact_commit_error_state_,     sm::Immediate);

	s.AddTransition(artifact_commit_leave_state_,     se::Success,          cleanup_state_,                   sm::Immediate);
	s.AddTransition(artifact_commit_leave_state_,     se::Failure,          cleanup_state_,                   sm::Immediate);

	s.AddTransition(artifact_commit_error_state_,     se::Success,          rollback_query_state_,            sm::Immediate);
	s.AddTransition(artifact_commit_error_state_,     se::Failure,          rollback_query_state_,            sm::Immediate);

	s.AddTransition(artifact_rollback_enter_state_,   se::Success,          artifact_rollback_state_,         sm::Immediate);
	s.AddTransition(artifact_rollback_enter_state_,   se::Failure,          artifact_rollback_state_,         sm::Immediate);

	s.AddTransition(artifact_rollback_state_,         se::Success,          artifact_rollback_leave_state_,   sm::Immediate);
	s.AddTransition(artifact_rollback_state_,         se::Failure,          artifact_rollback_leave_state_,   sm::Immediate);

	s.AddTransition(artifact_rollback_leave_state_,   se::Success,          artifact_failure_state_,          sm::Immediate);
	s.AddTransition(artifact_rollback_leave_state_,   se::Failure,          artifact_failure_error_state_,    sm::Immediate);

	s.AddTransition(artifact_failure_enter_state_,    se::Success,          artifact_failure_state_,          sm::Immediate);
	s.AddTransition(artifact_failure_enter_state_,    se::Failure,          artifact_failure_state_,          sm::Immediate);

	s.AddTransition(artifact_failure_state_,          se::Success,          artifact_failure_leave_state_,    sm::Immediate);
	s.AddTransition(artifact_failure_state_,          se::Failure,          artifact_failure_leave_state_,    sm::Immediate);

	s.AddTransition(artifact_failure_leave_state_,    se::Success,          cleanup_state_,                   sm::Immediate);
	s.AddTransition(artifact_failure_leave_state_,    se::Failure,          cleanup_state_,                   sm::Immediate);

	s.AddTransition(cleanup_state_,                   se::Success,          exit_state_,                      sm::Immediate);
	s.AddTransition(cleanup_state_,                   se::Failure,          exit_state_,                      sm::Immediate);
	// clang-format on
}

static io::ExpectedReaderPtr ReaderFromUrl(
	events::EventLoop &loop, http::Client &http_client, const string &src) {
	auto req = make_shared<http::OutgoingRequest>();
	req->SetMethod(http::Method::GET);
	auto err = req->SetAddress(src);
	if (err != error::NoError) {
		return expected::unexpected(err);
	}
	error::Error inner_err;
	io::AsyncReaderPtr reader;
	err = http_client.AsyncCall(
		req,
		[&loop, &inner_err, &reader](http::ExpectedIncomingResponsePtr exp_resp) {
			// No matter what happens, we will want to stop the loop after the headers
			// are received.
			loop.Stop();

			if (!exp_resp) {
				inner_err = exp_resp.error();
				return;
			}

			auto resp = exp_resp.value();

			if (resp->GetStatusCode() != http::StatusOK) {
				inner_err = context::MakeError(
					context::UnexpectedHttpResponse,
					to_string(resp->GetStatusCode()) + ": " + resp->GetStatusMessage());
				return;
			}

			auto exp_reader = resp->MakeBodyAsyncReader();
			if (!exp_reader) {
				inner_err = exp_reader.error();
				return;
			}
			reader = exp_reader.value();
		},
		[](http::ExpectedIncomingResponsePtr exp_resp) {
			if (!exp_resp) {
				log::Warning("While reading HTTP body: " + exp_resp.error().String());
			}
		});

	// Loop until the headers are received. Then we return and let the reader drive the
	// rest of the download.
	loop.Run();

	if (err != error::NoError) {
		return expected::unexpected(err);
	}

	if (inner_err != error::NoError) {
		return expected::unexpected(inner_err);
	}

	return make_shared<events::io::ReaderFromAsyncReader>(loop, reader);
}

ResultAndError Download(
	Context &context,
	const string &src,
	const artifact::config::Signature verify_signature,
	InstallOptions options) {
	auto exp_in_progress = LoadStateData(main_context.GetMenderStoreDB());
	if (!exp_in_progress) {
		return {Result::FailedNothingDone, exp_in_progress.error()};
	}
	auto &in_progress = exp_in_progress.value();

	if (in_progress) {
		return {
			Result::FailedNothingDone,
			error::Error(
				make_error_condition(errc::operation_in_progress),
				"Update already in progress. Please commit or roll back first")};
	}

	io::ReaderPtr artifact_reader;

	shared_ptr<events::EventLoop> event_loop;
	http::ClientPtr http_client;

	if (src.find("http://") == 0 || src.find("https://") == 0) {
		event_loop = make_shared<events::EventLoop>();
		http_client =
			make_shared<http::Client>(main_context.GetConfig().GetHttpClientConfig(), *event_loop);
		auto reader = ReaderFromUrl(*event_loop, *http_client, src);
		if (!reader) {
			return {Result::FailedNothingDone, reader.error()};
		}
		artifact_reader = reader.value();
	} else {
		auto stream = io::OpenIfstream(src);
		if (!stream) {
			return {Result::FailedNothingDone, stream.error()};
		}
		auto file_stream = make_shared<ifstream>(std::move(stream.value()));
		artifact_reader = make_shared<io::StreamReader>(file_stream);
	}

	string art_scripts_path = main_context.GetConfig().paths.GetArtScriptsPath();

	// Clear the artifact scripts directory so we don't risk old scripts lingering.
	auto err = path::DeleteRecursively(art_scripts_path);
	if (err != error::NoError) {
		return {Result::FailedNothingDone, err.WithContext("When preparing to parse artifact")};
	}

	artifact::config::ParserConfig config {
		.artifact_scripts_filesystem_path = main_context.GetConfig().paths.GetArtScriptsPath(),
		.artifact_scripts_version = 3,
		.artifact_verify_keys = main_context.GetConfig().artifact_verify_keys,
		.verify_signature = verify_signature,
	};

	auto exp_parser = artifact::Parse(*artifact_reader, config);
	if (!exp_parser) {
		return {Result::FailedNothingDone, exp_parser.error()};
	}
	auto &parser = exp_parser.value();

	auto exp_header = artifact::View(parser, 0);
	if (!exp_header) {
		return {Result::FailedNothingDone, exp_header.error()};
	}
	auto &header = exp_header.value();

	if (header.header.payload_type == "") {
		auto data = StateDataFromPayloadHeaderView(header);
		return DoEmptyPayloadArtifact(main_context, data, options);
	}

	update_module::UpdateModule update_module(main_context, header.header.payload_type);

	err = update_module.CleanAndPrepareFileTree(update_module.GetUpdateModuleWorkDir(), header);
	if (err != error::NoError) {
		err = err.FollowedBy(update_module.Cleanup());
		return {Result::FailedNothingDone, err};
	}

	StateData data = StateDataFromPayloadHeaderView(header);

	auto exp_matches = main_context.MatchesArtifactDepends(header.header);
	if (!exp_matches) {
		log::Error(exp_matches.error().String());
		return {Result::FailedNothingDone, err};
	} else if (!exp_matches.value()) {
		// reasons already logged
		return {Result::FailedNothingDone, err};
	}

	auto result = DoDownloadState(main_context, data, parser, update_module);
	if (result.result != Result::Downloaded) {
		return result;
	}

	data.completed_state = "Download";
	err = SaveStateData(main_context.GetMenderStoreDB(), data);
	if (err != error::NoError) {
		err = err.FollowedBy(update_module.Cleanup());
		// Yes, we downloaded, but remember that the Download state is not supposed to have
		// any system-visible effects.
		return {Result::FailedNothingDone, err};
	}

	if (options != InstallOptions::NoStdout) {
		cout << "Installing artifact..." << endl;
	}

	return result;
}

ResultAndError Install(Context &context) {
	auto exp_in_progress = LoadStateData(main_context.GetMenderStoreDB());
	if (!exp_in_progress) {
		return {Result::FailedNothingDone, exp_in_progress.error()};
	}
	auto &in_progress = exp_in_progress.value();

	if (!in_progress) {
		return {
			Result::NoUpdateInProgress,
			context::MakeError(context::NoUpdateInProgressError, "Cannot install")};
	}
	auto &data = in_progress.value();

	update_module::UpdateModule update_module(main_context, data.payload_types[0]);

	error::Error err;
	auto result = DoInstallState(main_context, data, update_module);
	switch (result.result) {
	case Result::Installed:
	case Result::InstalledRebootRequired:
		data.completed_state = "ArtifactInstall";
		err = SaveStateData(main_context.GetMenderStoreDB(), data);
		if (err != error::NoError) {
			return InstallationFailureHandler(main_context, data, update_module);
		}
		break;
	case Result::InstalledAndCommitted:
	case Result::InstalledAndCommittedRebootRequired:
	case Result::InstalledButFailedInPostCommit:
	case Result::FailedNothingDone:
	case Result::FailedAndRolledBack:
	case Result::FailedAndNoRollback:
	case Result::FailedAndRollbackFailed:
		// All other results do not need to save anything, because we completed in one way
		// or another. We are spelling out every state though, just to make sure this code
		// is revisited if anything is added.
		break;
	case Result::NoUpdateInProgress:
	case Result::Downloaded:
	case Result::Committed:
	case Result::RolledBack:
	case Result::NoRollback:
	case Result::RollbackFailed:
		// These should not happen from here.
		assert(false);
		return {Result::FailedAndRollbackFailed, error::MakeError(error::ProgrammingError, "Should not reach state " + to_string(static_cast<int>(result.result)) + " from here")};
	}

	return result;
}

ResultAndError DownloadAndInstall(
	Context &context,
	const string &src,
	const artifact::config::Signature verify_signature,
	InstallOptions options) {
	auto result = Download(main_context, src, verify_signature, options);
	if (result.result != Result::Downloaded) {
		return result;
	}

	return Install(main_context);
}

ResultAndError Commit(Context &context) {
	auto exp_in_progress = LoadStateData(main_context.GetMenderStoreDB());
	if (!exp_in_progress) {
		return {Result::FailedNothingDone, exp_in_progress.error()};
	}
	auto &in_progress = exp_in_progress.value();

	if (!in_progress) {
		return {
			Result::NoUpdateInProgress,
			context::MakeError(context::NoUpdateInProgressError, "Cannot commit")};
	}
	auto &data = in_progress.value();

	if (data.completed_state != "ArtifactInstall") {
		return {Result::FailedNothingDone, context::MakeError(context::WrongOperationError,
			"Cannot commit from this state. "
			"Make sure that the `install` command has run successfully and the device is expecting a commit.")};
	}

	update_module::UpdateModule update_module(main_context, data.payload_types[0]);

	if (data.payload_types[0] == "rootfs-image") {
		// Special case for rootfs-image upgrades. See comments inside the function.
		auto err = update_module.EnsureRootfsImageFileTree(update_module.GetUpdateModuleWorkDir());
		if (err != error::NoError) {
			return {Result::FailedNothingDone, err};
		}
	}

	return DoCommit(main_context, data, update_module);
}

ResultAndError Rollback(Context &context) {
	auto exp_in_progress = LoadStateData(main_context.GetMenderStoreDB());
	if (!exp_in_progress) {
		return {Result::FailedNothingDone, exp_in_progress.error()};
	}
	auto &in_progress = exp_in_progress.value();

	if (!in_progress) {
		return {
			Result::NoUpdateInProgress,
			context::MakeError(context::NoUpdateInProgressError, "Cannot roll back")};
	}
	auto &data = in_progress.value();

	if (data.completed_state != "ArtifactInstall") {
		return {Result::FailedNothingDone, context::MakeError(context::WrongOperationError,
			"Cannot roll back from this state. "
			"Make sure that the `install` command has run successfully and the device is ready to roll back.")};
	}

	update_module::UpdateModule update_module(main_context, data.payload_types[0]);

	if (data.payload_types[0] == "rootfs-image") {
		// Special case for rootfs-image upgrades. See comments inside the function.
		auto err = update_module.EnsureRootfsImageFileTree(update_module.GetUpdateModuleWorkDir());
		if (err != error::NoError) {
			return {Result::FailedNothingDone, err};
		}
	}

	auto result = DoRollback(main_context, data, update_module);

	if (result.result == Result::NoRollback) {
		// No support for rollback. Return instead of clearing update data. It should be
		// cleared by calling commit or restoring the rollback capability.
		return result;
	}

	auto err = update_module.Cleanup();
	if (err != error::NoError) {
		result.result = Result::FailedAndRollbackFailed;
		result.err = result.err.FollowedBy(err);
	}

	if (result.result == Result::RolledBack) {
		err = RemoveStateData(main_context.GetMenderStoreDB());
	} else {
		err = CommitBrokenArtifact(main_context, data);
	}
	if (err != error::NoError) {
		result.result = Result::RollbackFailed;
		result.err = result.err.FollowedBy(err);
	}

	return result;
}

ResultAndError DoDownloadState(
	Context &context,
	StateData &data,
	artifact::Artifact &artifact,
	update_module::UpdateModule &update_module) {
	auto payload = artifact.Next();
	if (!payload) {
		return {Result::FailedNothingDone, payload.error()};
	}

	const auto &default_paths {main_context.GetConfig().paths};
	events::EventLoop loop;
	auto script_runner {executor::ScriptRunner(
		loop,
		chrono::seconds {main_context.GetConfig().state_script_timeout_seconds},
		chrono::seconds {main_context.GetConfig().state_script_retry_interval_seconds},
		chrono::seconds {main_context.GetConfig().state_script_retry_timeout_seconds},
		default_paths.GetArtScriptsPath(),
		default_paths.GetRootfsScriptsPath())};

	// ProvidePayloadFileSizes
	auto with_sizes = update_module.ProvidePayloadFileSizes();
	if (!with_sizes) {
		log::Error("Could not query for provide file sizes: " + with_sizes.error().String());
		return InstallationFailureHandler(main_context, data, update_module);
	}

	// Download Enter
	auto err = script_runner.RunScripts(executor::State::Download, executor::Action::Enter);
	if (err != error::NoError) {
		err = err.FollowedBy(
			script_runner.RunScripts(executor::State::Download, executor::Action::Error));
		err = err.FollowedBy(update_module.Cleanup());
		err = err.FollowedBy(RemoveStateData(main_context.GetMenderStoreDB()));
		return {Result::FailedNothingDone, err};
	}
	if (with_sizes.value()) {
		err = update_module.DownloadWithFileSizes(payload.value());
	} else {
		err = update_module.Download(payload.value());
	}
	if (err != error::NoError) {
		err = err.FollowedBy(
			script_runner.RunScripts(executor::State::Download, executor::Action::Error));
		err = err.FollowedBy(update_module.Cleanup());
		err = err.FollowedBy(RemoveStateData(main_context.GetMenderStoreDB()));
		return {Result::FailedNothingDone, err};
	}

	payload = artifact.Next();
	if (payload) {
		err = error::Error(
			make_error_condition(errc::not_supported),
			"Multiple payloads are not supported in standalone mode");
	} else if (
		payload.error().code
		!= artifact::parser_error::MakeError(artifact::parser_error::EOFError, "").code) {
		err = payload.error();
	}
	if (err != error::NoError) {
		err = err.FollowedBy(
			script_runner.RunScripts(executor::State::Download, executor::Action::Error));
		err = err.FollowedBy(update_module.Cleanup());
		err = err.FollowedBy(RemoveStateData(main_context.GetMenderStoreDB()));
		return {Result::FailedNothingDone, err};
	}

	// Download Leave
	err = script_runner.RunScripts(executor::State::Download, executor::Action::Leave);
	if (err != error::NoError) {
		err = err.FollowedBy(
			script_runner.RunScripts(executor::State::Download, executor::Action::Error));
		err = err.FollowedBy(update_module.Cleanup());
		err = err.FollowedBy(RemoveStateData(main_context.GetMenderStoreDB()));
		return {Result::FailedNothingDone, err};
	}

	return {Result::Downloaded, error::NoError};
}

ResultAndError DoInstallState(
	Context &context,
	StateData &data,
	update_module::UpdateModule &update_module) {
	const auto &default_paths {main_context.GetConfig().paths};
	events::EventLoop loop;
	auto script_runner {executor::ScriptRunner(
		loop,
		chrono::seconds {main_context.GetConfig().state_script_timeout_seconds},
		chrono::seconds {main_context.GetConfig().state_script_retry_interval_seconds},
		chrono::seconds {main_context.GetConfig().state_script_retry_timeout_seconds},
		default_paths.GetArtScriptsPath(),
		default_paths.GetRootfsScriptsPath())};

	// Install Enter
	auto err = script_runner.RunScripts(executor::State::ArtifactInstall, executor::Action::Enter);
	if (err != error::NoError) {
		auto install_leave_error {
			script_runner.RunScripts(executor::State::ArtifactInstall, executor::Action::Error)};
		log::Error(
			"Failure during Install Enter script execution: "
			+ err.FollowedBy(install_leave_error).String());
		return InstallationFailureHandler(main_context, data, update_module);
	}

	err = update_module.ArtifactInstall();
	if (err != error::NoError) {
		auto install_leave_error {
			script_runner.RunScripts(executor::State::ArtifactInstall, executor::Action::Error)};
		log::Error("Installation failed: " + err.FollowedBy(install_leave_error).String());
		return InstallationFailureHandler(main_context, data, update_module);
	}

	// Install Leave
	err = script_runner.RunScripts(executor::State::ArtifactInstall, executor::Action::Leave);
	if (err != error::NoError) {
		auto install_leave_error {
			script_runner.RunScripts(executor::State::ArtifactInstall, executor::Action::Error)};

		log::Error(
			"Failure during Install Leave script execution: "
			+ err.FollowedBy(install_leave_error).String());

		return InstallationFailureHandler(main_context, data, update_module);
	}

	auto reboot = update_module.NeedsReboot();
	if (!reboot) {
		log::Error("Could not query for reboot: " + reboot.error().String());
		return InstallationFailureHandler(main_context, data, update_module);
	}

	auto rollback_support = update_module.SupportsRollback();
	if (!rollback_support) {
		log::Error("Could not query for rollback support: " + rollback_support.error().String());
		return InstallationFailureHandler(main_context, data, update_module);
	}

	if (rollback_support.value()) {
		if (reboot.value() != update_module::RebootAction::No) {
			return {Result::InstalledRebootRequired, error::NoError};
		} else {
			return {Result::Installed, error::NoError};
		}
	}

	cout << "Update Module doesn't support rollback. Committing immediately." << endl;

	auto result = DoCommit(main_context, data, update_module);
	if (result.result == Result::Committed) {
		if (reboot.value() != update_module::RebootAction::No) {
			result.result = Result::InstalledAndCommittedRebootRequired;
		} else {
			result.result = Result::InstalledAndCommitted;
		}
	}
	return result;
}

ResultAndError DoCommit(
	Context &context,
	StateData &data,
	update_module::UpdateModule &update_module) {
	const auto &default_paths {main_context.GetConfig().paths};
	events::EventLoop loop;
	auto script_runner {executor::ScriptRunner(
		loop,
		chrono::seconds {main_context.GetConfig().state_script_timeout_seconds},
		chrono::seconds {main_context.GetConfig().state_script_retry_interval_seconds},
		chrono::seconds {main_context.GetConfig().state_script_retry_timeout_seconds},
		default_paths.GetArtScriptsPath(),
		default_paths.GetRootfsScriptsPath())};
	// Commit Enter
	auto err = script_runner.RunScripts(executor::State::ArtifactCommit, executor::Action::Enter);
	if (err != error::NoError) {
		log::Error("Commit Enter State Script error: " + err.String());
		// Commit Error
		auto commit_error =
			script_runner.RunScripts(executor::State::ArtifactCommit, executor::Action::Error);
		if (commit_error != error::NoError) {
			log::Error("Commit Error State Script error: " + commit_error.String());
		}
		return InstallationFailureHandler(main_context, data, update_module);
	}

	err = update_module.ArtifactCommit();
	if (err != error::NoError) {
		log::Error("Commit failed: " + err.String());
		// Commit Error
		auto commit_error =
			script_runner.RunScripts(executor::State::ArtifactCommit, executor::Action::Error);
		if (commit_error != error::NoError) {
			log::Error("Commit Error State Script error: " + commit_error.String());
		}
		return InstallationFailureHandler(main_context, data, update_module);
	}

	auto result = Result::Committed;
	error::Error return_err;

	// Commit Leave
	err = script_runner.RunScripts(executor::State::ArtifactCommit, executor::Action::Leave);
	if (err != error::NoError) {
		auto leave_err =
			script_runner.RunScripts(executor::State::ArtifactCommit, executor::Action::Error);
		log::Error("Error during Commit Leave script: " + err.FollowedBy(leave_err).String());
		result = Result::InstalledButFailedInPostCommit;
		return_err = err.FollowedBy(leave_err);
	}


	err = update_module.Cleanup();
	if (err != error::NoError) {
		result = Result::InstalledButFailedInPostCommit;
		return_err = return_err.FollowedBy(err);
	}

	err = main_context.CommitArtifactData(
		data.artifact_name,
		data.artifact_group,
		data.artifact_provides,
		data.artifact_clears_provides,
		[](database::Transaction &txn) {
			return txn.Remove(context::MenderContext::standalone_state_key);
		});
	if (err != error::NoError) {
		result = Result::InstalledButFailedInPostCommit;
		return_err = return_err.FollowedBy(err);
	}

	return {result, return_err};
}

ResultAndError DoRollback(
	Context &context,
	StateData &data,
	update_module::UpdateModule &update_module) {
	auto exp_rollback_support = update_module.SupportsRollback();
	if (!exp_rollback_support) {
		return {Result::NoRollback, exp_rollback_support.error()};
	}

	if (exp_rollback_support.value()) {
		auto default_paths {main_context.GetConfig().paths};
		events::EventLoop loop;
		auto script_runner {executor::ScriptRunner(
			loop,
			chrono::seconds {main_context.GetConfig().state_script_timeout_seconds},
			chrono::seconds {main_context.GetConfig().state_script_retry_interval_seconds},
			chrono::seconds {main_context.GetConfig().state_script_retry_timeout_seconds},
			default_paths.GetArtScriptsPath(),
			default_paths.GetRootfsScriptsPath())};

		// Rollback Enter
		auto err =
			script_runner.RunScripts(executor::State::ArtifactRollback, executor::Action::Enter);
		if (err != error::NoError) {
			return {Result::RollbackFailed, err};
		}
		err = update_module.ArtifactRollback();
		if (err != error::NoError) {
			return {Result::RollbackFailed, err};
		}
		// Rollback Leave
		err = script_runner.RunScripts(executor::State::ArtifactRollback, executor::Action::Leave);
		if (err != error::NoError) {
			return {Result::RollbackFailed, err};
		}
		return {Result::RolledBack, error::NoError};
	} else {
		return {Result::NoRollback, error::NoError};
	}
}

ResultAndError DoEmptyPayloadArtifact(
	Context &context, StateData &data, InstallOptions options) {
	if (options != InstallOptions::NoStdout) {
		cout << "Installing artifact..." << endl;
		cout << "Artifact with empty payload. Committing immediately." << endl;
	}

	auto err = main_context.CommitArtifactData(
		data.artifact_name,
		data.artifact_group,
		data.artifact_provides,
		data.artifact_clears_provides,
		[](database::Transaction &txn) { return error::NoError; });
	if (err != error::NoError) {
		return {Result::InstalledButFailedInPostCommit, err};
	}
	return {Result::InstalledAndCommitted, err};
}

ResultAndError InstallationFailureHandler(
	Context &context,
	StateData &data,
	update_module::UpdateModule &update_module) {
	error::Error err;

	auto default_paths {main_context.GetConfig().paths};
	events::EventLoop loop;
	auto script_runner {executor::ScriptRunner(
		loop,
		chrono::seconds {main_context.GetConfig().state_script_timeout_seconds},
		chrono::seconds {main_context.GetConfig().state_script_retry_interval_seconds},
		chrono::seconds {main_context.GetConfig().state_script_retry_timeout_seconds},
		default_paths.GetArtScriptsPath(),
		default_paths.GetRootfsScriptsPath())};

	auto result = DoRollback(main_context, data, update_module);
	switch (result.result) {
	case Result::RolledBack:
		result.result = Result::FailedAndRolledBack;
		break;
	case Result::NoRollback:
		result.result = Result::FailedAndNoRollback;
		break;
	case Result::RollbackFailed:
		result.result = Result::FailedAndRollbackFailed;
		break;
	default:
		// Should not happen.
		assert(false);
		return {
			Result::FailedAndRollbackFailed,
			error::MakeError(
				error::ProgrammingError,
				"Unexpected result in InstallationFailureHandler. This is a bug.")};
	}

	// Failure Enter
	err = script_runner.RunScripts(
		executor::State::ArtifactFailure, executor::Action::Enter, executor::OnError::Ignore);
	if (err != error::NoError) {
		log::Error("Failure during execution of ArtifactFailure Enter script: " + err.String());
		result.result = Result::FailedAndRollbackFailed;
		result.err = result.err.FollowedBy(err);
	}

	err = update_module.ArtifactFailure();
	if (err != error::NoError) {
		result.result = Result::FailedAndRollbackFailed;
		result.err = result.err.FollowedBy(err);
	}

	// Failure Leave
	err = script_runner.RunScripts(
		executor::State::ArtifactFailure, executor::Action::Leave, executor::OnError::Ignore);
	if (err != error::NoError) {
		log::Error("Failure during execution of ArtifactFailure Enter script: " + err.String());
		result.result = Result::FailedAndRollbackFailed;
		result.err = result.err.FollowedBy(err);
	}

	err = update_module.Cleanup();
	if (err != error::NoError) {
		result.result = Result::FailedAndRollbackFailed;
		result.err = result.err.FollowedBy(err);
	}

	if (result.result == Result::FailedAndRolledBack) {
		err = RemoveStateData(main_context.GetMenderStoreDB());
	} else {
		err = CommitBrokenArtifact(main_context, data);
	}
	if (err != error::NoError) {
		result.result = Result::FailedAndRollbackFailed;
		result.err = result.err.FollowedBy(err);
	}

	return result;
}

error::Error CommitBrokenArtifact(context::MenderContext &main_context, StateData &data) {
	data.artifact_name += main_context.broken_artifact_name_suffix;
	if (data.artifact_provides) {
		data.artifact_provides.value()["artifact_name"] = data.artifact_name;
	}
	return main_context.CommitArtifactData(
		data.artifact_name,
		data.artifact_group,
		data.artifact_provides,
		data.artifact_clears_provides,
		[](database::Transaction &txn) {
			return txn.Remove(context::MenderContext::standalone_state_key);
		});
}

} // namespace standalone
} // namespace update
} // namespace mender
