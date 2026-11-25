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
from typing import Callable

import pytest
from dex_examples.app import ExampleApp
from dex_examples.config import start_options
from dex_examples.http_app import create_app
from dex_examples.products.microservices.orchestration_flow import OrchestrationFlow
from tests.integ.conftest import WAIT_TIMEOUT

from dex import (
    AsyncClient,
    AttributeMatch,
    Context,
    StepDecision,
    WaitForAttributeOptions,
    WorkerInvocationError,
)

pytestmark = pytest.mark.integ

INITIAL_DATA = "test initial data"


async def test_orchestration_completes_when_ready_is_published(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("microservice")
    await client.start_flow(app.orchestration, flow_id, INITIAL_DATA, start_options())

    await client.wait_for_attribute_match(
        flow_id,
        OrchestrationFlow.data,
        AttributeMatch.equal_to(INITIAL_DATA),
        WaitForAttributeOptions(request_timeout=WAIT_TIMEOUT),
    )
    await client.invoke_rpc(app.orchestration.signal_ready, flow_id)

    assert (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
        str
    ) == INITIAL_DATA


async def test_orchestration_swap_replaces_the_data_before_completion(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow_id = new_flow_id("microservice")
    await client.start_flow(app.orchestration, flow_id, INITIAL_DATA, start_options())

    await client.wait_for_attribute_match(
        flow_id,
        OrchestrationFlow.data,
        AttributeMatch.equal_to(INITIAL_DATA),
        WaitForAttributeOptions(request_timeout=WAIT_TIMEOUT),
    )
    assert await client.invoke_rpc(app.orchestration.swap, flow_id, "swapped data") == (
        INITIAL_DATA
    )

    await client.invoke_rpc(app.orchestration.signal_ready, flow_id)
    assert (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
        str
    ) == "swapped data"


async def test_orchestration_swap_before_call_api1_is_rejected(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    flow_id = new_flow_id("microservice")
    barrier = _ExecutionBarrier(app.orchestration.call_api1.execute)
    monkeypatch.setattr(app.orchestration.call_api1, "execute", barrier.execute)
    await client.start_flow(app.orchestration, flow_id, INITIAL_DATA, start_options())
    try:
        await asyncio.wait_for(barrier.started.wait(), WAIT_TIMEOUT.total_seconds())
        with pytest.raises(WorkerInvocationError, match="data is not ready yet"):
            await client.invoke_rpc(app.orchestration.swap, flow_id, "too early")
    finally:
        barrier.release.set()

    await client.wait_for_attribute_match(
        flow_id,
        OrchestrationFlow.data,
        AttributeMatch.equal_to(INITIAL_DATA),
        WaitForAttributeOptions(request_timeout=WAIT_TIMEOUT),
    )
    await client.invoke_rpc(app.orchestration.signal_ready, flow_id)
    assert (await client.wait_for_flow(flow_id, WAIT_TIMEOUT)).single_output(
        str
    ) == INITIAL_DATA


async def test_concurrent_swap_routes_retry_lock_conflicts(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    http_client = create_app(app).test_client()
    flow_id = new_flow_id("microservice")
    await client.start_flow(app.orchestration, flow_id, INITIAL_DATA, start_options())
    await client.wait_for_attribute_match(
        flow_id,
        OrchestrationFlow.data,
        AttributeMatch.equal_to(INITIAL_DATA),
        WaitForAttributeOptions(request_timeout=WAIT_TIMEOUT),
    )

    replacements = [f"swap-{index}" for index in range(8)]
    responses = await asyncio.gather(
        *(
            http_client.get(
                "/products/microservices/swap",
                query_string={"workflowId": flow_id, "data": replacement},
            )
            for replacement in replacements
        )
    )

    assert [response.status_code for response in responses] == [200] * len(responses)
    previous_values = [await response.get_data(as_text=True) for response in responses]
    final_value = await client.invoke_rpc(app.orchestration.get_data, flow_id)
    # Serialized swaps form one chain, so every written value is observed exactly once.
    assert sorted([*previous_values, final_value]) == sorted([INITIAL_DATA, *replacements])


class _ExecutionBarrier:
    def __init__(self, execute: Callable[[Context, str], StepDecision]) -> None:
        self.original_execute = execute
        self.started = asyncio.Event()
        self.release = asyncio.Event()

    async def execute(self, context: Context, input: str) -> StepDecision:
        self.started.set()
        await asyncio.wait_for(self.release.wait(), WAIT_TIMEOUT.total_seconds())
        return self.original_execute(context, input)
