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

#ifndef MENDER_UPDATE_STANDALONE_HPP
#define MENDER_UPDATE_STANDALONE_HPP

#include <unordered_map>

#include <common/error.hpp>
#include <common/expected.hpp>
#include <common/key_value_database.hpp>
#include <common/json.hpp>
#include <common/optional.hpp>

#include <artifact/artifact.hpp>

#include <mender-update/context.hpp>
#include <mender-update/standalone/context.hpp>

namespace mender {
namespace update {
namespace standalone {

using namespace std;

namespace database = mender::common::key_value_database;
namespace error = mender::common::error;
namespace expected = mender::common::expected;
namespace json = mender::common::json;

namespace artifact = mender::artifact;

// Standalone script states:
//
// Download
// ArtifactInstall
// ArtifactCommit (Leave - no error handling)
// ArtifactRollback - no error handling
// ArtifactFailure - no error handling

// Return true if there is standalone data (indicating that an update is in progress), false if not.
// Note: StateData is expected to be empty. IOW it will not clear fields that happen to be
// empty in the database.
ExpectedOptionalStateData LoadStateData(database::KeyValueDatabase &db);

StateData StateDataFromPayloadHeaderView(const artifact::PayloadHeaderView &header);
error::Error SaveStateData(database::KeyValueDatabase &db, const StateData &data);

error::Error RemoveStateData(database::KeyValueDatabase &db);

class StateMachine {
public:
	StateMachine();

private:
	common::state_machine::StateMachine state_machine_;

	ScriptRunnerState download_enter_state_;
	DownloadState download_state_;
	ScriptRunnerState download_leave_state_;
	ScriptRunnerState download_error_state_;

	ScriptRunnerState artifact_install_enter_state_;
	ArtifactInstallState artifact_install_state_;
	ScriptRunnerState artifact_install_leave_state_;
	ScriptRunnerState artifact_install_error_state_;

	RebootAndRollbackQueryState reboot_and_rollback_query_state_;

	ScriptRunnerState artifact_commit_enter_state_;
	ArtifactCommitState artifact_commit_state_;
	ScriptRunnerState artifact_commit_leave_state_;
	ScriptRunnerState artifact_commit_error_state_;

	RollbackQueryState rollback_query_state_;

	ScriptRunnerState artifact_rollback_enter_state_;
	ArtifactRollbackState artifact_rollback_state_;
	ScriptRunnerState artifact_rollback_leave_state_;

	ScriptRunnerState artifact_failure_enter_state_;
	ArtifactFailureState artifact_failure_state_;
	ScriptRunnerState artifact_failure_leave_state_;

	CleanupState cleanup_state_;
};

ResultAndError Download(Context &context);
ResultAndError Install(Context &context);
ResultAndError DownloadAndInstall(Context &context);
ResultAndError Commit(Context &context);
ResultAndError Rollback(Context &context);

ResultAndError DoDownloadState(
	Context &context,
	StateData &data,
	artifact::Artifact &artifact,
	update_module::UpdateModule &update_module);
ResultAndError DoInstallState(
	Context &context,
	StateData &data,
	update_module::UpdateModule &update_module);
ResultAndError DoCommit(
	Context &context,
	StateData &data,
	update_module::UpdateModule &update_module);
ResultAndError DoRollback(
	Context &context,
	StateData &data,
	update_module::UpdateModule &update_module);

ResultAndError DoEmptyPayloadArtifact(
	Context &context,
	StateData &data,
	InstallOptions options = InstallOptions::None);

ResultAndError InstallationFailureHandler(
	Context &context,
	StateData &data,
	update_module::UpdateModule &update_module);

error::Error CommitBrokenArtifact(Context &context, StateData &data);

} // namespace standalone
} // namespace update
} // namespace mender

#endif // MENDER_UPDATE_STANDALONE_HPP
