# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Tool call Steps: approvals, rejections, retries, deadlines, and failed turns."""

from __future__ import annotations

import asyncio
import json
import time
from datetime import UTC, datetime

from dex_agents import (
    APPROVAL_DEADLINE_PASSED,
    FRAMEWORK_RUN_STATE,
    OUTCOME_UNKNOWN_TOOL_RESULT,
    PENDING_INTERRUPTS,
    TURN_FAILURE_NOTE_FOOTER,
    TURN_FAILURE_NOTE_HEADER,
    AgentEventType,
    AgentInterrupt,
    AgentSessionStatus,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
)
from dex_agents.testing import WAIT_FOR_COMPLETED_EVENT, wait_for_agent_snapshot
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
    assert waiting.state.status is AgentSessionStatus.RUNNING_TOOLS
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

    # The tool wrote this event from an executor thread.
    events = await harness.agent_events(session_id)
    thread_deltas = [
        event
        for event in events
        if event.type == AgentEventType.TEXT_DELTA
        and json.loads(event.payload) == {"text": "publishing launch"}
    ]
    assert len(thread_deltas) == 1
    assert thread_deltas[0].turn_id == "turn-1"
    assert thread_deltas[0].step_execution_id.startswith("CallSingleAttemptTool-")
    assert all(event.step_execution_id for event in events)


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
    finished = [
        json.loads(event.payload)
        for event in await harness.agent_events(session_id)
        if event.type == AgentEventType.TOOL_CALL_FINISHED
    ]
    assert [payload["status"] for payload in finished] == ["rejected"]


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


async def test_idempotent_write_retries_with_one_idempotency_key(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5, "fail_attempts": 1}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    key = f"{session_id}/turn-1/turn-1-call-0"
    assert snapshot.messages[-1].text == f"charge: charged 5 with key {key}"
    assert harness.idempotency_keys.names() == [
        f"charge {key} attempt 1",
        f"charge {key} attempt 2",
    ]


async def test_idempotent_write_that_exhausts_its_attempts_records_an_unknown_outcome(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5, "fail_attempts": 3}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"charge: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("charge") == 3
    assert "RecordToolFailure" in await harness.executed_step_types(session_id)


async def test_read_that_exhausts_its_attempts_records_a_failure(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call lookup {"query": "flaky", "fail_attempts": 3}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    final_text = snapshot.messages[-1].text or ""
    assert final_text.startswith("lookup: The tool call failed: ")
    assert "lookup attempt 3 lost its connection" in final_text
    assert harness.invocations.count("lookup") == 3


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
    assert await _approval_wait_attempts(harness, session_id) == [1]


async def test_an_approval_before_a_long_deadline_resumes_the_turn_at_once(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker({INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE: "60"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "prompt"}')
    interrupt = await _wait_for_pending_interrupt(harness, session_id)

    approved_at = time.monotonic()
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert time.monotonic() - approved_at < 10
    assert snapshot.messages[-1].text == "publish: published prompt"
    assert await _approval_wait_attempts(harness, session_id) == [1]


async def test_resolve_interrupt_after_the_deadline_returns_false(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker({INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE: "3"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "late"}')
    interrupt = await _wait_for_pending_interrupt(harness, session_id)
    assert interrupt.deadline is not None
    remaining = datetime.fromisoformat(interrupt.deadline) - datetime.now(UTC)
    await asyncio.sleep(remaining.total_seconds() + 0.1)

    assert not await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == f"publish: {APPROVAL_DEADLINE_PASSED}"
    assert harness.invocations.count("publish") == 0


async def test_deadline_rejects_the_interrupt_left_after_another_is_resolved(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker({INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE: "3"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "a"} && call publish {"message": "b"}'
    )
    interrupts = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot.pending_interrupts
        if len(snapshot.pending_interrupts) == 2
        else None,
        description="two pending interrupts",
    )
    first, second = interrupts
    assert await harness.sessions.resolve_interrupt(session_id, first.interrupt_id, approved=True)

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == (
        f"publish: published a; publish: {APPROVAL_DEADLINE_PASSED}"
    )
    assert harness.invocations.count("publish") == 1
    assert not await harness.sessions.resolve_interrupt(
        session_id, second.interrupt_id, approved=True
    )
    assert await _approval_wait_attempts(harness, session_id) == [1, 1]


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
    assert harness.invocations.count("model:turn-1:1") == 3

    assert await harness.sessions.send_user_message(session_id, "say recovered")
    recovered = await harness.wait_for_turns(session_id, 2)
    assert recovered.state is not None
    assert recovered.state.last_turn_error is None
    assert recovered.messages[-1].text == "said: recovered"


async def test_failed_turn_after_a_write_leaves_a_note_and_the_write_runs_once(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call-then-fail charge {"amount": 7}')
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "scripted failure after the tool calls ran" in (failed.state.last_turn_error or "")
    note = failed.messages[-1]
    assert note.role is MessageRole.USER
    assert note.turn_id == "turn-1"
    key = f"{session_id}/turn-1/turn-1-call-0"
    assert note.text == "\n".join(
        [
            TURN_FAILURE_NOTE_HEADER,
            f'- charge {{"amount": 7}} -> succeeded: charged 7 with key {key}',
            TURN_FAILURE_NOTE_FOOTER,
        ]
    )
    assert failed.tool_calls == []
    assert harness.invocations.count("charge") == 1

    # The next turn's model sees the note in its history.
    assert await harness.sessions.send_user_message(session_id, "recall")
    recalled = await harness.wait_for_turns(session_id, 2)
    assert recalled.messages[-1].text == 'history: call-then-fail charge {"amount": 7} | ' + (
        note.text or ""
    )
    assert harness.invocations.count("charge") == 1


async def _approval_wait_attempts(harness: ScriptedAgentHarness, session_id: str) -> list[int]:
    history = await harness.stack.read_step_history(session_id)
    return [
        entry.final_attempt
        for entry in history
        if entry.step_type == "AwaitToolApproval" and entry.event_type == WAIT_FOR_COMPLETED_EVENT
    ]


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
