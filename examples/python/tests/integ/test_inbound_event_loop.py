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

"""Inbound event loop design pattern."""

from __future__ import annotations

from typing import Callable

import pytest
from dex import (
    AsyncClient,
    DexServiceError,
    FlowStatus,
    StartFlowOptions,
    StepExecutionId,
    TimerId,
    WorkerInvocationError,
)

from dex_examples.app import ExampleApp
from dex_examples.patterns.inbound_event_loop.support_ticket_flow import (
    CURRENT_TRANSCRIPT_CHUNK,
    TRANSCRIPT_CHUNKS,
)
from tests.integ.conftest import WAIT_TIMEOUT, wait_until

pytestmark = pytest.mark.integ


async def transcript_texts(client: AsyncClient, flow_id: str) -> list[str]:
    current_chunk = TRANSCRIPT_CHUNKS.instance(CURRENT_TRANSCRIPT_CHUNK)
    values = await client.get_attributes(flow_id, current_chunk)
    chunk = values.get(current_chunk)
    return [reply.text for reply in chunk.replies] if chunk else []


async def completed_output(client: AsyncClient, flow_id: str) -> str:
    result = await client.wait_for_flow(flow_id, WAIT_TIMEOUT)
    assert result.status is FlowStatus.COMPLETED, result.error_message
    outputs = [completion.decode(str) for completion in result.completions]
    assert len(outputs) == 1, outputs
    return outputs[0]


async def test_inbound_event_loop_records_replies_before_the_resolution(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = app.support_ticket
    flow_id = new_flow_id("inbound-event-loop")
    await client.start_flow(flow, flow_id, "", StartFlowOptions())

    with pytest.raises(WorkerInvocationError, match="reply text is required"):
        await client.invoke_rpc(flow.send_ticket_reply, flow_id, "  ")
    with pytest.raises(WorkerInvocationError, match="resolution reason is required"):
        await client.invoke_rpc(flow.resolve_ticket, flow_id, " ")

    await client.invoke_rpc(flow.send_ticket_reply, flow_id, "printer is offline")
    await client.invoke_rpc(flow.send_ticket_reply, flow_id, "restarted it, still offline")
    await client.invoke_rpc(flow.resolve_ticket, flow_id, "replaced the network cable")

    assert await completed_output(client, flow_id) == "resolved: replaced the network cable"
    assert await transcript_texts(client, flow_id) == [
        "printer is offline",
        "restarted it, still offline",
    ]


async def test_inbound_event_loop_closes_a_quiet_ticket(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = app.support_ticket
    flow_id = new_flow_id("inbound-event-loop-inactive")
    await client.start_flow(flow, flow_id, "", StartFlowOptions())
    await client.invoke_rpc(flow.send_ticket_reply, flow_id, "is anyone there?")
    await wait_until(
        "the reply to be recorded",
        lambda: _has_transcript(client, flow_id, ["is anyone there?"]),
    )

    async def is_timer_skipped() -> bool:
        # The reply ended execution 1, so execution 2 holds the inactivity Timer.
        try:
            await client.skip_timer(
                flow_id,
                StepExecutionId("AwaitTicketMessage", 2),
                TimerId(condition_index=0),
            )
        except DexServiceError:
            return False
        return True

    await wait_until("the inactivity Timer to be skipped", is_timer_skipped)
    assert await completed_output(client, flow_id) == "closed after inactivity"


async def _has_transcript(
    client: AsyncClient, flow_id: str, expected: list[str]
) -> bool:
    return await transcript_texts(client, flow_id) == expected
