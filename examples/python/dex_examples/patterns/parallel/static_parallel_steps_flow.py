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

from dex import (
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    go_to_many,
    graceful_complete,
)


class WorkAStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(f"A:{input}")


class WorkBStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(f"B:{input}")


class InitStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return go_to_many(
            StepMovement.of(WorkAStep, input),
            StepMovement.of(WorkBStep, input),
        )


class StaticParallelStepsFlow(Flow[str]):
    def __init__(self) -> None:
        self.init = InitStep()
        self.work_a = WorkAStep()
        self.work_b = WorkBStep()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.init).other_steps(self.work_a, self.work_b)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
