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


# dex:group group-id:work-a group-label:"Work A"
# dex:explanation text:"Do work A."
class WorkA(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(f"A:{input}")


# dex:group group-id:work-b group-label:"Work B"
# dex:explanation text:"Do work B."
class WorkB(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return graceful_complete(f"B:{input}")


# dex:group group-id:setup group-label:"Setup"
# dex:explanation text:"Start work A and work B in parallel."
class InitStep(Step[str]):
    def execute(self, context: Context, input: str) -> StepDecision:
        return go_to_many(
            StepMovement.of(WorkA, input),
            StepMovement.of(WorkB, input),
        )


class StaticParallelStepsFlow(Flow[str]):
    def __init__(self) -> None:
        self.init = InitStep()
        self.work_a = WorkA()
        self.work_b = WorkB()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.init).other_steps(self.work_a, self.work_b)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
