# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deferred tool calls: approvals, rejections, retries, deadlines, and failures."""

from __future__ import annotations

from dex_agents import (
    APPROVAL_DEADLINE_PASSED,
    FRAMEWORK_RUN_STATE,
    PENDING_INTERRUPTS,
    AgentInterrupt,
    AgentSessionStatus,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
)
from dex_agents.testing import wait_for_agent_snapshot
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE


async def test_approved_write_survives_a_worker_restart_and_runs_once(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "launch"}')
    interrupt = await _wait_for_pending_interrupt(harness, session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert waiting.state is not None
    assert waiting.state.status is AgentSessionStatus.WAITING_FOR_INTERRUPT
    assert [(call.tool_name, call.effect, call.status) for call in waiting.tool_calls] == [
        ("publish", ToolEffect.NON_IDEMPOTENT_WRITE, ToolCallStatus.AWAITING_APPROVAL)
    ]
    assert interrupt.tool_name == "publish"
    assert interrupt.deadline is None

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
    assert snapshot.tool_calls == []
    values = await harness.stack.client.get_attributes(
        session_id, FRAMEWORK_RUN_STATE, PENDING_INTERRUPTS
    )
    assert FRAMEWORK_RUN_STATE not in values
    assert values[PENDING_INTERRUPTS] == []


async def test_rejected_write_never_runs_and_the_model_sees_the_rejection(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "risky"}')
    interrupt = await _wait_for_pending_interrupt(harness, session_id)

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="not today"
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: not today"
    assert harness.invocations.count("publish") == 0


async def test_resolve_interrupt_returns_false_the_second_time(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "once"}')
    interrupt = await _wait_for_pending_interrupt(harness, session_id)

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


async def test_approved_calls_run_in_deferral_order(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call publish {"message": "first"} && call lookup {"query": "inline"}'
        ' && call charge {"amount": 3}',
    )
    interrupt = await _wait_for_pending_interrupt(harness, session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert [(call.tool_name, call.deferral_order, call.status) for call in waiting.tool_calls] == [
        ("publish", 0, ToolCallStatus.AWAITING_APPROVAL),
        ("charge", 1, ToolCallStatus.APPROVED),
    ]
    assert harness.invocations.names() == ["run_turn:turn-1", "lookup"]

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == (
        f"publish: published first; lookup: found inline; "
        f"charge: charged 3 with key {session_id}/turn-1-call-2"
    )
    assert harness.invocations.names() == [
        "run_turn:turn-1",
        "lookup",
        "publish",
        "charge",
        "run_turn:turn-1",
    ]


async def test_idempotent_write_retries_with_one_idempotency_key(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5, "fail_attempts": 1}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"charge: charged 5 with key {session_id}/turn-1-call-0"
    assert harness.invocations.count("charge") == 2


async def test_idempotent_write_that_exhausts_its_attempts_records_a_failure(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5, "fail_attempts": 3}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    final_text = snapshot.messages[-1].text or ""
    assert final_text.startswith("charge: The tool call failed: ")
    assert "charge attempt 3 lost its connection" in final_text
    assert harness.invocations.count("charge") == 3


async def test_tool_execution_error_reaches_the_model_without_a_retry(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call charge {"amount": -1}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "charge: The tool call failed: amount must be positive"
    assert harness.invocations.count("charge") == 1


async def test_expired_interrupt_deadline_rejects_the_call(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker({INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE: "2"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "slow human"}')
    interrupt = await _wait_for_pending_interrupt(harness, session_id)
    assert interrupt.deadline is not None

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == f"publish: {APPROVAL_DEADLINE_PASSED}"
    assert snapshot.pending_interrupts == []
    assert not await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    assert harness.invocations.count("publish") == 0


async def test_failed_turn_records_its_error_and_the_next_turn_runs(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "fail")
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "scripted failure" in (failed.state.last_turn_error or "")
    assert [message.text for message in failed.messages] == ["fail"]
    assert harness.invocations.count("run_turn:turn-1") == 3

    assert await harness.sessions.send_user_message(session_id, "say recovered")
    recovered = await harness.wait_for_turns(session_id, 2)
    assert recovered.state is not None
    assert recovered.state.last_turn_error is None
    assert recovered.messages[-1].text == "said: recovered"


async def _wait_for_pending_interrupt(
    harness: ScriptedAgentHarness,
    session_id: str,
) -> AgentInterrupt:
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="a pending interrupt",
    )
