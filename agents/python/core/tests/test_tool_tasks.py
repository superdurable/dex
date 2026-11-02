# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Tool tasks: a tool starts remote work, and ``PollToolTask`` polls it on a Timer."""

from __future__ import annotations

import asyncio
import json
import time
from datetime import timedelta

import pytest

from dex_agents import (
    MISSING_TOOL_TASK_POLLER,
    OUTCOME_UNKNOWN_TOOL_RESULT,
    AgentEvent,
    AgentEventType,
    AgentInterrupt,
    AgentToolCall,
    InterruptKind,
    ToolCallStatus,
    ToolEffect,
)
from dex_agents.testing import (
    WAIT_FOR_COMPLETED_EVENT,
    wait_for_agent_snapshot,
    wait_until,
)
from tests.conftest import ScriptedAgentHarness
from tests.task_agent import (
    INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE,
    TASK_QUESTION,
    WITHOUT_POLLER_ENVIRONMENT_VARIABLE,
)

CALL_0 = "turn-1-call-0"


async def test_a_started_task_is_polled_on_a_timer_until_it_finishes(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call report {"topic": "q1", "polls": 3}')
    waiting = await _wait_for_tool_call(harness, session_id, ToolCallStatus.WAITING_FOR_TASK)
    assert waiting.effect is ToolEffect.READ
    assert (waiting.task_id or "").startswith("task-")
    assert waiting.task_poll_interval_seconds == 1

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "report: report on q1"
    assert harness.task_invocations.names() == [
        f"start {CALL_0} attempt 1",
        f"create {CALL_0}",
        *[f"poll {CALL_0} attempt 1 pending none"] * 3,
    ]
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "PollToolTask",
        "PollToolTask",
        "PollToolTask",
        "ResumeTurn",
        "RunModel",
    ]
    events = await harness.agent_events(session_id)
    started = [
        json.loads(event.payload)
        for event in events
        if event.type == AgentEventType.TOOL_TASK_STARTED
    ]
    assert started == [{"tool_call_id": CALL_0, "tool_name": "report", "task_id": waiting.task_id}]
    assert [event.type for event in events].count(AgentEventType.TOOL_CALL_STARTED) == 1
    assert _finished_statuses(events) == ["succeeded"]


async def test_worker_killed_while_a_task_waits_for_its_next_poll_resumes_polling(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call report {"topic": "q2", "interval": 3, "poll_gate": "report-gate"}'
    )
    gate = harness.gate("report-gate")
    await gate.wait_until_reached()
    # The second PollToolTask waits on its Timer when the Worker dies.
    await _wait_for_step_methods(harness, session_id, "PollToolTask", WAIT_FOR_COMPLETED_EVENT, 2)

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "report: report on q2"
    assert harness.task_invocations.count(f"create {CALL_0}") == 1
    assert harness.task_invocations.count(f"start {CALL_0} attempt 1") == 1
    assert _finished_statuses(await harness.agent_events(session_id)) == ["succeeded"]


async def test_worker_killed_during_a_poll_polls_again_and_never_starts_a_second_task(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call report {"topic": "q3", "polls": 1, "block_gate": "poll-gate"}'
    )
    gate = harness.gate("poll-gate")
    await gate.wait_until_reached()

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "report: report on q3"
    assert gate.reached_count() == 2
    assert harness.task_invocations.count(f"create {CALL_0}") == 1
    assert (
        sum(name.startswith(f"start {CALL_0} ") for name in harness.task_invocations.names()) == 1
    )
    assert _finished_statuses(await harness.agent_events(session_id)) == ["succeeded"]
    assert (await harness.executed_step_types(session_id)).count("ResumeTurn") == 1


async def test_a_retried_tool_attempt_resumes_the_task_its_first_attempt_started(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call report {"topic": "q4", "start_gate": "start-gate"}'
    )
    gate = harness.gate("start-gate")
    # The first attempt created the task and saved its ID as the heartbeat checkpoint.
    await gate.wait_until_reached()

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "report: report on q4"
    assert harness.task_invocations.names()[:3] == [
        f"start {CALL_0} attempt 1",
        f"create {CALL_0}",
        f"start {CALL_0} attempt 2",
    ]
    assert harness.task_invocations.count(f"create {CALL_0}") == 1
    history = await harness.stack.read_step_history(session_id)
    assert [entry.final_attempt for entry in history if entry.step_type == "CallTool"] == [2]


async def test_each_poll_reads_the_continuation_the_last_pending_poll_returned(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call report {"topic": "q31", "polls": 4, "continuations": true}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "report: report on q31"
    # Odd polls return a continuation and even polls none, which clears it.
    assert [
        name for name in harness.task_invocations.names() if name.startswith("continuation ")
    ] == [
        f"continuation {CALL_0} none",
        f"continuation {CALL_0} pending-1",
        f"continuation {CALL_0} none",
        f"continuation {CALL_0} pending-3",
    ]


async def test_a_task_that_asks_for_input_raises_a_question_and_gets_the_answer(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call report {"topic": "q5", "ask": true, "polls": 3}'
    )
    question = await _wait_for_pending_interrupt(harness, session_id)
    assert (question.interrupt_id, question.kind, question.tool_name) == (
        f"{CALL_0}-input-1",
        InterruptKind.QUESTION,
        "report",
    )
    assert (question.question, question.choices, question.allows_free_text) == (
        TASK_QUESTION,
        ["pdf", "html"],
        False,
    )
    waiting = await _wait_for_tool_call(harness, session_id, ToolCallStatus.AWAITING_ANSWER)
    assert waiting.task_id is not None
    assert waiting.input_question == TASK_QUESTION

    assert not await harness.sessions.answer_question(session_id, question.interrupt_id, "docx")
    assert await harness.sessions.answer_question(session_id, question.interrupt_id, "pdf")
    assert not await harness.sessions.answer_question(session_id, question.interrupt_id, "html")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "report: report on q5 as pdf"
    # Only the first poll after the answer gets it as pending_answer, so it goes out once.
    assert [name for name in harness.task_invocations.names() if name.startswith("poll ")] == [
        f"poll {CALL_0} attempt 1 pending none",
        f"poll {CALL_0} attempt 1 pending answered",
        f"poll {CALL_0} attempt 1 pending none",
    ]
    assert harness.task_invocations.count(f"deliver {CALL_0} pdf") == 1
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "PollToolTask",
        "AwaitQuestionAnswer",
        "AwaitQuestionAnswer",
        "PollToolTask",
        "PollToolTask",
        "ResumeTurn",
        "RunModel",
    ]


async def test_declining_a_started_write_task_question_does_not_claim_the_tool_never_ran(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    # publish_report is a non_idempotent_write; its task is created by the tool Step and then asks.
    await harness.sessions.start_session(
        session_id, 'call publish_report {"topic": "q9", "ask": true}'
    )
    question = await _wait_for_pending_interrupt(harness, session_id)
    assert harness.task_invocations.count(f"create {CALL_0}") == 1
    assert await harness.sessions.resolve_interrupt(
        session_id, question.interrupt_id, approved=False
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    # The task started, so the call never reads as "the tool never ran"; this task cannot
    # take a decline, so the write is recorded by its effect, after a best-effort cancel.
    assert _finished_statuses(await harness.agent_events(session_id)) == [
        str(ToolCallStatus.OUTCOME_UNKNOWN)
    ]
    assert snapshot.messages[-1].text == f"publish_report: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.task_invocations.count(f"cancel {CALL_0}") == 3
    assert "RecordToolOutcomeUnknown" in await harness.executed_step_types(session_id)


@pytest.mark.parametrize("control", ["steer", "cancel_turn"])
async def test_steering_or_cancelling_waits_for_a_task_and_keeps_its_question(
    task_harness: ScriptedAgentHarness, control: str
) -> None:
    harness = task_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call report {"topic": "q30", "ask": true, "polls": 3}'
    )
    question = await _wait_for_pending_interrupt(harness, session_id)

    if control == "steer":
        assert await harness.sessions.steer_user_message(session_id, text="say steered")
    else:
        assert await harness.sessions.cancel_turn(session_id, "turn-1")
    # ResumeTurn takes the control wake and waits again; the task's question stays.
    await wait_until(
        "ResumeTurn to take the control wake", lambda: _resume_turn_ran(harness, session_id)
    )
    snapshot = await harness.sessions.get_agent_snapshot(session_id)
    assert [interrupt.interrupt_id for interrupt in snapshot.pending_interrupts] == [
        question.interrupt_id
    ]
    assert snapshot.state is not None and snapshot.state.active_turn_id == "turn-1"
    (waiting,) = snapshot.tool_calls
    assert (waiting.status, waiting.cancellation_reason) == (ToolCallStatus.AWAITING_ANSWER, None)
    assert waiting.task_id is not None

    assert await harness.sessions.answer_question(session_id, question.interrupt_id, "pdf")
    snapshot = await harness.wait_for_turns(session_id, 2 if control == "steer" else 1)

    assert harness.task_invocations.count(f"deliver {CALL_0} pdf") == 1
    assert harness.task_invocations.count(f"create {CALL_0}") == 1
    events = await harness.agent_events(session_id)
    assert _finished_statuses(events) == ["succeeded"]
    finished = next(
        index
        for index, event in enumerate(events)
        if event.type == AgentEventType.TOOL_CALL_FINISHED
    )
    ended_type = (
        AgentEventType.TURN_STEERED if control == "steer" else AgentEventType.TURN_CANCELLED
    )
    ended = [index for index, event in enumerate(events) if event.type == ended_type]
    assert len(ended) == 1 and ended[0] > finished
    assert not any(
        event.type == AgentEventType.INTERRUPT_RESOLVED
        and json.loads(event.payload).get("cancellation_reason") is not None
        for event in events
    )
    # The turn never ran its model again: the steer or cancellation ended it after the result.
    assert not any(message.text == "report: report on q30 as pdf" for message in snapshot.messages)
    if control == "steer":
        assert snapshot.messages[-1].text == "steered"


async def _resume_turn_ran(harness: ScriptedAgentHarness, session_id: str) -> bool | None:
    return "ResumeTurn" in await harness.executed_step_types(session_id) or None


async def test_a_declined_task_question_goes_to_the_task_and_its_result_ends_the_call(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish_report {"topic": "q11", "ask": true, "takes_decline": true}'
    )
    question = await _wait_for_pending_interrupt(harness, session_id)
    assert await harness.sessions.resolve_interrupt(
        session_id, question.interrupt_id, approved=False, rejection_message="no format"
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        "publish_report: report on q11 without a format (declined)"
    )
    assert harness.task_invocations.count(f"deliver {CALL_0} declined") == 1
    assert _finished_statuses(await harness.agent_events(session_id)) == ["succeeded"]


async def test_an_expired_task_question_goes_to_the_task_as_a_passed_deadline(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    await harness.stack.start_worker({INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE: "2"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call report {"topic": "q12", "ask": true, "takes_decline": true}'
    )
    question = await _wait_for_pending_interrupt(harness, session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "report: report on q12 without a format (deadline_passed)"
    assert not await harness.sessions.answer_question(session_id, question.interrupt_id, "pdf")
    assert harness.task_invocations.count(f"deliver {CALL_0} deadline_passed") == 1


async def test_an_answer_reaches_the_task_without_waiting_a_full_poll_interval(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call report {"topic": "q10", "ask": true, "interval": 12}'
    )
    question = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="the task's question",
        timeout=timedelta(seconds=60),
    )
    answered_at = time.monotonic()
    assert await harness.sessions.answer_question(session_id, question.interrupt_id, "pdf")

    async def is_delivered() -> bool:
        return harness.task_invocations.count(f"deliver {CALL_0} pdf") == 1

    await wait_until("the answer delivered", is_delivered, timeout=timedelta(seconds=60))
    delay = time.monotonic() - answered_at
    assert delay < 5, f"the answer waited {delay:.1f}s, the 12s poll interval, to reach the task"


async def test_racing_answers_to_two_parallel_task_questions_each_resolve_once(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call report {"topic": "a", "ask": true} && call report {"topic": "b", "ask": true}',
    )
    await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot if len(snapshot.pending_interrupts) == 2 else None,
        description="two pending task questions",
    )
    first, second = f"{CALL_0}-input-1", "turn-1-call-1-input-1"

    answered = await asyncio.gather(
        harness.sessions.answer_question(session_id, first, "pdf"),
        harness.sessions.answer_question(session_id, second, "html"),
        harness.sessions.answer_question(session_id, first, "html"),
        harness.sessions.answer_question(session_id, second, "pdf"),
    )
    assert answered.count(True) == 2
    assert answered[0] != answered[2]
    assert answered[1] != answered[3]
    first_answer = "pdf" if answered[0] else "html"
    second_answer = "html" if answered[1] else "pdf"
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        f"report: report on a as {first_answer}; report: report on b as {second_answer}"
    )
    assert harness.task_invocations.count(f"deliver {CALL_0} {first_answer}") == 1
    assert harness.task_invocations.count(f"deliver turn-1-call-1 {second_answer}") == 1
    assert _finished_statuses(await harness.agent_events(session_id)) == ["succeeded"] * 2
    assert snapshot.pending_interrupts == []


async def test_a_poll_that_keeps_failing_records_the_outcome_by_effect(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call report {"topic": "a", "fail_polls": true}'
        ' && call publish_report {"topic": "b", "fail_polls": true}'
        ' && call report {"topic": "c", "fail_task": true}',
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    read_result, write_result, failed_task_result = (snapshot.messages[-1].text or "").split("; ")
    assert read_result.startswith("report: The tool call failed: ")
    assert "the task server is unreachable" in read_result
    assert write_result == f"publish_report: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert failed_task_result == "report: The tool call failed: the report failed"
    polls = [name for name in harness.task_invocations.names() if name.startswith("poll ")]
    for tool_call_id in (CALL_0, "turn-1-call-1"):
        assert [name for name in polls if name.startswith(f"poll {tool_call_id} ")] == [
            f"poll {tool_call_id} attempt {attempt} pending none" for attempt in (1, 2, 3)
        ]
    assert harness.task_invocations.count("create turn-1-call-1") == 1
    steps = await harness.executed_step_types(session_id)
    assert "RecordToolFailure" in steps
    assert "RecordToolOutcomeUnknown" in steps


async def test_a_task_started_for_a_runner_that_cannot_poll_is_recorded_by_effect(
    task_harness: ScriptedAgentHarness,
) -> None:
    harness = task_harness
    await harness.stack.start_worker({WITHOUT_POLLER_ENVIRONMENT_VARIABLE: "1"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call report {"topic": "a"} && call publish_report {"topic": "b"}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        f"report: The tool call failed: {MISSING_TOOL_TASK_POLLER}; "
        f"publish_report: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    )
    assert "PollToolTask" not in await harness.executed_step_types(session_id)


def _finished_statuses(events: list[AgentEvent]) -> list[str]:
    return [
        json.loads(event.payload)["status"]
        for event in events
        if event.type == AgentEventType.TOOL_CALL_FINISHED
    ]


async def _wait_for_tool_call(
    harness: ScriptedAgentHarness, session_id: str, status: ToolCallStatus
) -> AgentToolCall:
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(
            (tool_call for tool_call in snapshot.tool_calls if tool_call.status is status), None
        ),
        description=f"a tool call {status}",
    )


async def _wait_for_pending_interrupt(
    harness: ScriptedAgentHarness, session_id: str
) -> AgentInterrupt:
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="a pending interrupt",
    )


async def _wait_for_step_methods(
    harness: ScriptedAgentHarness,
    session_id: str,
    step_type: str,
    event_type: str,
    count: int,
) -> None:
    async def has_completed() -> bool:
        history = await harness.stack.read_step_history(session_id)
        completed = [
            entry
            for entry in history
            if entry.step_type == step_type and entry.event_type == event_type
        ]
        return len(completed) >= count

    await wait_until(f"{count} {step_type} {event_type}", has_completed)
