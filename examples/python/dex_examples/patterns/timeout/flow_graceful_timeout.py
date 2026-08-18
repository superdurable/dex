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
    StepMovement,
    Timer,
    Wait,
    force_complete,
    force_fail,
    go_to_multi,
)

TIMEOUT_DURATION = timedelta(minutes=1)
SLOW_TASK_DURATION = timedelta(seconds=65)


class Timeout(Step[None]):
    def wait_for(self, context: Context, input: None) -> Wait:
        del context, input
        return Wait.until(Timer.by_duration(TIMEOUT_DURATION))

    def execute(self, context: Context, input: None) -> StepDecision:
        del context, input
        return force_fail("Workflow did not finish the task in time")


class Task(Step[bool]):
    def wait_for(self, context: Context, input: bool) -> Wait:
        del context
        if input:
            return Wait.skip_immediately()
        return Wait.until(Timer.by_duration(SLOW_TASK_DURATION))

    def execute(self, context: Context, input: bool) -> StepDecision:
        del context, input
        return force_complete("Workflow completed successfully")


class Init(Step[bool]):
    def __init__(self, timeout: Timeout, task: Task) -> None:
        self.timeout = timeout
        self.task = task

    def execute(self, context: Context, input: bool) -> StepDecision:
        del context
        return go_to_multi(
            StepMovement.of(self.timeout, None),
            StepMovement.of(self.task, input),
        )


class FlowGracefulTimeout(Flow[bool]):
    def __init__(self) -> None:
        self.timeout = Timeout()
        self.task = Task()
        self.init = Init(self.timeout, self.task)

    def get_steps(self) -> StepList[bool]:
        return StepList.start_step(self.init).other_steps(self.timeout, self.task)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
