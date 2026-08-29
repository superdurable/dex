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

from __future__ import annotations

from datetime import timedelta

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    Timer,
    Wait,
    force_complete,
    force_fail,
)

SLOW_TASK_DURATION = timedelta(seconds=65)


class LongWaitStep(Step[bool]):
    def wait_for(self, context: Context, input: bool) -> Wait:
        if input:
            return Wait.skip_immediately()
        return Wait.until(Timer.by_duration(SLOW_TASK_DURATION))

    def execute(self, context: Context, input: bool) -> StepDecision:
        return force_complete("Workflow completed successfully")


class FlowGracefulTimeout(Flow[bool]):
    def __init__(self) -> None:
        self.long_wait_step = LongWaitStep()

    def get_steps(self) -> StepList[bool]:
        return StepList.start_step(self.long_wait_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()

    def handle_timeout(self, context: Context) -> StepDecision:
        return force_fail("Workflow did not finish the task in time")
