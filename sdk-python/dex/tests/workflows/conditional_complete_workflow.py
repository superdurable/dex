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

import time

from dex.command_request import (
    CommandRequest,
    InternalChannelCommand,
    SignalChannelCommand,
)
from dex.command_results import CommandResults
from dex.communication import Communication
from dex.communication_schema import CommunicationMethod, CommunicationSchema
from dex.persistence import Persistence
from dex.persistence_schema import PersistenceField, PersistenceSchema
from dex.rpc import rpc
from dex.state_decision import StateDecision
from dex.state_schema import StateSchema
from dex.workflow import ObjectWorkflow
from dex.workflow_context import WorkflowContext
from dex.workflow_state import T, WorkflowState

test_signal_channel = "test-1"
test_internal_channel = "test-2"

da_counter = "counter"


class WaitState(WorkflowState[bool]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        use_signal: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        if use_signal:
            return CommandRequest.for_all_command_completed(
                SignalChannelCommand.by_name(test_signal_channel),
            )
        else:
            return CommandRequest.for_all_command_completed(
                InternalChannelCommand.by_name(test_internal_channel),
            )

    def execute(
        self,
        ctx: WorkflowContext,
        use_signal: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        counter = persistence.get_data_attribute(da_counter)
        if counter is None:
            counter = 0
        counter += 1
        persistence.set_data_attribute(da_counter, counter)

        if ctx.state_execution_id == "WaitState-1":
            # wait for 3 seconds so that the channel can have a new message
            time.sleep(3)
        if use_signal:
            return StateDecision.force_complete_if_signal_channel_empty_or_else(
                test_signal_channel, counter, WaitState, use_signal
            )
        else:
            return StateDecision.force_complete_if_internal_channel_empty_or_else(
                test_internal_channel, counter, WaitState, use_signal
            )


class ConditionalCompleteWorkflow(ObjectWorkflow):
    def get_communication_schema(self) -> CommunicationSchema:
        return CommunicationSchema.create(
            CommunicationMethod.signal_channel_def(test_signal_channel, int),
            CommunicationMethod.internal_channel_def(test_internal_channel, int),
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(da_counter, int),
        )

    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(WaitState())

    @rpc()
    def test_rpc_publish_channel(self, com: Communication):
        com.publish_to_internal_channel(test_internal_channel, 0)
