# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""steer_user_message and cancel_turn against a real agent graph on a real Dex Server."""

from __future__ import annotations

from dex_agents import (
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT,
    TURN_CANCELLED_NOTE_HEADER,
    TURN_STEERED_NOTE_HEADER,
    AgentEventType,
    AgentSessionStatus,
    AgentSnapshot,
    MessageRole,
)
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import SYSTEM_PROMPT

SLOW_WRITE_COMMAND = 'call slow_write {"message": "note", "gate": "tool-gate"}'
APPROVAL_COMMAND = 'call publish {"message": "note"}'
TWO_RESPONSE_COMMAND = 'call lookup {"query": "a"} then call publish {"message": "b"}'


def _model_calls(harness: ScriptedAgentHarness) -> list[str]:
    return [name for name in harness.invocations.names() if name.startswith("model:")]


def _tool_runs(harness: ScriptedAgentHarness) -> list[str]:
    return [
        name
        for name in harness.invocations.names()
        if not name.startswith(("model:", "cancelled at "))
    ]


def _turn_events(events: list[str]) -> list[str]:
    return [event for event in events if event.startswith("turn_")]


async def _turn_event_types(harness: ScriptedAgentHarness, session_id: str) -> list[str]:
    return _turn_events([event.type for event in await harness.list_agent_events(session_id)])


def _tool_call_messages(snapshot: AgentSnapshot) -> list[tuple[str, MessageRole, str | None]]:
    """Return each message about a tool call, so a repeated message shows up twice."""
    return [
        (message.turn_id, message.role, message.tool_call_id)
        for message in snapshot.messages
        if message.tool_call_id is not None
    ]


def _tool_results(snapshot: AgentSnapshot) -> list[tuple[str | None, str | None]]:
    return [
        (message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ]


def _has_core_note(snapshot: AgentSnapshot) -> bool:
    return any(
        (message.text or "").startswith((TURN_STEERED_NOTE_HEADER, TURN_CANCELLED_NOTE_HEADER))
        for message in snapshot.messages
    )


async def test_cancel_turn_stops_a_running_model_call(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall model-gate")
    await harness.gate("model-gate").wait_until_reached()

    assert await harness.sessions.cancel_turn(session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    # The gate was never released: the call stopped inside it.
    assert harness.invocations.count("cancelled at model-gate") == 1
    assert _model_calls(harness) == ["model:1:1", "model:1:2"]
    assert snapshot.messages[0].text == "stall model-gate"
    assert not any("stalled at" in (message.text or "") for message in snapshot.messages)
    # The cancelled model call never stored the lookup result, so the runner stores it.
    assert _tool_results(snapshot) == [("lookup", "found stall")]
    assert _tool_call_messages(snapshot) == [
        ("turn-1", MessageRole.ASSISTANT, "stall-lookup"),
        ("turn-1", MessageRole.TOOL, "stall-lookup"),
    ]
    assert not _has_core_note(snapshot)
    assert snapshot.state is not None
    assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
    assert snapshot.state.last_turn_error is None
    assert await _turn_event_types(harness, session_id) == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_CANCELLED,
    ]

    assert await harness.sessions.send_user_message(session_id, "say again")
    following = await harness.wait_for_turns(session_id, 2)
    assert following.messages[-1].text == "said: again"
    assert _tool_call_messages(following) == _tool_call_messages(snapshot)
    assert harness.invocations.count("lookup") == 1


async def test_cancel_turn_lets_a_started_tool_finish(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, SLOW_WRITE_COMMAND)
    await harness.gate("tool-gate").wait_until_reached()

    assert await harness.sessions.cancel_turn(session_id)
    harness.gate("tool-gate").release()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert _tool_runs(harness) == ["slow_write"]
    assert _model_calls(harness) == ["model:1:1"]
    assert await _turn_event_types(harness, session_id) == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_CANCELLED,
    ]
    assert _tool_results(snapshot) == [("slow_write", "wrote note")]
    assert not _has_core_note(snapshot)
    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None


async def test_cancel_turn_rejects_a_call_waiting_for_approval(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, APPROVAL_COMMAND)
    await harness.wait_for_pending_interrupt(session_id)

    assert await harness.sessions.cancel_turn(session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert _tool_runs(harness) == []
    assert snapshot.pending_interrupts == []
    assert await _turn_event_types(harness, session_id) == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_CANCELLED,
    ]
    assert _tool_results(snapshot) == [("publish", TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT)]
    assert not _has_core_note(snapshot)

    assert await harness.sessions.send_user_message(session_id, "say again")
    following = await harness.wait_for_turns(session_id, 2)
    assert following.messages[-1].text == "said: again"
    assert _tool_runs(harness) == []


async def test_steering_during_a_tool_ends_the_turn_and_starts_a_new_one(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, SLOW_WRITE_COMMAND)
    await harness.gate("tool-gate").wait_until_reached()

    assert await harness.sessions.steer_user_message(session_id, text="recall")
    harness.gate("tool-gate").release()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert _tool_runs(harness) == ["slow_write"]
    # The steered turn made no further model call; the new turn is the next user message.
    assert len(_model_calls(harness)) == 2
    assert await _turn_event_types(harness, session_id) == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_STEERED,
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_COMPLETED,
    ]
    # The steered turn keeps its tool call and result instead of the core's note.
    assert _tool_call_messages(snapshot) == [
        ("turn-1", MessageRole.ASSISTANT, "call-1-0-0"),
        ("turn-1", MessageRole.TOOL, "call-1-0-0"),
    ]
    assert _tool_results(snapshot) == [("slow_write", "wrote note")]
    assert not _has_core_note(snapshot)
    assert snapshot.messages[-1].text == f"history: {SLOW_WRITE_COMMAND}; system: {SYSTEM_PROMPT}"
    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None


async def test_steering_a_call_waiting_for_approval_stores_the_steering_result(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, APPROVAL_COMMAND)
    await harness.wait_for_pending_interrupt(session_id)

    assert await harness.sessions.steer_user_message(session_id, text="recall")
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert _tool_runs(harness) == []
    assert _tool_results(snapshot) == [("publish", TOOL_CALL_CANCELLED_BY_STEERING_RESULT)]
    assert _tool_call_messages(snapshot) == [
        ("turn-1", MessageRole.ASSISTANT, "call-1-0-0"),
        ("turn-1", MessageRole.TOOL, "call-1-0-0"),
    ]
    assert not _has_core_note(snapshot)
    assert snapshot.messages[-1].text == f"history: {APPROVAL_COMMAND}; system: {SYSTEM_PROMPT}"


async def test_messages_of_a_turn_are_stored_while_a_call_waits_for_approval(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, TWO_RESPONSE_COMMAND)
    interrupt = await harness.wait_for_pending_interrupt(session_id, "publish")
    waiting = await harness.sessions.get_agent_snapshot(session_id)

    assert _tool_call_messages(waiting) == [
        ("turn-1", MessageRole.ASSISTANT, "call-1-0-0"),
        ("turn-1", MessageRole.TOOL, "call-1-0-0"),
        ("turn-1", MessageRole.ASSISTANT, "call-1-1-0"),
    ]
    assert _tool_results(waiting) == [("lookup", "found a")]

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    # Completion stores only what the turn had not stored yet.
    assert snapshot.messages[: len(waiting.messages)] == waiting.messages
    assert _tool_call_messages(snapshot) == [
        ("turn-1", MessageRole.ASSISTANT, "call-1-0-0"),
        ("turn-1", MessageRole.TOOL, "call-1-0-0"),
        ("turn-1", MessageRole.ASSISTANT, "call-1-1-0"),
        ("turn-1", MessageRole.TOOL, "call-1-1-0"),
    ]
    assert snapshot.messages[-1].text == "lookup: found a; publish: published b"
    assert _tool_runs(harness) == ["lookup", "audit:publish", "publish"]


async def test_steering_lets_a_final_answer_complete_its_turn(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall model-gate")
    await harness.gate("model-gate").wait_until_reached()

    assert await harness.sessions.steer_user_message(session_id, text="say instead")
    harness.gate("model-gate").release()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert any("stalled at model-gate" in (message.text or "") for message in snapshot.messages)
    assert snapshot.messages[-1].text == "said: instead"
    assert await _turn_event_types(harness, session_id) == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_COMPLETED,
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_COMPLETED,
    ]
    assert _tool_call_messages(snapshot) == [
        ("turn-1", MessageRole.ASSISTANT, "stall-lookup"),
        ("turn-1", MessageRole.TOOL, "stall-lookup"),
    ]
