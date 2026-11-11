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
from dataclasses import dataclass
from typing import Callable

from dex import (
    AsyncContext,
    Context,
    Flow,
    Join,
    JoinRound,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    Wait,
    go_to_many,
    graceful_complete,
)


@dataclass(frozen=True)
class WorkItem:
    index: int
    round: JoinRound


def build_work_item(index: int) -> Callable[[JoinRound], StepMovement[WorkItem]]:
    return lambda join_round: StepMovement.of(DoWork, WorkItem(index, join_round))


# dex:group group-id:parallel-work group-label:"Parallel work"
# dex:explanation text:"Do one unit of work and report that it finished."
class DoWork(Step[WorkItem]):
    def __init__(self, work_completed: Join[int]) -> None:
        self.work_completed = work_completed

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: WorkItem
    ) -> StepDecision:
        await asyncio.sleep(random.uniform(0.05, 0.5))
        return self.work_completed.end_branch(context, input.round, input.index)


# dex:group group-id:await group-label:"Await"
# dex:explanation text:"Wait for every work Step to report, then complete."
class Await(Step[JoinRound]):
    def __init__(self, work_completed: Join[int]) -> None:
        self.work_completed = work_completed

    def wait_for(self, context: Context, input: JoinRound) -> Wait:
        return Wait.until(self.work_completed.for_all_branches(input))

    def execute(self, context: Context, input: JoinRound) -> StepDecision:
        return graceful_complete(
            len(self.work_completed.branch_results(context, input))
        )


# dex:group group-id:setup group-label:"Setup"
# dex:explanation text:"Start one work Step per item and the Step that awaits them."
class InitStep(Step[int]):
    def __init__(self, work_completed: Join[int]) -> None:
        self.work_completed = work_completed

    def execute(self, context: Context, input: int) -> StepDecision:
        return go_to_many(
            *self.work_completed.fork(
                context,
                [build_work_item(index) for index in range(input)],
                lambda join_round: StepMovement.of(Await, join_round),
            )
        )


class AwaitParallelStepsFlow(Flow[int]):
    work_completed = Join("parallel-complete", int)

    def __init__(self) -> None:
        self.init = InitStep(self.work_completed)
        self.work = DoWork(self.work_completed)
        self.await_step = Await(self.work_completed)

    def get_steps(self) -> StepList[int]:
        return StepList.start_step(self.init).other_steps(self.work, self.await_step)

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.work_completed)
