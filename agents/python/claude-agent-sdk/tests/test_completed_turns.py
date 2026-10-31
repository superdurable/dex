# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turns that complete inside RunModel, history replay, snapshots, and usage."""

from __future__ import annotations

import json

from dex_agents import (
    FRAMEWORK_RUN_STATE,
    AgentEventType,
    AgentSessionStatus,
    AgentUsage,
    MessageRole,
)
from dex_agents_claude_agent_sdk import CLAUDE_TRANSCRIPT_ITEM_KIND, USER_PROMPT_ITEM_KIND
from tests.conftest import ClaudeAgentHarness


async def test_read_tool_turn_replays_history_through_a_resumed_cli_session(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call lookup {"query": "docs"}')
    first = await harness.wait_for_turns(session_id, 1)

    assert [(message.role, message.tool_name) for message in first.messages] == [
        (MessageRole.USER, None),
        (MessageRole.ASSISTANT, "lookup"),
        (MessageRole.TOOL, "lookup"),
        (MessageRole.ASSISTANT, None),
    ]
    assert first.messages[1].tool_arguments == '{"query": "docs"}'
    assert first.messages[2].tool_output == "found docs"
    assert first.messages[-1].text == "lookup: found docs"
    assert first.tool_calls == []
    assert first.pending_interrupts == []
    assert json.loads(first.messages[0].framework_item)["kind"] == USER_PROMPT_ITEM_KIND
    transcript_items = [json.loads(message.framework_item) for message in first.messages[1:]]
    assert {item["kind"] for item in transcript_items} == {CLAUDE_TRANSCRIPT_ITEM_KIND}
    assert len({item["session_id"] for item in transcript_items}) == 1
    tool_events = [
        (event.type, json.loads(event.payload)["tool_name"])
        for event in await harness.list_events(session_id)
        if event.type in (AgentEventType.TOOL_CALL_STARTED, AgentEventType.TOOL_CALL_FINISHED)
    ]
    assert tool_events == [
        (AgentEventType.TOOL_CALL_STARTED, "lookup"),
        (AgentEventType.TOOL_CALL_FINISHED, "lookup"),
    ]

    assert await harness.sessions.send_user_message(session_id, "recall")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == 'history: call lookup {"query": "docs"}'
    assert [message.turn_id for message in second.messages] == [
        "turn-1",
        "turn-1",
        "turn-1",
        "turn-1",
        "turn-2",
        "turn-2",
    ]
    assert harness.invocations.names() == [
        "cli:new",
        "model_call",
        "lookup",
        "model_call",
        "cli:resume",
        "model_call",
    ]


async def test_snapshot_returns_the_conversation_usage_and_streamed_text(
    harness: ClaudeAgentHarness,
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
    assert [(message.role, message.text) for message in snapshot.messages] == [
        (MessageRole.USER, "say hi"),
        (MessageRole.ASSISTANT, "said: hi"),
    ]
    assert snapshot.usage == AgentUsage(
        model_calls=1, input_tokens=13, output_tokens=5, cached_input_tokens=3
    )
    assert snapshot.queued_message_count == 0
    assert snapshot.pending_interrupts == []
    assert snapshot.tool_calls == []
    deltas = await harness.text_deltas(session_id)
    assert [attempt for attempt, _ in deltas] == [1, 1]
    assert "".join(text for _, text in deltas) == "said: hi"
    values = await harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE)
    assert FRAMEWORK_RUN_STATE not in values


async def test_cli_error_result_fails_the_turn_and_the_next_turn_runs(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "fail")
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "scripted failure" in (failed.state.last_turn_error or "")
    assert [message.text for message in failed.messages] == ["fail"]
    assert harness.invocations.count("cli:new") == 3

    assert await harness.sessions.send_user_message(session_id, "say recovered")
    recovered = await harness.wait_for_turns(session_id, 2)
    assert recovered.state is not None
    assert recovered.state.last_turn_error is None
    assert recovered.messages[-1].text == "said: recovered"
