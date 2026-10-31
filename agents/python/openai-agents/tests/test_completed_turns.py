# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turn Step shapes, history replay, snapshots, queued messages, and live events."""

from __future__ import annotations

import json

from dex_agents import (
    AGENT_STATE,
    FRAMEWORK_RUN_STATE,
    AgentEventType,
    AgentSessionStatus,
    AgentUsage,
    MessageRole,
)
from dex_agents.testing import wait_until
from tests.conftest import SupportAgentHarness

MODEL_CALL_USAGE = AgentUsage(
    model_calls=1, input_tokens=10, output_tokens=5, cached_input_tokens=2, reasoning_tokens=1
)


async def test_read_tool_turn_runs_model_tool_and_resume_steps_and_replays_history(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call lookup {"query": "docs"}')
    first = await harness.wait_for_turns(session_id, 1)

    assert [
        (message.role, message.tool_call_id, message.tool_name) for message in first.messages
    ] == [
        (MessageRole.USER, None, None),
        (MessageRole.ASSISTANT, "call-1-0-0", "lookup"),
        (MessageRole.TOOL, "call-1-0-0", "lookup"),
        (MessageRole.ASSISTANT, None, None),
    ]
    assert first.messages[1].tool_arguments == '{"query": "docs"}'
    assert first.messages[2].tool_output == "found docs"
    assert first.messages[-1].text == "lookup: found docs"
    assert first.usage.model_calls == 2
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]
    assert harness.invocations.names() == ["model:1:1", "lookup", "model:1:2"]

    assert await harness.sessions.send_user_message(session_id, "recall")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == ('history: call lookup {"query": "docs"}; tools: found docs')
    assert [message.turn_id for message in second.messages] == ["turn-1"] * 4 + ["turn-2"] * 2
    assert harness.tool_runs() == ["lookup"]


async def test_snapshot_returns_the_conversation_after_a_completed_turn(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    assert await harness.sessions.send_user_message(session_id, "say hi")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
    assert snapshot.state.last_turn_error is None
    assert [(message.sequence, message.role, message.text) for message in snapshot.messages] == [
        (1, MessageRole.USER, "say hi"),
        (2, MessageRole.ASSISTANT, "said: hi"),
    ]
    assert json.loads(snapshot.messages[0].framework_item) == {"role": "user", "content": "say hi"}
    answer_item = json.loads(snapshot.messages[1].framework_item)
    assert (answer_item["type"], answer_item["role"]) == ("message", "assistant")
    assert answer_item["content"][0]["text"] == "said: hi"
    assert snapshot.usage == MODEL_CALL_USAGE
    assert snapshot.pending_interrupts == []
    assert snapshot.tool_calls == []
    assert snapshot.queued_message_count == 0
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "AwaitUserMessage",
        "RunModel",
    ]

    values = await harness.stack.client.get_attributes(session_id, AGENT_STATE, FRAMEWORK_RUN_STATE)
    assert values[AGENT_STATE] == snapshot.state
    assert FRAMEWORK_RUN_STATE not in values


async def test_a_message_sent_during_a_turn_waits_in_the_queue(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall busy-gate")
    gate = harness.gate("busy-gate")
    await gate.wait_until_reached()

    assert await harness.sessions.send_user_message(session_id, "say later")

    async def queued_count() -> int:
        return (await harness.sessions.get_agent_snapshot(session_id)).queued_message_count

    assert await wait_until("one queued message", queued_count) == 1
    during = await harness.sessions.get_agent_snapshot(session_id)
    assert during.state is not None
    assert during.state.status is AgentSessionStatus.RUNNING_MODEL

    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert [message.text for message in snapshot.messages] == [
        "stall busy-gate",
        "thinking before busy-gate",
        "stalled at busy-gate",
        "say later",
        "said: later",
    ]
    assert snapshot.queued_message_count == 0


async def test_text_and_reasoning_summary_deltas_reach_agent_events(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "think hard")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert [(message.role, message.text) for message in snapshot.messages] == [
        (MessageRole.USER, "think hard"),
        (MessageRole.ASSISTANT, None),
        (MessageRole.ASSISTANT, "thought: hard"),
    ]
    assert json.loads(snapshot.messages[1].framework_item)["type"] == "reasoning"
    events = await harness.list_agent_events(session_id)
    deltas = [
        (event.type, json.loads(event.payload)["text"], event.turn_id, event.attempt)
        for event in events
        if event.type in (AgentEventType.TEXT_DELTA, AgentEventType.REASONING_DELTA)
    ]
    assert deltas == [
        (AgentEventType.REASONING_DELTA, "pondering hard", "turn-1", 1),
        (AgentEventType.TEXT_DELTA, "thought: hard", "turn-1", 1),
    ]
    assert all(event.step_execution_id for event in events)
    usage_events = [event for event in events if event.type == AgentEventType.USAGE_RECORDED]
    assert json.loads(usage_events[-1].payload)["recorded"]["reasoning_tokens"] == 1
