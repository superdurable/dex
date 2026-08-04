# Legacy Materials in this file remain under their original licenses.
# See LEGACY_NOTICES.md.

# Modifications Copyright (c) 2026 Super Durable, Inc.
#
# Modifications after the Legacy Cutoff are licensed under the
# Super Durable Source License 1.0.
# Legacy Materials remain under their original licenses.
# See LICENSE and LEGACY_NOTICES.md.

from time import sleep

from dex.command_request import CommandRequest, TimerCommand
from dex.command_results import CommandResults
from dex.communication import Communication
from dex.dex_api.models import SearchAttributeValueType
from dex.persistence import Persistence
from dex.persistence_schema import PersistenceField, PersistenceSchema
from dex.state_decision import StateDecision
from dex.state_schema import StateSchema
from dex.workflow import ObjectWorkflow
from dex.workflow_context import WorkflowContext
from dex.workflow_state import T, WorkflowState

sa_keyword_key = "CustomKeywordField"
sa_double_key = "CustomDoubleField"
sa_int_key = "CustomIntField"
sa_bool_key = "CustomBoolField"
sa_datetime_key = "CustomDatetimeField"
sa_keyword_array_key = "CustomKeywordArrayField"

initial_sa_keyword: str = "initial_keyword"
initial_sa_double: float = 1.11
initial_sa_int: int = 1
initial_sa_bool: bool = True
initial_sa_datetime: str = "2024-11-09T16:00:01.731455544-08:00"
initial_sa_keyword_array: list[str] = ["initial_keyword-1", "initial_keyword-2"]

sa_keyword: str = "keyword"
sa_double: float = 2.34
sa_int: int = 234
sa_bool: bool = False
sa_datetime: str = "2024-11-12T16:00:01.731455544-08:00"
sa_keyword_array: list[str] = ["keyword-1", "keyword-2"]

final_sa_keyword: str = "final_keyword"
final_sa_int: int = 567
final_sa_bool: bool = False
final_sa_datetime: str = "2024-12-13T16:00:01.731455544-08:00"
final_sa_keyword_array: list[str] = ["final_keyword-1", "final_keyword-2"]

class SearchAttributeStateInit(WorkflowState[None]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        return CommandRequest.for_all_command_completed(
            TimerCommand.by_seconds(2),
        )

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        persistence.set_search_attribute_keyword(sa_keyword_key, sa_keyword)
        persistence.set_search_attribute_double(sa_double_key, sa_double)
        persistence.set_search_attribute_boolean(sa_bool_key, sa_bool)
        persistence.set_search_attribute_keyword_array(
            sa_keyword_array_key, sa_keyword_array
        )
        persistence.set_search_attribute_int64(sa_int_key, sa_int)
        persistence.set_search_attribute_datetime(sa_datetime_key, sa_datetime)
        return StateDecision.single_next_state(SearchAttributeState1)

class SearchAttributeState1(WorkflowState[None]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        return CommandRequest.for_all_command_completed(
            TimerCommand.by_seconds(2),
        )

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        return StateDecision.single_next_state(SearchAttributeState2)

class SearchAttributeState2(WorkflowState[None]):
    def wait_until(
        self,
        ctx: WorkflowContext,
        input: T,
        persistence: Persistence,
        communication: Communication,
    ) -> CommandRequest:
        return CommandRequest.empty()

    def execute(
        self,
        ctx: WorkflowContext,
        input: T,
        command_results: CommandResults,
        persistence: Persistence,
        communication: Communication,
    ) -> StateDecision:
        # Delay updating search attributes to allow for the previous assertion
        sleep(1)
        persistence.set_search_attribute_keyword(sa_keyword_key, final_sa_keyword)
        persistence.set_search_attribute_boolean(sa_bool_key, final_sa_bool)
        persistence.set_search_attribute_keyword_array(
            sa_keyword_array_key, final_sa_keyword_array
        )
        persistence.set_search_attribute_int64(sa_int_key, final_sa_int)
        persistence.set_search_attribute_datetime(sa_datetime_key, final_sa_datetime)
        return StateDecision.graceful_complete_workflow()

class PersistenceSearchAttributesWorkflow(ObjectWorkflow):
    def get_workflow_states(self) -> StateSchema:
        return StateSchema.with_starting_state(
            SearchAttributeStateInit(), SearchAttributeState1(), SearchAttributeState2()
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.create(
            PersistenceField.search_attribute_def(
                sa_keyword_key, SearchAttributeValueType.KEYWORD
            ),
            PersistenceField.search_attribute_def(
                sa_double_key, SearchAttributeValueType.DOUBLE
            ),
            PersistenceField.search_attribute_def(
                sa_bool_key, SearchAttributeValueType.BOOL
            ),
            PersistenceField.search_attribute_def(
                sa_keyword_array_key, SearchAttributeValueType.KEYWORD_ARRAY
            ),
            PersistenceField.search_attribute_def(
                sa_int_key, SearchAttributeValueType.INT
            ),
            PersistenceField.search_attribute_def(
                sa_datetime_key, SearchAttributeValueType.DATETIME
            ),
        )
