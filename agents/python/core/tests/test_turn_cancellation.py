# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""cancel_turn cancels the running model call and every wait, on a real Dex Server."""

from __future__ import annotations

import asyncio
import json
import time
from collections.abc import Sequence

from dex_agents import (
    TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT,
    AgentEvent,
    AgentEventType,
    AgentInterrupt,
    AgentSessionStatus,
    InterruptKind,
    MessageRole,
    ToolCallStatus,
)
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    WAIT_FOR_COMPLETED_EVENT,
    wait_for_agent_snapshot,
    wait_until,
)
from tests.conftest import ScriptedAgentHarness
from tests.stepwise_agent import (
    CANCEL_RPC_GATE_ENVIRONMENT_VARIABLE,
    FINISH_CANCELLATION_GATE_ENVIRONMENT_VARIABLE,
)

# A cancelled model call stops at once; only the engine's round trips remain.
PROMPT_CANCELLATION_SECONDS = 15


async def test_cancel_turn_stops_a_running_model_call_at_once(
    harness: ScriptedAgentHarness,
) -> None:
    """Owner decision 2: the model call is cancelled, not waited for."""
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall model-gate")
    await harness.gate("model-gate").wait_until_reached()

    cancelled_at = time.monotonic()
    assert await harness.sessions.cancel_turn(session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert time.monotonic() - cancelled_at < PROMPT_CANCELLATION_SECONDS

    # The gate was never released: the call stopped inside it, and its answer never existed.
    assert harness.invocations.count("model cancelled at model-gate") == 1
    assert harness.invocations.count("model:turn-1:1") == 1
    assert [(message.role, message.text) for message in snapshot.messages] == [
        (MessageRole.USER, "stall model-gate")
    ]
    assert snapshot.state is not None
    assert snapshot.state.status is AgentSessionStatus.WAITING_FOR_USER_MESSAGE
    assert snapshot.state.last_turn_error is None
    events = await harness.agent_events(session_id)
    assert [event.type for event in events if event.type.startswith("turn_")] == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_CANCELLED,
    ]
    assert _payloads(events, AgentEventType.TURN_CANCELLED) == [{"message_count": 0}]
    # A cancelled RunModel completes no Execute and is never retried.
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "FinishTurnCancellation",
        "ApplyTurnControl",
    ]
    assert not await harness.sessions.cancel_turn(session_id)

    # The next turn sees the cancelled turn's user message, and the cancellation does not repeat.
    assert await harness.sessions.send_user_message(session_id, "say again")
    again = await harness.wait_for_turns(session_id, 2)
    assert again.messages[-1].text == "said: again"


async def test_cancel_turn_twice_cancels_once(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall model-gate")
    await harness.gate("model-gate").wait_until_reached()

    # The second call finds the request recorded, or the turn already ended.
    first, second = await asyncio.gather(
        harness.sessions.cancel_turn(session_id), harness.sessions.cancel_turn(session_id)
    )
    assert first or second
    await harness.wait_for_turns(session_id, 1)

    steps = await harness.executed_step_types(session_id)
    assert steps.count("FinishTurnCancellation") == 1
    assert steps.count("ApplyTurnControl") == 1
    events = await harness.agent_events(session_id)
    assert [event.type for event in events].count(AgentEventType.TURN_CANCELLED) == 1


async def test_a_model_call_that_commits_while_cancel_turn_decides_ends_the_turn_once(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """The race of owner decision 2: RunModel commits between the RPC's read and its commit."""
    harness = stepwise_harness
    await harness.stack.start_worker({CANCEL_RPC_GATE_ENVIRONMENT_VARIABLE: "rpc-gate"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, "stall-then-call model-gate order-3 | launch notes"
    )
    model_gate = harness.gate("model-gate")
    await model_gate.wait_until_reached()

    cancelling = asyncio.create_task(harness.sessions.cancel_turn(session_id))
    rpc_gate = harness.gate("rpc-gate")
    await rpc_gate.wait_until_reached()
    # cancel_turn read a running model call. Now the model call commits its tool calls.
    model_gate.release()
    approval = await _wait_for_pending_interrupt(harness, session_id)
    await _wait_for_tool_call_finished(harness, session_id, "lookup_order")
    rpc_gate.release()
    assert await cancelling

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert harness.invocations.count("cancelled at model-gate") == 0
    assert harness.invocations.count("model:turn-1:2") == 0
    assert harness.invocations.count("lookup_order") == 1
    assert harness.invocations.count("publish_note") == 0
    assert [
        (message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ] == [
        ("lookup_order", "found order-3"),
        ("publish_note", TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT),
    ]
    assert snapshot.pending_interrupts == []
    assert not await harness.sessions.resolve_interrupt(
        session_id, approval.interrupt_id, approved=True
    )
    events = await harness.agent_events(session_id)
    assert [event.type for event in events].count(AgentEventType.TURN_CANCELLED) == 1
    assert AgentEventType.TURN_COMPLETED not in [event.type for event in events]
    assert [
        payload["cancellation_reason"]
        for payload in _payloads(events, AgentEventType.INTERRUPT_RESOLVED)
    ] == ["turn_cancelled"]
    steps = await harness.executed_step_types(session_id)
    # RunModel won the race and committed; the approval wait was cancelled; ResumeTurn ended it.
    assert steps.count("RunModel") == 1
    assert "AwaitToolApproval" not in steps
    assert steps.count("FinishTurnCancellation") == 1
    assert steps[-2:] == ["ResumeTurn", "ApplyTurnControl"]


async def test_a_cancel_that_loses_to_a_final_answer_never_cancels_the_next_queued_turn(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """The next turn cannot start while cancel_turn decides, so its RunModel is never cancelled."""
    harness = stepwise_harness
    await harness.stack.start_worker({CANCEL_RPC_GATE_ENVIRONMENT_VARIABLE: "rpc-gate"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "lookup-then-stall model-gate order-1")
    model_gate = harness.gate("model-gate")
    await model_gate.wait_until_reached()
    assert await harness.sessions.send_user_message(
        session_id, "stall-then-call next-gate order-2 | note"
    )

    cancelling = asyncio.create_task(harness.sessions.cancel_turn(session_id))
    rpc_gate = harness.gate("rpc-gate")
    await rpc_gate.wait_until_reached()
    # cancel_turn read a running model call. Now it commits its final answer.
    model_gate.release()
    await harness.wait_for_turns(session_id, 1)
    next_gate = harness.gate("next-gate")
    await asyncio.sleep(3)
    # The queued turn waits for the RPC's commit before its model call starts.
    assert next_gate.reached_count() == 0
    rpc_gate.release()
    assert await cancelling

    await next_gate.wait_until_reached()
    next_gate.release()
    approval = await _wait_for_pending_interrupt(harness, session_id)
    assert harness.invocations.count("cancelled at next-gate") == 0
    snapshot = await harness.sessions.get_agent_snapshot(session_id)
    assert snapshot.state is not None and snapshot.state.active_turn_id == "turn-2"
    assert snapshot.state.status is AgentSessionStatus.RUNNING_TOOLS
    events = await harness.agent_events(session_id)
    assert AgentEventType.TURN_CANCELLED not in [event.type for event in events]
    assert await harness.sessions.resolve_interrupt(
        session_id, approval.interrupt_id, approved=False
    )
    finished = await harness.wait_for_turns(session_id, 2)
    assert finished.state is not None and finished.state.last_turn_error is None
    steps = await harness.executed_step_types(session_id)
    assert steps.count("FinishTurnCancellation") == 1
    assert "ApplyTurnControl" not in steps


async def test_cancel_turn_ends_a_question_and_a_durable_wait_at_once(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "wait 3600 the nightly build")
    await _wait_for_step_method(harness, session_id, "AwaitTimer", WAIT_FOR_COMPLETED_EVENT)
    await _wait_for_step_method(harness, session_id, "ResumeTurn", WAIT_FOR_COMPLETED_EVENT, 0)
    assert await harness.sessions.cancel_turn(session_id)
    waited = await harness.wait_for_turns(session_id, 1)

    assert await harness.sessions.send_user_message(session_id, "ask Which build? | a | b")
    question = await _wait_for_pending_interrupt(harness, session_id)
    assert question.kind is InterruptKind.QUESTION
    assert await harness.sessions.cancel_turn(session_id, "turn-2")
    asked = await harness.wait_for_turns(session_id, 2)
    assert not await harness.sessions.answer_question(session_id, question.interrupt_id, "a")

    assert asked.messages[: len(waited.messages)] == waited.messages
    assert [
        (message.turn_id, message.tool_name, message.tool_output)
        for message in asked.messages
        if message.role is MessageRole.TOOL
    ] == [
        ("turn-1", "durable_wait", TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT),
        ("turn-2", "ask_user", TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT),
    ]
    events = await harness.agent_events(session_id)
    assert [
        (payload["kind"], payload["cancellation_reason"])
        for payload in _payloads(events, AgentEventType.INTERRUPT_RESOLVED)
    ] == [("question", "turn_cancelled")]
    assert [event.type for event in events].count(AgentEventType.TURN_CANCELLED) == 2
    # Neither wait completed its Execute: cancel_turn cancelled both Steps.
    steps = await harness.executed_step_types(session_id)
    assert "AwaitTimer" not in steps and "AwaitQuestionAnswer" not in steps
    assert steps.count("FinishTurnCancellation") == 2


async def test_cancel_turn_lets_a_started_tool_finish_first(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "slow-write write-gate the memo")
    gate = harness.gate("write-gate")
    await gate.wait_until_reached()

    assert await harness.sessions.cancel_turn(session_id)
    await _wait_for_step_completions(harness, session_id, "FinishTurnCancellation", 1)
    running = await harness.sessions.get_agent_snapshot(session_id)
    assert running.state is not None and running.state.active_turn_id == "turn-1"
    assert [call.status for call in running.tool_calls] == [ToolCallStatus.APPROVED]
    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert harness.invocations.count("slow_write") == 1
    assert harness.invocations.count("cancelled at write-gate") == 0
    assert [
        message.tool_output for message in snapshot.messages if message.role is MessageRole.TOOL
    ] == ["wrote the memo"]
    # The model is not called after the tool: the cancelled turn ends with its result.
    assert snapshot.messages[-1].role is MessageRole.TOOL
    events = await harness.agent_events(session_id)
    assert [_payload["status"] for _payload in _payloads(events, "tool_call_finished")] == [
        "succeeded"
    ]
    steps = await harness.executed_step_types(session_id)
    assert steps[-2:] == ["ResumeTurn", "ApplyTurnControl"]
    assert steps.count("RunModel") == 1


async def test_a_worker_killed_in_finish_turn_cancellation_ends_the_turn_once(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    environment = {FINISH_CANCELLATION_GATE_ENVIRONMENT_VARIABLE: "finish-gate"}
    worker = await harness.stack.start_worker(environment)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "publish launch notes")
    await _wait_for_pending_interrupt(harness, session_id)
    assert await harness.sessions.cancel_turn(session_id)
    finish_gate = harness.gate("finish-gate")
    await finish_gate.wait_until_reached()

    worker.kill()
    finish_gate.release()
    await harness.stack.start_worker(environment)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert finish_gate.reached_count() == 2
    assert [
        message.tool_output for message in snapshot.messages if message.role is MessageRole.TOOL
    ] == [TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT]
    events = await harness.agent_events(session_id)
    assert _payloads(events, "tool_call_finished") == [
        {
            "status": "rejected",
            "tool_call_id": snapshot.messages[1].tool_call_id,
            "tool_name": "publish_note",
        }
    ]
    assert [event.type for event in events].count(AgentEventType.TURN_CANCELLED) == 1
    history = await harness.stack.read_step_history(session_id)
    finished = [
        entry
        for entry in history
        if entry.step_type == "FinishTurnCancellation"
        and entry.event_type == EXECUTE_COMPLETED_EVENT
    ]
    # Dex redelivers the killed attempt, so the decision commits once.
    assert len(finished) == 1
    steps = await harness.executed_step_types(session_id)
    assert steps.count("ApplyTurnControl") == 1


async def test_a_steer_never_cancels_a_running_model_call(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """Steering is unchanged: the model call finishes, then the turn ends."""
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall-then-call model-gate order-1 | x")
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()
    assert await harness.sessions.steer_user_message(session_id, text="say steered")
    await asyncio.sleep(1)
    gate.release()
    await harness.wait_for_turns(session_id, 2)
    assert harness.invocations.count("cancelled at model-gate") == 0
    assert "FinishTurnCancellation" not in await harness.executed_step_types(session_id)


def _payloads(events: Sequence[AgentEvent], event_type: str) -> list[dict[str, object]]:
    return [json.loads(event.payload) for event in events if event.type == event_type]


async def _wait_for_pending_interrupt(
    harness: ScriptedAgentHarness, session_id: str
) -> AgentInterrupt:
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="a pending interrupt",
    )


async def _wait_for_tool_call_finished(
    harness: ScriptedAgentHarness, session_id: str, tool_name: str
) -> None:
    async def probe() -> bool | None:
        events = await harness.agent_events(session_id)
        return (
            any(
                payload["tool_name"] == tool_name
                for payload in _payloads(events, "tool_call_finished")
            )
            or None
        )

    await wait_until(f"{tool_name} to finish", probe)


async def _wait_for_step_method(
    harness: ScriptedAgentHarness,
    session_id: str,
    step_type: str,
    event_type: str,
    count: int = 1,
) -> None:
    async def probe() -> bool | None:
        history = await harness.stack.read_step_history(session_id)
        completed = [
            entry
            for entry in history
            if entry.step_type == step_type and entry.event_type == event_type
        ]
        return len(completed) >= count or None

    await wait_until(f"{step_type} {event_type}", probe)


async def _wait_for_step_completions(
    harness: ScriptedAgentHarness, session_id: str, step_type: str, count: int
) -> None:
    async def probe() -> bool | None:
        return (await harness.executed_step_types(session_id)).count(step_type) >= count or None

    await wait_until(f"{count} {step_type} Step(s)", probe)
