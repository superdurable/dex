# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from typing import Union

from dex.command_results import CommandResults
from dex.communication import Communication
from dex.dex_api.models import RetryPolicy
from dex.persistence import Persistence
from dex.state_decision import StateDecision
from dex.state_schema import StateSchema
from dex.workflow import ObjectWorkflow
from dex.workflow_context import WorkflowContext
from dex.workflow_state import T, WorkflowState
from dex.workflow_state_options import WorkflowStateOptions

class AbnormalExitState1(WorkflowState[Union[int, str]]):
    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        raise RuntimeError("abnormal exit state")

    def get_state_options(self) -> WorkflowStateOptions:
        return WorkflowStateOptions(
            execute_api_retry_policy=RetryPolicy(maximum_attempts=1)
        )

class AbnormalExitWorkflow(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(AbnormalExitState1())
