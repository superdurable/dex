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

"""The Decision with Deadline response window answers every response exactly once."""

from __future__ import annotations

import asyncio
from collections.abc import AsyncIterator, Callable
from dataclasses import dataclass
from typing import Any

import pytest
import pytest_asyncio
from dex import (
    AsyncClient,
    AsyncWorker,
    BlobCacheConfig,
    ClientOptions,
    Registry,
    RpcLockConflictError,
    StartFlowOptions,
    WorkerOptions,
    open_blob_cache,
)
from dex.flow_state import StepMethodName, StepMethodPendingReason

from dex_examples.patterns.deferred_choice.offer_response_flow import (
    OFFER_RESPONSE_RECORDED,
    OFFER_RESPONSE_WINDOW_CLOSED,
)
from dex_examples.shared.worker_options import (
    PULL_REGISTRATION_TIMEOUT_SECONDS,
    new_example_pull_worker_options,
)
from tests.integ.conftest import WAIT_TIMEOUT, server_address, wait_until
from tests.integ.offer_response_window_probe_flow import (
    HELD_RESPONSE,
    OfferResponseWindowProbeFlow,
)
from tests.integ.test_control_structure_patterns import (
    outputs_of,
    skip_offer_expiry,
    wait_for_completed_flow,
    wait_for_step,
)

pytestmark = pytest.mark.integ

LOCK_CONFLICT_ATTEMPTS = 10
LOCK_CONFLICT_BACKOFF_SECONDS = 0.2


@dataclass(frozen=True)
class OfferResponseWindowProbe:
    flow: OfferResponseWindowProbeFlow
    client: AsyncClient


@pytest_asyncio.fixture(scope="module", loop_scope="session")
async def probe(
    tmp_path_factory: pytest.TempPathFactory,
) -> AsyncIterator[OfferResponseWindowProbe]:
    """Serves the test-only probe Flow from its own Worker."""
    flow = OfferResponseWindowProbeFlow()
    # The held response RPC is async, so it does not block the Worker's event loop.
    registry = Registry((flow,), allow_async_handlers=True)
    cache_dir = tmp_path_factory.mktemp("offer-response-window-probe-cache")
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
        yield OfferResponseWindowProbe(flow, probe_client)
    finally:
        await probe_client.close()
        await worker.close()
        await worker_task
        cache.close()


async def start_probe(
    probe: OfferResponseWindowProbe, flow_id: str, offer_id: str
) -> str:
    await probe.client.start_flow(probe.flow, flow_id, offer_id, StartFlowOptions())
    return flow_id


async def test_deferred_choice_response_after_the_decision_gets_window_closed(
    probe: OfferResponseWindowProbe, new_flow_id: Callable[[str], str]
) -> None:
    flow, client = probe.flow, probe.client
    flow_id = await start_probe(probe, new_flow_id("pattern-deferred-choice-window"), "offer-3")
    assert await client.invoke_rpc(flow.accept_probe_offer, flow_id, "yes") == (
        OFFER_RESPONSE_RECORDED
    )
    await wait_for_step(client, flow_id, "AwaitOfferResponse")

    assert await client.invoke_rpc(flow.reject_probe_offer, flow_id, "changed my mind") == (
        OFFER_RESPONSE_WINDOW_CLOSED
    )

    await client.invoke_rpc(flow.open_probe_gate, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "FulfillOffer", str) == ["fulfilled offer-3"]
    assert outputs_of(result, "CloseRejectedOffer", str) == []


async def test_deferred_choice_only_the_first_of_two_responses_is_recorded(
    probe: OfferResponseWindowProbe, new_flow_id: Callable[[str], str]
) -> None:
    flow, client = probe.flow, probe.client
    flow_id = await start_probe(probe, new_flow_id("pattern-deferred-choice-double"), "offer-5")
    # Both responses go out at once, before the decision; the first one closes the window.
    first = await invoke_retrying_lock_conflicts(client, flow.accept_probe_offer, flow_id, "yes")
    second = await invoke_retrying_lock_conflicts(client, flow.reject_probe_offer, flow_id, "no")
    assert (first, second) == (OFFER_RESPONSE_RECORDED, OFFER_RESPONSE_WINDOW_CLOSED)

    await client.invoke_rpc(flow.open_probe_gate, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "FulfillOffer", str) == ["fulfilled offer-5"]
    assert outputs_of(result, "CloseRejectedOffer", str) == []


async def invoke_retrying_lock_conflicts(
    client: AsyncClient, rpc: Callable[..., Any], flow_id: str, note: str
) -> str:
    """Invokes a response RPC, retrying lock conflicts with the decision a bounded number of times."""
    for _ in range(LOCK_CONFLICT_ATTEMPTS - 1):
        try:
            return await client.invoke_rpc(rpc, flow_id, note)
        except RpcLockConflictError:
            await asyncio.sleep(LOCK_CONFLICT_BACKOFF_SECONDS)
    return await client.invoke_rpc(rpc, flow_id, note)


async def is_execute_waiting_for_locks(client: AsyncClient, flow_id: str, step_type: str) -> bool:
    state = await client.get_flow_state(flow_id)
    return any(
        execution.step_type == step_type
        and execution.invocation is not None
        and execution.invocation.method is StepMethodName.EXECUTE
        and execution.invocation.pending_reason is StepMethodPendingReason.WAITING_FOR_LOCKS
        for execution in state.active_step_executions
    )


async def test_deferred_choice_response_committed_after_the_timer_won_is_taken(
    probe: OfferResponseWindowProbe, new_flow_id: Callable[[str], str]
) -> None:
    flow, client = probe.flow, probe.client
    flow_id = await start_probe(probe, new_flow_id("pattern-deferred-choice-late"), "offer-4")
    await wait_for_step(client, flow_id, "OpenOfferProbe")

    held = asyncio.create_task(
        client.invoke_rpc(flow.accept_probe_offer_when_released, flow_id, "yes, just in time")
    )
    try:
        await asyncio.wait_for(HELD_RESPONSE.entered.wait(), WAIT_TIMEOUT.total_seconds())
        # The Timer wins while the RPC holds the window lock, so Execute waits for the RPC.
        await skip_offer_expiry(client, flow_id)
        await wait_until(
            "AwaitOfferResponse execute to wait for the window lock",
            lambda: is_execute_waiting_for_locks(client, flow_id, "AwaitOfferResponse"),
        )
    finally:
        HELD_RESPONSE.released.set()
    assert await held == OFFER_RESPONSE_RECORDED

    await wait_for_step(client, flow_id, "RouteResponseBeforeExpiry")
    await client.invoke_rpc(flow.open_probe_gate, flow_id)
    result = await wait_for_completed_flow(client, flow_id)
    assert outputs_of(result, "FulfillOffer", str) == ["fulfilled offer-4"]
    assert outputs_of(result, "ExpireOffer", str) == []
