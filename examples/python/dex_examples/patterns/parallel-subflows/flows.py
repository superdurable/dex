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

import asyncio
from dataclasses import dataclass
from typing import Callable

from dex import (
    AsyncClient,
    Attribute,
    Channel,
    Context,
    Flow,
    FlowNotActiveError,
    FlowStatus,
    IdReusePolicy,
    PersistenceSchema,
    RPCResult,
    Step,
    StepDecision,
    StepList,
    StepMovement,
    StepOptions,
    StartFlowOptions,
    SubFlow,
    Wait,
    force_complete_if_channels_empty,
    go_to,
    go_to_many,
    graceful_complete,
    rpc,
)

DEFAULT_CONCURRENCY = 10
MAX_BUFFERED_REQUESTS = 100


@dataclass(frozen=True)
class ParentInput:
    requests: list[str]
    concurrency: int = DEFAULT_CONCURRENCY


@dataclass(frozen=True)
class SubmitRequestInput:
    request: str
    parent_ids: list[str]


class DoWorkStep(Step[str]):
    async def execute(self, context: Context, request: str) -> StepDecision:
        await asyncio.sleep((50 + len(request) % 10 * 50) / 1000)
        return graceful_complete(request)


class ExampleSubFlow(Flow[str]):
    def __init__(self) -> None:
        self.do_work = DoWorkStep()

    def get_steps(self) -> StepList[str]:
        return StepList.start_step(self.do_work)


class SubFlowsStep(Step[list[str]]):
    def __init__(self, example_subflow: ExampleSubFlow) -> None:
        self.example_subflow = example_subflow

    def wait_for(self, context: Context, requests: list[str]) -> Wait:
        return Wait.all_of(
            *(SubFlow.run(self.example_subflow, request) for request in requests)
        )

    def execute(self, context: Context, requests: list[str]) -> StepDecision:
        return graceful_complete()


class BasicParentFlow(Flow[list[str]]):
    def __init__(self, example_subflow: ExampleSubFlow) -> None:
        self.subflows = SubFlowsStep(example_subflow)

    def get_steps(self) -> StepList[list[str]]:
        return StepList.start_step(self.subflows)


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
        self, context: Context, request: str
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


class LongLiveInitStep(Step[ParentInput]):
    def __init__(
        self,
        request_channel: Channel[str],
        stopped: Attribute[bool],
    ) -> None:
        self.request_channel = request_channel
        self.stopped = stopped

    def get_step_type(self) -> str:
        return "InitStep"

    def execute(self, context: Context, input: ParentInput) -> StepDecision:
        for request in input.requests:
            self.request_channel.publish(context, request)
        self.stopped.set(context, False)
        concurrency = input.concurrency if input.concurrency > 0 else DEFAULT_CONCURRENCY
        return go_to_many(
            *(StepMovement.of(LongLiveHandleRequestStep, None) for _ in range(concurrency))
        )


class LongLiveHandleRequestStep(Step[None]):
    def __init__(self, request_channel: Channel[str]) -> None:
        self.request_channel = request_channel

    def get_step_type(self) -> str:
        return "HandleRequestStep"

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(self.request_channel.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        return go_to(LongLiveHandleSubFlowStep, self.request_channel.results(context)[0])


class LongLiveHandleSubFlowStep(Step[str]):
    def __init__(
        self,
        example_subflow: ExampleSubFlow,
        stopped: Attribute[bool],
    ) -> None:
        self.example_subflow = example_subflow
        self.stopped = stopped

    def get_step_type(self) -> str:
        return "HandleSubFlowStep"

    def wait_for(self, context: Context, request: str) -> Wait:
        return Wait.until(SubFlow.run(self.example_subflow, request))

    def execute(self, context: Context, request: str) -> StepDecision:
        if self.stopped.get(context):
            return graceful_complete()
        return go_to(LongLiveHandleRequestStep, None)


class AdvancedLongLiveParentFlow(Flow[ParentInput]):
    request_channel = Channel("RequestChannel", str)
    stopped = Attribute("Stopped", bool)

    def __init__(self, example_subflow: ExampleSubFlow) -> None:
        self.init = LongLiveInitStep(self.request_channel, self.stopped)
        self.handle_request = LongLiveHandleRequestStep(self.request_channel)
        self.handle_subflow = LongLiveHandleSubFlowStep(example_subflow, self.stopped)

    def get_steps(self) -> StepList[ParentInput]:
        return StepList.start_step(self.init).other_steps(
            self.handle_request, self.handle_subflow
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.request_channel, self.stopped)

    @rpc
    def send_request(self, context: Context, request: str) -> RPCResult[bool]:
        if self.request_channel.size(context) >= MAX_BUFFERED_REQUESTS:
            return RPCResult(False)
        self.request_channel.publish(context, request)
        return RPCResult(True)

    @rpc
    def stop(self, context: Context) -> None:
        self.stopped.set(context, True)


class ShortLiveInitStep(Step[ParentInput]):
    def __init__(
        self,
        request_channel: Channel[str],
        curr_subflow_num: Attribute[int],
    ) -> None:
        self.request_channel = request_channel
        self.curr_subflow_num = curr_subflow_num

    def get_step_type(self) -> str:
        return "InitStep"

    def execute(self, context: Context, input: ParentInput) -> StepDecision:
        for request in input.requests:
            self.request_channel.publish(context, request)
        self.curr_subflow_num.set(context, 0)
        concurrency = input.concurrency if input.concurrency > 0 else DEFAULT_CONCURRENCY
        return go_to_many(
            *(StepMovement.of(ShortLiveHandleRequestStep, None) for _ in range(concurrency))
        )


class ShortLiveHandleRequestStep(Step[None]):
    def __init__(
        self,
        request_channel: Channel[str],
        curr_subflow_num: Attribute[int],
    ) -> None:
        self.request_channel = request_channel
        self.curr_subflow_num = curr_subflow_num

    def get_step_type(self) -> str:
        return "HandleRequestStep"

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_lock_attributes=(self.curr_subflow_num.lock(),))

    def wait_for(self, context: Context, input: None) -> Wait:
        return Wait.until(self.request_channel.for_one())

    def execute(self, context: Context, input: None) -> StepDecision:
        request = self.request_channel.results(context)[0]
        self.curr_subflow_num.set(context, (self.curr_subflow_num.get(context) or 0) + 1)
        return go_to(ShortLiveHandleSubFlowStep, request)


class ShortLiveHandleSubFlowStep(Step[str]):
    def __init__(
        self,
        example_subflow: ExampleSubFlow,
        request_channel: Channel[str],
        curr_subflow_num: Attribute[int],
    ) -> None:
        self.example_subflow = example_subflow
        self.request_channel = request_channel
        self.curr_subflow_num = curr_subflow_num

    def get_step_type(self) -> str:
        return "HandleSubFlowStep"

    def get_step_options(self) -> StepOptions:
        return StepOptions(execute_lock_attributes=(self.curr_subflow_num.lock(),))

    def wait_for(self, context: Context, request: str) -> Wait:
        return Wait.until(SubFlow.run(self.example_subflow, request))

    def execute(self, context: Context, request: str) -> StepDecision:
        current = (self.curr_subflow_num.get(context) or 0) - 1
        self.curr_subflow_num.set(context, current)
        if current == 0:
            return force_complete_if_channels_empty(
                None,
                StepMovement.of(ShortLiveHandleRequestStep, None),
                self.request_channel,
            )
        return go_to(ShortLiveHandleRequestStep, None)


class AdvancedShortLiveParentFlow(Flow[ParentInput]):
    request_channel = Channel("RequestChannel", str)
    curr_subflow_num = Attribute("CurrSubFlowNum", int)

    def __init__(self, example_subflow: ExampleSubFlow) -> None:
        self.init = ShortLiveInitStep(self.request_channel, self.curr_subflow_num)
        self.handle_request = ShortLiveHandleRequestStep(
            self.request_channel, self.curr_subflow_num
        )
        self.handle_subflow = ShortLiveHandleSubFlowStep(
            example_subflow, self.request_channel, self.curr_subflow_num
        )

    def get_steps(self) -> StepList[ParentInput]:
        return StepList.start_step(self.init).other_steps(
            self.handle_request, self.handle_subflow
        )

    def get_persistence_schema(self) -> PersistenceSchema:
        return PersistenceSchema.of(self.request_channel, self.curr_subflow_num)

    @rpc
    def send_request(self, context: Context, request: str) -> RPCResult[bool]:
        if self.request_channel.size(context) >= MAX_BUFFERED_REQUESTS:
            return RPCResult(False)
        self.request_channel.publish(context, request)
        return RPCResult(True)


class SubmitStep(Step[SubmitRequestInput]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        parent_flow: AdvancedShortLiveParentFlow,
    ) -> None:
        self.client_provider = client_provider
        self.parent_flow = parent_flow

    async def execute(  # type: ignore[override]
        self, context: Context, input: SubmitRequestInput
    ) -> StepDecision:
        if not input.parent_ids:
            raise ValueError("at least one parent Flow ID is required")
        parent_id = input.parent_ids[partition(input.request, len(input.parent_ids))]
        accepted = await enqueue_request(
            self.client_provider(), self.parent_flow, parent_id, input.request
        )
        if not accepted:
            raise RuntimeError(f"parent {parent_id} rejected the request")
        return graceful_complete(parent_id)


async def enqueue_request(
    client: AsyncClient,
    parent_flow: AdvancedShortLiveParentFlow,
    parent_id: str,
    request: str,
) -> bool:
    try:
        return await client.invoke_rpc(parent_flow.send_request, parent_id, request)
    except FlowNotActiveError:
        await client.start_flow(
            parent_flow,
            parent_id,
            ParentInput([request], DEFAULT_CONCURRENCY),
            StartFlowOptions(id_reuse_policy=IdReusePolicy.ALLOW_IF_NOT_RUNNING),
        )
        return True


def partition(request: str, partitions: int) -> int:
    hash_value = 2_166_136_261
    for byte in request.encode():
        hash_value ^= byte
        hash_value = hash_value * 16_777_619 & 0xFFFFFFFF
    return hash_value % partitions


class SubmitRequestFlow(Flow[SubmitRequestInput]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        parent_flow: AdvancedShortLiveParentFlow,
    ) -> None:
        self.submit = SubmitStep(client_provider, parent_flow)

    def get_steps(self) -> StepList[SubmitRequestInput]:
        return StepList.start_step(self.submit)
