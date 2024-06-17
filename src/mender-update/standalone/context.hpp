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

#ifndef MENDER_UPDATE_STANDALONE_CONTEXT_HPP
#define MENDER_UPDATE_STANDALONE_CONTEXT_HPP

#include <unordered_map>

#include <common/error.hpp>
#include <common/expected.hpp>
#include <common/optional.hpp>

#include <artifact/artifact.hpp>

#include <mender-update/update_module/v3/update_module.hpp>

namespace mender {
namespace update {
namespace standalone {

using namespace std;

namespace context = mender::update::context;

namespace update_module = mender::update::update_module::v3;

// The keys and data, respectively, of the JSON object living under the `standalone_data_key` entry
// in the database. Be sure to take into account upgrades when changing this.
struct StateDataKeys {
	static const string version;
	static const string artifact_name;
	static const string artifact_group;
	static const string artifact_provides;
	static const string artifact_clears_provides;
	static const string payload_types;

	// Introduced in version 2, not valid in version 1.
	//
	// Can theoretically contain any state, similar to how daemon mode works (although it uses
	// different names), but in practice only Download and ArtifactInstall are handled, and
	// exposed in CLI, at the time of writing.

	// TODO: Make constants for this
	static const string completed_state;
};
struct StateData {
	int version;
	string artifact_name;
	string artifact_group;
	optional<unordered_map<string, string>> artifact_provides;
	optional<vector<string>> artifact_clears_provides;
	vector<string> payload_types;
	string completed_state;
};
using ExpectedOptionalStateData = expected::expected<optional<StateData>, error::Error>;

enum class Result {
	NoResult = 0x0,

	// Flags
	NothingDone = 0x0,
	NoUpdateInProgress = 0x1,
	Downloaded = 0x2,
	Installed = 0x4,
	RebootRequired = 0x8,
	Committed = 0x10,
	Failed = 0x20,
	FailedInPostCommit = 0x40,
	NoRollback = 0x80,
	RolledBack = 0x100,
	RollbackFailed = 0x200,
};

inline bool ResultIs(Result result, Result flags) {
	return static_cast<int>(result) == static_cast<int>(flags);
}

inline bool ResultContains(Result result, Result flags) {
	return (static_cast<int>(result) & static_cast<int>(flags)) == static_cast<int>(flags);
}

struct ResultAndError {
	Result result;
	error::Error err;
};

enum class InstallOptions {
	None,
	NoStdout,
};

struct Context {
	events::EventLoop loop;

	context::MenderContext main_context;
	StateData state_data;

	string artifact_src;

	unique_ptr<update_module::UpdateModule> update_module;
	unique_ptr<executor::ScriptRunner> script_runner;

	artifact::config::Signature verify_signature;
	InstallOptions options;

	ResultAndError result_and_error;
};

} // namespace standalone
} // namespace update
} // namespace mender

#endif // MENDER_UPDATE_STANDALONE_CONTEXT_HPP
