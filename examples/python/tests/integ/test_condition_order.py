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

"""The declared-first AnyOf condition wins when both messages are pending."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator, Callable
from dataclasses import dataclass

import pytest
import pytest_asyncio
from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    FlowStatus,
    Registry,
    StartFlowOptions,
    WorkerOptions,
    open_blob_cache,
)

from dex_examples.shared.worker_options import (
    PULL_REGISTRATION_TIMEOUT_SECONDS,
    new_example_pull_worker_options,
)
from tests.integ.cancel_case_order_probe_flow import CancelCaseOrderProbeFlow
from tests.integ.condition_order_probe import ConditionOrderOutcome
from tests.integ.conftest import WAIT_TIMEOUT, server_address
from tests.integ.deferred_choice_order_probe_flow import DeferredChoiceOrderProbeFlow

pytestmark = pytest.mark.integ


@dataclass(frozen=True)
class ConditionOrderProbes:
    deferred_choice: DeferredChoiceOrderProbeFlow
    cancel_case: CancelCaseOrderProbeFlow
    client: AsyncClient


@pytest_asyncio.fixture(scope="module", loop_scope="session")
async def probes(
    client: AsyncClient, tmp_path_factory: pytest.TempPathFactory
) -> AsyncIterator[ConditionOrderProbes]:
    """Serves the test-only probe Flows from their own Worker."""
    deferred_choice = DeferredChoiceOrderProbeFlow()
    cancel_case = CancelCaseOrderProbeFlow()
    registry = Registry((deferred_choice, cancel_case))
    cache_dir = tmp_path_factory.mktemp("condition-order-probe-cache")
    cache = open_blob_cache(BlobCacheConfig(str(cache_dir), 64 << 20))
    worker = AsyncWorker(
        registry,
        cache,
        WorkerOptions(
            server_address=server_address(), pull=new_example_pull_worker_options()
        ),
    )
    probe_client = AsyncClient(
        registry, cache, ClientOptions(server_address=server_address())
    )
    worker_task = asyncio.create_task(worker.start())
    try:
        await worker.wait_pull_registered(timeout=PULL_REGISTRATION_TIMEOUT_SECONDS)
        yield ConditionOrderProbes(deferred_choice, cancel_case, probe_client)
    finally:
        await probe_client.close()
        await worker.close()
        await worker_task
        cache.close()


async def test_deferred_choice_declared_first_response_wins_when_both_are_pending(
    probes: ConditionOrderProbes, new_flow_id: Callable[[str], str]
) -> None:
    flow = probes.deferred_choice
    flow_id = await start_probe(probes, flow, new_flow_id("pattern-deferred-choice-order"))
    # Publish the later-declared response first: declaration order wins, not publish order.
    await probes.client.invoke_rpc(flow.reject_probe_offer, flow_id, "no")
    await probes.client.invoke_rpc(flow.accept_probe_offer, flow_id, "yes")
    await probes.client.invoke_rpc(flow.open_probe_gate, flow_id)
    assert await probe_outcomes(probes, flow_id, "AwaitOfferProbeResponse") == [
        ConditionOrderOutcome("accepted", 1)
    ]


async def test_cancel_case_cancel_wins_when_both_are_pending(
    probes: ConditionOrderProbes, new_flow_id: Callable[[str], str]
) -> None:
    flow = probes.cancel_case
    flow_id = await start_probe(probes, flow, new_flow_id("pattern-cancel-case-order"))
    await probes.client.invoke_rpc(flow.decide_probe_application, flow_id, "approved")
    await probes.client.invoke_rpc(
        flow.cancel_probe_application, flow_id, "applicant withdrew"
    )
    await probes.client.invoke_rpc(flow.open_probe_gate, flow_id)
    assert await probe_outcomes(probes, flow_id, "AwaitProbeDecision") == [
        ConditionOrderOutcome("canceled", 1)
    ]


async def start_probe(
    probes: ConditionOrderProbes,
    flow: DeferredChoiceOrderProbeFlow | CancelCaseOrderProbeFlow,
    flow_id: str,
) -> str:
    await probes.client.start_flow(flow, flow_id, None, StartFlowOptions())
    return flow_id


async def probe_outcomes(
    probes: ConditionOrderProbes, flow_id: str, step_type: str
) -> list[ConditionOrderOutcome]:
    result = await probes.client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.COMPLETED, result.error_message
    return [
        completion.decode(ConditionOrderOutcome)
        for completion in result.completions
        if completion.step_type == step_type
    ]
