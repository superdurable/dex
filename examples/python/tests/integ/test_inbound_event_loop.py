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

import asyncio
from typing import Any, Callable

import pytest
from dex import (
    AsyncClient,
    Context,
    DexServiceError,
    FlowStatus,
    StartFlowOptions,
    Step,
    StepExecutionId,
    TimerId,
    WorkerInvocationError,
)

from dex_examples.app import ExampleApp
from dex_examples.patterns.inbound_event_loop.support_ticket_flow import (
    CURRENT_TRANSCRIPT_CHUNK,
    MAX_REPLY_LENGTH,
    TRANSCRIPT_CHUNK_SIZE,
    TRANSCRIPT_CHUNKS,
    archive_chunk_token,
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


async def test_inbound_event_loop_counts_reply_length_in_characters(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
) -> None:
    flow = app.support_ticket
    flow_id = new_flow_id("inbound-event-loop-length")
    await client.start_flow(flow, flow_id, "", StartFlowOptions())
    # Each emoji is one character but four UTF-8 bytes and two UTF-16 code units.
    longest_reply = "😀" * MAX_REPLY_LENGTH

    with pytest.raises(WorkerInvocationError, match="reply text exceeds 4000 characters"):
        await client.invoke_rpc(flow.send_ticket_reply, flow_id, longest_reply + "😀")
    await client.invoke_rpc(flow.send_ticket_reply, flow_id, longest_reply)
    await client.invoke_rpc(flow.resolve_ticket, flow_id, "answered")

    assert await completed_output(client, flow_id) == "resolved: answered"
    assert await transcript_texts(client, flow_id) == [longest_reply]


class CloseStepGate:
    """Holds the first execution of a close Step of one Flow until the test releases it."""

    def __init__(self, flow_id: str, step: Step[Any]) -> None:
        self.flow_id = flow_id
        self.entered = asyncio.Event()
        self.released = asyncio.Event()
        self._original_execute = step.execute

    def execute(self, context: Context, input: Any) -> Any:
        if context.flow_id != self.flow_id or self.entered.is_set():
            return self._original_execute(context, input)
        self.entered.set()
        return self._execute_after_release(context, input)

    async def _execute_after_release(self, context: Context, input: Any) -> Any:
        await asyncio.wait_for(self.released.wait(), WAIT_TIMEOUT.total_seconds())
        return self._original_execute(context, input)


async def test_inbound_event_loop_falls_back_for_messages_sent_during_each_close(
    app: ExampleApp,
    client: AsyncClient,
    new_flow_id: Callable[[str], str],
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    flow = app.support_ticket
    flow_id = new_flow_id("inbound-event-loop-close-fallback")
    inactive_gate = CloseStepGate(flow_id, flow.close_inactive_ticket)
    resolved_gate = CloseStepGate(flow_id, flow.close_resolved_ticket)
    monkeypatch.setattr(flow.close_inactive_ticket, "execute", inactive_gate.execute)
    monkeypatch.setattr(flow.close_resolved_ticket, "execute", resolved_gate.execute)
    await client.start_flow(flow, flow_id, "", StartFlowOptions())

    async def is_timer_skipped() -> bool:
        try:
            await client.skip_timer(
                flow_id,
                StepExecutionId("AwaitTicketMessage", 1),
                TimerId(condition_index=0),
            )
        except DexServiceError:
            return False
        return True

    await wait_until("the inactivity Timer to be skipped", is_timer_skipped)
    await asyncio.wait_for(inactive_gate.entered.wait(), WAIT_TIMEOUT.total_seconds())
    # The resolution commits before the inactivity close checks its Channels.
    await client.invoke_rpc(flow.resolve_ticket, flow_id, "replaced the network cable")
    inactive_gate.released.set()

    await asyncio.wait_for(resolved_gate.entered.wait(), WAIT_TIMEOUT.total_seconds())
    reply_count = TRANSCRIPT_CHUNK_SIZE + 1
    replies = [f"reply {sequence}" for sequence in range(1, reply_count + 1)]
    for reply in replies:
        await client.invoke_rpc(flow.send_ticket_reply, flow_id, reply)
    resolved_gate.released.set()

    assert await completed_output(client, flow_id) == "resolved: replaced the network cable"
    archived_chunk = TRANSCRIPT_CHUNKS.instance(archive_chunk_token(1))
    current_chunk = TRANSCRIPT_CHUNKS.instance(CURRENT_TRANSCRIPT_CHUNK)
    values = await client.get_attributes(flow_id, archived_chunk, current_chunk)
    archived = values.get(archived_chunk)
    current = values.get(current_chunk)
    assert archived is not None and current is not None
    assert [reply.sequence for reply in archived.replies] == list(
        range(1, TRANSCRIPT_CHUNK_SIZE + 1)
    )
    assert [reply.sequence for reply in current.replies] == [reply_count]
    assert [reply.text for reply in archived.replies + current.replies] == replies


async def _has_transcript(
    client: AsyncClient, flow_id: str, expected: list[str]
) -> bool:
    return await transcript_texts(client, flow_id) == expected
