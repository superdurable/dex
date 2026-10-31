# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turns that complete without a deferred tool call, and the snapshot RPCs."""

from __future__ import annotations

from dex_agents import (
    AGENT_STATE,
    FRAMEWORK_RUN_STATE,
    AgentSessionStatus,
    AgentUsage,
    MessageRole,
)
from tests.conftest import ScriptedAgentHarness


async def test_completed_turn_replays_history_on_the_next_turn(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say hello")
    first = await harness.wait_for_turns(session_id, 1)
    assert [(message.role, message.text) for message in first.messages] == [
        (MessageRole.USER, "say hello"),
        (MessageRole.ASSISTANT, "said: hello"),
    ]

    assert await harness.sessions.send_user_message(session_id, "recall")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == "history: say hello"
    assert [message.turn_id for message in second.messages] == [
        "turn-1",
        "turn-1",
        "turn-2",
        "turn-2",
    ]


async def test_read_tool_runs_inside_the_turn(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call lookup {"query": "docs"}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert [(message.role, message.tool_name) for message in snapshot.messages] == [
        (MessageRole.USER, None),
        (MessageRole.ASSISTANT, "lookup"),
        (MessageRole.TOOL, "lookup"),
        (MessageRole.ASSISTANT, None),
    ]
    assert snapshot.messages[2].tool_output == "found docs"
    assert snapshot.messages[-1].text == "lookup: found docs"
    assert snapshot.tool_calls == []
    assert snapshot.pending_interrupts == []
    assert harness.invocations.count("lookup") == 1
    assert harness.invocations.count("run_turn:turn-1") == 1


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
    assert snapshot.state.next_message_sequence == 3
    assert snapshot.state.last_turn_error is None
    assert [message.sequence for message in snapshot.messages] == [1, 2]
    assert [message.text for message in snapshot.messages] == ["say hi", "said: hi"]
    assert all(message.created_at for message in snapshot.messages)
    assert snapshot.usage == AgentUsage(model_calls=1, input_tokens=10, output_tokens=5)
    assert snapshot.queued_message_count == 0
    assert snapshot.pending_interrupts == []
    assert snapshot.tool_calls == []

    values = await harness.stack.client.get_attributes(session_id, AGENT_STATE, FRAMEWORK_RUN_STATE)
    assert values[AGENT_STATE] == snapshot.state
    assert FRAMEWORK_RUN_STATE not in values


async def test_list_agent_messages_pages_backward(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say one")
    await harness.wait_for_turns(session_id, 1)
    assert await harness.sessions.send_user_message(session_id, "say two")
    await harness.wait_for_turns(session_id, 2)

    newest = await harness.sessions.list_agent_messages(session_id, limit=3)
    assert [message.sequence for message in newest.messages] == [2, 3, 4]
    assert newest.next_before_sequence == 2
    oldest = await harness.sessions.list_agent_messages(
        session_id, before_sequence=newest.next_before_sequence, limit=3
    )
    assert [message.text for message in oldest.messages] == ["say one"]
    assert oldest.next_before_sequence is None
