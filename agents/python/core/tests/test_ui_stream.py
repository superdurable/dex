# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Server-Sent Events and snapshot JSON for browsers, resumed with Last-Event-ID."""

from __future__ import annotations

import asyncio
import json
from collections.abc import AsyncGenerator, Callable, Generator
from dataclasses import dataclass
from datetime import timedelta
from pathlib import Path
from typing import Any

from dex import BlobCacheConfig, Client, ClientOptions, Registry, StreamMessage, open_blob_cache

from dex_agents import AgentEvent, AgentEventType
from dex_agents.testing import AgentTestStack, wait_for_agent_snapshot, wait_until
from dex_agents.testing.worker import TEST_BLOB_CACHE_BYTES
from dex_agents.ui_stream import (
    AGENT_EVENT_STREAM_HEADERS,
    KEEP_ALIVE_FRAME,
    InvalidAgentEventResumeTokenError,
    _parse_turn_number,
    agent_event_to_json,
    read_agent_event_stream_snapshot,
    read_agent_event_stream_snapshot_sync,
    stream_agent_event_frames,
    stream_agent_event_frames_sync,
)
from tests.conftest import CORE_DIR, WORKER_MODULE, ScriptedAgentHarness
from tests.scripted_agent import STATE_DIR_ENVIRONMENT_VARIABLE, build_scripted_agent_flow

POLL_TIMEOUT = timedelta(seconds=1)


@dataclass(frozen=True)
class ServerSentAgentEvent:
    id: str
    data: dict[str, Any]


async def test_event_frames_resume_from_the_last_event_id_with_no_repeated_or_missing_event(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    client = harness.stack.client
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hello")
    await harness.wait_for_turns(session_id, 1)
    opening = await read_agent_event_stream_snapshot(client, harness.flow, session_id)
    opening_json = opening.to_json()
    assert json.loads(json.dumps(opening_json)) == opening_json
    assert opening_json["snapshot"]["state"]["status"] == "waiting_for_user_message"
    assert [message["role"] for message in opening_json["snapshot"]["messages"]] == [
        "user",
        "assistant",
    ]
    assert opening.resume_token != ""
    assert AGENT_EVENT_STREAM_HEADERS["content-type"] == "text/event-stream"

    first_reading = asyncio.create_task(
        _read_frames_until(
            await stream_agent_event_frames(
                client,
                harness.flow,
                session_id,
                resume_token=opening.resume_token,
                poll_timeout=POLL_TIMEOUT,
            ),
            lambda data: data["type"] == AgentEventType.TOOL_CALL_STARTED,
        )
    )
    assert await harness.sessions.send_user_message(
        session_id, 'call lookup {"query": "q", "gate": "lookup-gate"}'
    )
    first_events = await first_reading
    harness.gate("lookup-gate").release()
    await harness.wait_for_turns(session_id, 2)
    second_events = await _read_frames_until(
        await stream_agent_event_frames(
            client,
            harness.flow,
            session_id,
            resume_token=first_events[-1].id,
            poll_timeout=POLL_TIMEOUT,
        ),
        lambda data: data["type"] == AgentEventType.TURN_COMPLETED,
    )

    expected = await _retained_messages_after(harness, session_id, opening.resume_token)
    received = [*first_events, *second_events]
    assert [event.id for event in received] == [message.resume_token for message in expected]
    assert [event.data for event in received] == [
        agent_event_to_json(message.value) for message in expected
    ]
    assert received[0].data["type"] == AgentEventType.TURN_STARTED
    assert received[0].data["turn_id"] == "turn-2"


async def test_event_frames_show_the_new_attempt_after_a_worker_killed_during_run_model(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall model-gate")
    reading = asyncio.create_task(
        _read_frames_until(
            await stream_agent_event_frames(
                harness.stack.client, harness.flow, session_id, poll_timeout=POLL_TIMEOUT
            ),
            lambda data: data["type"] == AgentEventType.TURN_COMPLETED,
        )
    )
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()
    await wait_until("the first attempt's text", lambda: _has_text_delta(harness, session_id))

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    events = await reading

    deltas = [event.data for event in events if event.data["type"] == AgentEventType.TEXT_DELTA]
    assert [(delta["attempt"], json.loads(delta["payload"])["text"]) for delta in deltas] == [
        (1, "thinking before model-gate"),
        (2, "thinking before model-gate"),
    ]
    assert deltas[0]["step_execution_id"] == deltas[1]["step_execution_id"]
    assert deltas[0]["step_execution_id"].startswith("RunModel-")
    assert events[-1].data["attempt"] == 2
    assert events[-1].data["step_execution_id"] == deltas[1]["step_execution_id"]


async def test_snapshot_json_and_event_frames_carry_an_approval_round_trip(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    client = harness.stack.client
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "launch"}')
    interrupt = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="a pending approval",
    )

    opening = await read_agent_event_stream_snapshot(client, harness.flow, session_id)
    opening_json = opening.to_json()
    assert [
        (pending["interrupt_id"], pending["kind"], pending["tool_name"])
        for pending in opening_json["snapshot"]["pending_interrupts"]
    ] == [(interrupt.interrupt_id, "tool_approval", "publish")]
    assert [
        (call["tool_name"], call["effect"], call["status"])
        for call in opening_json["snapshot"]["tool_calls"]
    ] == [("publish", "non_idempotent_write", "awaiting_approval")]
    replayed = await _read_frames_until(
        await stream_agent_event_frames(
            client,
            harness.flow,
            session_id,
            resume_token=opening.resume_token,
            poll_timeout=POLL_TIMEOUT,
        ),
        lambda data: data["type"] == AgentEventType.INTERRUPT_RAISED,
    )
    assert [event.data["type"] for event in replayed] == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TOOL_CALL_DEFERRED,
        AgentEventType.INTERRUPT_RAISED,
    ]
    assert json.loads(replayed[-1].data["payload"])["interrupt_id"] == interrupt.interrupt_id

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    rest = await _read_frames_until(
        await stream_agent_event_frames(
            client,
            harness.flow,
            session_id,
            resume_token=replayed[-1].id,
            poll_timeout=POLL_TIMEOUT,
        ),
        lambda data: data["type"] == AgentEventType.TURN_COMPLETED,
    )
    payloads = {
        event.data["type"]: json.loads(event.data["payload"])
        for event in rest
        if event.data["type"]
        in (AgentEventType.INTERRUPT_RESOLVED, AgentEventType.TOOL_CALL_FINISHED)
    }
    assert payloads[AgentEventType.INTERRUPT_RESOLVED] == {
        "approved": True,
        "interrupt_id": interrupt.interrupt_id,
        "rejection_message": None,
    }
    assert payloads[AgentEventType.TOOL_CALL_FINISHED]["status"] == "succeeded"
    assert harness.invocations.count("publish") == 1
    closing = await read_agent_event_stream_snapshot(client, harness.flow, session_id)
    assert closing.to_json()["snapshot"]["messages"][-1]["text"] == "publish: published launch"
    assert closing.resume_token == rest[-1].id


async def test_sync_snapshot_and_event_frames_serve_wsgi_frameworks(
    harness: ScriptedAgentHarness,
    tmp_path: Path,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hi")
    await harness.wait_for_turns(session_id, 1)
    blob_cache = open_blob_cache(
        BlobCacheConfig(str(tmp_path / "sync-client-cache"), TEST_BLOB_CACHE_BYTES)
    )
    client = Client(
        Registry([harness.flow], allow_async_handlers=True),
        blob_cache,
        ClientOptions(server_address=harness.stack.engine.address),
    )
    try:
        opening = await asyncio.to_thread(
            read_agent_event_stream_snapshot_sync, client, harness.flow, session_id
        )
        frames = await asyncio.to_thread(
            stream_agent_event_frames_sync,
            client,
            harness.flow,
            session_id,
            poll_timeout=POLL_TIMEOUT,
        )
        events = await asyncio.to_thread(
            _read_sync_frames_until,
            frames,
            lambda data: data["type"] == AgentEventType.TURN_COMPLETED,
        )
    finally:
        client.close()
        blob_cache.close()

    expected = await _retained_messages_after(harness, session_id, "")
    assert [event.id for event in events] == [message.resume_token for message in expected]
    assert opening.snapshot.state is not None
    assert opening.snapshot.state.turn_count == 1
    assert opening.resume_token == expected[-1].resume_token


async def test_a_resume_token_of_another_session_is_rejected_before_the_first_frame(
    harness: ScriptedAgentHarness,
    tmp_path: Path,
) -> None:
    await harness.stack.start_worker()
    client = harness.stack.client
    other_session_id = harness.new_session_id()
    await harness.sessions.start_session(other_session_id, "say elsewhere")
    await harness.wait_for_turns(other_session_id, 1)
    other_token = (
        await read_agent_event_stream_snapshot(client, harness.flow, other_session_id)
    ).resume_token
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say here")
    await harness.wait_for_turns(session_id, 1)

    try:
        await stream_agent_event_frames(client, harness.flow, session_id, resume_token=other_token)
    except InvalidAgentEventResumeTokenError as error:
        assert error.session_id == session_id
    else:
        raise AssertionError("a token of another session must be rejected")
    blob_cache = open_blob_cache(
        BlobCacheConfig(str(tmp_path / "sync-client-cache"), TEST_BLOB_CACHE_BYTES)
    )
    sync_client = Client(
        Registry([harness.flow], allow_async_handlers=True),
        blob_cache,
        ClientOptions(server_address=harness.stack.engine.address),
    )
    try:
        try:
            await asyncio.to_thread(
                stream_agent_event_frames_sync,
                sync_client,
                harness.flow,
                session_id,
                resume_token=other_token,
            )
        except InvalidAgentEventResumeTokenError:
            pass
        else:
            raise AssertionError("the synchronous variant must reject the token too")
    finally:
        sync_client.close()
        blob_cache.close()


async def test_snapshot_and_event_frames_work_with_the_stream_store_disabled(
    tmp_path: Path,
) -> None:
    config_path = tmp_path / "server-zd.yaml"
    config_path.write_text("streamStore:\n  backend: disabled\n")
    state_dir = tmp_path / "agent-state"
    state_dir.mkdir()
    flow = build_scripted_agent_flow(state_dir)
    async with AgentTestStack(
        tmp_path,
        [flow],
        WORKER_MODULE,
        worker_cwd=CORE_DIR,
        worker_env={STATE_DIR_ENVIRONMENT_VARIABLE: str(state_dir)},
        server_zd_config=config_path,
    ) as stack:
        await stack.start_worker()
        sessions = stack.sessions(flow)
        session_id = "disabled-stream-session"
        await sessions.start_session(session_id, "say quietly")
        await wait_for_agent_snapshot(
            sessions,
            session_id,
            lambda snapshot: snapshot.state is not None and snapshot.state.turn_count == 1,
            description="the finished turn",
        )

        opening = await read_agent_event_stream_snapshot(stack.client, flow, session_id)
        assert opening.resume_token == ""
        assert opening.to_json()["snapshot"]["messages"][-1]["text"] == "said: quietly"
        frames = await stream_agent_event_frames(
            stack.client, flow, session_id, poll_timeout=POLL_TIMEOUT
        )
        try:
            assert await anext(frames) == KEEP_ALIVE_FRAME
        finally:
            await frames.aclose()


def test_turn_numbers_accept_only_ascii_digits() -> None:
    assert _parse_turn_number("turn-12") == 12
    assert _parse_turn_number("turn-\u0661\u0662") is None
    assert _parse_turn_number("turn-") is None
    assert _parse_turn_number("step-3") is None


async def _read_frames_until(
    frames: AsyncGenerator[str, None],
    is_last_event: Callable[[dict[str, Any]], bool],
) -> list[ServerSentAgentEvent]:
    events: list[ServerSentAgentEvent] = []
    try:
        async for frame in frames:
            event = _parse_frame(frame)
            if event is None:
                continue
            events.append(event)
            if is_last_event(event.data):
                return events
    finally:
        await frames.aclose()
    raise AssertionError("the event stream ended early")


def _read_sync_frames_until(
    frames: Generator[str, None, None],
    is_last_event: Callable[[dict[str, Any]], bool],
) -> list[ServerSentAgentEvent]:
    events: list[ServerSentAgentEvent] = []
    try:
        for frame in frames:
            event = _parse_frame(frame)
            if event is None:
                continue
            events.append(event)
            if is_last_event(event.data):
                return events
    finally:
        frames.close()
    raise AssertionError("the event stream ended early")


def _parse_frame(frame: str) -> ServerSentAgentEvent | None:
    assert frame.endswith("\n\n")
    if frame == KEEP_ALIVE_FRAME:
        return None
    fields = dict(line.split(": ", 1) for line in frame.rstrip("\n").split("\n"))
    assert set(fields) == {"id", "data"}
    return ServerSentAgentEvent(fields["id"], json.loads(fields["data"]))


async def _retained_messages_after(
    harness: ScriptedAgentHarness,
    session_id: str,
    resume_token: str,
) -> list[StreamMessage[AgentEvent]]:
    page = await harness.stack.client.list_stream_messages(
        session_id, harness.flow.agent_events, 500
    )
    retained = list(reversed(page.messages))
    if resume_token == "":
        return retained
    start = next(
        index for index, message in enumerate(retained) if message.resume_token == resume_token
    )
    return retained[start + 1 :]


async def _has_text_delta(harness: ScriptedAgentHarness, session_id: str) -> bool:
    return any(
        event.type == AgentEventType.TEXT_DELTA for event in await harness.agent_events(session_id)
    )
