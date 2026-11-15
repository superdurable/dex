# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turns that steering or ``cancel_turn`` ends before their final answer store their messages."""

from __future__ import annotations

import base64
import json
from collections.abc import Sequence
from pathlib import Path
from types import SimpleNamespace
from typing import Any

import pytest
from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    TURN_STEERED_NOTE_HEADER,
    AgentEventType,
    AgentMessage,
    MessageRole,
)
from langchain_core.messages import AIMessage, ToolMessage

from dex_agents_langgraph import LangGraphAgentRunner, framework_message_to_langchain_message
from dex_agents_langgraph._checkpoints import RUN_STATE_FORMAT, TurnCheckpointSaver
from tests.conftest import AGENT_FLOW_TYPES, ScriptedAgentHarness
from tests.scripted_agents import build_create_agent

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
    # A completed turn whose user rejects with the same text stores the same result.
    rejected_session_id = harness.new_session_id()
    await harness.sessions.start_session(rejected_session_id, LOOKUP_THEN_PUBLISH)
    [interrupt] = await harness.wait_for_pending_interrupts(rejected_session_id)
    assert await harness.sessions.resolve_interrupt(
        rejected_session_id,
        interrupt.interrupt_id,
        approved=False,
        rejection_message=TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    )
    rejected = await harness.wait_for_turns(rejected_session_id, 1)
    for index in (2, 4):
        assert snapshot.messages[index].framework_item == rejected.messages[index].framework_item
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


@pytest.mark.parametrize(
    "framework_run_state", ["not json", '{"$schemaVersion": "1.21", "session_items": []}']
)
def test_a_run_state_that_is_not_a_checkpoint_raises(
    tmp_path: Path, framework_run_state: str
) -> None:
    runner = LangGraphAgentRunner(build_create_agent(tmp_path))
    with pytest.raises(ValueError):
        runner.build_unfinished_turn_messages(_turn(framework_run_state, {}))


def test_a_record_whose_call_is_not_in_the_checkpoint_or_names_another_tool_raises(
    tmp_path: Path,
) -> None:
    runner = LangGraphAgentRunner(build_create_agent(tmp_path))
    missing = SimpleNamespace(tool_call_id="call-b", tool_name="lookup")
    with pytest.raises(ValueError, match="not in the run state"):
        runner.build_unfinished_turn_messages(_turn(_checkpoint_run_state(), {"call-b": missing}))
    other_tool = SimpleNamespace(tool_call_id="call-a", tool_name="charge")
    with pytest.raises(ValueError, match="recorded for tool charge"):
        runner.build_unfinished_turn_messages(
            _turn(_checkpoint_run_state(), {"call-a": other_tool})
        )


@pytest.mark.parametrize(
    "messages",
    [
        [ToolMessage("found", tool_call_id="call-a", id="r-1"), "call"],
        [
            "call",
            ToolMessage("a", tool_call_id="call-a", id="r-1"),
            ToolMessage("b", tool_call_id="call-a", id="r-2"),
        ],
        ["call", ToolMessage("found", tool_call_id="call-b", id="r-1")],
        ["call", AIMessage("later", id="ai-2")],
    ],
)
def test_results_out_of_order_or_repeated_raise(tmp_path: Path, messages: list[Any]) -> None:
    runner = LangGraphAgentRunner(build_create_agent(tmp_path))
    with pytest.raises(ValueError, match="does not follow one open call|before a later message"):
        runner.build_unfinished_turn_messages(_turn(_checkpoint_run_state(messages), {}))


def test_a_pending_call_without_a_record_gets_the_unknown_outcome(tmp_path: Path) -> None:
    runner = LangGraphAgentRunner(build_create_agent(tmp_path))
    messages = runner.build_unfinished_turn_messages(_turn(_checkpoint_run_state(), {}))
    assert [(message.role, message.tool_output) for message in messages] == [
        (MessageRole.ASSISTANT, None),
        (MessageRole.TOOL, OUTCOME_UNKNOWN_TOOL_RESULT),
    ]


def _checkpoint_run_state(messages: list[Any] | None = None) -> str:
    call = AIMessage(
        content="", id="ai-1", tool_calls=[{"id": "call-a", "name": "lookup", "args": {}}]
    )
    state_messages = [call if message == "call" else message for message in messages or ["call"]]
    data_type, data = TurnCheckpointSaver().serde.dumps_typed(
        {"channel_values": {"messages": state_messages}}
    )
    stored = {
        "checkpoint_ns": "",
        "checkpoint": {"type": data_type, "data": base64.b64encode(data).decode("ascii")},
        "metadata": {},
        "pending_writes": [],
    }
    document = {
        "format": RUN_STATE_FORMAT,
        "thread_id": "session",
        "checkpoints": [stored],
        "interrupts": [],
    }
    return json.dumps(document)


def _turn(framework_run_state: str, tool_calls: dict[str, Any]) -> Any:
    return SimpleNamespace(
        turn_id="turn-1",
        framework_run_state=framework_run_state,
        tool_calls=tool_calls,
        messages=[],
        context_summary=None,
        recorded_tool_call=tool_calls.get,
    )


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
