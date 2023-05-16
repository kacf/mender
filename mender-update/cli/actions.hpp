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

#ifndef MENDER_UPDATE_ACTIONS_HPP
#define MENDER_UPDATE_ACTIONS_HPP

#include <common/error.hpp>
#include <common/expected.hpp>

#include <mender-update/context.hpp>

namespace mender {
namespace update {
namespace cli {

using namespace std;

namespace error = mender::common::error;
namespace context = mender::update::context;

error::Error ShowArtifact(context::MenderContext &main_context);

error::Error ShowProvides(context::MenderContext &main_context);

error::Error Install(context::MenderContext &main_context, const string &src, bool reboot_exit_code);

} // namespace cli
} // namespace update
} // namespace mender

#endif // MENDER_UPDATE_ACTIONS_HPP
