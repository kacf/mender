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

#include <common/events_io.hpp>
#include <common/log.hpp>
#include <common/conf/paths.hpp>

namespace mender {
namespace update {
namespace standalone {

namespace events = mender::common::events;
namespace io = mender::common::io;
namespace log = mender::common::log;
namespace paths = mender::common::conf::paths;

const string StandaloneDataKeys::version {"Version"};
const string StandaloneDataKeys::artifact_name {"ArtifactName"};
const string StandaloneDataKeys::artifact_group {"ArtifactGroup"};
const string StandaloneDataKeys::artifact_provides {"ArtifactTypeInfoProvides"};
const string StandaloneDataKeys::artifact_clears_provides {"ArtifactClearsProvide"};
const string StandaloneDataKeys::payload_types {"PayloadTypes"};

template <typename T>
expected::expected<T, error::Error> GetEntry(const json::Json &json, const string &key, bool missing_ok) {
	auto exp_value = json.Get(key);
	if (!exp_value) {
		if (missing_ok && exp_value.error().code != json::MakeError(json::KeyError, "").code) {
			return T();
		} else {
			auto err = exp_value.error();
			err.message += ": Could not get `" + key + "` from state data";
			return expected::unexpected(err);
		}
	} else {
		return exp_value.value().Get<T>();
	}
}

expected::ExpectedBool LoadStandaloneData(database::KeyValueDatabase &db, StandaloneData &dst) {
	StandaloneDataKeys keys;

	auto exp_bytes = db.Read(context::MenderContext::standalone_state_key);
	if (!exp_bytes) {
		auto &err = exp_bytes.error();
		if (err.code == database::MakeError(database::KeyError, "").code) {
			return false;
		} else {
			return expected::unexpected(err);
		}
	}

	auto exp_json = json::Load(string(exp_bytes.value().begin(), exp_bytes.value().end()));
	if (!exp_json) {
		return expected::unexpected(exp_json.error());
	}
	auto &json = exp_json.value();

	auto exp_int = GetEntry<int>(json, keys.version, false);
	if (!exp_int) {
		return expected::unexpected(exp_int.error());
	}
	dst.version = exp_int.value();

	auto exp_string = GetEntry<string>(json, keys.artifact_name, false);
	if (!exp_string) {
		return expected::unexpected(exp_string.error());
	}
	dst.artifact_name = exp_string.value();

	exp_string = GetEntry<string>(json, keys.artifact_group, true);
	if (!exp_string) {
		return expected::unexpected(exp_string.error());
	}
	dst.artifact_group = exp_string.value();

	auto exp_map = GetEntry<json::KeyValueMap>(json, keys.artifact_provides, true);
	if (!exp_map) {
		return expected::unexpected(exp_map.error());
	}
	dst.artifact_provides = exp_map.value();

	auto exp_array = GetEntry<vector<string>>(json, keys.artifact_clears_provides, true);
	if (!exp_array) {
		return expected::unexpected(exp_array.error());
	}
	dst.artifact_clears_provides = exp_array.value();

	exp_array = GetEntry<vector<string>>(json, keys.payload_types, false);
	if (!exp_array) {
		return expected::unexpected(exp_array.error());
	}
	dst.payload_types = exp_array.value();

	if (dst.version != context::MenderContext::standalone_data_version) {
		return expected::unexpected(error::Error(make_error_condition(errc::not_supported), "State data has a version which is not supported by this client"));
	}

	if (dst.artifact_name == "") {
		return expected::unexpected(context::MakeError(context::DatabaseValueError, "`" + keys.artifact_name + "` is empty"));
	}

	if (dst.payload_types.size() == 0) {
		return expected::unexpected(context::MakeError(context::DatabaseValueError, "`" + keys.payload_types + "` is empty"));
	}
	if (dst.payload_types.size() >= 2) {
		return expected::unexpected(error::Error(make_error_condition(errc::not_supported), "`" + keys.payload_types + "` contains multiple payloads"));
	}

	return true;
}

void StandaloneDataFromPayloadHeaderView(const artifact::PayloadHeaderView &header, StandaloneData &dst) {
	dst.version = context::MenderContext::standalone_data_version;
	dst.artifact_name = header.header.artifact_name;
	dst.artifact_group = header.header.artifact_group;
	dst.artifact_provides = header.header.type_info.artifact_provides;
	dst.artifact_clears_provides = header.header.type_info.clears_artifact_provides;
	dst.payload_types.clear();
	dst.payload_types.push_back(header.header.payload_type);
}

error::Error SaveStandaloneData(database::KeyValueDatabase &db, const StandaloneData &data) {
	StandaloneDataKeys keys;
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

	ss << "}";

	string strdata = move(ss.str());
	vector<uint8_t> bytedata(strdata.begin(), strdata.end());

	return db.Write(context::MenderContext::standalone_state_key, bytedata);
}

error::Error RemoveStandaloneData(database::KeyValueDatabase &db) {
	return db.Remove(context::MenderContext::standalone_state_key);
}

ResultAndError Install(context::MenderContext &main_context, const string &src) {
	StandaloneData data;
	auto exp_in_progress = LoadStandaloneData(main_context.GetMenderStoreDB(), data);
	if (!exp_in_progress) {
		return {Result::FailedNothingDone, exp_in_progress.error()};
	}

	if (exp_in_progress.value()) {
		return {Result::FailedNothingDone, error::Error(make_error_condition(errc::operation_in_progress), "Update already in progress. Please commit or roll back first")};
	}

	if (src.find("http://") == 0 || src.find("https://") == 0) {
		return {Result::FailedNothingDone, error::Error(make_error_condition(errc::not_supported), "HTTP not supported yet")};
	}

	auto stream = make_shared<ifstream>(src);
	if (!stream->good()) {
		int errnum = errno;
		return {Result::FailedNothingDone, error::Error(generic_category().default_error_condition(errnum), "Could not open " + src)};
	}
	io::StreamReader artifact_reader(stream);

	artifact::config::ParserConfig config {
		paths::DefaultArtScriptsPath,
	};
	auto parser = artifact::Parse(artifact_reader, config);
	if (!parser) {
		return {Result::FailedNothingDone, parser.error()};
	}

	auto header = artifact::View(parser.value(), 0);
	if (!header) {
		return {Result::FailedNothingDone, header.error()};
	}

	update_module::UpdateModule update_module(main_context, header.value().header.payload_type);

	auto err = update_module.PrepareFileTree(update_module.GetUpdateModuleWorkDir(), header.value());
	if (err != error::NoError) {
		auto err2 = update_module.Cleanup();
		if (err2 != error::NoError) {
			log::Error("Update Module Cleanup failed: " + err2.String());
		}
		return {Result::FailedNothingDone, err};
	}

	StandaloneDataFromPayloadHeaderView(header.value(), data);
	err = SaveStandaloneData(main_context.GetMenderStoreDB(), data);
	if (err != error::NoError) {
		return {Result::FailedNothingDone, err};
	}

	return DoInstallStates(main_context, data, parser.value(), update_module);
}

ResultAndError Commit(context::MenderContext &main_context) {
	StandaloneData data;
	auto exp_in_progress = LoadStandaloneData(main_context.GetMenderStoreDB(), data);
	if (!exp_in_progress) {
		return {Result::FailedNothingDone, exp_in_progress.error()};
	}

	if (!exp_in_progress.value()) {
		return {Result::NoUpdateInProgress, error::NoError};
	}

	update_module::UpdateModule update_module(main_context, data.payload_types[0]);

	return DoCommit(main_context, data, update_module);
}

ResultAndError DoInstallStates(
	context::MenderContext &main_context,
	StandaloneData &data,
	artifact::Artifact &artifact,
	update_module::UpdateModule &update_module) {

	auto payload = artifact.Next();
	// TODO: Bootstrap artifact
	if (!payload) {
		return {Result::FailedNothingDone, payload.error()};
	}

	cout << "Installing artifact..." << endl;

	auto err = update_module.Download(payload.value());
	if (err != error::NoError) {
		auto err2 = update_module.Cleanup();
		if (err2 == error::NoError) {
			log::Error("Update Module Cleanup failed: " + err2.String());
		}
		err2 = RemoveStandaloneData(main_context.GetMenderStoreDB());
		if (err2 != error::NoError) {
			log::Error("Removing update data from database failed: " + err2.String());
		}
		return {Result::FailedNothingDone, err};
	}

	err = update_module.ArtifactInstall();
	if (err != error::NoError) {
		log::Error("Installation failed: " + err.String());
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
	context::MenderContext &main_context,
	StandaloneData &data,
	update_module::UpdateModule &update_module) {

	auto err = update_module.ArtifactCommit();
	if (err != error::NoError) {
		log::Error("Commit failed: " + err.String());
		return InstallationFailureHandler(main_context, data, update_module);
	}

	auto result = Result::Committed;
	error::Error return_err;

	err = update_module.Cleanup();
	if (err != error::NoError) {
		log::Error("Update Module Cleanup failed: " + err.String());
		result = Result::InstalledButFailedInPostCommit;
		return_err = move(err);
	}

	err = main_context.CommitArtifactData(
		data.artifact_name,
		data.artifact_group,
		data.artifact_provides,
		data.artifact_clears_provides,
		[](database::Transaction &txn) {
			return txn.Remove(context::MenderContext::standalone_state_key);
		}
	);
	if (err != error::NoError) {
		result = Result::InstalledButFailedInPostCommit;
		return_err = move(err);
	}

	return {result, return_err};
}

ResultAndError InstallationFailureHandler(
	context::MenderContext &main_context,
	StandaloneData &data,
	update_module::UpdateModule &update_module) {

	error::Error err;
	error::Error return_err;
	bool successful_rollback;
	bool rollback_support;

	auto exp_rollback_support = update_module.SupportsRollback();
	if (!exp_rollback_support) {
		log::Error("Could not query for rollback support: " + exp_rollback_support.error().String());
		successful_rollback = false;
		rollback_support = false;
		return_err = move(err);
	} else {
		rollback_support = exp_rollback_support.value();
		if (rollback_support) {
			err = update_module.ArtifactRollback();
			if (err == error::NoError) {
				successful_rollback = true;
			} else {
				log::Error("Rollback failed: " + err.String());
				successful_rollback = false;
				return_err = move(err);
			}
		} else {
			log::Info("No rollback support.");
			successful_rollback = false;
		}
	}

	err = update_module.ArtifactFailure();
	if (err != error::NoError) {
		log::Error("Update Module ArtifactFailure state failed: " + err.String());
		successful_rollback = false;
		return_err = move(err);
	}

	err = update_module.Cleanup();
	if (err != error::NoError) {
		log::Error("Update Module Cleanup failed: " + err.String());
		successful_rollback = false;
		return_err = move(err);
	}

	if (successful_rollback) {
		err = RemoveStandaloneData(main_context.GetMenderStoreDB());
		if (err != error::NoError) {
			successful_rollback = false;
			return_err = move(err);
		}
	} else {
		data.artifact_name += main_context.broken_artifact_name_suffix;
		if (data.artifact_provides) {
			data.artifact_provides.value()["artifact_name"] = data.artifact_name;
		}
		err = main_context.CommitArtifactData(
			data.artifact_name,
			data.artifact_group,
			data.artifact_provides,
			data.artifact_clears_provides,
			[](database::Transaction &txn) {
				return txn.Remove(context::MenderContext::standalone_state_key);
			}
		);
		if (err != error::NoError) {
			return_err = move(err);
		}
	}

	if (successful_rollback) {
		return {Result::FailedAndRolledBack, return_err};
	} else if (rollback_support) {
		return {Result::FailedAndNoRollback, return_err};
	} else {
		return {Result::FailedAndRollbackFailed, return_err};
	}
}

} // namespace standalone
} // namespace update
} // namespace mender
