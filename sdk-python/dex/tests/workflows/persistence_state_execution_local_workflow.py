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

from dex.rpc import rpc
from dex.command_request import CommandRequest
from dex.command_results import CommandResults
from dex.communication import Communication
from dex.persistence import Persistence
from dex.persistence_schema import PersistenceField, PersistenceSchema
from dex.state_decision import StateDecision
from dex.state_schema import StateSchema
from dex.workflow import ObjectWorkflow
from dex.workflow_context import WorkflowContext
from dex.workflow_state import T, WorkflowState

PERSISTENCE_LOCAL_KEY = "persistence-test-key"
PERSISTENCE_LOCAL_VALUE = "persistence-test-value"
PERSISTENCE_DATA_ATTRIBUTE_KEY = "persistence-data-attribute-key"


class PersistenceStateExecutionLocalRWState(WorkflowState[None]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ):
        persistence.set_state_execution_local(
            PERSISTENCE_LOCAL_KEY, PERSISTENCE_LOCAL_VALUE
        )
        return CommandRequest.empty()

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ):
        value = persistence.get_state_execution_local(PERSISTENCE_LOCAL_KEY)
        persistence.set_data_attribute(PERSISTENCE_DATA_ATTRIBUTE_KEY, value)
        return StateDecision.graceful_complete_workflow()


class PersistenceStateExecutionLocalWorkflow(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(PersistenceStateExecutionLocalRWState())

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(PERSISTENCE_DATA_ATTRIBUTE_KEY, str)
        )

    @rpc()
    def test_persistence_read(self, persistence: Persistence):
        return persistence.get_data_attribute(PERSISTENCE_DATA_ATTRIBUTE_KEY)
