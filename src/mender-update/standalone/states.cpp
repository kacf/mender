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

#include <mender-update/standalone/states.hpp>

#include <common/http.hpp>
#include <common/events_io.hpp>
#include <common/io.hpp>
#include <common/key_value_database.hpp>
#include <common/log.hpp>
#include <common/path.hpp>

namespace mender {
namespace update {
namespace standalone {

namespace database = mender::common::key_value_database;
namespace events = mender::common::events;
namespace http = mender::common::http;
namespace io = mender::common::io;
namespace log = mender::common::log;
namespace path = mender::common::path;

// This is used to catch mistakes where we don't set the error before exiting the state machine.
static const error::Error kFallbackError = error::MakeError(
	error::ProgrammingError,
	"Returned from standalone operation without setting error code.");

static void UpdateResult(ResultAndError &result, const ResultAndError &update) {
	if (result.err == kFallbackError or result.err == error::NoError) {
		result.err = update.err;
	} else {
		result.err = result.err.FollowedBy(update.err);
	}

	result.result = result.result | update.result;
}

ResultAndError DoEmptyPayloadArtifact(
	Context &context, StateData &data, InstallOptions options) {
	if (options != InstallOptions::NoStdout) {
		cout << "Installing artifact..." << endl;
		cout << "Artifact with empty payload. Committing immediately." << endl;
	}

	auto err = context.main_context.CommitArtifactData(
		data.artifact_name,
		data.artifact_group,
		data.artifact_provides,
		data.artifact_clears_provides,
		[](database::Transaction &txn) { return error::NoError; });
	if (err != error::NoError) {
		return {Result::Failed | Result::FailedInPostCommit, err};
	}
	return {Result::Downloaded | Result::Installed | Result::Committed, err};
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

error::Error DoDownloadState(
	Context &ctx,
	artifact::Artifact &artifact) {

	auto payload = artifact.Next();
	if (!payload) {
		return payload.error();
	}

	// ProvidePayloadFileSizes
	auto with_sizes = ctx.update_module->ProvidePayloadFileSizes();
	if (!with_sizes) {
		log::Error("Could not query for provide file sizes: " + with_sizes.error().String());
		return with_sizes.error();
	}

	error::Error err;
	if (with_sizes.value()) {
		err = ctx.update_module->DownloadWithFileSizes(payload.value());
	} else {
		err = ctx.update_module->Download(payload.value());
	}
	if (err != error::NoError) {
		return err;
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
		return err;
	}

	return error::NoError;
}

// TODO Go through all UpdateResult and make sure they have right parameters.

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

void DownloadState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	io::ReaderPtr artifact_reader;

	shared_ptr<events::EventLoop> event_loop;
	http::ClientPtr http_client;

	auto &main_context = ctx.main_context;

	if (ctx.artifact_src.find("http://") == 0 || ctx.artifact_src.find("https://") == 0) {
		event_loop = make_shared<events::EventLoop>();
		http_client =
			make_shared<http::Client>(main_context.GetConfig().GetHttpClientConfig(), *event_loop);
		auto reader = ReaderFromUrl(*event_loop, *http_client, ctx.artifact_src);
		if (!reader) {
			UpdateResult(ctx.result_and_error, {Result::Failed, reader.error()});
			poster.PostEvent(StateEvent::Failure);
			return;
		}
		artifact_reader = reader.value();
	} else {
		auto stream = io::OpenIfstream(ctx.artifact_src);
		if (!stream) {
			UpdateResult(ctx.result_and_error, {Result::Failed, stream.error()});
			poster.PostEvent(StateEvent::Failure);
			return;
		}
		auto file_stream = make_shared<ifstream>(std::move(stream.value()));
		artifact_reader = make_shared<io::StreamReader>(file_stream);
	}

	if (ctx.options != InstallOptions::NoStdout) {
		cout << "Streaming artifact..." << endl;
	}

	string art_scripts_path = main_context.GetConfig().paths.GetArtScriptsPath();

	// Clear the artifact scripts directory so we don't risk old scripts lingering.
	auto err = path::DeleteRecursively(art_scripts_path);
	if (err != error::NoError) {
		UpdateResult(ctx.result_and_error, {Result::Failed, err.WithContext("When preparing to parse artifact")});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	artifact::config::ParserConfig config {
		.artifact_scripts_filesystem_path = main_context.GetConfig().paths.GetArtScriptsPath(),
		.artifact_scripts_version = 3,
		.artifact_verify_keys = main_context.GetConfig().artifact_verify_keys,
		.verify_signature = ctx.verify_signature,
	};

	auto exp_parser = artifact::Parse(*artifact_reader, config);
	if (!exp_parser) {
		UpdateResult(ctx.result_and_error, {Result::Failed, exp_parser.error()});
		poster.PostEvent(StateEvent::Failure);
		return;
	}
	auto &parser = exp_parser.value();

	auto exp_header = artifact::View(parser, 0);
	if (!exp_header) {
		UpdateResult(ctx.result_and_error, {Result::Failed, exp_header.error()});
		poster.PostEvent(StateEvent::Failure);
		return;
	}
	auto &header = exp_header.value();

	if (header.header.payload_type == "") {
		auto data = StateDataFromPayloadHeaderView(header);
		// TODO
		return DoEmptyPayloadArtifact(main_context, data, options);
		poster.PostEvent(StateEvent::EmptyPayloadArtifact);
		return;
	}

	ctx.update_module = make_unique<update_module::UpdateModule>(main_context, header.header.payload_type);

	err = ctx.update_module->CleanAndPrepareFileTree(ctx.update_module->GetUpdateModuleWorkDir(), header);
	if (err != error::NoError) {
		UpdateResult(ctx.result_and_error, {Result::Failed, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	StateData data = StateDataFromPayloadHeaderView(header);

	auto exp_matches = main_context.MatchesArtifactDepends(header.header);
	if (!exp_matches) {
		UpdateResult(ctx.result_and_error, {Result::Failed, exp_matches.error()});
		poster.PostEvent(StateEvent::Failure);
		return;
	} else if (!exp_matches.value()) {
		// reasons already logged
		UpdateResult(ctx.result_and_error, {Result::Failed, error::NoError});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	auto err = DoDownloadState(main_context, data, parser, ctx.update_module);
	if (err != error::NoError) {
		UpdateResult(ctx.result_and_error, {Result::Failed, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	data.completed_state = CompletedStates::download;
	err = SaveStateData(main_context.GetMenderStoreDB(), data);
	if (err != error::NoError) {
		// Yes, we downloaded, but remember that the Download state is not supposed to have
		// any system-visible effects.
		UpdateResult(ctx.result_and_error, {Result::Failed, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	if (options != InstallOptions::NoStdout) {
		cout << "Installing artifact..." << endl;
	}

	ctx.result_and_error = result;
	poster.PostEvent(StateEvent::Success);
}

void ArtifactInstallState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	if (ctx.options != InstallOptions::NoStdout) {
		cout << "Installing artifact..." << endl;
	}

	auto err = ctx.update_module.ArtifactInstall();
	if (err != error::NoError) {
		log::Error("Installation failed: " + err.FollowedBy(install_leave_error).String());
		UpdateResult(ctx.result_and_error, {Result::FailedNoRollbackAttempted, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	UpdateResult(ctx.result_and_error, {Result::Installed, error::NoError});
	poster.PostEvent(StateEvent::Success);
}

void RebootAndRollbackQueryState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	auto reboot = ctx.update_module->NeedsReboot();
	if (!reboot) {
		log::Error("Could not query for reboot: " + reboot.error().String());
		UpdateResult(ctx.result_and_error, {Result::FailedNoRollbackAttempted, reboot.error()});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	auto rollback_support = ctx.update_module->SupportsRollback();
	if (!rollback_support) {
		log::Error("Could not query for rollback support: " + rollback_support.error().String());
		UpdateResult(ctx.result_and_error, {Result::FailedNoRollbackAttempted, rollback_support.error()});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	if (rollback_support.value()) {
		if (reboot.value() != ctx.update_module::RebootAction::No) {
			UpdateResult(ctx.result_and_error, {Result::InstalledRebootRequired, error::NoError});
		} else {
			UpdateResult(ctx.result_and_error, {Result::Installed, error::NoError});
		}
		poster.PostEvent(StateEvent::NeedsInteraction);
		return;
	}

	cout << "Update Module doesn't support rollback. Committing immediately." << endl;
	poster.PostEvent(StateEvent::Success);
}

void ArtifactCommitState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	err = ctx.update_module->ArtifactCommit();
	if (err != error::NoError) {
		log::Error("Commit failed: " + err.String());
		UpdateResult(ctx.result_and_error, {Result::FailedNoRollbackAttempted, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	UpdateResult(ctx.result_and_error, {Result::Installed, error::NoError});
	poster.PostEvent(StateEvent::Success);
}

void RollbackQueryState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	auto rollback_support = ctx.update_module->SupportsRollback();
	if (!rollback_support) {
		log::Error("Could not query for rollback support: " + rollback_support.error().String());
		UpdateResult(ctx.result_and_error, {Result::FailedAndRollbackFailed, rollback_support.error()});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	if (not rollback_support.value()) {
		UpdateResult(ctx.result_and_error, {Result::FailedAndNoRollback, error::NoError});
		poster.PostEvent(StateEvent::NothingToDo);
		return;
	}

	poster.PostEvent(StateEvent::Success);
}

void ArtifactRollbackState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	err = ctx.update_module->ArtifactRollback();
	if (err != error::NoError) {
		UpdateResult(ctx.result_and_error, {Result::RollbackFailed, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	UpdateResult(ctx.result_and_error, {Result::RolledBack, error::NoError});
	poster.PostEvent(StateEvent::Success);
}

void ArtifactFailureState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	err = ctx.update_module->ArtifactFailure();
	if (err != error::NoError) {
		UpdateResult(ctx.result_and_error, {Result::RollbackFailed, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	poster.PostEvent(StateEvent::Success);
}

void CleanupState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	// If this is null, then it is simply a no-op, the update did not even get started.
	if (ctx.update_module != nullptr) {
		err = ctx.update_module->Cleanup();
		if (err != error::NoError) {
			UpdateResult(ctx.result_and_error, {Result::RollbackFailed, err});
			poster.PostEvent(StateEvent::Failure);
			return;
		}
	}

	auto &data = ctx.state_data;

	error::Error err;
	if (data.rolled_back) {
		// Successful rollback.
		err = db.Remove(context::MenderContext::standalone_state_key);
	} else {
		if (data.failed) {
			// Unsuccessful rollback or missing rollback support.
			data.artifact_name += ctx.main_context.broken_artifact_name_suffix;
			if (data.artifact_provides) {
				data.artifact_provides.value()["artifact_name"] = data.artifact_name;
			}
			// Fall through to success case.
		}
		// Commit artifact data and remove state data
		err = main_context.CommitArtifactData(
			data.artifact_name,
			data.artifact_group,
			data.artifact_provides,
			data.artifact_clears_provides,
			[](database::Transaction &txn) {
				return txn.Remove(context::MenderContext::standalone_state_key);
			});
	}
	if (err != error::NoError) {
		err = err.WithContext("Error while updating database");
		UpdateResult(ctx.result_and_error, {Result::RollbackFailed, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	poster.PostEvent(StateEvent::Success);
}

void ScriptRunnerState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	auto err = script_runner.RunScripts(state_, action_, on_error_);
	if (err != error::NoError) {
		log::Error("Error executing script: " + err.String());
		UpdateResult(ctx.result_and_error, {result_on_error_, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	poster.PostEvent(StateEvent::Success);
}

void ExitState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	loop_.Stop();
}

} // namespace standalone
} // namespace update
} // namespace mender
