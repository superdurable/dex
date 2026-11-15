# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turns that steering or ``cancel_turn`` ends before their final answer store their messages."""

from __future__ import annotations

import json
from collections.abc import Sequence

import pytest
from dex_agents import (
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    TURN_STEERED_NOTE_HEADER,
    AgentEventType,
    AgentMessage,
    MessageRole,
)

from dex_agents_langgraph import framework_message_to_langchain_message
from tests.conftest import AGENT_FLOW_TYPES, ScriptedAgentHarness

LOOKUP_THEN_PUBLISH = 'call lookup {"query": "x"} then call publish {"message": "note"}'


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_steering_while_a_write_waits_stores_the_messages_with_the_steering_result(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, LOOKUP_THEN_PUBLISH)
    await harness.wait_for_pending_interrupts(session_id)

    assert await harness.sessions.steer_user_message(session_id, text="say instead")
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert [_describe(message) for message in snapshot.messages] == [
        f"turn-1 user {LOOKUP_THEN_PUBLISH}",
        "turn-1 assistant call lookup",
        "turn-1 tool found x",
        "turn-1 assistant call publish",
        f"turn-1 tool {TOOL_CALL_CANCELLED_BY_STEERING_RESULT}",
        "turn-2 user say instead",
        "turn-2 assistant said: instead",
    ]
    steering_result = framework_message_to_langchain_message(snapshot.messages[4])
    assert steering_result.model_dump(include={"id", "tool_call_id", "status", "name"}) == {
        "id": f"dex-tool-result-turn-1-{snapshot.messages[3].tool_call_id}",
        "tool_call_id": snapshot.messages[3].tool_call_id,
        "status": "error",
        "name": "publish",
    }
    _assert_every_call_answered_once(snapshot.messages)
    assert harness.invocations.count("publish") == 0
    steered = [
        json.loads(event.payload)
        for event in await harness.list_agent_events(session_id)
        if event.type == AgentEventType.TURN_STEERED
    ]
    assert steered == [{"steered_message_count": 1, "message_count": 4}]


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_cancelling_a_resumed_model_call_stores_the_recorded_result(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    completed_session_id = harness.new_session_id()
    await harness.sessions.start_session(completed_session_id, 'call lookup {"query": "x"}')
    completed = await harness.wait_for_turns(completed_session_id, 1)

    session_id = harness.new_session_id()
    command = 'call lookup {"query": "x"} then stall model-gate'
    await harness.sessions.start_session(session_id, command)
    await harness.gate("model-gate").wait_until_reached()
    assert await harness.sessions.cancel_turn(session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert [_describe(message) for message in snapshot.messages] == [
        f"turn-1 user {command}",
        "turn-1 assistant call lookup",
        "turn-1 tool found x",
    ]
    # The result matches that of the same call in a completed turn.
    assert snapshot.messages[2].framework_item == completed.messages[2].framework_item
    assert all(message.origin is None for message in snapshot.messages)

    assert await harness.sessions.send_user_message(session_id, "recall")
    following = await harness.wait_for_turns(session_id, 2)
    assert following.messages[-1].text == f"history: {command}"
    _assert_every_call_answered_once(following.messages)


async def test_steering_a_deep_agent_during_a_tool_stores_its_messages(
    deep_agent_harness: ScriptedAgentHarness,
) -> None:
    harness = deep_agent_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    command = (
        'call write_file {"file_path": "/notes.md", "content": "buy milk"} '
        'then call wait_at_gate {"gate": "files"}'
    )
    await harness.sessions.start_session(session_id, command)
    gate = harness.gate("files")
    await gate.wait_until_reached()

    assert await harness.sessions.steer_user_message(session_id, text="say instead")
    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert [_describe(message) for message in snapshot.messages] == [
        f"turn-1 user {command}",
        "turn-1 assistant call write_file",
        "turn-1 tool Updated file /notes.md",
        "turn-1 assistant call wait_at_gate",
        "turn-1 tool passed files",
        "turn-2 user say instead",
        "turn-2 assistant said: instead",
    ]
    assert not any(
        (message.text or "").startswith(TURN_STEERED_NOTE_HEADER) for message in snapshot.messages
    )
    _assert_every_call_answered_once(snapshot.messages)


def _describe(message: AgentMessage) -> str:
    if message.role is MessageRole.ASSISTANT and message.tool_call_id is not None:
        detail = f"call {message.tool_name}"
    elif message.role is MessageRole.TOOL:
        detail = str(message.tool_output)
    else:
        detail = str(message.text)
    return f"{message.turn_id} {message.role.value} {detail}"


def _assert_every_call_answered_once(messages: Sequence[AgentMessage]) -> None:
    calls = [
        tool_call["id"]
        for message in messages
        if message.role is MessageRole.ASSISTANT
        for tool_call in getattr(framework_message_to_langchain_message(message), "tool_calls", [])
    ]
    results = [message.tool_call_id for message in messages if message.role is MessageRole.TOOL]
    assert sorted(calls) == sorted(results)  # type: ignore[type-var]
    assert len(set(calls)) == len(calls)
