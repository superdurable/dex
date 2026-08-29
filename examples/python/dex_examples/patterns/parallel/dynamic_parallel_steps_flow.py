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

import asyncio
import random

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


class DoWorkStep(Step[int]):
    async def execute(  # type: ignore[override]
        self, context: Context, input: int
    ) -> StepDecision:
        await asyncio.sleep(random.uniform(0.05, 0.5))
        return graceful_complete(input)


class InitStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to_many(
            *(StepMovement.of(DoWorkStep, index) for index in range(input))
        )


class DynamicParallelStepsFlow(Flow[int]):
    def __init__(self) -> None:
        self.init = InitStep()
        self.work = DoWorkStep()

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.init).other_steps(self.work)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of()
