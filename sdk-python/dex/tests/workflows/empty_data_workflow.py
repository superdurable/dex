# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from dex.command_results import CommandResults
from dex.communication import Communication
from dex.persistence import Persistence
from dex.persistence_schema import PersistenceField, PersistenceSchema
from dex.state_decision import StateDecision
from dex.state_schema import StateSchema
from dex.workflow import ObjectWorkflow
from dex.workflow_context import WorkflowContext
from dex.workflow_state import WorkflowState

TEST_DA_KEY = "test-da"

class State1(WorkflowState[None]):
    def execute(
        self,
        ctx: WorkflowContext,
        input: None,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        assert input is None
        test_da = persistence.get_data_attribute(TEST_DA_KEY)
        assert test_da is None

        return StateDecision.graceful_complete_workflow(output="success")

class EmptyDataWorkflow(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(State1())

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.data_attribute_def(TEST_DA_KEY, None),
        )
