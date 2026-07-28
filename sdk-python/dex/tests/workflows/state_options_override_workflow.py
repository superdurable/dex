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
from dex.persistence_schema import PersistenceSchema, PersistenceField

from dex.dex_api.models import RetryPolicy, WaitUntilApiFailurePolicy
from dex.persistence import Persistence
from dex.state_decision import StateDecision
from dex.state_schema import StateSchema
from dex.workflow import ObjectWorkflow
from dex.workflow_context import WorkflowContext
from dex.workflow_state import T, WorkflowState
from dex.workflow_state_options import WorkflowStateOptions

output_da = "output_da"


class InitState(WorkflowState[str]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        persistence.set_data_attribute(
            output_da, str(input) + "_InitState_waitUntil_completed"
        )
        return CommandRequest.empty()

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        data = persistence.get_data_attribute(output_da)
        data += "_InitState_execute_completed"
        return StateDecision.single_next_state(
            NonInitState,
            data,
            WorkflowStateOptions(
                wait_until_api_retry_policy=RetryPolicy(maximum_attempts=2),
                proceed_to_execute_when_wait_until_retry_exhausted=WaitUntilApiFailurePolicy.PROCEED_ON_FAILURE,
            ),
        )


class NonInitState(WorkflowState[str]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        raise RuntimeError("test failure")

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        data = str(input) + "_NonInitState_execute_completed"
        return StateDecision.graceful_complete_workflow(data)

    def get_state_options(self) -> WorkflowStateOptions:
        return WorkflowStateOptions(
            wait_until_api_retry_policy=RetryPolicy(maximum_attempts=1),
            proceed_to_execute_when_wait_until_retry_exhausted=WaitUntilApiFailurePolicy.FAIL_WORKFLOW_ON_FAILURE,
        )


class StateOptionsOverrideWorkflow(ObjectWorkflow):
    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(output_da, str),
        )

    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(InitState(), NonInitState())
