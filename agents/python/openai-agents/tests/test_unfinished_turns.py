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
import sys
from collections.abc import Sequence
from types import SimpleNamespace
from typing import Any

import pytest
from agents import Agent

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    AgentEventType,
    AgentMessage,
    AgentMessageOrigin,
    MessageRole,
)
from dex_agents_openai_agents._unfinished_turns import read_unfinished_turn_items
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
    # A completed turn whose user rejects with the same text stores the same items.
    rejected_session_id = harness.new_session_id()
    await harness.sessions.start_session(rejected_session_id, LOOKUP_THEN_PUBLISH)
    interrupt = await harness.wait_for_pending_interrupt(rejected_session_id)
    assert await harness.sessions.resolve_interrupt(
        rejected_session_id,
        interrupt.interrupt_id,
        approved=False,
        rejection_message=TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    )
    rejected = await harness.wait_for_turns(rejected_session_id, 1)
    assert [message.framework_item for message in snapshot.messages[1:5]] == [
        message.framework_item for message in rejected.messages[1:5]
    ]
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


async def test_a_failed_call_of_a_cancelled_turn_stores_the_failure_as_a_completed_turn_does(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    completed_session_id = harness.new_session_id()
    await harness.sessions.start_session(completed_session_id, 'call charge {"amount": 0}')
    completed = await harness.wait_for_turns(completed_session_id, 1)

    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 0} then stall model-gate'
    )
    await harness.gate("model-gate").wait_until_reached()
    assert await harness.sessions.cancel_turn(session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[2].tool_output == "The tool call failed: amount must be positive"
    assert snapshot.messages[2].framework_item == completed.messages[2].framework_item


async def test_a_pending_call_of_a_tool_with_an_output_schema_falls_back_to_the_note(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    command = 'call pay {"order": "o-1", "amount": 7} then stall model-gate'
    await harness.sessions.start_session(session_id, command)
    await harness.gate("model-gate").wait_until_reached()
    assert await harness.sessions.cancel_turn(session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    # Only the SDK can encode a schema tool's result, so the session stores its note.
    assert [message.role for message in snapshot.messages] == [MessageRole.USER] * 2
    assert snapshot.messages[1].origin == AgentMessageOrigin.TURN_CANCELLED_NOTE


@pytest.mark.parametrize(
    "framework_run_state",
    [
        "not json",
        '{"format": "dex-agents-langgraph.checkpoint.v1"}',
        '{"$schemaVersion": "1.21"}',
        '{"$schemaVersion": "999", "session_items": []}',
    ],
)
def test_a_run_state_that_is_not_a_run_state_of_a_known_schema_raises(
    framework_run_state: str,
) -> None:
    with pytest.raises(ValueError):
        read_unfinished_turn_items(_turn(framework_run_state, {}), _AGENT, _text_output)


@pytest.mark.parametrize(
    "run_item",
    [
        {"type": "future_run_item", "raw_item": {"type": "future_call", "opaque": "x"}},
        {"type": "tool_call_item", "raw_item": {"type": "future_call", "call_id": "call-a"}},
        {"type": "input_item", "raw_item": {"role": "user", "content": "x"}},
        {"type": "tool_call_item", "raw_item": {"type": "program", "call_id": "program-a"}},
    ],
)
def test_an_unknown_run_item_raises(run_item: dict[str, Any]) -> None:
    with pytest.raises(ValueError):
        read_unfinished_turn_items(_turn(_run_state([run_item]), {}), _AGENT, _text_output)


def test_a_programmatic_call_raises() -> None:
    program_call = _function_call("call-a", caller={"type": "program", "caller_id": "program-a"})
    with pytest.raises(ValueError, match="belongs to a program"):
        read_unfinished_turn_items(_turn(_run_state([program_call]), {}), _AGENT, _text_output)


@pytest.mark.parametrize(
    "call_ids",
    [
        ["result call-a", "call call-a"],
        ["call call-a", "result call-a", "result call-a"],
        ["call call-a", "result call-b"],
        ["call call-a", "call call-b", "result call-a", "call call-c"],
    ],
)
def test_results_out_of_order_or_repeated_raise(call_ids: list[str]) -> None:
    items = [
        _function_call(call_id) if kind == "call" else _function_output(call_id)
        for kind, call_id in (entry.split() for entry in call_ids)
    ]
    with pytest.raises(ValueError, match="does not follow one open call|starts before"):
        read_unfinished_turn_items(_turn(_run_state(items), {}), _AGENT, _text_output)


def test_a_record_whose_call_is_not_in_the_run_state_or_names_another_tool_raises() -> None:
    run_state = _run_state([_function_call("call-a")])
    missing = SimpleNamespace(tool_call_id="call-b", tool_name="lookup")
    with pytest.raises(ValueError, match="not in the run state"):
        read_unfinished_turn_items(_turn(run_state, {"call-b": missing}), _AGENT, _text_output)
    other_tool = SimpleNamespace(tool_call_id="call-a", tool_name="charge")
    with pytest.raises(ValueError, match="recorded for tool charge"):
        read_unfinished_turn_items(_turn(run_state, {"call-a": other_tool}), _AGENT, _text_output)


def test_a_missing_private_sdk_function_raises_so_the_session_stores_its_note(
    monkeypatch: pytest.MonkeyPatch,
) -> None:
    turn = _turn(_run_state([_function_call("call-a")]), {})
    monkeypatch.delattr("agents.run_state._deserialize_items")
    with pytest.raises(ValueError, match="lacks the RunState item functions"):
        read_unfinished_turn_items(turn, _AGENT, _text_output)
    monkeypatch.undo()
    # A removed module raises ImportError, which falls back the same way.
    monkeypatch.setitem(sys.modules, "agents.run_internal.items", None)
    with pytest.raises(ValueError, match="lacks the RunState item functions"):
        read_unfinished_turn_items(turn, _AGENT, _text_output)


def test_a_pending_call_without_a_record_gets_the_unknown_outcome() -> None:
    items = read_unfinished_turn_items(
        _turn(_run_state([_function_call("call-a")]), {}), _AGENT, _text_output
    )
    assert items[-1] == {
        "call_id": "call-a",
        "output": OUTCOME_UNKNOWN_TOOL_RESULT,
        "type": "function_call_output",
    }


_AGENT: Agent[Any] = Agent(name="Assistant")
_AGENT_DATA = {"name": "Assistant"}


def _text_output(function_call: Any, record: Any) -> str:
    return OUTCOME_UNKNOWN_TOOL_RESULT if record is None else "found"


def _run_state(session_items: list[dict[str, Any]]) -> str:
    return json.dumps({"$schemaVersion": "1.21", "session_items": session_items})


def _function_call(call_id: str, **extra: Any) -> dict[str, Any]:
    raw_item = {"arguments": "{}", "call_id": call_id, "name": "lookup", "type": "function_call"}
    return {"type": "tool_call_item", "raw_item": {**raw_item, **extra}, "agent": _AGENT_DATA}


def _function_output(call_id: str) -> dict[str, Any]:
    raw_item = {"call_id": call_id, "output": "found", "type": "function_call_output"}
    return {"type": "tool_call_output_item", "raw_item": raw_item, "agent": _AGENT_DATA}


def _turn(framework_run_state: str, tool_calls: dict[str, Any]) -> Any:
    return SimpleNamespace(
        turn_id="turn-1",
        framework_run_state=framework_run_state,
        tool_calls=tool_calls,
        messages=[],
        recorded_tool_call=tool_calls.get,
    )


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
