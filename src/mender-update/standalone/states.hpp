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

#ifndef MENDER_UPDATE_STANDALONE_STATES_HPP
#define MENDER_UPDATE_STANDALONE_STATES_HPP

#include <common/state_machine.hpp>

#include <mender-update/standalone/context.hpp>
#include <mender-update/standalone/state_events.hpp>

namespace mender {
namespace update {
namespace standalone {

using namespace std;

namespace sm = mender::common::state_machine;

using StateType = sm::State<Context, StateEvent>;

class SaveState : virtual public StateType {
public:
	// Sub states should implement OnEnterSaveState instead, since we do state saving in
	// here. Note that not all states that participate in an update are SaveStates that get
	// their database key saved. Some states are not because it's good enough to rely on the
	// previously saved state.
	void OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) override final;

	virtual void OnEnterSaveState(Context &ctx, sm::EventPoster<StateEvent> &poster) = 0;
	virtual const string &DatabaseStateString() const = 0;
	virtual bool IsFailureState() const = 0;
};

class DownloadState : virtual public StateType {
public:
	void OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) override;
};

class DownloadState : virtual public StateType {
public:
	void OnEnter(Context &ctx, sm::EventPoster<StateEvent> &poster) override;
};

} // namespace standalone
} // namespace update
} // namespace mender

#endif // MENDER_UPDATE_STANDALONE_STATES_HPP
