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

#include <common/events.hpp>
#include <common/key_value_database.hpp>
#include <common/json.hpp>

#include <mender-update/context.hpp>

namespace mender {
namespace update {
namespace standalone {

namespace events = mender::common::events;
namespace database = mender::common::key_value_database;
namespace json = mender::common::json;

namespace context = mender::update::context;

const string StandaloneDataKeys::version {"Version"};
const string StandaloneDataKeys::artifact_name {"ArtifactName"};
const string StandaloneDataKeys::artifact_group {"ArtifactGroup"};
const string StandaloneDataKeys::artifact_provides {"ArtifactTypeInfoProvides"};
const string StandaloneDataKeys::artifact_clears_provides {"ArtifactClearsProvide"};
const string StandaloneDataKeys::payload_types {"PayloadTypes"};

template <typename T>
expected::expected<T, error::Error> GetEntry(const Json &json, const string &key, bool missing_ok) {
	auto exp_value = json.Get(key);
	if (!exp_value) {
		if (missing_ok && exp_value.error().code != json::MakeError(json::KeyError, "").code) {
			return T();
		} else {
			auto err = exp_value.error();
			err.message += ": Could not get `" + key.version + "` from state data";
			return expected::unexpected(err);
		}
	} else {
		return exp_value.value().Get<T>();
	}
}

expected::ExpectedBool LoadStandaloneData(database::KeyValueDatabase &db, StandaloneData &dst) {
	auto exp_bytes = db.Read(context::MenderContext::standalone_state_key);
	if (!exp_bytes) {
		auto &err = exp_bytes.error();
		if (err.code == database::MakeError(KeyError, "").code) {
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

	auto exp_array = GetEntry<json::StringVector>(json, keys.artifact_clears_provides, true);
	if (!exp_array) {
		return expected::unexpected(exp_array.error());
	}
	dst.artifact_clears_provides = exp_array.value();

	exp_array = GetEntry<json::StringVector>(json, keys.payload_types, false);
	if (!exp_array) {
		return expected::unexpected(exp_array.error());
	}
	dst.payload_types = exp_array.value();

	if (dst.version != MenderContext::standalone_data_version) {
		return expected::unexpected(error::Error(make_error_condition(errc::not_supported), "State data has a version which is not supported by this client"));
	}

	if (dst.artifact_name == "") {
		return expected::unexpected(context::MakeError(context::DatabaseValueError, "`" + key.artifact_name + "` is empty"));
	}

	if (dst.payload_types.size() == 0) {
		return expected::unexpected(context::MakeError(context::DatabaseValueError, "`" + key.payload_types + "` is empty"));
	}
	if (dst.payload_types.size() >= 2) {
		return expected::unexpected(error::Error(make_error_condition(errc::not_supported), "`" + key.payload_types + "` contains multiple payloads"));
	}

	return true;
}

error::Error SaveStandaloneData(artifact::PayloadHeaderView &header) {
	StandaloneDataKeys keys;
	stringstream ss;
	ss << "{";
	ss << "\"" << keys.version << "\": " << MenderContext::standalone_data_version;

	ss << ",";
	ss << "\"" << keys.artifact_name << "\": \"" << header.artifact_name << "\"";

	ss << ",";
	ss << "\"" << keys.artifact_group << "\": \"" << header.artifact_group << "\"";

	ss << ",";
	ss << "\"" << keys.payload_types << "\": [\"" << header.payload_type << "\"]";

	ss << ",";
	ss << "\"" << keys.artifact_provides << "\": [";
	auto array = header.type_info.Get("
}

ResultAndError Install(context::MenderContext &main_context, const string &src) {
	StandaloneData update_data;
	auto exp_in_progress = LoadStandaloneData(main_context.db, update_data);
	if (!exp_in_progress) {
		return {Result::FailureNothingDone, exp_in_progress.error()};
	}

	if (exp_in_progress.value()) {
		return {Result::FailureNothingDone, error::Error(make_error_condition(errc::in_progress), "Update already in progress. Please commit or roll back first")};
	}

	if (src.find("http://") == 0 || src.find("https://") == 0) {
		return {Result::FailureNothingDone, error::Error(make_error_condition(errc::not_supported), "HTTP not supported yet")};
	}

	events::io::AsyncFileDescriptorReaderPtr async_artifact_reader;
	events::io::ReaderFromAsyncReader artifact_reader([&async_artifact_reader](events::EventLoop &loop) {
		async_artifact_reader = make_shared<events::io::AsyncFileDescriptorReader>(loop);
		return async_artifact_reader;
	});
	auto err = async_artifact_reader->Open(src);
	if (err != error::NoError) {
		return {Result::FailureNothingDone, err};
	}

	ParserConfig config {
		paths::DefaultArtScriptsPath,
	};
	auto parser = artifact::Parse(artifact_reader, config);
	if (!parser) {
		return {Result::FailureNothingDone, parser.error()};
	}

	auto header = artifact::View(parser, 0);
	if (!header) {
		return {Result::FailureNothingDone, header.error()};
	}

	UpdateModule update_module(main_context, header.value().header.payload_type);

	err = update_module.PrepareFileTree(update_module.GetUpdateModuleWorkDir(), header.value());
	if (err != error::NoError) {
		return {Result::FailureNothingDone, err};
	}

	return DoInstallStates(update_module);
}

ResultAndError DoInstallStates(UpdateModule &update_module) {
}

} // namespace standalone
} // namespace update
} // namespace mender
