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

from dataclasses import dataclass

from dex.command_request import CommandRequest, InternalChannelCommand
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

test_data_attribute = "test-1"
channel_name = "test-2"
idle_channel_name = "test-3"


@dataclass
class Mydata:
    strdata: str
    intdata: int


class WaitState(WorkflowState[None]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        return CommandRequest.for_all_command_completed(
            InternalChannelCommand.by_name(channel_name)
        )

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        return StateDecision.single_next_state(EndState)


class EndState(WorkflowState[None]):
    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        return StateDecision.graceful_complete_workflow("done")


class RPCWorkflow(ObjectWorkflow):
    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(test_data_attribute, int)
        )

    def get_communication_schema(self) -> CommunicationSchema:
        return CommunicationSchema.create(
            CommunicationMethod.internal_channel_def(channel_name, type(None)),
            CommunicationMethod.internal_channel_def(idle_channel_name, str),
        )

    def get_workflow_states(self) -> StateSchema:
        return StateSchema.no_starting_state(WaitState(), EndState())

    @rpc(timeout_seconds=100)
    def test_simple_rpc(self):
        return 123

    @rpc()
    def test_rpc_persistence_write(
        self,
        input: int,
        persistence: Persistence,
    ):
        persistence.set_data_attribute(test_data_attribute, input)

    @rpc()
    def test_rpc_persistence_read(self, pers: Persistence):
        return pers.get_data_attribute(test_data_attribute)

    @rpc()
    def test_rpc_trigger_state(self, pers: Persistence, com: Communication, i: int):
        pers.set_data_attribute(test_data_attribute, i)
        com.trigger_state_execution(WaitState)

    @rpc()
    def test_rpc_publish_channel(self, com: Communication):
        com.publish_to_internal_channel(channel_name)

    @rpc()
    def test_rpc_publish_to_idle_channel(self, com: Communication, data: str):
        com.publish_to_internal_channel(idle_channel_name, data)
        return com.get_internal_channel_size(idle_channel_name)

    @rpc()
    def test_rpc_input_type(self, input: Mydata) -> Mydata:
        if input.intdata != 100 or input.strdata != "test":
            raise Exception("input type test failed")
        return input
