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

"""Pass WaitFor-computed data to Execute without a Flow Attribute."""

from __future__ import annotations

from dex import (
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Wait,
    graceful_complete,
)

approval = Channel("Approval", str)


class NoteWaitStep(Step[int]):
    def wait_for(self, context: Context, input: int) -> Wait:
        context.set_step_execution_local("note", f"approval:{input}")
        return Wait.until(approval.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        note = context.get_step_execution_local("note", str)
        return graceful_complete(note or "")


class StepExecutionLocalFlow(Flow[int]):
    def __init__(self) -> None:
        self.note_wait = NoteWaitStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.note_wait)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(approval)
