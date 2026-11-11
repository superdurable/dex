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
from typing import Any

from dex import (
    AsyncContext,
    Channel,
    Context,
    Flow,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    dead_end,
    go_to_many,
    graceful_complete,
)


# dex:group group-id:parallel-work group-label:"Parallel work"
# dex:explanation text:"Do one unit of work and report that it finished."
class DoWork(Step[int]):
    def __init__(self, complete_ch: Channel[None]) -> None:
        self.complete_ch = complete_ch

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: int
    ) -> StepDecision:
        await asyncio.sleep(random.uniform(0.05, 0.5))
        self.complete_ch.publish(context, None)
        return dead_end()


# dex:group group-id:await group-label:"Await"
# dex:explanation text:"Wait for every work Step to report, then complete."
class Await(Step[int]):
    def __init__(self, complete_ch: Channel[None]) -> None:
        self.complete_ch = complete_ch

    def wait_for(self, context: Context, input: int) -> Wait:
        return Wait.until(self.complete_ch.for_n(input))

    def execute(self, context: Context, input: int) -> StepDecision:
        return graceful_complete(input)


# dex:group group-id:setup group-label:"Setup"
# dex:explanation text:"Start one work Step per item and the Step that awaits them."
class InitStep(Step[int]):
    def execute(self, context: Context, input: int) -> StepDecision:
        movements: list[StepMovement[Any]] = [StepMovement.of(Await, input)]
        movements.extend(
            StepMovement.of(DoWork, index) for index in range(input)
        )
        return go_to_many(*movements)


class AwaitParallelStepsFlow(Flow[int]):
    complete_ch = Channel[None]("parallel-complete", type(None))

    def __init__(self) -> None:
        self.init = InitStep()
        self.work = DoWork(self.complete_ch)
        self.await_step = Await(self.complete_ch)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.init).other_steps(self.work, self.await_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.complete_ch)
