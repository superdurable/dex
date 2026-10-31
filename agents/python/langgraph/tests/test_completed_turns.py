# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turns that complete without a deferred tool call, history replay, and the snapshot."""

from __future__ import annotations

import json

import pytest
from dex_agents import (
    AGENT_STATE,
    FRAMEWORK_RUN_STATE,
    AgentSessionStatus,
    AgentUsage,
    MessageRole,
    ToolCallStatus,
)
from langchain_core.messages import AIMessage, HumanMessage, ToolMessage

from dex_agents_langgraph import framework_message_to_langchain_message
from tests.conftest import AGENT_FLOW_TYPES, ScriptedAgentHarness


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_read_tool_turn_replays_history_on_the_next_turn(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    command = 'call lookup {"query": "docs"}'
    await harness.sessions.start_session(session_id, command)
    first = await harness.wait_for_turns(session_id, 1)

    assert [(message.role, message.text, message.tool_name) for message in first.messages] == [
        (MessageRole.USER, command, None),
        (MessageRole.ASSISTANT, "step 1", "lookup"),
        (MessageRole.TOOL, None, "lookup"),
        (MessageRole.ASSISTANT, "lookup: found docs", None),
    ]
    tool_request, tool_result = first.messages[1], first.messages[2]
    assert tool_request.tool_call_id == tool_result.tool_call_id == "call-0-0-0"
    assert tool_request.tool_arguments == '{"query": "docs"}'
    assert tool_result.tool_output == "found docs"
    replayed_request = framework_message_to_langchain_message(tool_request)
    assert isinstance(replayed_request, AIMessage)
    assert replayed_request.tool_calls[0]["args"] == {"query": "docs"}
    replayed_result = framework_message_to_langchain_message(tool_result)
    assert isinstance(replayed_result, ToolMessage)
    assert (replayed_result.name, replayed_result.text) == ("lookup", "found docs")
    assert first.tool_calls == []
    assert first.pending_interrupts == []
    assert harness.invocations.count("lookup") == 1
    events = await harness.list_agent_events(session_id)
    tool_events = [
        (event.type, json.loads(event.payload))
        for event in events
        if event.type in ("tool_call_deferred", "tool_call_started", "tool_call_finished")
    ]
    assert tool_events == [
        (
            "tool_call_deferred",
            {
                "tool_call_id": "call-0-0-0",
                "tool_name": "lookup",
                "effect": "read",
                "requires_approval": False,
            },
        ),
        ("tool_call_started", {"tool_call_id": "call-0-0-0", "tool_name": "lookup"}),
        (
            "tool_call_finished",
            {
                "tool_call_id": "call-0-0-0",
                "tool_name": "lookup",
                "status": str(ToolCallStatus.SUCCEEDED),
            },
        ),
    ]
    # Step mode: one RunModel per model call, and the READ tool in its own CallTool Step.
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]
    assert harness.model_call_count(1) == harness.model_call_count(2) == 1
    assert await harness.list_streamed_text(session_id) == "step 1lookup: found docs"

    assert await harness.sessions.send_user_message(session_id, "recall")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == f"history: {command}"
    assert [message.turn_id for message in second.messages] == ["turn-1"] * 4 + ["turn-2"] * 2
    assert second.usage == AgentUsage(model_calls=3, input_tokens=30, output_tokens=15)
    assert harness.invocations.count("lookup") == 1


async def test_snapshot_returns_the_conversation_after_a_completed_turn(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    assert await harness.sessions.send_user_message(session_id, "say hi")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
    assert snapshot.state.next_message_sequence == 3
    assert snapshot.state.last_turn_error is None
    assert [(message.sequence, message.role, message.text) for message in snapshot.messages] == [
        (1, MessageRole.USER, "say hi"),
        (2, MessageRole.ASSISTANT, "said: hi"),
    ]
    user_message = framework_message_to_langchain_message(snapshot.messages[0])
    assert isinstance(user_message, HumanMessage)
    assert (user_message.id or "").startswith("dex-user-")
    reply = framework_message_to_langchain_message(snapshot.messages[1])
    assert isinstance(reply, AIMessage)
    assert reply.text == "said: hi"
    assert reply.usage_metadata is not None
    assert reply.usage_metadata["input_tokens"] == 10
    assert snapshot.usage == AgentUsage(model_calls=1, input_tokens=10, output_tokens=5)
    assert snapshot.pending_interrupts == []
    assert snapshot.tool_calls == []
    assert await harness.list_streamed_text(session_id) == "said: hi"

    values = await harness.stack.client.get_attributes(session_id, AGENT_STATE, FRAMEWORK_RUN_STATE)
    assert values[AGENT_STATE] == snapshot.state
    assert FRAMEWORK_RUN_STATE not in values
