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

#include <mender-update/standalone/context.hpp>

namespace mender {
namespace update {
namespace standalone {

const string StateDataKeys::version {"Version"};
const string StateDataKeys::artifact_name {"ArtifactName"};
const string StateDataKeys::artifact_group {"ArtifactGroup"};
const string StateDataKeys::artifact_provides {"ArtifactTypeInfoProvides"};
const string StateDataKeys::artifact_clears_provides {"ArtifactClearsProvides"};
const string StateDataKeys::payload_types {"PayloadTypes"};
const string StateDataKeys::completed_state {"CompletedState"};
const string StateDataKeys::failed {"Failed"};
const string StateDataKeys::rolled_back {"RolledBack"};

const string CompletedStates::download_leave {"Download_Leave"};
const string CompletedStates::artifact_install_leave {"ArtifactInstall_Leave"};
const string CompletedStates::artifact_commit {"ArtifactCommit"};
const string CompletedStates::artifact_commit_leave {"ArtifactCommit_Leave"};
const string CompletedStates::artifact_rollback_leave {"ArtifactRollback_Leave"};
const string CompletedStates::artifact_failure_leave {"ArtifactFailure_Leave"};

} // namespace standalone
} // namespace update
} // namespace mender
