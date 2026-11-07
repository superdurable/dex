# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""steer_user_message and cancel_turn against a real agent graph on a real Dex Server."""

from __future__ import annotations

from dex_agents import TURN_STEERED_NOTE_HEADER, AgentEventType, AgentSessionStatus
from tests.conftest import SupportAgentHarness

SLOW_WRITE_COMMAND = 'call slow_write {"message": "note", "gate": "tool-gate"}'
APPROVAL_COMMAND = 'call publish {"message": "note"}'


def _turn_events(events: list[str]) -> list[str]:
    return [event for event in events if event.startswith("turn_")]


async def _turn_event_types(harness: SupportAgentHarness, session_id: str) -> list[str]:
    return _turn_events([event.type for event in await harness.list_agent_events(session_id)])


async def test_cancel_turn_stops_a_running_model_call(harness: SupportAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall model-gate")
    await harness.gate("model-gate").wait_until_reached()

    assert await harness.sessions.cancel_turn(session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    # The gate was never released: the call stopped inside it.
    assert harness.invocations.count("model cancelled at model-gate") == 1
    assert harness.model_calls() == ["model:1:1"]
    assert [message.text for message in snapshot.messages] == ["stall model-gate"]
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


async def test_cancel_turn_lets_a_started_tool_finish(harness: SupportAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, SLOW_WRITE_COMMAND)
    await harness.gate("tool-gate").wait_until_reached()

    assert await harness.sessions.cancel_turn(session_id)
    harness.gate("tool-gate").release()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert harness.tool_runs() == ["slow_write"]
    assert harness.model_calls() == ["model:1:1"]
    assert await _turn_event_types(harness, session_id) == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_CANCELLED,
    ]
    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None


async def test_cancel_turn_rejects_a_call_waiting_for_approval(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, APPROVAL_COMMAND)
    await harness.wait_for_pending_interrupt(session_id)

    assert await harness.sessions.cancel_turn(session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert harness.tool_runs() == []
    assert snapshot.pending_interrupts == []
    assert await _turn_event_types(harness, session_id) == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_CANCELLED,
    ]


async def test_steering_during_a_tool_ends_the_turn_and_starts_a_new_one(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, SLOW_WRITE_COMMAND)
    await harness.gate("tool-gate").wait_until_reached()

    assert await harness.sessions.steer_user_message(session_id, text="recall")
    harness.gate("tool-gate").release()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert harness.tool_runs() == ["slow_write"]
    # The steered turn made no further model call; the new turn is the next user message.
    assert len(harness.model_calls()) == 2
    assert await _turn_event_types(harness, session_id) == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_STEERED,
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_COMPLETED,
    ]
    # The next model call sees the core's note, because the runner stores no partial transcript.
    assert (snapshot.messages[1].text or "").startswith(TURN_STEERED_NOTE_HEADER)
    assert "wrote note" in (snapshot.messages[-1].text or "")
    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None


async def test_steering_lets_a_final_answer_complete_its_turn(
    harness: SupportAgentHarness,
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
