# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Steering and cancelling an ADK turn store its events and tool results, not the core's note."""

from __future__ import annotations

import json
from typing import Any

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT,
    TURN_CANCELLED_NOTE_HEADER,
    TURN_STEERED_NOTE_HEADER,
    AgentEventType,
    AgentInterrupt,
    AgentMessage,
    AgentSessionClient,
    AgentSnapshot,
    MessageRole,
    ToolCallStatus,
)
from dex_agents.testing import wait_for_agent_snapshot
from tests.conftest import AdkAgentHarness
from tests.scripted_adk_agent import SLOW_WRITE_GATE
from tests.scripted_adk_topologies import (
    PARALLEL_APPROVAL_FLOW_TYPE,
    REDACTED_RESULT,
    REDACTING_FLOW_TYPE,
)

SLOW_WRITE_COMMAND = 'call slow_write {"message": "note"}'
APPROVAL_COMMAND = 'call publish {"message": "note"}'


async def test_a_completed_turn_stores_what_the_after_tool_callback_returns(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(REDACTING_FLOW_TYPE)
    await sessions.start_session(session_id, SLOW_WRITE_COMMAND)
    harness.gate(SLOW_WRITE_GATE).release()
    snapshot = await harness.wait_for_turns(session_id, 1, sessions)

    assert _tool_outputs(snapshot) == [("slow_write", "redacted")]
    assert snapshot.messages[-1].text == "slow_write: redacted"


async def test_steering_withholds_a_result_that_an_after_tool_callback_would_replace(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(REDACTING_FLOW_TYPE)
    await sessions.start_session(session_id, SLOW_WRITE_COMMAND)
    gate = harness.gate(SLOW_WRITE_GATE)
    await gate.wait_until_reached()

    assert await sessions.steer_user_message(session_id, text="recall")
    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2, sessions)

    # The raw output never reaches storage; the callback would have replaced it.
    assert _tool_responses(snapshot) == [("slow_write", {"error": OUTCOME_UNKNOWN_TOOL_RESULT})]
    assert not any("wrote note" in message.framework_item for message in snapshot.messages)
    assert REDACTED_RESULT["result"] not in (snapshot.messages[-1].text or "")
    assert snapshot.messages[-1].text == f"history: {SLOW_WRITE_COMMAND}"
    assert harness.invocations.count("slow_write") == 1


async def test_steering_during_a_tool_stores_its_call_and_result_instead_of_a_note(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, SLOW_WRITE_COMMAND)
    gate = harness.gate(SLOW_WRITE_GATE)
    await gate.wait_until_reached()

    assert await harness.sessions.steer_user_message(session_id, text="recall")
    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert _turn_messages(snapshot, "turn-1") == [
        (MessageRole.USER, SLOW_WRITE_COMMAND, None),
        (MessageRole.ASSISTANT, None, "slow_write"),
        (MessageRole.TOOL, None, "slow_write"),
    ]
    assert _tool_outputs(snapshot) == [("slow_write", "wrote note")]
    assert not _has_core_note(snapshot)
    # The next model call replays the stored call and result, and no note.
    assert snapshot.messages[-1].text == f"history: {SLOW_WRITE_COMMAND}"
    assert harness.invocations.count("slow_write") == 1
    assert await _turn_event_types(harness, session_id) == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_STEERED,
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_COMPLETED,
    ]


async def test_cancel_turn_stores_the_cancellation_result_of_a_call_waiting_for_approval(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, APPROVAL_COMMAND)
    await harness.wait_for_interrupt(session_id, "publish")

    assert await harness.sessions.cancel_turn(session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.pending_interrupts == []
    assert _turn_messages(snapshot, "turn-1") == [
        (MessageRole.USER, APPROVAL_COMMAND, None),
        (MessageRole.ASSISTANT, None, "publish"),
        (MessageRole.TOOL, None, "publish"),
    ]
    assert _tool_responses(snapshot) == [
        ("publish", {"error": TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT})
    ]
    assert not _has_core_note(snapshot)
    # The stored events hold no confirmation request, so nothing resumes the call.
    assert not any(
        "adk_request_confirmation" in message.framework_item for message in snapshot.messages
    )

    assert await harness.sessions.send_user_message(session_id, "say again")
    following = await harness.wait_for_turns(session_id, 2)
    assert following.messages[-1].text == "said: again"
    assert harness.invocations.count("publish") == 0


async def test_cancel_turn_during_a_resumed_model_call_stores_the_recorded_result(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall model-gate")
    await harness.gate("model-gate").wait_until_reached()

    assert await harness.sessions.cancel_turn(session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    # The cancelled model call never gave ADK the lookup result, so the runner stores it.
    assert _turn_messages(snapshot, "turn-1") == [
        (MessageRole.USER, "stall model-gate", None),
        (MessageRole.ASSISTANT, None, "lookup"),
        (MessageRole.TOOL, None, "lookup"),
    ]
    assert _tool_outputs(snapshot) == [("lookup", "found stall")]
    assert not _has_core_note(snapshot)
    assert await _turn_event_types(harness, session_id) == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_CANCELLED,
    ]

    assert await harness.sessions.send_user_message(session_id, "say again")
    following = await harness.wait_for_turns(session_id, 2)
    assert following.messages[-1].text == "said: again"
    assert harness.invocations.count("lookup") == 1


async def test_steering_parallel_branches_gives_each_branch_its_own_results(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(PARALLEL_APPROVAL_FLOW_TYPE)
    await sessions.start_session(session_id, "first")
    record_a = await _wait_for_branch_interrupt(sessions, session_id, "record_a", "first")
    await _wait_for_branch_interrupt(sessions, session_id, "record_b", "first")
    assert await sessions.resolve_interrupt(session_id, record_a.interrupt_id, approved=True)
    await wait_for_agent_snapshot(
        sessions,
        session_id,
        lambda snapshot: snapshot
        if any(
            call.tool_name == "record_a" and call.status is ToolCallStatus.SUCCEEDED
            for call in snapshot.tool_calls
        )
        else None,
        description="a finished record_a call",
    )

    assert await sessions.steer_user_message(session_id, text="second")
    for tool_name in ("record_a", "record_b"):
        interrupt = await _wait_for_branch_interrupt(sessions, session_id, tool_name, "second")
        assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=True)
    snapshot = await harness.wait_for_turns(session_id, 2, sessions)

    steered_responses = [
        (event["author"], event["branch"], part["function_response"])
        for event in _turn_events(snapshot, "turn-1")
        for part in event.get("content", {}).get("parts", [])
        if "function_response" in part
    ]
    assert sorted(
        (author, branch, response["name"], response["response"])
        for author, branch, response in steered_responses
    ) == [
        ("researcher_a", "research.researcher_a", "record_a", {"result": "recorded a: first"}),
        (
            "researcher_b",
            "research.researcher_b",
            "record_b",
            {"error": TOOL_CALL_CANCELLED_BY_STEERING_RESULT},
        ),
    ]
    assert not _has_core_note(snapshot)
    # Each branch's model saw its own steered result: two results of its tool in all.
    assert sorted(
        message.text or ""
        for message in snapshot.messages
        if message.turn_id == "turn-2" and (message.text or "").startswith("record_")
    ) == [
        "record_a: recorded a: second; results seen: 2",
        "record_b: recorded b: second; results seen: 2",
    ]
    assert harness.invocations.count("record_a") == 2
    assert harness.invocations.count("record_b") == 1


def _turn_messages(
    snapshot: AgentSnapshot, turn_id: str
) -> list[tuple[MessageRole, str | None, str | None]]:
    return [
        (message.role, message.text, message.tool_name)
        for message in snapshot.messages
        if message.turn_id == turn_id
    ]


def _tool_outputs(snapshot: AgentSnapshot) -> list[tuple[str | None, str | None]]:
    return [
        (message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ]


def _tool_responses(snapshot: AgentSnapshot) -> list[tuple[str | None, Any]]:
    return [
        (message.tool_name, json.loads(message.tool_output or "null"))
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ]


def _turn_events(snapshot: AgentSnapshot, turn_id: str) -> list[dict[str, Any]]:
    return [_event(message) for message in snapshot.messages if message.turn_id == turn_id]


def _event(message: AgentMessage) -> dict[str, Any]:
    event: dict[str, Any] = json.loads(message.framework_item)
    return event


def _has_core_note(snapshot: AgentSnapshot) -> bool:
    return any(
        (message.text or "").startswith((TURN_STEERED_NOTE_HEADER, TURN_CANCELLED_NOTE_HEADER))
        for message in snapshot.messages
    )


async def _turn_event_types(harness: AdkAgentHarness, session_id: str) -> list[str]:
    return [
        event.type
        for event in await harness.agent_events(session_id)
        if event.type.startswith("turn_")
    ]


async def _wait_for_branch_interrupt(
    sessions: AgentSessionClient, session_id: str, tool_name: str, note: str
) -> AgentInterrupt:
    return await wait_for_agent_snapshot(
        sessions,
        session_id,
        lambda snapshot: next(
            (
                interrupt
                for interrupt in snapshot.pending_interrupts
                if interrupt.tool_name == tool_name
                and json.loads(interrupt.arguments) == {"note": note}
            ),
            None,
        ),
        description=f"a pending {tool_name} approval for {note}",
    )
