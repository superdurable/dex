# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Stepwise mode: the built-in loop, built-in tools, and Worker crashes, on a real Dex Server."""

from __future__ import annotations

import asyncio
import json
import time
from datetime import UTC, datetime

import pytest
from dex import StepExecutionId, TimerId

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    PENDING_INTERRUPTS,
    QUESTION_DEADLINE_PASSED,
    QUESTION_DECLINED_TOOL_RESULT,
    TURN_FAILURE_NOTE_HEADER,
    AgentEventType,
    AgentInterrupt,
    AgentSnapshot,
    InterruptKind,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
)
from dex_agents.stepwise import TOOL_CALL_LIMIT_REACHED_RESULT
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    WAIT_FOR_COMPLETED_EVENT,
    wait_for_agent_snapshot,
    wait_until,
)
from tests.conftest import ScriptedAgentHarness
from tests.stepwise_agent import (
    INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE as STEPWISE_INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE,
)
from tests.stepwise_agent import (
    MAX_MODEL_CALLS_ENVIRONMENT_VARIABLE,
    UNFINISHED_BUILDER_ENVIRONMENT_VARIABLE,
)


async def test_a_turn_runs_a_read_tool_between_two_model_calls_and_replays_history(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "lookup-gated lookup-gate order-7")
    gate = harness.gate("lookup-gate")
    await gate.wait_until_reached()

    # The assistant's tool call item is stored while the tool runs.
    running = await harness.sessions.get_agent_snapshot(session_id)
    assert [(message.role, message.tool_name) for message in running.messages] == [
        (MessageRole.USER, None),
        (MessageRole.ASSISTANT, "lookup_order"),
    ]
    assert [(call.tool_name, call.effect, call.status) for call in running.tool_calls] == [
        ("lookup_order", ToolEffect.READ, ToolCallStatus.APPROVED)
    ]
    gate.release()
    first = await harness.wait_for_turns(session_id, 1)

    assert [(message.sequence, message.role) for message in first.messages] == [
        (1, MessageRole.USER),
        (2, MessageRole.ASSISTANT),
        (3, MessageRole.TOOL),
        (4, MessageRole.ASSISTANT),
    ]
    assert first.messages[2].tool_output == "found order-7"
    assert first.messages[-1].text == "lookup_order: found order-7"
    assert first.usage.model_calls == 2
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]
    completed = [
        json.loads(event.payload)
        for event in await harness.agent_events(session_id)
        if event.type == AgentEventType.TURN_COMPLETED
    ]
    assert completed == [{"message_count": 3}]

    assert await harness.sessions.send_user_message(session_id, "say hello")
    assert await harness.sessions.send_user_message(session_id, "recall")
    recalled = await harness.wait_for_turns(session_id, 3)
    # Turn 1 stored two model items, and "say hello" a reasoning item and a message.
    assert recalled.messages[-1].text == (
        "history: lookup-gated lookup-gate order-7 | say hello (4 model items)"
    )
    assert harness.invocations.count("lookup_order") == 1


async def test_a_write_waits_for_approval_and_runs_once(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "publish launch notes")
    interrupt = await _wait_for_pending_interrupt(harness, session_id)
    assert interrupt.kind is InterruptKind.TOOL_APPROVAL
    assert interrupt.tool_name == "publish_note"
    assert harness.invocations.count("publish_note") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == 'publish_note: {"published": "launch notes"}'
    assert harness.invocations.count("publish_note") == 1
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "AwaitToolApproval",
        "CallSingleAttemptTool",
        "ResumeTurn",
        "RunModel",
    ]


async def test_ask_user_raises_a_question_that_answer_question_resolves_once(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "ask-strict Which color? | red | blue")
    question = await _wait_for_pending_interrupt(harness, session_id)
    assert question.kind is InterruptKind.QUESTION
    assert question.interrupt_id == question.tool_call_id
    assert (question.tool_name, question.question, question.choices) == (
        "ask_user",
        "Which color?",
        ["red", "blue"],
    )
    assert not question.allows_free_text
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert [(call.tool_name, call.status) for call in waiting.tool_calls] == [
        ("ask_user", ToolCallStatus.AWAITING_ANSWER)
    ]

    assert not await harness.sessions.answer_question(session_id, question.interrupt_id, "green")
    assert not await harness.sessions.answer_question(session_id, question.interrupt_id, "  ")
    assert not await harness.sessions.resolve_interrupt(
        session_id, question.interrupt_id, approved=True
    )
    assert await harness.sessions.answer_question(session_id, question.interrupt_id, "blue")
    assert not await harness.sessions.answer_question(session_id, question.interrupt_id, "red")
    assert not await harness.sessions.resolve_interrupt(
        session_id, question.interrupt_id, approved=False
    )

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == 'ask_user: {"status":"answered","answer":"blue"}'
    assert snapshot.pending_interrupts == []
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "AwaitQuestionAnswer",
        "ResumeTurn",
        "RunModel",
    ]
    events = await harness.agent_events(session_id)
    raised = [
        json.loads(event.payload)
        for event in events
        if event.type == AgentEventType.INTERRUPT_RAISED
    ]
    assert raised == [
        {
            "allows_free_text": False,
            "choices": ["red", "blue"],
            "deadline": None,
            "interrupt_id": question.interrupt_id,
            "kind": "question",
            "question": "Which color?",
            "tool_call_id": question.tool_call_id,
            "tool_name": "ask_user",
        }
    ]
    resolved = [
        json.loads(event.payload)
        for event in events
        if event.type == AgentEventType.INTERRUPT_RESOLVED
    ]
    assert resolved == [
        {
            "answered": True,
            "approved": True,
            "cancellation_reason": None,
            "interrupt_id": question.interrupt_id,
            "kind": "question",
            "rejection_message": None,
        }
    ]


async def test_declined_and_invalid_questions_reach_the_model(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "ask Shall I continue?")
    question = await _wait_for_pending_interrupt(harness, session_id)
    assert question.allows_free_text
    assert await harness.sessions.resolve_interrupt(
        session_id, question.interrupt_id, approved=False
    )
    declined = await harness.wait_for_turns(session_id, 1)
    assert declined.messages[-1].text == f"ask_user: {QUESTION_DECLINED_TOOL_RESULT}"

    assert await harness.sessions.send_user_message(session_id, 'ask-raw {"question": ""}')
    invalid = await harness.wait_for_turns(session_id, 2)
    assert invalid.messages[-1].text == (
        "ask_user: The tool call failed: invalid ask_user arguments: "
        "question must be a non-empty string"
    )
    assert "AwaitQuestionAnswer" not in (await harness.executed_step_types(session_id))[5:]


async def test_a_tool_question_runs_the_tool_again_with_the_answer(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "transfer 25")
    question = await _wait_for_pending_interrupt(harness, session_id)
    assert question.interrupt_id == f"{question.tool_call_id}-input-1"
    assert (question.tool_name, question.question, question.choices) == (
        "transfer_funds",
        "Which account?",
        ["checking", "savings"],
    )

    assert not await harness.sessions.answer_question(
        session_id, question.interrupt_id, "brokerage"
    )
    assert await harness.sessions.answer_question(session_id, question.interrupt_id, "savings")
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "transfer_funds: moved 25 from savings"
    assert harness.invocations.names().count("transfer_funds:ask") == 1
    assert harness.invocations.names().count("transfer_funds:run") == 1
    # The first AwaitQuestionAnswer raises the question under the lock; the second takes the answer.
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallSingleAttemptTool",
        "AwaitQuestionAnswer",
        "AwaitQuestionAnswer",
        "CallSingleAttemptTool",
        "ResumeTurn",
        "RunModel",
    ]


async def test_two_parallel_tools_that_both_ask_raise_two_answerable_questions(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "transfer-pair 10 20")
    questions = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot.pending_interrupts
        if len(snapshot.pending_interrupts) == 2
        else None,
        description="two pending tool questions",
    )
    by_call = {question.tool_call_id: question for question in questions}
    assert len(by_call) == 2
    for tool_call_id, question in by_call.items():
        assert question.interrupt_id == f"{tool_call_id}-input-1"
        assert question.kind is InterruptKind.QUESTION
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert [call.status for call in waiting.tool_calls] == [ToolCallStatus.AWAITING_ANSWER] * 2
    first, second = (by_call[call.tool_call_id] for call in waiting.tool_calls)

    assert await harness.sessions.answer_question(session_id, second.interrupt_id, "savings")
    remaining = await harness.sessions.get_agent_snapshot(session_id)
    assert [question.interrupt_id for question in remaining.pending_interrupts] == [
        first.interrupt_id
    ]
    assert await harness.sessions.answer_question(session_id, first.interrupt_id, "checking")
    assert not await harness.sessions.answer_question(session_id, first.interrupt_id, "savings")

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == (
        "transfer_funds: moved 10 from checking; transfer_funds: moved 20 from savings"
    )
    assert harness.invocations.names().count("transfer_funds:ask") == 2
    assert harness.invocations.names().count("transfer_funds:run") == 2
    raised = [
        json.loads(event.payload)["interrupt_id"]
        for event in await harness.agent_events(session_id)
        if event.type == AgentEventType.INTERRUPT_RAISED
    ]
    assert sorted(raised) == sorted([first.interrupt_id, second.interrupt_id])


async def test_durable_wait_suspends_the_turn_on_a_timer_that_no_process_holds(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    started_at = time.monotonic()
    await harness.sessions.start_session(session_id, "wait 2 the build to finish")
    waiting = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot
        if any(call.status is ToolCallStatus.WAITING_FOR_TIMER for call in snapshot.tool_calls)
        else None,
        description="a durable wait",
    )
    record = waiting.tool_calls[0]
    assert record.tool_name == "durable_wait"
    assert record.wait_until is not None
    await _wait_for_step_method(harness, session_id, "AwaitTimer", WAIT_FOR_COMPLETED_EVENT)

    # Nothing runs during the wait: no Worker is alive until the Timer has fired.
    worker.kill()
    await asyncio.sleep(
        max(0.0, (datetime.fromisoformat(record.wait_until) - datetime.now(UTC)).total_seconds())
        + 0.5
    )
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert time.monotonic() - started_at >= 2
    assert snapshot.messages[-1].text == (
        'durable_wait: {"status":"completed","waited_seconds":2,' '"reason":"the build to finish"}'
    )
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "AwaitTimer",
        "ResumeTurn",
        "RunModel",
    ]
    timer_events = [
        json.loads(event.payload)
        for event in await harness.agent_events(session_id)
        if event.type == AgentEventType.TIMER_STARTED
    ]
    assert timer_events == [
        {
            "reason": "the build to finish",
            "tool_call_id": record.tool_call_id,
            "wait_until": record.wait_until,
        }
    ]


async def test_a_long_wait_ends_by_skip_timer_and_an_invalid_one_fails_at_once(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "wait 86400 tomorrow")
    await _wait_for_step_method(harness, session_id, "AwaitTimer", WAIT_FOR_COMPLETED_EVENT)
    await harness.stack.client.skip_timer(
        session_id, StepExecutionId("AwaitTimer", 1), TimerId(condition_index=0)
    )
    skipped = await harness.wait_for_turns(session_id, 1)
    assert skipped.messages[-1].text == (
        'durable_wait: {"status":"completed","waited_seconds":86400,"reason":"tomorrow"}'
    )

    assert await harness.sessions.send_user_message(session_id, "wait 0 never")
    invalid = await harness.wait_for_turns(session_id, 2)
    assert invalid.messages[-1].text == (
        "durable_wait: The tool call failed: invalid durable_wait arguments: "
        "duration_seconds must be between 1 and 31536000"
    )
    assert (await harness.executed_step_types(session_id)).count("AwaitTimer") == 1


async def test_a_failed_turn_stores_every_tool_result_before_its_note(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "lookup-then-fail order-9")
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "scripted failure after the tool ran" in (failed.state.last_turn_error or "")
    assert [(message.role, message.tool_name) for message in failed.messages] == [
        (MessageRole.USER, None),
        (MessageRole.ASSISTANT, "lookup_order"),
        (MessageRole.TOOL, "lookup_order"),
        (MessageRole.USER, None),
    ]
    assert failed.messages[2].tool_output == "found order-9"
    assert (failed.messages[3].text or "").startswith(TURN_FAILURE_NOTE_HEADER)

    assert await harness.sessions.send_user_message(session_id, "recall")
    recalled = await harness.wait_for_turns(session_id, 2)
    assert (recalled.messages[-1].text or "").startswith("history: lookup-then-fail order-9 | ")
    assert harness.invocations.count("lookup_order") == 1


async def test_worker_killed_during_run_model_repeats_only_that_model_call(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "lookup-then-stall model-gate order-3")
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "lookup_order: found order-3"
    assert harness.invocations.count("model:turn-1:1") == 1
    assert harness.invocations.count("lookup_order") == 1
    assert harness.invocations.count("model:turn-1:2") == 2
    # Each item is stored once, in contiguous sequences.
    assert [(message.sequence, message.role) for message in snapshot.messages] == [
        (1, MessageRole.USER),
        (2, MessageRole.ASSISTANT),
        (3, MessageRole.TOOL),
        (4, MessageRole.ASSISTANT),
    ]
    # The killed call had no output yet, so Dex redelivered it; it completed once.
    history = await harness.stack.read_step_history(session_id)
    model_steps = [
        entry.step_execution_id
        for entry in history
        if entry.step_type == "RunModel" and entry.event_type == EXECUTE_COMPLETED_EVENT
    ]
    assert len(model_steps) == len(set(model_steps)) == 2


async def test_worker_killed_during_a_single_attempt_write_records_outcome_unknown(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "slow-write write-gate ledger entry")
    gate = harness.gate("write-gate")
    await gate.wait_until_reached()

    worker.kill()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"slow_write: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1
    assert "RecordToolOutcomeUnknown" in await harness.executed_step_types(session_id)


async def test_worker_killed_while_a_question_waits_and_the_answer_still_resumes(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "ask Ship it today?")
    question = await _wait_for_pending_interrupt(harness, session_id)
    await _wait_for_step_method(
        harness, session_id, "AwaitQuestionAnswer", WAIT_FOR_COMPLETED_EVENT
    )

    worker.kill()
    await harness.stack.start_worker()
    values = await harness.stack.client.get_attributes(session_id, PENDING_INTERRUPTS)
    assert values[PENDING_INTERRUPTS] == [question]
    assert await harness.sessions.answer_question(session_id, question.interrupt_id, "yes, ship")
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == 'ask_user: {"status":"answered","answer":"yes, ship"}'
    assert harness.invocations.count("model:turn-1:1") == 1


async def test_worker_killed_during_the_rerun_after_a_tool_question_records_outcome_unknown(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "transfer 40 transfer-gate")
    question = await _wait_for_pending_interrupt(harness, session_id)
    assert await harness.sessions.answer_question(session_id, question.interrupt_id, "checking")
    gate = harness.gate("transfer-gate")
    await gate.wait_until_reached()

    worker.kill()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == f"transfer_funds: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.names().count("transfer_funds:run") == 1
    assert gate.reached_count() == 1


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


async def _wait_for_step_method(
    harness: ScriptedAgentHarness,
    session_id: str,
    step_type: str,
    event_type: str,
) -> None:
    async def probe() -> AgentSnapshot | None:
        history = await harness.stack.read_step_history(session_id)
        if any(
            entry.step_type == step_type and entry.event_type == event_type for entry in history
        ):
            return await harness.sessions.get_agent_snapshot(session_id)
        return None

    await wait_until(f"{step_type} {event_type}", probe)


async def test_ask_user_treats_null_optional_arguments_as_absent(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'ask-raw {"question": "Proceed?", "choices": null, "allows_free_text": null}'
    )
    question = await _wait_for_pending_interrupt(harness, session_id)
    assert (question.question, question.choices, question.allows_free_text) == (
        "Proceed?",
        [],
        True,
    )
    assert await harness.sessions.answer_question(session_id, question.interrupt_id, "go ahead")
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == 'ask_user: {"status":"answered","answer":"go ahead"}'


async def test_a_question_deadline_rejects_the_call_and_refuses_a_late_answer(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker({STEPWISE_INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE: "2"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "ask Shall we ship?")
    question = await _wait_for_pending_interrupt(harness, session_id)
    assert question.deadline is not None
    assert question.deadline.endswith("Z")

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == f"ask_user: {QUESTION_DEADLINE_PASSED}"
    assert snapshot.pending_interrupts == []
    assert not await harness.sessions.answer_question(session_id, question.interrupt_id, "yes")
    resolved = [
        json.loads(event.payload)
        for event in await harness.agent_events(session_id)
        if event.type == AgentEventType.INTERRUPT_RESOLVED
    ]
    assert [(payload["answered"], payload["rejection_message"]) for payload in resolved] == [
        (False, QUESTION_DEADLINE_PASSED)
    ]


async def test_worker_killed_while_awaiting_a_timer_still_fires_it_once(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "wait 3 the replacement Worker")
    await _wait_for_step_method(harness, session_id, "AwaitTimer", WAIT_FOR_COMPLETED_EVENT)

    worker.kill()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        'durable_wait: {"status":"completed","waited_seconds":3,'
        '"reason":"the replacement Worker"}'
    )
    assert (await harness.executed_step_types(session_id)).count("AwaitTimer") == 1
    events = await harness.agent_events(session_id)
    assert [event.type for event in events].count(AgentEventType.TIMER_STARTED) == 1
    finished = [
        json.loads(event.payload)
        for event in events
        if event.type == AgentEventType.TOOL_CALL_FINISHED
    ]
    assert [payload["tool_name"] for payload in finished] == ["durable_wait"]


@pytest.mark.parametrize("builder_mode", ["raise", "malformed"])
async def test_a_broken_unfinished_turn_builder_still_records_the_failure_with_its_note(
    stepwise_harness: ScriptedAgentHarness,
    builder_mode: str,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker({UNFINISHED_BUILDER_ENVIRONMENT_VARIABLE: builder_mode})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "lookup-then-fail order-11")
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "scripted failure after the tool ran" in (failed.state.last_turn_error or "")
    # Without the builder's tool result, the note alone records what ran.
    assert [(message.role, message.tool_name) for message in failed.messages] == [
        (MessageRole.USER, None),
        (MessageRole.ASSISTANT, "lookup_order"),
        (MessageRole.USER, None),
    ]
    assert (failed.messages[-1].text or "").startswith(TURN_FAILURE_NOTE_HEADER)
    assert "RecordTurnFailure" in await harness.executed_step_types(session_id)

    assert await harness.sessions.send_user_message(session_id, "say again")
    recovered = await harness.wait_for_turns(session_id, 2)
    assert recovered.messages[-1].text == "said: again"


async def test_the_model_call_limit_offers_no_tools_and_the_turn_ends_with_an_answer(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker({MAX_MODEL_CALLS_ENVIRONMENT_VARIABLE: "2"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "loop obey order-5")
    obeyed = await harness.wait_for_turns(session_id, 1)
    assert obeyed.messages[-1].text == "stopped after 2 lookups"
    assert harness.invocations.count("lookup_order") == 2
    assert harness.invocations.count("model:turn-1:3") == 1

    # A model that asks for a tool anyway gets a result for it, and the turn still ends.
    assert await harness.sessions.send_user_message(session_id, "loop defy order-6")
    defied = await harness.wait_for_turns(session_id, 2)
    assert defied.state is not None
    assert defied.state.last_turn_error is None
    last_result = defied.messages[-1]
    assert (last_result.role, last_result.tool_name, last_result.tool_output) == (
        MessageRole.TOOL,
        "lookup_order",
        TOOL_CALL_LIMIT_REACHED_RESULT.format(max_model_calls_per_turn=2),
    )
    assert harness.invocations.count("lookup_order") == 4
    assert harness.invocations.count("model:turn-2:3") == 1
    assert (await harness.executed_step_types(session_id)).count("RecordTurnFailure") == 0


async def test_a_tool_question_whose_interrupt_id_another_call_uses_fails_clearly(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "collide")
    approval = await _wait_for_pending_interrupt(harness, session_id)
    assert (approval.interrupt_id, approval.kind) == ("pay-input-1", InterruptKind.TOOL_APPROVAL)
    await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot
        if any(
            call.tool_call_id == "pay" and call.status is ToolCallStatus.FAILED
            for call in snapshot.tool_calls
        )
        else None,
        description="the colliding tool question to fail",
    )
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert [interrupt.interrupt_id for interrupt in waiting.pending_interrupts] == ["pay-input-1"]

    assert await harness.sessions.resolve_interrupt(session_id, "pay-input-1", approved=True)
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == (
        "transfer_funds: The tool call failed: the tool's question cannot use interrupt ID "
        "pay-input-1, which another tool call of this turn uses; "
        'publish_note: {"published": "receipt"}'
    )
    assert harness.invocations.names().count("transfer_funds:run") == 0
