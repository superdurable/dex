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

"""Minimal Step Flow: wait_for, execute, and go_to between two Steps."""

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
    go_to,
    graceful_complete,
)

approval = Channel("Approval", str)


class StepSecond(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(input + 1)


class ExampleStep(Step[int]):
    def __init__(self, second: StepSecond) -> None:
        self.second = second

    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(approval.for_one())

    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to(StepSecond, input + 1)


class StepFlow(Flow[int]):
    def __init__(self) -> None:
        self.second = StepSecond()
        self.example = ExampleStep(self.second)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.example).other_steps(self.second)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(approval)
