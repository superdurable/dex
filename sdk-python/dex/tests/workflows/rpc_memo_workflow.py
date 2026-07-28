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

from typing import Optional

from dex.command_request import CommandRequest, InternalChannelCommand
from dex.command_results import CommandResults
from dex.communication import Communication
from dex.communication_schema import CommunicationMethod, CommunicationSchema
from dex.dex_api.models.search_attribute_value_type import SearchAttributeValueType
from dex.persistence import Persistence
from dex.persistence_options import PersistenceOptions
from dex.persistence_schema import PersistenceField, PersistenceSchema
from dex.rpc import rpc
from dex.state_decision import StateDecision
from dex.state_schema import StateSchema
from dex.workflow import ObjectWorkflow
from dex.workflow_context import WorkflowContext
from dex.workflow_state import WorkflowState


INTERNAL_CHANNEL_NAME = "test-channel-1"
TEST_DATA_OBJECT_KEY = "data-obj-1"
TEST_SEARCH_ATTRIBUTE_KEY = "CustomKeywordField"
TEST_SEARCH_ATTRIBUTE_INT = "CustomIntField"
RPC_INPUT = "rpc-input"
RPC_OUTPUT = 100
TEST_STR = "test-str"
TEST_DELAY = 0.1


class RpcMemoWorkflow(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(RpcMemoWorkflowState1())

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(TEST_DATA_OBJECT_KEY, str),
            PersistenceField.search_attribute_def(
                TEST_SEARCH_ATTRIBUTE_INT, SearchAttributeValueType.INT
            ),
            PersistenceField.search_attribute_def(
                TEST_SEARCH_ATTRIBUTE_KEY, SearchAttributeValueType.KEYWORD
            ),
        )

    def get_communication_schema(self) -> CommunicationSchema:
        return CommunicationSchema.create(
            CommunicationMethod.internal_channel_def(INTERNAL_CHANNEL_NAME, None)
        )

    def get_persistence_options(self) -> PersistenceOptions:
        return PersistenceOptions(enable_caching=True)

    @rpc()
    def test_rpc_func1(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ) -> int:
        if not ctx.workflow_id or not ctx.workflow_run_id:
            raise RuntimeError("invalid context")

        persistence.set_data_attribute(TEST_DATA_OBJECT_KEY, None)
        persistence.set_data_attribute(TEST_DATA_OBJECT_KEY, input)
        persistence.set_search_attribute_keyword(TEST_SEARCH_ATTRIBUTE_KEY, input)
        persistence.set_search_attribute_int64(TEST_SEARCH_ATTRIBUTE_INT, RPC_OUTPUT)
        communication.publish_to_internal_channel(INTERNAL_CHANNEL_NAME, None)
        return RPC_OUTPUT

    @rpc()
    def test_rpc_func0(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ) -> int:
        if not ctx.workflow_id or not ctx.workflow_run_id:
            raise RuntimeError("invalid context")

        persistence.set_data_attribute(TEST_DATA_OBJECT_KEY, TEST_STR)
        persistence.set_search_attribute_keyword(TEST_SEARCH_ATTRIBUTE_KEY, TEST_STR)
        persistence.set_search_attribute_int64(TEST_SEARCH_ATTRIBUTE_INT, RPC_OUTPUT)
        communication.publish_to_internal_channel(INTERNAL_CHANNEL_NAME, None)
        return RPC_OUTPUT

    @rpc()
    def test_rpc_proc1(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ):
        if not ctx.workflow_id or not ctx.workflow_run_id:
            raise RuntimeError("invalid context")

        persistence.set_data_attribute(TEST_DATA_OBJECT_KEY, input)
        persistence.set_search_attribute_keyword(TEST_SEARCH_ATTRIBUTE_KEY, input)
        persistence.set_search_attribute_int64(TEST_SEARCH_ATTRIBUTE_INT, RPC_OUTPUT)
        communication.publish_to_internal_channel(INTERNAL_CHANNEL_NAME, None)

    @rpc()
    def test_rpc_proc0(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ):
        if not ctx.workflow_id or not ctx.workflow_run_id:
            raise RuntimeError("invalid context")

        persistence.set_data_attribute(TEST_DATA_OBJECT_KEY, TEST_STR)
        persistence.set_search_attribute_keyword(TEST_SEARCH_ATTRIBUTE_KEY, TEST_STR)
        persistence.set_search_attribute_int64(TEST_SEARCH_ATTRIBUTE_INT, RPC_OUTPUT)
        communication.publish_to_internal_channel(INTERNAL_CHANNEL_NAME, None)

    @rpc()
    def test_rpc_func1_readonly(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ) -> int:
        if not ctx.workflow_id or not ctx.workflow_run_id:
            raise RuntimeError("invalid context")
        return RPC_OUTPUT

    @rpc()
    def test_rpc_set_data_attribute(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ):
        if not ctx.workflow_id or not ctx.workflow_run_id:
            raise RuntimeError("invalid context")
        persistence.set_data_attribute(TEST_DATA_OBJECT_KEY, input)

    @rpc()
    def test_rpc_get_data_attribute(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ) -> str:
        if not ctx.workflow_id or not ctx.workflow_run_id:
            raise RuntimeError("invalid context")
        return persistence.get_data_attribute(TEST_DATA_OBJECT_KEY)

    @rpc(bypass_caching_for_strong_consistency=True)
    def test_rpc_get_data_attribute_strong_consistency(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ) -> str:
        if not ctx.workflow_id or not ctx.workflow_run_id:
            raise RuntimeError("invalid context")
        return persistence.get_data_attribute(TEST_DATA_OBJECT_KEY)

    @rpc()
    def test_rpc_set_keyword(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ):
        if not ctx.workflow_id or not ctx.workflow_run_id:
            raise RuntimeError("invalid context")
        persistence.set_search_attribute_keyword(TEST_SEARCH_ATTRIBUTE_KEY, input)

    @rpc(bypass_caching_for_strong_consistency=True)
    def test_rpc_get_keyword_strong_consistency(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ) -> Optional[str]:
        if not ctx.workflow_id or not ctx.workflow_run_id:
            raise RuntimeError("invalid context")
        return persistence.get_search_attribute_keyword(TEST_SEARCH_ATTRIBUTE_KEY)

    @rpc()
    def test_rpc_get_keyword(
        self,
        ctx: WorkflowContext,
        input: str,
        persistence: Persistence,
        communication: Communication,
    ) -> Optional[str]:
        if not ctx.workflow_id or not ctx.workflow_run_id:
            raise RuntimeError("invalid context")
        return persistence.get_search_attribute_keyword(TEST_SEARCH_ATTRIBUTE_KEY)


class RpcMemoWorkflowState1(WorkflowState[int]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: int,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        return CommandRequest.for_any_command_completed(
            InternalChannelCommand.by_name(INTERNAL_CHANNEL_NAME)
        )

    def execute(
        self,
        ctx: WorkflowContext,
        input: int,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        return StateDecision.graceful_complete_workflow()
