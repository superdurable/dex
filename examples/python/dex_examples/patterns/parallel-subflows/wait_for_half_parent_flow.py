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

from typing import Callable

from dex import (
    AsyncClient,
    AsyncContext,
    Channel,
    Context,
    Flow,
    FlowStatus,
    PersistenceSchema,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    SubFlow,
    Wait,
    go_to_many,
    graceful_complete,
)

from dex_examples.patterns.parallel_subflows.example_subflow import ExampleSubFlow


class WaitForHalfInitStep(Step[list[str]]):
    def execute(self, context: Context, requests: list[str]) -> StepDecision:
        if not requests:
            return graceful_complete()
        return go_to_many(
            StepMovement.of(WaitSubFlowsStep, len(requests)),
            *(StepMovement.of(SubFlowStep, request) for request in requests),
        )


class SubFlowStep(Step[str]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        example_subflow: ExampleSubFlow,
        subflow_completed_ch: Channel[bool],
        all_done_ch: Channel[bool],
    ) -> None:
        self.client_provider = client_provider
        self.example_subflow = example_subflow
        self.subflow_completed_ch = subflow_completed_ch
        self.all_done_ch = all_done_ch

    def wait_for(self, context: Context, request: str) -> Wait:
        return Wait.any_of(
            SubFlow.run(self.example_subflow, request), self.all_done_ch.for_one()
        )

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, request: str
    ) -> StepDecision:
        result = SubFlow.get_condition_results(context)
        if result.status is not FlowStatus.RUNNING:
            self.subflow_completed_ch.publish(context, True)
            return graceful_complete()
        await self.client_provider().stop_flow(SubFlow.get_flow_id(context))
        return graceful_complete()


class WaitSubFlowsStep(Step[int]):
    def __init__(
        self, subflow_completed_ch: Channel[bool], all_done_ch: Channel[bool]
    ) -> None:
        self.subflow_completed_ch = subflow_completed_ch
        self.all_done_ch = all_done_ch

    def wait_for(self, context: Context, total: int) -> Wait:
        return Wait.until(self.subflow_completed_ch.for_n((total + 1) // 2))

    def execute(self, context: Context, total: int) -> StepDecision:
        for _ in range(total - (total + 1) // 2):
            self.all_done_ch.publish(context, True)
        return graceful_complete()


class WaitForHalfParentFlow(Flow[list[str]]):
    subflow_completed_ch = Channel("SubFlowCompletedCh", bool)
    all_done_ch = Channel("AllDoneCh", bool)

    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        example_subflow: ExampleSubFlow,
    ) -> None:
        self.init = WaitForHalfInitStep()
        self.subflow = SubFlowStep(
            client_provider, example_subflow, self.subflow_completed_ch, self.all_done_ch
        )
        self.wait_subflows = WaitSubFlowsStep(
            self.subflow_completed_ch, self.all_done_ch
        )

    def get_steps(self) -> StepList[list[str]]:
        return StepList.start_step(self.init).other_steps(
            self.subflow, self.wait_subflows
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.subflow_completed_ch, self.all_done_ch)
