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

from typing import Callable

from dex import (
    AsyncClient,
    AsyncContext,
    Flow,
    FlowNotActiveError,
    IdReusePolicy,
    StartFlowOptions,
    Step,
    StepDecision,
    StepList,
    graceful_complete,
)

from dex_examples.patterns.parallel_subflows.advanced_short_live_parent_flow import (
    AdvancedShortLiveParentFlow,
)
from dex_examples.patterns.parallel_subflows.models import (
    DEFAULT_CONCURRENCY,
    ParentInput,
    SubmitRequestInput,
)


class SubmitStep(Step[SubmitRequestInput]):
    def __init__(
        self,
        client_provider: Callable[[], AsyncClient],
        parent_flow: AdvancedShortLiveParentFlow,
    ) -> None:
        self.client_provider = client_provider
        self.parent_flow = parent_flow

    async def execute(  # type: ignore[override]
        self, context: AsyncContext, input: SubmitRequestInput
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
