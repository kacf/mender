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

namespace mender {
namespace update {
namespace standalone {

// This is used to catch mistakes where we don't set the error before exiting the state machine.
static const kFallbackError = error::MakeError(
	error::ProgrammingError,
	"Returned from standalone operation without setting error code.");

static void UpdateResult(ResultAndError &result, const ResultAndError &update) {
	if (result.err == kFallbackError or result.err == error::NoError) {
		result.err = update.err;
	} else {
		result.err = result.err.FollowedBy(update.err);
	}

	if (static_cast<int>(result.result) > static_cast<int>(update.result)) {
		result.err = result.err.FollowedBy(
			error::ProgrammingError,
			"Result set twice, tried to update " +
			to_string(static_cast<int>(result.result)) +
			" with " +
			to_string(static_cast<int>(update.result)));
	}
	result.result = update.result;
}

void Prepare() {
	const auto &default_paths {main_context.GetConfig().paths};
	ctx.script_runner = make_unique<executor::ScriptRunner>(
		ctx.loop,
		chrono::seconds {main_context.GetConfig().state_script_timeout_seconds},
		chrono::seconds {main_context.GetConfig().state_script_retry_interval_seconds},
		chrono::seconds {main_context.GetConfig().state_script_retry_timeout_seconds},
		default_paths.GetArtScriptsPath(),
		default_paths.GetRootfsScriptsPath());
}

error::Error DoDownloadState(
	Context &ctx,
	artifact::Artifact &artifact) {
	auto payload = artifact.Next();
	if (!payload) {
		return {Result::FailedNothingDone, payload.error()};
	}

	auto &main_context = ctx.main_context;

	// ProvidePayloadFileSizes
	auto with_sizes = update_module.ProvidePayloadFileSizes();
	if (!with_sizes) {
		log::Error("Could not query for provide file sizes: " + with_sizes.error().String());
		return with_sizes.error();
	}

	// Download Enter
	auto err = script_runner.RunScripts(executor::State::Download, executor::Action::Enter);
	if (err != error::NoError) {
		return err;
	}
	if (with_sizes.value()) {
		err = update_module.DownloadWithFileSizes(payload.value());
	} else {
		err = update_module.Download(payload.value());
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

	// Download Leave
	err = script_runner.RunScripts(executor::State::Download, executor::Action::Leave);
	if (err != error::NoError) {
		return err;
	}

	return error::NoError;
}

void DownloadState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	auto exp_in_progress = LoadStateData(main_context.GetMenderStoreDB());
	if (!exp_in_progress) {
		return {Result::FailedNothingDone, exp_in_progress.error()};
	}
	auto &in_progress = exp_in_progress.value();

	if (in_progress) {
		ctx.result_and_error = {
			Result::FailedNothingDone,
			error::Error(
				make_error_condition(errc::operation_in_progress),
				"Update already in progress. Please commit or roll back first")};
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	io::ReaderPtr artifact_reader;

	shared_ptr<events::EventLoop> event_loop;
	http::ClientPtr http_client;

	if (ctx.artifact_src.find("http://") == 0 || ctx.artifact_src.find("https://") == 0) {
		event_loop = make_shared<events::EventLoop>();
		http_client =
			make_shared<http::Client>(main_context.GetConfig().GetHttpClientConfig(), *event_loop);
		auto reader = ReaderFromUrl(*event_loop, *http_client, src);
		if (!reader) {
			UpdateResult(ctx.result_and_error, {Result::FailedNothingDone, reader.error()});
			poster.PostEvent(StateEvent::Failure);
			return;
		}
		artifact_reader = reader.value();
	} else {
		auto stream = io::OpenIfstream(ctx.artifact_src);
		if (!stream) {
			UpdateResult(ctx.result_and_error, {Result::FailedNothingDone, stream.error()});
			poster.PostEvent(StateEvent::Failure);
			return;
		}
		auto file_stream = make_shared<ifstream>(std::move(stream.value()));
		artifact_reader = make_shared<io::StreamReader>(file_stream);
	}

	string art_scripts_path = main_context.GetConfig().paths.GetArtScriptsPath();

	// Clear the artifact scripts directory so we don't risk old scripts lingering.
	auto err = path::DeleteRecursively(art_scripts_path);
	if (err != error::NoError) {
		UpdateResult(ctx.result_and_error, {Result::FailedNothingDone, err.WithContext("When preparing to parse artifact")});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	artifact::config::ParserConfig config {
		.artifact_scripts_filesystem_path = main_context.GetConfig().paths.GetArtScriptsPath(),
		.artifact_scripts_version = 3,
		.artifact_verify_keys = main_context.GetConfig().artifact_verify_keys,
		.verify_signature = verify_signature,
	};

	auto exp_parser = artifact::Parse(*artifact_reader, config);
	if (!exp_parser) {
		UpdateResult(ctx.result_and_error, {Result::FailedNothingDone, exp_parser.error()});
		poster.PostEvent(StateEvent::Failure);
		return;
	}
	auto &parser = exp_parser.value();

	auto exp_header = artifact::View(parser, 0);
	if (!exp_header) {
		UpdateResult(ctx.result_and_error, {Result::FailedNothingDone, exp_header.error()});
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

	// TODO: Make a split here? Needs cleanup after, but not before
	// Decision: No, but make sure that cleanup takes into account that update_module can be nullptr.
	err = update_module.CleanAndPrepareFileTree(update_module.GetUpdateModuleWorkDir(), header);
	if (err != error::NoError) {
		UpdateResult(ctx.result_and_error, {Result::FailedNothingDone, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	StateData data = StateDataFromPayloadHeaderView(header);

	auto exp_matches = main_context.MatchesArtifactDepends(header.header);
	if (!exp_matches) {
		UpdateResult(ctx.result_and_error, {Result::FailedNothingDone, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	} else if (!exp_matches.value()) {
		// reasons already logged
		UpdateResult(ctx.result_and_error, {Result::FailedNothingDone, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	auto result = DoDownloadState(main_context, data, parser, update_module);
	if (result.result != Result::Downloaded) {
		ctx.result_and_error = result;
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	data.completed_state = "Download";
	err = SaveStateData(main_context.GetMenderStoreDB(), data);
	if (err != error::NoError) {
		// Yes, we downloaded, but remember that the Download state is not supposed to have
		// any system-visible effects.
		UpdateResult(ctx.result_and_error, {Result::FailedNothingDone, err});
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
	auto reboot = update_module.NeedsReboot();
	if (!reboot) {
		log::Error("Could not query for reboot: " + reboot.error().String());
		UpdateResult(ctx.result_and_error, {Result::FailedNoRollbackAttempted, reboot.error()});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	auto rollback_support = update_module.SupportsRollback();
	if (!rollback_support) {
		log::Error("Could not query for rollback support: " + rollback_support.error().String());
		UpdateResult(ctx.result_and_error, {Result::FailedNoRollbackAttempted, rollback_support.error()});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	if (rollback_support.value()) {
		if (reboot.value() != update_module::RebootAction::No) {
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
	err = update_module.ArtifactCommit();
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
	auto rollback_support = update_module.SupportsRollback();
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
	err = update_module.ArtifactRollback();
	if (err != error::NoError) {
		UpdateResult(ctx.result_and_error, {Result::RollbackFailed, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	UpdateResult(ctx.result_and_error, {Result::RolledBack, error::NoError});
	poster.PostEvent(StateEvent::Success);
}

void ArtifactFailureState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	err = update_module.ArtifactFailure();
	if (err != error::NoError) {
		UpdateResult(ctx.result_and_error, {Result::RollbackFailed, err});
		poster.PostEvent(StateEvent::Failure);
		return;
	}

	poster.PostEvent(StateEvent::Success);
}

void CleanupState::OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) {
	// If this is null, then it is simply a no-op, the update did not even get started.
	if (update_module != nullptr) {
		err = update_module.Cleanup();
		if (err != error::NoError) {
			UpdateResult(ctx.result_and_error, {Result::RollbackFailed, err});
			poster.PostEvent(StateEvent::Failure);
			return;
		}
	}

	poster.PostEvent(StateEvent::Success);
}

} // namespace standalone
} // namespace update
} // namespace mender
