# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turns without tool calls, failed turns, and the snapshot."""

from __future__ import annotations

from dex_agents import FRAMEWORK_RUN_STATE, AgentSessionStatus
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import SYSTEM_PROMPT


async def test_snapshot_returns_the_conversation_after_a_completed_turn(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    assert not await harness.sessions.send_user_message(session_id, "   ")
    assert await harness.sessions.send_user_message(session_id, "say hi")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
    assert snapshot.state.last_turn_error is None
    assert [message.sequence for message in snapshot.messages] == [1, 2, 3]
    assert [message.text for message in snapshot.messages] == ["say hi", None, "said: hi"]
    assert all(message.turn_id == "turn-1" for message in snapshot.messages)
    assert all(message.created_at for message in snapshot.messages)
    assert snapshot.usage.model_calls == 1
    assert snapshot.usage.input_tokens > 0
    assert snapshot.usage.output_tokens > 0
    assert snapshot.queued_message_count == 0
    assert snapshot.pending_interrupts == []
    assert snapshot.tool_calls == []

    values = await harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE)
    assert FRAMEWORK_RUN_STATE not in values


async def test_failed_first_turn_is_replayed_with_the_system_prompt(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "fail")
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "scripted model failure" in (failed.state.last_turn_error or "")
    assert [message.text for message in failed.messages] == ["fail"]

    assert await harness.sessions.send_user_message(session_id, "recall")
    recovered = await harness.wait_for_turns(session_id, 2)
    assert recovered.state is not None
    assert recovered.state.last_turn_error is None
    assert recovered.messages[-1].text == f"history: fail; system: {SYSTEM_PROMPT}"
