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

initial_da_1 = "initial_da_1"
initial_da_value_1 = "value_1"
initial_da_2 = "initial_da_2"
initial_da_value_2 = "value_2"

test_da_1 = "test_da_1"
test_da_2 = "test_da_2"

final_test_da_value_1 = "1234"
final_test_da_value_2 = 1234
final_initial_da_value_1 = initial_da_value_1
final_initial_da_value_2 = "no-more-init"

test_da_prefix = "test-da-prefix"
test_da_prefix_key_1 = "test-da-prefix-1"
test_da_prefix_key_2 = "test-da-prefix-2"
test_da_prefix_value_1 = "test-da-value-1"
test_da_prefix_value_2 = "test-da-value-2"
test_da_set_key = "test_da_set_key"
test_da_set_value = "test_da_set_value"

expected_final_das = {
    initial_da_1: initial_da_value_1,
    initial_da_2: final_initial_da_value_2,
    test_da_1: final_test_da_value_1,
    test_da_2: final_test_da_value_2,
    test_da_prefix_key_1: test_da_prefix_value_1,
    test_da_prefix_key_2: test_da_prefix_value_2,
    test_da_set_key: test_da_set_value,
}


class DataAttributeRWState(WorkflowState[None]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        persistence.set_data_attribute(test_da_1, "123")
        persistence.set_data_attribute(test_da_2, 123)

        return CommandRequest.empty()

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        da1 = persistence.get_data_attribute(test_da_1)
        da2 = persistence.get_data_attribute(test_da_2)
        assert da1 == "123"
        assert da2 == 123

        persistence.set_data_attribute(test_da_1, final_test_da_value_1)
        persistence.set_data_attribute(test_da_2, final_test_da_value_2)
        persistence.set_data_attribute(initial_da_2, final_initial_da_value_2)
        persistence.set_data_attribute(test_da_prefix_key_1, test_da_prefix_value_1)
        persistence.set_data_attribute(test_da_prefix_key_2, test_da_prefix_value_2)
        return StateDecision.graceful_complete_workflow()


class PersistenceDataAttributesWorkflow(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(DataAttributeRWState())

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(initial_da_1, str),
            PersistenceField.data_attribute_def(initial_da_2, str),
            PersistenceField.data_attribute_def(test_da_1, str),
            PersistenceField.data_attribute_def(test_da_2, int),
            PersistenceField.data_attribute_prefix_def(test_da_prefix, str),
            PersistenceField.data_attribute_def(test_da_set_key, str),
        )
