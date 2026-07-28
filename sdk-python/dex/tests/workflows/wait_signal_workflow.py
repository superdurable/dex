# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

from dex.dex_api.models.timer_result import TimerStatus
from dex.command_request import (
    CommandRequest,
    SignalChannelCommand,
    TimerCommand,
)
from dex.command_results import (
    CommandResults,
    SignalChannelCommandResult,
    TimerCommandResult,
)
from dex.communication import Communication
from dex.communication_schema import CommunicationMethod, CommunicationSchema
from dex.dex_api.models import ChannelRequestStatus
from dex.persistence import Persistence
from dex.rpc import rpc
from dex.state_decision import StateDecision
from dex.state_schema import StateSchema
from dex.workflow import ObjectWorkflow
from dex.workflow_context import WorkflowContext
from dex.workflow_state import T, WorkflowState

test_channel_int = "test-int"
test_channel_none = "test-none"
test_channel_str = "test-str"
test_idle_channel_none = "test-idle"

test_channel1 = "test-channel1"
test_channel1_id = "test-channel1-id"
test_channel2 = "test-channel2"
test_channel2_id = "test-channel2-id"
test_timer_id = "test-timer-id"


class WaitState1(WorkflowState[None]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        return CommandRequest.for_all_command_completed(
            SignalChannelCommand.by_name(test_channel_int),
            SignalChannelCommand.by_name(test_channel_none),
            SignalChannelCommand.by_name(test_channel_str),
        )

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        assert len(command_results.signal_channel_commands) == 3
        sig1 = command_results.signal_channel_commands[0]
        sig2 = command_results.signal_channel_commands[1]
        sig3 = command_results.signal_channel_commands[2]
        assert sig1 == SignalChannelCommandResult(
            test_channel_int, 123, ChannelRequestStatus.RECEIVED, ""
        )
        assert sig2 == SignalChannelCommandResult(
            test_channel_none, None, ChannelRequestStatus.RECEIVED, ""
        )
        assert sig3 == SignalChannelCommandResult(
            test_channel_str, "abc", ChannelRequestStatus.RECEIVED, ""
        )
        return StateDecision.single_next_state(WaitState2, sig3.value)


class WaitState2(WorkflowState[str]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        return CommandRequest.for_any_command_combination_completed(
            [
                [
                    test_channel1_id,
                    test_timer_id,
                ]
            ],
            SignalChannelCommand.by_name(test_channel1, test_channel1_id),
            SignalChannelCommand.by_name(test_channel2, test_channel2_id),
            TimerCommand.by_seconds(1, test_timer_id),
        )

    def execute(
        self,
        ctx: WorkflowContext,
        input: str,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        assert len(command_results.signal_channel_commands) == 2
        assert (
            len(
                [
                    r
                    for r in command_results.signal_channel_commands
                    if r.status == ChannelRequestStatus.RECEIVED
                ]
            )
            == 1
        )
        sig1 = command_results.signal_channel_commands[0]
        tim1 = command_results.timer_commands[0]
        assert sig1 == SignalChannelCommandResult(
            test_channel1, None, ChannelRequestStatus.RECEIVED, test_channel1_id
        )
        assert tim1 == TimerCommandResult(
            TimerStatus.FIRED,
            test_timer_id,
        )
        return StateDecision.graceful_complete_workflow(input)


class WaitSignalWorkflow(ObjectWorkflow):
    def get_communication_schema(self) -> CommunicationSchema:
        return CommunicationSchema.create(
            CommunicationMethod.signal_channel_def(test_channel_int, int),
            CommunicationMethod.signal_channel_def(test_channel_none, None),
            CommunicationMethod.signal_channel_def(test_channel_str, str),
            CommunicationMethod.signal_channel_def(test_idle_channel_none, None),
            CommunicationMethod.signal_channel_def(test_channel1, None),
            CommunicationMethod.signal_channel_def(test_channel2, None),
        )

    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(WaitState1(), WaitState2())

    @rpc()
    def get_idle_signal_channel_size(self, com: Communication):
        return com.get_signal_channel_size(test_idle_channel_none)
