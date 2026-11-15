# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turns that steering or ``cancel_turn`` ends before their final answer store their items."""

from __future__ import annotations

import json
from collections.abc import Sequence

from dex_agents import (
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    AgentEventType,
    AgentMessage,
    MessageRole,
)
from tests.conftest import SupportAgentHarness

LOOKUP_THEN_PUBLISH = 'call lookup {"query": "x"} then call publish {"message": "note"}'


async def test_steering_while_a_write_waits_stores_the_items_with_the_steering_result(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, LOOKUP_THEN_PUBLISH)
    await harness.wait_for_pending_interrupt(session_id)

    assert await harness.sessions.steer_user_message(session_id, text="recall")
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert [_describe(message) for message in snapshot.messages] == [
        f"turn-1 user {LOOKUP_THEN_PUBLISH}",
        "turn-1 assistant call lookup",
        "turn-1 tool found x",
        "turn-1 assistant call publish",
        f"turn-1 tool {TOOL_CALL_CANCELLED_BY_STEERING_RESULT}",
        "turn-2 user recall",
        f"turn-2 assistant history: {LOOKUP_THEN_PUBLISH}; tools: found x | "
        f"{TOOL_CALL_CANCELLED_BY_STEERING_RESULT}",
    ]
    assert json.loads(snapshot.messages[4].framework_item) == {
        "call_id": snapshot.messages[3].tool_call_id,
        "output": TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
        "type": "function_call_output",
    }
    _assert_every_call_answered_once(snapshot.messages)
    assert harness.tool_runs() == ["lookup"]
    steered = [
        event.payload
        for event in await harness.list_agent_events(session_id)
        if event.type == AgentEventType.TURN_STEERED
    ]
    assert [json.loads(payload) for payload in steered] == [
        {"steered_message_count": 1, "message_count": 4}
    ]


async def test_cancelling_a_resumed_model_call_stores_the_recorded_result(
    harness: SupportAgentHarness,
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
    # The items match those of the same call in a completed turn.
    assert [message.framework_item for message in snapshot.messages[1:]] == [
        message.framework_item for message in completed.messages[1:3]
    ]
    assert all(message.origin is None for message in snapshot.messages)

    assert await harness.sessions.send_user_message(session_id, "recall")
    following = await harness.wait_for_turns(session_id, 2)
    assert following.messages[-1].text == f"history: {command}; tools: found x"
    _assert_every_call_answered_once(following.messages)


def _describe(message: AgentMessage) -> str:
    if message.role is MessageRole.ASSISTANT and message.tool_call_id is not None:
        detail = f"call {message.tool_name}"
    else:
        detail = message.text if message.text is not None else str(message.tool_output)
    return f"{message.turn_id} {message.role.value} {detail}"


def _assert_every_call_answered_once(messages: Sequence[AgentMessage]) -> None:
    calls = [
        message.tool_call_id
        for message in messages
        if message.role is MessageRole.ASSISTANT and message.tool_call_id is not None
    ]
    results = [message.tool_call_id for message in messages if message.role is MessageRole.TOOL]
    assert sorted(calls) == sorted(results)  # type: ignore[type-var]
    assert len(set(calls)) == len(calls)
