# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deferred tool calls: approvals, rejections, idempotency keys, and repeated suspensions."""

from __future__ import annotations

import json

import pytest
from dex_agents import (
    FRAMEWORK_RUN_STATE,
    PENDING_INTERRUPTS,
    TURN_FAILURE_NOTE_FOOTER,
    TURN_FAILURE_NOTE_HEADER,
    AgentInterrupt,
    AgentSessionStatus,
    AgentUsage,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
)
from dex_agents.testing import wait_for_agent_snapshot
from langchain_core.messages import HumanMessage, ToolMessage

from dex_agents_langgraph import framework_message_to_langchain_message
from tests.conftest import AGENT_FLOW_TYPES, ScriptedAgentHarness
from tests.scripted_agents import UNPROTECTED_AGENT_FLOW_TYPE


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_approved_write_survives_a_worker_restart_and_runs_once(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "launch"}')
    [interrupt] = await harness.wait_for_pending_interrupts(session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert waiting.state is not None
    assert waiting.state.status is AgentSessionStatus.RUNNING_TOOLS
    assert [(call.tool_name, call.effect, call.status) for call in waiting.tool_calls] == [
        ("publish", ToolEffect.NON_IDEMPOTENT_WRITE, ToolCallStatus.AWAITING_APPROVAL)
    ]
    assert (interrupt.tool_call_id, interrupt.tool_name, interrupt.arguments) == (
        "call-0-0-0",
        "publish",
        '{"message": "launch"}',
    )
    values = await harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE)
    run_state = json.loads(values[FRAMEWORK_RUN_STATE])
    assert run_state["format"] == "dex-agents-langgraph.checkpoint.v1"
    assert [entry["tool_call_ids"] for entry in run_state["interrupts"]] == [["call-0-0-0"]]

    worker.kill()
    await harness.stack.start_worker()
    after_restart = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot,
        description="a snapshot from the replacement Worker",
    )
    assert after_restart.pending_interrupts == [interrupt]
    assert harness.invocations.count("publish") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published launch"
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.tool_output for message in tool_results] == ["published launch"]
    assert harness.invocations.count("publish") == 1
    assert harness.model_call_count() == 2
    executed = await harness.executed_step_types(session_id)
    assert executed[1:] == [
        "RunModel",
        "AwaitToolApproval",
        "CallSingleAttemptTool",
        "ResumeTurn",
        "RunModel",
    ]
    assert snapshot.usage == AgentUsage(model_calls=2, input_tokens=20, output_tokens=10)
    assert snapshot.tool_calls == []
    values = await harness.stack.client.get_attributes(
        session_id, FRAMEWORK_RUN_STATE, PENDING_INTERRUPTS
    )
    assert FRAMEWORK_RUN_STATE not in values
    assert values[PENDING_INTERRUPTS] == []


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_rejected_write_never_runs_and_the_model_sees_the_rejection(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "risky"}')
    [interrupt] = await harness.wait_for_pending_interrupts(session_id)

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="not today"
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: not today"
    [tool_result] = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert tool_result.tool_output == "not today"
    replayed = framework_message_to_langchain_message(tool_result)
    assert isinstance(replayed, ToolMessage)
    assert replayed.status == "error"
    assert harness.invocations.count("publish") == 0


async def test_resolve_interrupt_returns_false_the_second_time(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "once"}')
    [interrupt] = await harness.wait_for_pending_interrupts(session_id)

    assert not await harness.sessions.resolve_interrupt(session_id, "unknown-call", approved=True)
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    assert not await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="too late"
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published once"
    assert harness.invocations.count("publish") == 1


async def test_idempotent_write_receives_its_idempotency_key_and_reports_tool_errors(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5} && call charge {"amount": -1}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        f"charge: charged 5 with key {session_id}/turn-1/call-0-0-0; "
        "charge: The tool call failed: amount must be positive"
    )
    assert snapshot.pending_interrupts == []
    assert harness.invocations.count("charge") == 2


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_two_approvals_of_one_model_message_wait_together(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "a"} && call publish {"message": "b"}'
    )
    interrupts = await harness.wait_for_pending_interrupts(session_id, count=2)
    assert sorted(interrupt.tool_call_id for interrupt in interrupts) == [
        "call-0-0-0",
        "call-0-0-1",
    ]
    for interrupt in interrupts:
        assert await harness.sessions.resolve_interrupt(
            session_id, interrupt.interrupt_id, approved=True
        )

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published a; publish: published b"
    assert harness.invocations.count("publish") == 2
    assert harness.model_call_count() == 2
    executed = await harness.executed_step_types(session_id)
    assert executed.count("RunModel") == 2
    assert executed.count("CallSingleAttemptTool") == 2


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_a_later_model_call_in_the_same_turn_requests_tools_again(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "a"} then call publish {"message": "b"}'
    )
    first = await _wait_for_interrupt_of(harness, session_id, "call-0-0-0")
    assert await harness.sessions.resolve_interrupt(session_id, first.interrupt_id, approved=True)
    second = await _wait_for_interrupt_of(harness, session_id, "call-0-1-0")
    assert harness.invocations.count("publish") == 1
    assert await harness.sessions.resolve_interrupt(session_id, second.interrupt_id, approved=True)

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published a; publish: published b"
    assert [
        message.text for message in snapshot.messages if message.role is MessageRole.ASSISTANT
    ] == ["step 1", "step 2", "publish: published a; publish: published b"]
    assert harness.invocations.count("publish") == 2
    assert [harness.model_call_count(number) for number in (1, 2, 3)] == [1, 1, 1]
    assert snapshot.usage.model_calls == 3
    executed = await harness.executed_step_types(session_id)
    assert executed.count("RunModel") == 3
    assert executed.count("ResumeTurn") == 2


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_a_turn_that_fails_after_a_write_leaves_a_note_for_the_next_turn(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    command = 'call charge {"amount": 5} then fail'
    await harness.sessions.start_session(session_id, command)
    failed = await harness.wait_for_turns(session_id, 1)

    idempotency_key = f"{session_id}/turn-1/call-0-0-0"
    expected_note = "\n".join(
        [
            TURN_FAILURE_NOTE_HEADER,
            f'- charge {{"amount": 5}} -> succeeded: charged 5 with key {idempotency_key}',
            TURN_FAILURE_NOTE_FOOTER,
        ]
    )
    assert failed.state is not None
    assert "scripted model failure" in (failed.state.last_turn_error or "")
    assert [(message.role, message.text, message.turn_id) for message in failed.messages] == [
        (MessageRole.USER, command, "turn-1"),
        (MessageRole.USER, expected_note, "turn-1"),
    ]
    replayed_note = framework_message_to_langchain_message(failed.messages[1])
    assert isinstance(replayed_note, HumanMessage)
    assert replayed_note.text == expected_note
    assert harness.invocations.count("charge") == 1
    # The first model call ran once; only the failing second call ran on every RunModel attempt.
    assert harness.model_call_count(1) == 1
    assert harness.model_call_count(2) == 3

    assert await harness.sessions.send_user_message(session_id, "recall")
    recalled = await harness.wait_for_turns(session_id, 2)
    assert recalled.messages[-1].text == f"history: {command} | {expected_note}"
    assert recalled.state is not None
    assert recalled.state.last_turn_error is None
    assert harness.invocations.count("charge") == 1


@pytest.mark.parametrize("agent_flow_type", [UNPROTECTED_AGENT_FLOW_TYPE])
async def test_no_tool_runs_inside_run_model_without_the_durable_middleware(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call lookup {"query": "x"} && call publish {"message": "unsafe"}'
    )
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "DurableToolCallMiddleware" in (failed.state.last_turn_error or "")
    assert [message.role for message in failed.messages] == [MessageRole.USER]
    assert harness.tool_invocations() == []
    assert harness.model_call_count() == 3


async def _wait_for_interrupt_of(
    harness: ScriptedAgentHarness,
    session_id: str,
    tool_call_id: str,
) -> AgentInterrupt:
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(
            (
                interrupt
                for interrupt in snapshot.pending_interrupts
                if interrupt.tool_call_id == tool_call_id
            ),
            None,
        ),
        description=f"a pending interrupt for {tool_call_id}",
    )
