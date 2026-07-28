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

from iwf.command_request import CommandRequest, InternalChannelCommand, TimerCommand
from iwf.command_results import CommandResults
from iwf.communication import Communication
from iwf.communication_schema import CommunicationSchema, CommunicationMethod
from iwf.iwf_api.models import ChannelRequestStatus
from iwf.persistence import Persistence
from iwf.persistence_schema import PersistenceSchema, PersistenceField
from iwf.rpc import rpc
from iwf.state_decision import StateDecision
from iwf.state_schema import StateSchema
from iwf.workflow import ObjectWorkflow
from iwf.workflow_context import WorkflowContext
from iwf.workflow_state import WorkflowState

TEST_APPROVAL_KEY = "Approval"
TEST_STRING_KEY = "TestString"

class BasicWorkflow(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(
            BasicWorkflowState1(),
            BasicWorkflowState2())


    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(TEST_STRING_KEY, str),
        )

    def get_communication_schema(self) -> CommunicationSchema:
        return CommunicationSchema.create(
            CommunicationMethod.internal_channel_def(TEST_APPROVAL_KEY, str)
        )

    @rpc()
    def append_string(self, st: str, persistence: Persistence) -> str:
        current = persistence.get_data_attribute(TEST_STRING_KEY)
        if current is None:
            current = ""
        current = current + ", " + st
        persistence.set_data_attribute(TEST_STRING_KEY, current)
        return current

    @rpc()
    def approve(self, communication: Communication):
        communication.publish_to_internal_channel(TEST_APPROVAL_KEY, "approved")

class BasicWorkflowState1(WorkflowState[int]):
    def execute(
            self,
            ctx: WorkflowContext,
            data: int,
            command_results: CommandResults,
            persistence: Persistence,
            communication: Communication,
    ) -> StateDecision:
        output = data + 1
        return StateDecision.single_next_state(BasicWorkflowState2, output)

class BasicWorkflowState2(WorkflowState[int]):
    def wait_until(
            self,
            ctx: WorkflowContext,
            data: int,
            persistence: Persistence,
            communication: Communication,
    ) -> CommandRequest:
        return CommandRequest.for_any_command_completed(
            InternalChannelCommand.by_name(TEST_APPROVAL_KEY),
            TimerCommand.by_seconds(data)
        )

    def execute(
            self,
            ctx: WorkflowContext,
            data: int,
            command_results: CommandResults,
            persistence: Persistence,
            communication: Communication,
    ) -> StateDecision:
        internal_channel_result = command_results.internal_channel_commands[0]
        if internal_channel_result.status == ChannelRequestStatus.RECEIVED:
            return StateDecision.graceful_complete_workflow(internal_channel_result.value)
        else:
            return StateDecision.single_next_state(BasicWorkflowState2, data)


