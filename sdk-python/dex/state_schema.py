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

from dataclasses import dataclass, field
from typing import List

from dex.workflow_state import WorkflowState


@dataclass
class StateDef:
    state: WorkflowState
    can_start_workflow: bool

    @classmethod
    def starting_state(cls, state: WorkflowState):
        return StateDef(state, True)

    @classmethod
    def non_starting_state(cls, state: WorkflowState):
        return StateDef(state, False)


@dataclass
class StateSchema:
    states: List[StateDef] = field(default_factory=list)

    # TODO: it's super weird that we can't use type hint here " ->StateSchema" for return
    # But the pattern works for state_movement.py
    @classmethod
    def with_starting_state(
        cls, starting_state: WorkflowState, *non_starting_states: WorkflowState
    ):
        return StateSchema(
            [StateDef.starting_state(starting_state)]
            + [StateDef.non_starting_state(s) for s in non_starting_states]
        )

    @classmethod
    def no_starting_state(cls, *non_starting_states: WorkflowState):
        return StateSchema(
            [StateDef.non_starting_state(s) for s in non_starting_states]
        )
