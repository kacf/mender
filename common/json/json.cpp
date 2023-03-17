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

#include <common/json.hpp>

namespace mender {
namespace common {
namespace json {

const JsonErrorCategoryClass JsonErrorCategory;

const char *JsonErrorCategoryClass::name() const noexcept {
	return "JsonErrorCategory";
}

string JsonErrorCategoryClass::message(int code) const {
	switch (code) {
	case NoError:
		return "Success";
	case ParseError:
		return "Parse error";
	case KeyError:
		return "Key error";
	case IndexError:
		return "Index error";
	case TypeError:
		return "Type error";
	default:
		return "Unknown";
	}
}

error::Error MakeError(JsonErrorCode code, const string &msg) {
	return error::Error(error_condition(code, JsonErrorCategory), msg);
}

} // namespace json
} // namespace common
} // namespace mender