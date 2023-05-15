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
#include <common/conf/paths.hpp>

namespace mender {
namespace update {
namespace standalone {

namespace events = mender::common::events;
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

ResultAndError Install(context::MenderContext &main_context, const string &src) {
	StandaloneData update_data;
	auto exp_in_progress = LoadStandaloneData(main_context.GetMenderStoreDB(), update_data);
	if (!exp_in_progress) {
		return {Result::FailedNothingDone, exp_in_progress.error()};
	}

	if (exp_in_progress.value()) {
		return {Result::FailedNothingDone, error::Error(make_error_condition(errc::operation_in_progress), "Update already in progress. Please commit or roll back first")};
	}

	if (src.find("http://") == 0 || src.find("https://") == 0) {
		return {Result::FailedNothingDone, error::Error(make_error_condition(errc::not_supported), "HTTP not supported yet")};
	}

	events::io::AsyncFileDescriptorReaderPtr async_artifact_reader;
	events::io::ReaderFromAsyncReader artifact_reader([&async_artifact_reader](events::EventLoop &loop) {
		async_artifact_reader = make_shared<events::io::AsyncFileDescriptorReader>(loop);
		return async_artifact_reader;
	});
	auto err = async_artifact_reader->Open(src);
	if (err != error::NoError) {
		return {Result::FailedNothingDone, err};
	}

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

	err = update_module.PrepareFileTree(update_module.GetUpdateModuleWorkDir(), header.value());
	if (err != error::NoError) {
		return {Result::FailedNothingDone, err};
	}

	return DoInstallStates(update_module);
}

ResultAndError DoInstallStates(update_module::UpdateModule &update_module) {
	return {Result::FailedNothingDone, error::NoError};
}

} // namespace standalone
} // namespace update
} // namespace mender
