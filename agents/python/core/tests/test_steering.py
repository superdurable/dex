# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Steering, queue edits, and turn cancellation, on a real Dex Server."""

from __future__ import annotations

import asyncio
import base64
import json
from collections.abc import Awaitable, Sequence

from dex import ChannelMessageNotFoundError

from dex_agents import (
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT,
    TURN_CANCELLED_NOTE_HEADER,
    TURN_STEERED_NOTE_HEADER,
    AgentEventType,
    AgentInterrupt,
    AgentMessage,
    AgentSnapshot,
    InteractionMode,
    InterruptKind,
    MessageRole,
    PlanStatus,
    SteerUserMessageRequest,
    ToolCallStatus,
)
from dex_agents.testing import (
    EXECUTE_COMPLETED_EVENT,
    WAIT_FOR_COMPLETED_EVENT,
    resolve_dexcli_path,
    wait_for_agent_snapshot,
    wait_until,
)
from dex_agents.ui_stream import read_agent_event_stream_snapshot, stream_agent_event_frames
from tests.conftest import ScriptedAgentHarness
from tests.plan_agent import read_model_requests
from tests.stepwise_agent import UNFINISHED_BUILDER_ENVIRONMENT_VARIABLE
from tests.test_released_compatibility import released_python  # noqa: F401  (fixture)
from tests.test_ui_stream import POLL_TIMEOUT, _read_frames_until

RACE_ROUNDS = 50
RACE_PARALLEL_SESSIONS = 10
# Staggered calls land before, during, and after ResumeTurn and the tool Step see the steer.
RACE_OFFSETS = 5
RACE_OFFSET_SECONDS = 0.03


async def test_a_steer_during_a_model_call_lets_it_finish_and_cancels_the_calls_it_asked_for(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """A5-1: steering never cancels an in-flight model call."""
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, "stall-then-call model-gate order-1 | launch notes"
    )
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()

    assert await harness.sessions.steer_user_message(session_id, text="say steered")
    steering = await harness.sessions.get_agent_snapshot(session_id)
    assert [pending.message.text for pending in steering.steered_messages] == ["say steered"]
    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert harness.invocations.count("model:turn-1:1") == 1
    assert harness.invocations.count("model:turn-1:2") == 0
    assert harness.invocations.count("lookup_order") == 0
    assert harness.invocations.count("publish_note") == 0
    # The turn's exact transcript: both calls, their cancellation results, then the steered message.
    assert _message_shape(snapshot.messages) == [
        ("turn-1", MessageRole.USER, None),
        ("turn-1", MessageRole.ASSISTANT, "lookup_order"),
        ("turn-1", MessageRole.ASSISTANT, "publish_note"),
        ("turn-1", MessageRole.TOOL, "lookup_order"),
        ("turn-1", MessageRole.TOOL, "publish_note"),
        ("turn-2", MessageRole.USER, None),
        *[("turn-2", message.role, None) for message in snapshot.messages[6:]],
    ]
    assert [message.tool_output for message in snapshot.messages[3:5]] == [
        TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
        TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    ]
    assert snapshot.messages[5].text == "say steered"
    assert snapshot.messages[-1].text == "said: steered"
    assert snapshot.steered_messages == []
    assert snapshot.state is not None and snapshot.state.last_turn_error is None

    events = await harness.agent_events(session_id)
    assert _payloads(events, AgentEventType.TURN_STEERED) == [
        {"message_count": 4, "steered_message_count": 1}
    ]
    assert [payload["status"] for payload in _payloads(events, "tool_call_finished")] == [
        "rejected",
        "rejected",
    ]
    assert not _payloads(events, AgentEventType.TOOL_CALL_STARTED)
    assert _payloads(events, AgentEventType.INTERRUPT_RESOLVED) == [
        {
            "answered": False,
            "approved": False,
            "cancellation_reason": "steered",
            "interrupt_id": snapshot.messages[2].tool_call_id,
            "kind": "tool_approval",
            "rejection_message": TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
        }
    ]
    executed = await harness.executed_step_types(session_id)
    assert executed[:2] == ["StartAgentSession", "RunModel"]
    assert executed[-2:] == ["ApplyTurnControl", "RunModel"]
    assert executed.count("RunModel") == 2


async def test_a_steer_during_a_single_attempt_tool_lets_it_run_once_and_applies_after_it(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """A5-2: steering never cancels an in-flight tool call."""
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "slow-write write-gate ledger entry")
    gate = harness.gate("write-gate")
    await gate.wait_until_reached()

    assert await harness.sessions.steer_user_message(session_id, text="say after the write")
    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1
    assert harness.invocations.count("model:turn-1:2") == 0
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [(message.tool_name, message.tool_output) for message in tool_results] == [
        ("slow_write", "wrote ledger entry")
    ]
    assert snapshot.messages[-1].text == "said: after the write"
    executed = await harness.executed_step_types(session_id)
    assert executed.count("CallSingleAttemptTool") == 1
    assert executed[-2:] == ["ApplyTurnControl", "RunModel"]


async def test_a_steer_cancels_a_waiting_approval_while_a_parallel_read_finishes(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """A5-3: ResumeTurn cancels the approval, the read runs to the end, then the turn ends."""
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "parallel read-gate order-2 | launch notes")
    gate = harness.gate("read-gate")
    await gate.wait_until_reached()
    approval = await _wait_for_pending_interrupt(harness, session_id)
    assert approval.kind is InterruptKind.TOOL_APPROVAL

    assert await harness.sessions.steer_user_message(session_id, text="say redirected")
    cancelled = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot
        if not snapshot.pending_interrupts
        and any(call.status is ToolCallStatus.REJECTED for call in snapshot.tool_calls)
        else None,
        description="the cancelled approval",
    )
    publish = next(call for call in cancelled.tool_calls if call.tool_name == "publish_note")
    assert publish.cancellation_reason == "steered"
    assert publish.rejection_message == TOOL_CALL_CANCELLED_BY_STEERING_RESULT
    lookup = next(call for call in cancelled.tool_calls if call.tool_name == "lookup_order")
    assert lookup.status is ToolCallStatus.APPROVED
    assert not await harness.sessions.resolve_interrupt(
        session_id, approval.interrupt_id, approved=True
    )

    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert harness.invocations.count("lookup_order") == 1
    assert harness.invocations.count("publish_note") == 0
    assert [
        (message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ] == [
        ("lookup_order", "found order-2"),
        ("publish_note", TOOL_CALL_CANCELLED_BY_STEERING_RESULT),
    ]
    assert snapshot.messages[-1].text == "said: redirected"
    executed = await harness.executed_step_types(session_id)
    resume_index = len(executed) - 1 - executed[::-1].index("ResumeTurn")
    assert executed[resume_index + 1 :] == ["ApplyTurnControl", "RunModel"]
    assert "CallSingleAttemptTool" not in executed


async def test_racing_approvals_and_steers_give_every_call_one_result_and_never_stall(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """A5-4: the PendingInterrupts lock and the flags pick exactly one outcome per call."""
    harness = stepwise_harness
    await harness.stack.start_worker()
    limit = asyncio.Semaphore(RACE_PARALLEL_SESSIONS)

    async def race(round_index: int) -> tuple[bool, str]:
        async with limit:
            session_id = harness.new_session_id()
            await harness.sessions.start_session(session_id, f"publish race-{round_index}")
            approval = await _wait_for_pending_interrupt(harness, session_id)
            # A negative offset steers after the approval; a positive one approves after the steer.
            offset = (round_index % RACE_OFFSETS - RACE_OFFSETS // 2) * RACE_OFFSET_SECONDS
            resolved, steered = await asyncio.gather(
                _after(
                    max(offset, 0),
                    harness.sessions.resolve_interrupt(
                        session_id, approval.interrupt_id, approved=True
                    ),
                ),
                _after(
                    max(-offset, 0),
                    harness.sessions.steer_user_message(session_id, text=f"say next-{round_index}"),
                ),
            )
            assert steered
            snapshot = await harness.wait_for_turns(session_id, 2)
            assert snapshot.messages[-1].text == f"said: next-{round_index}"
            results = [
                message.tool_output
                for message in snapshot.messages
                if message.role is MessageRole.TOOL
            ]
            assert len(results) == 1
            result = results[0] or ""
            assert result in (
                TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
                json.dumps({"published": f"race-{round_index}"}),
            )
            events = await harness.agent_events(session_id)
            finished = _payloads(events, AgentEventType.TOOL_CALL_FINISHED)
            assert len(finished) == 1
            # A tool that started has a result of its own; a cancelled one never started.
            is_published = result != TOOL_CALL_CANCELLED_BY_STEERING_RESULT
            assert bool(_payloads(events, AgentEventType.TOOL_CALL_STARTED)) == is_published
            if not resolved:
                assert not is_published
            return resolved, result

    outcomes = await asyncio.gather(*(race(index) for index in range(RACE_ROUNDS)))
    published = [
        result for _, result in outcomes if result != TOOL_CALL_CANCELLED_BY_STEERING_RESULT
    ]
    # No tool ran twice, and none ran without its result.
    assert harness.invocations.count("publish_note") == len(published)


async def test_a_worker_killed_during_apply_turn_control_ends_the_turn_once(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """A5-5: the rerun ends the turn once and consumes each steered message once."""
    harness = stepwise_harness
    worker = await harness.stack.start_worker(
        {UNFINISHED_BUILDER_ENVIRONMENT_VARIABLE: "gate:builder-gate"}
    )
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "lookup-gated read-gate order-5")
    read_gate = harness.gate("read-gate")
    await read_gate.wait_until_reached()
    assert await harness.sessions.steer_user_message(session_id, text="say one")
    assert await harness.sessions.steer_user_message(session_id, text="say two")
    read_gate.release()
    builder_gate = harness.gate("builder-gate")
    await builder_gate.wait_until_reached()

    worker.kill()
    builder_gate.release()
    await harness.stack.start_worker({UNFINISHED_BUILDER_ENVIRONMENT_VARIABLE: "gate:builder-gate"})
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert builder_gate.reached_count() == 2
    assert [message.sequence for message in snapshot.messages] == list(
        range(1, len(snapshot.messages) + 1)
    )
    user_texts = [message.text for message in snapshot.messages if message.role is MessageRole.USER]
    assert user_texts == ["lookup-gated read-gate order-5", "say one", "say two"]
    assert [m.tool_output for m in snapshot.messages if m.role is MessageRole.TOOL] == [
        "found order-5"
    ]
    assert snapshot.messages[-1].text == "said: two"
    assert snapshot.steered_messages == []
    history = await harness.stack.read_step_history(session_id)
    applied = [
        entry
        for entry in history
        if entry.step_type == "ApplyTurnControl" and entry.event_type == EXECUTE_COMPLETED_EVENT
    ]
    assert len(applied) == 1
    events = await harness.agent_events(session_id)
    assert _payloads(events, AgentEventType.TURN_STEERED) == [
        {"message_count": 2, "steered_message_count": 2}
    ]


async def test_queued_messages_can_be_deleted_or_steered_until_a_turn_consumes_them(
    harness: ScriptedAgentHarness,
) -> None:
    """A5-6: queue changes are safe against consumption."""
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall busy-gate")
    gate = harness.gate("busy-gate")
    await gate.wait_until_reached()
    assert await harness.sessions.send_user_message(session_id, "say deleted")
    assert await harness.sessions.send_user_message(session_id, "say kept")
    queued = (await harness.sessions.get_agent_snapshot(session_id)).queued_messages
    assert [pending.message.text for pending in queued] == ["say deleted", "say kept"]

    assert await harness.sessions.delete_queued_message(session_id, queued[0].message_id)
    assert not await harness.sessions.delete_queued_message(session_id, queued[0].message_id)
    assert not await harness.sessions.steer_user_message(session_id, message_id="no-such-id")
    assert not await harness.sessions.steer_user_message(session_id, text="   ")
    assert not await harness.sessions.steer_user_message(session_id)
    assert not await harness.stack.client.invoke_rpc(
        harness.flow.steer_user_message,
        session_id,
        SteerUserMessageRequest(message_id=queued[1].message_id, text="say both"),
    )
    remaining = await harness.sessions.get_agent_snapshot(session_id)
    assert [pending.message.text for pending in remaining.queued_messages] == ["say kept"]
    assert remaining.queued_message_count == 1
    assert remaining.steered_messages == []

    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert [message.text for message in snapshot.messages[-2:]] == ["say kept", "said: kept"]
    assert not await harness.sessions.steer_user_message(
        session_id, message_id=queued[1].message_id
    )
    assert not await harness.sessions.delete_queued_message(session_id, queued[1].message_id)
    assert snapshot.queued_messages == []
    assert harness.invocations.count("model:turn-3:1") == 0


async def test_steering_one_queued_message_twice_at_once_moves_it_once(
    harness: ScriptedAgentHarness,
) -> None:
    """A5-6: a message consumed before the commit fails the RPC, which commits nothing."""
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall busy-gate")
    gate = harness.gate("busy-gate")
    await gate.wait_until_reached()
    texts = [f"say queued-{index}" for index in range(20)]
    for text in texts:
        assert await harness.sessions.send_user_message(session_id, text)
    queued = (await harness.sessions.get_agent_snapshot(session_id)).queued_messages

    async def steer(message_id: str) -> bool | None:
        try:
            return await harness.sessions.steer_user_message(session_id, message_id=message_id)
        except ChannelMessageNotFoundError:
            return None

    for pending in queued:
        outcomes = await asyncio.gather(steer(pending.message_id), steer(pending.message_id))
        # One call moves the message; the other finds it gone, before or at its commit.
        assert sorted(outcomes, key=lambda outcome: outcome is True) in (
            [False, True],
            [None, True],
        )
    steering = await harness.sessions.get_agent_snapshot(session_id)
    assert steering.queued_messages == []
    assert [pending.message.text for pending in steering.steered_messages] == texts

    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)
    user_texts = [message.text for message in snapshot.messages if message.role is MessageRole.USER]
    assert user_texts[-len(texts) :] == texts
    events = await harness.agent_events(session_id)
    # The stalled turn answered before the steer applied, so the steered messages start turn 2.
    assert _payloads(events, AgentEventType.TURN_STEERED) == []
    assert snapshot.messages[-1].text == "said: queued-19"
    executed = await harness.executed_step_types(session_id)
    assert executed.count("ApplyTurnControl") == 1


async def test_cancel_turn_ends_an_approval_wait_and_a_running_model_call(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """A5-7: cancellation semantics."""
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "publish launch notes")
    approval = await _wait_for_pending_interrupt(harness, session_id)
    assert await harness.sessions.send_user_message(session_id, "say queued")
    assert not await harness.sessions.cancel_turn(session_id, "turn-7")

    assert await harness.sessions.cancel_turn(session_id, "turn-1")
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert harness.invocations.count("publish_note") == 0
    assert [
        (message.turn_id, message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ] == [("turn-1", "publish_note", TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT)]
    assert snapshot.messages[-1].text == "said: queued"
    assert not await harness.sessions.resolve_interrupt(
        session_id, approval.interrupt_id, approved=True
    )
    assert not await harness.sessions.cancel_turn(session_id)
    events = await harness.agent_events(session_id)
    assert _payloads(events, AgentEventType.TURN_CANCELLED) == [{"message_count": 2}]
    assert [
        payload["cancellation_reason"]
        for payload in _payloads(events, AgentEventType.INTERRUPT_RESOLVED)
    ] == ["turn_cancelled"]

    await harness.sessions.send_user_message(
        session_id, "stall-then-call model-gate order-3 | never"
    )
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()
    assert await harness.sessions.cancel_turn(session_id)
    cancelled = await harness.wait_for_turns(session_id, 3)
    gate.release()
    # The model call stopped inside the gate, so it asked for no tool calls.
    assert harness.invocations.count("model:turn-3:1") == 1
    assert harness.invocations.count("cancelled at model-gate") == 1
    assert harness.invocations.count("lookup_order") == 0
    assert [message.role for message in cancelled.messages if message.turn_id == "turn-3"] == [
        MessageRole.USER
    ]


async def test_cancel_turn_after_the_answer_committed_changes_nothing(
    harness: ScriptedAgentHarness,
) -> None:
    """A5-7: an answer that committed first wins, and an idle steer is queued."""
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall model-gate")
    harness.gate("model-gate").release()
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "stalled at model-gate"
    assert not await harness.sessions.cancel_turn(session_id, "turn-1")
    events = await harness.agent_events(session_id)
    assert [event.type for event in events if event.type.startswith("turn_")] == [
        AgentEventType.TURN_STARTED,
        AgentEventType.TURN_COMPLETED,
    ]

    # With no active turn, a steer is queued, so the idle wait of any Worker version takes it.
    assert await harness.sessions.steer_user_message(session_id, text="say idle steer")
    steered = await harness.wait_for_turns(session_id, 2)
    assert steered.messages[-1].text == "said: idle steer"
    executed = await harness.executed_step_types(session_id)
    assert executed[-2:] == ["AwaitUserMessage", "RunModel"]
    assert "ApplyTurnControl" not in executed
    assert "FinishTurnCancellation" not in executed


async def test_a_steer_to_a_session_left_idle_by_the_released_worker_starts_a_turn(
    harness: ScriptedAgentHarness,
    released_python: str,  # noqa: F811
) -> None:
    """An idle wait that a Worker without steering began takes a steer as a queued message."""
    released_worker = await harness.stack.start_worker(python_executable=released_python)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say before the upgrade")
    await harness.wait_for_turns(session_id, 1)
    await _wait_for_step_method(harness, session_id, "AwaitUserMessage", WAIT_FOR_COMPLETED_EVENT)
    released_worker.kill()
    await harness.stack.start_worker()

    assert await harness.sessions.steer_user_message(session_id, text="say steered")
    steered = await harness.wait_for_turns(session_id, 2)
    assert [(message.turn_id, message.text) for message in steered.messages] == [
        ("turn-1", "say before the upgrade"),
        ("turn-1", "said: before the upgrade"),
        ("turn-2", "say steered"),
        ("turn-2", "said: steered"),
    ]
    assert steered.steered_messages == []
    assert "ApplyTurnControl" not in await harness.executed_step_types(session_id)


async def test_a_turn_that_the_released_worker_started_refuses_steering_and_cancellation(
    harness: ScriptedAgentHarness,
    released_python: str,  # noqa: F811
) -> None:
    """Its waits may not wake on a steer, so the RPCs refuse it and the turn runs as before."""
    released_worker = await harness.stack.start_worker(python_executable=released_python)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "upgrade"}')
    approval = await _wait_for_pending_interrupt(harness, session_id)
    released_worker.kill()
    await harness.stack.start_worker()

    running = await harness.sessions.get_agent_snapshot(session_id)
    assert running.state is not None and not running.state.accepts_turn_control
    assert not await harness.sessions.steer_user_message(session_id, text="say steered")
    assert not await harness.sessions.cancel_turn(session_id)
    assert await harness.sessions.send_user_message(session_id, "say after")
    queued = (await harness.sessions.get_agent_snapshot(session_id)).queued_messages
    assert not await harness.sessions.steer_user_message(
        session_id, message_id=queued[0].message_id
    )
    assert (await harness.sessions.get_agent_snapshot(session_id)).steered_messages == []

    assert await harness.sessions.resolve_interrupt(
        session_id, approval.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert [message.text for message in snapshot.messages if message.text] == [
        'call publish {"message": "upgrade"}',
        "publish: published upgrade",
        "say after",
        "said: after",
    ]
    assert snapshot.state is not None and not snapshot.state.accepts_turn_control
    assert harness.invocations.count("publish") == 1


async def test_an_adapter_without_unfinished_turn_messages_gets_a_note(
    harness: ScriptedAgentHarness,
) -> None:
    """A5-8: the note keeps the next model from repeating the calls that ran."""
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call lookup {"query": "order-4", "gate": "read-gate"} && call publish {"message": "x"}',
    )
    gate = harness.gate("read-gate")
    await gate.wait_until_reached()
    await _wait_for_pending_interrupt(harness, session_id)
    assert await harness.sessions.steer_user_message(session_id, text="say instead")
    gate.release()
    steered = await harness.wait_for_turns(session_id, 2)

    assert _message_shape(steered.messages) == [
        ("turn-1", MessageRole.USER, None),
        ("turn-1", MessageRole.USER, None),
        ("turn-2", MessageRole.USER, None),
        ("turn-2", MessageRole.ASSISTANT, None),
    ]
    assert steered.messages[1].text == "\n".join(
        [
            TURN_STEERED_NOTE_HEADER,
            '- lookup {"query": "order-4", "gate": "read-gate"} -> succeeded: found order-4',
            f'- publish {{"message": "x"}} -> rejected: {TOOL_CALL_CANCELLED_BY_STEERING_RESULT}',
            "Do not repeat these tool calls unless the user asks again.",
        ]
    )
    assert steered.messages[-1].text == "said: instead"
    assert harness.invocations.count("model:turn-1:2") == 0

    assert await harness.sessions.send_user_message(
        session_id, 'call lookup {"query": "order-5", "gate": "second-gate"}'
    )
    second_gate = harness.gate("second-gate")
    await second_gate.wait_until_reached()
    assert await harness.sessions.cancel_turn(session_id, "turn-3")
    second_gate.release()
    cancelled = await harness.wait_for_turns(session_id, 3)
    assert (cancelled.messages[-1].text or "").startswith(
        f"{TURN_CANCELLED_NOTE_HEADER}\n- lookup "
    )
    assert cancelled.state is not None and cancelled.state.turn_count == 3


async def test_a_steer_ends_a_question_and_a_durable_wait_but_a_queued_message_does_not(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """A4-3: only steering interrupts a wait, and the model sees the cancellation result."""
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "wait 3600 the nightly build")
    await _wait_for_step_method(harness, session_id, "AwaitTimer", WAIT_FOR_COMPLETED_EVENT)
    assert await harness.sessions.send_user_message(session_id, "say queued")
    still_waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert [call.status for call in still_waiting.tool_calls] == [ToolCallStatus.WAITING_FOR_TIMER]
    assert still_waiting.queued_message_count == 1

    assert await harness.sessions.steer_user_message(session_id, text="ask Which build? | a | b")
    question = await _wait_for_pending_interrupt(harness, session_id)
    assert question.kind is InterruptKind.QUESTION
    waited = await harness.sessions.get_agent_snapshot(session_id)
    assert [message.tool_output for message in waited.messages if message.tool_name] == [
        None,
        TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
        None,
    ]

    assert await harness.sessions.steer_user_message(session_id, text="say done")
    snapshot = await harness.wait_for_turns(session_id, 4)
    assert not await harness.sessions.answer_question(session_id, question.interrupt_id, "a")
    turn_texts = [(message.turn_id, message.text) for message in snapshot.messages if message.text]
    assert ("turn-3", "said: done") in turn_texts
    assert turn_texts[-1] == ("turn-4", "said: queued")
    assert [
        (message.turn_id, message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ] == [
        ("turn-1", "durable_wait", TOOL_CALL_CANCELLED_BY_STEERING_RESULT),
        ("turn-2", "ask_user", TOOL_CALL_CANCELLED_BY_STEERING_RESULT),
    ]
    events = await harness.agent_events(session_id)
    assert [payload["kind"] for payload in _payloads(events, "interrupt_resolved")] == ["question"]


async def test_a_tool_question_asked_after_a_steer_is_never_raised(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """A tool that started before the steer and then asks a question ends without asking."""
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "transfer-late-ask 9 ask-gate")
    gate = harness.gate("ask-gate")
    await gate.wait_until_reached()

    assert await harness.sessions.steer_user_message(session_id, text="say skip it")
    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert harness.invocations.names().count("transfer_funds:ask") == 1
    assert harness.invocations.names().count("transfer_funds:run") == 0
    assert [
        (message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ] == [("transfer_funds", TOOL_CALL_CANCELLED_BY_STEERING_RESULT)]
    assert snapshot.messages[-1].text == "said: skip it"
    events = await harness.agent_events(session_id)
    assert not _payloads(events, AgentEventType.INTERRUPT_RAISED)
    executed = await harness.executed_step_types(session_id)
    assert executed.count("AwaitQuestionAnswer") == 1


async def test_a_wake_up_left_from_an_earlier_turn_never_cancels_a_call(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    """A5-9: the flags decide, so a stale ToolCallCancellations message only wakes the Step."""
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    await _publish_stale_tool_call_cancellation(harness, session_id, "reused-call")

    assert await harness.sessions.send_user_message(session_id, "publish-as reused-call memo")
    await _wait_for_step_completions(harness, session_id, "AwaitToolApproval", 1)
    approval = await _wait_for_pending_interrupt(harness, session_id)
    assert approval.interrupt_id == "reused-call"
    assert await harness.sessions.resolve_interrupt(session_id, "reused-call", approved=True)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == 'publish_note: {"published": "memo"}'
    assert harness.invocations.count("publish_note") == 1
    assert (await harness.executed_step_types(session_id)).count("AwaitToolApproval") == 2


async def test_a_retried_read_that_started_before_the_steer_runs_to_the_end(
    harness: ScriptedAgentHarness,
) -> None:
    """A retry reuses the first attempt's state, so steering never abandons a started read."""
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call lookup {"query": "q", "gate": "retry-gate", "fail_attempts": 1}'
    )
    gate = harness.gate("retry-gate")
    await gate.wait_until_reached()
    assert await harness.sessions.steer_user_message(session_id, text="say later")
    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert harness.invocations.count("lookup") == 2
    assert gate.reached_count() == 2
    note = next(
        message
        for message in snapshot.messages
        if message.turn_id == "turn-1" and message.sequence > 1
    )
    assert note.text is not None and note.text.startswith(TURN_STEERED_NOTE_HEADER)
    assert "-> succeeded: found q" in note.text
    assert snapshot.messages[-1].text == "said: later"


async def test_a_steered_message_starts_a_planning_turn_while_a_draft_exists(
    plan_harness: ScriptedAgentHarness,
) -> None:
    """A steered turn decides its mode as a queued turn does, and keeps a moved plan_mode."""
    harness = plan_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    assert await harness.sessions.send_user_message(session_id, "plan A | B", plan_mode=True)
    drafted = await harness.wait_for_turns(session_id, 1)
    assert drafted.plan is not None and drafted.plan.status is PlanStatus.DRAFT

    # The model call answers, so the steer starts the next turn through ApplyTurnControl.
    assert await harness.sessions.send_user_message(session_id, "stall draft-gate")
    gate = harness.gate("draft-gate")
    await gate.wait_until_reached()
    assert await harness.sessions.steer_user_message(session_id, text="revise C | D")
    gate.release()
    revised = await harness.wait_for_turns(session_id, 3)
    assert revised.plan is not None
    assert (revised.plan.revision, revised.plan.status) == (2, PlanStatus.DRAFT)
    assert [task.content for task in revised.plan.tasks] == ["C", "D"]
    assert revised.state is not None
    assert revised.state.interaction_mode is InteractionMode.PLANNING
    assert {
        tuple(request["tools"])
        for request in read_model_requests(harness.state_dir)
        if request["flow_id"] == session_id and request["turn_id"] == "turn-3"
    } == {("write_todos",)}
    assert (await harness.executed_step_types(session_id)).count("ApplyTurnControl") == 1


async def test_a_queued_plan_mode_message_moved_by_a_steer_keeps_plan_mode(
    plan_harness: ScriptedAgentHarness,
) -> None:
    """Steering by ID publishes the queued message unchanged, plan_mode included."""
    harness = plan_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall chat-gate")
    gate = harness.gate("chat-gate")
    await gate.wait_until_reached()
    assert await harness.sessions.send_user_message(session_id, "plan X | Y", plan_mode=True)
    queued = (await harness.sessions.get_agent_snapshot(session_id)).queued_messages
    assert [pending.message.plan_mode for pending in queued] == [True]
    assert await harness.sessions.steer_user_message(session_id, message_id=queued[0].message_id)
    steering = await harness.sessions.get_agent_snapshot(session_id)
    assert [pending.message.plan_mode for pending in steering.steered_messages] == [True]
    gate.release()
    planned = await harness.wait_for_turns(session_id, 2)
    assert planned.plan is not None and planned.plan.status is PlanStatus.DRAFT
    assert planned.state is not None
    assert planned.state.interaction_mode is InteractionMode.PLANNING


async def test_execute_plan_returns_false_while_a_steered_message_is_pending(
    plan_harness: ScriptedAgentHarness,
) -> None:
    """A pending steer starts the next turn, so an approval would run after the wrong turn."""
    harness = plan_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    assert await harness.sessions.send_user_message(session_id, "plan A | B", plan_mode=True)
    await harness.wait_for_turns(session_id, 1)
    # A steer that ended its turn and has not applied yet, held pending with no wake-up.
    await _publish_to_channel(
        harness, session_id, "SteeredUserMessages", {"text": "discuss", "plan_mode": False}
    )
    pending = await harness.sessions.get_agent_snapshot(session_id)
    assert [message.message.text for message in pending.steered_messages] == ["discuss"]

    assert not await harness.sessions.execute_plan(session_id, 1)
    await _publish_to_channel(harness, session_id, "TurnControlRequests", {"kind": "steer"})
    applied = await harness.wait_for_turns(session_id, 2)
    assert applied.steered_messages == []
    assert applied.messages[-1].text == "the plan has 2 tasks"
    assert await harness.sessions.execute_plan(session_id, 1)


async def test_event_frames_and_snapshot_json_carry_steering(
    harness: ScriptedAgentHarness,
) -> None:
    """The browser sees the steered turn end like a completed one, with no approval pause."""
    await harness.stack.start_worker()
    client = harness.stack.client
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "launch"}')
    await _wait_for_pending_interrupt(harness, session_id)
    assert await harness.sessions.send_user_message(session_id, "say queued")
    opening = await read_agent_event_stream_snapshot(client, harness.flow, session_id)
    opening_json = opening.to_json()
    assert json.loads(json.dumps(opening_json)) == opening_json
    queued_json = opening_json["snapshot"]["queued_messages"]
    assert [entry["message"]["text"] for entry in queued_json] == ["say queued"]
    assert opening_json["snapshot"]["steered_messages"] == []

    assert await harness.sessions.steer_user_message(
        session_id, message_id=queued_json[0]["message_id"]
    )
    frames = await _read_frames_until(
        await stream_agent_event_frames(
            client,
            harness.flow,
            session_id,
            resume_token=opening.resume_token,
            poll_timeout=POLL_TIMEOUT,
        ),
        lambda data: data["type"] == AgentEventType.TURN_COMPLETED,
    )
    types = [frame.data["type"] for frame in frames]
    assert types[: types.index(AgentEventType.TURN_STEERED) + 2][-3:] == [
        AgentEventType.TOOL_CALL_FINISHED,
        AgentEventType.TURN_STEERED,
        AgentEventType.TURN_STARTED,
    ]
    # Events are written before their Step commits, so wait for the turn's end to commit.
    await harness.wait_for_turns(session_id, 2)
    closing = await read_agent_event_stream_snapshot(client, harness.flow, session_id)
    closing_json = closing.to_json()
    assert closing_json["snapshot"]["messages"][-1]["text"] == "said: queued"
    assert closing_json["snapshot"]["queued_messages"] == []


def _message_shape(
    messages: Sequence[AgentMessage],
) -> list[tuple[str, MessageRole, str | None]]:
    return [(message.turn_id, message.role, message.tool_name) for message in messages]


def _payloads(events: Sequence[object], event_type: str) -> list[dict[str, object]]:
    return [
        json.loads(event.payload)  # type: ignore[attr-defined]
        for event in events
        if event.type == event_type  # type: ignore[attr-defined]
    ]


async def _after(delay_seconds: float, call: Awaitable[bool]) -> bool:
    await asyncio.sleep(delay_seconds)
    return await call


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


async def _publish_stale_tool_call_cancellation(
    harness: ScriptedAgentHarness, session_id: str, tool_call_id: str
) -> None:
    await _publish_to_channel(
        harness, session_id, f"ToolCallCancellations/{tool_call_id}", {"reason": "steered"}
    )


async def _publish_to_channel(
    harness: ScriptedAgentHarness, session_id: str, channel_name: str, value: dict[str, object]
) -> None:
    payload = base64.b64encode(json.dumps(value).encode()).decode()
    request = {
        "flowId": session_id,
        "messages": [
            {
                "channelName": channel_name,
                "value": {"objValue": {"encoding": "json", "payload": payload}},
            }
        ],
    }
    process = await asyncio.create_subprocess_exec(
        str(resolve_dexcli_path()),
        "api",
        "call",
        "PublishToChannel",
        "--server",
        harness.stack.engine.address,
        "--yes",
        "--data",
        json.dumps(request),
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    stdout, stderr = await process.communicate()
    assert process.returncode == 0, (stdout, stderr)


async def _wait_for_step_completions(
    harness: ScriptedAgentHarness,
    session_id: str,
    step_type: str,
    count: int,
) -> None:
    async def probe() -> bool:
        return (await harness.executed_step_types(session_id)).count(step_type) >= count

    await wait_until(f"{count} {step_type} Step(s)", probe)
