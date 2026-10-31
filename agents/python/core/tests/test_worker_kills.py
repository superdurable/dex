# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""SIGKILL the Worker child at the exact moment under test."""

from __future__ import annotations

import json

from dex import FlowConfig, StartFlowOptions, Step, StepDurability

from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT, AgentEvent, AgentEventType, MessageRole
from dex_agents.testing import wait_for_agent_snapshot, wait_until
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import GATED_PUBLISH_GATE, SLOW_WRITE_GATE


async def test_worker_killed_during_a_single_attempt_write_records_outcome_unknown(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call slow_write {"message": "ledger"}')
    gate = harness.gate(SLOW_WRITE_GATE)
    await gate.wait_until_reached()

    worker.kill()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"slow_write: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.tool_output for message in tool_results] == [OUTCOME_UNKNOWN_TOOL_RESULT]
    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1


async def test_worker_killed_during_run_agent_turn_reruns_the_segment(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall turn-gate")
    gate = harness.gate("turn-gate")
    await gate.wait_until_reached()
    first_attempt_delta = await wait_until(
        "the first attempt's text delta",
        lambda: _text_delta(harness, session_id, attempt=1),
    )

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "stalled at turn-gate"
    assert [message.text for message in snapshot.messages] == [
        "stall turn-gate",
        "stalled at turn-gate",
    ]
    assert harness.invocations.count("run_turn:turn-1") == 2
    assert harness.invocations.count("lookup") == 2
    assert gate.reached_count() == 2
    second_attempt_delta = await _text_delta(harness, session_id, attempt=2)
    assert second_attempt_delta is not None
    assert first_attempt_delta.turn_id == second_attempt_delta.turn_id == "turn-1"
    assert first_attempt_delta.step_execution_id == second_attempt_delta.step_execution_id


async def test_worker_killed_during_an_idempotent_write_retries_with_the_same_key(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 9, "gate": "charge-gate"}'
    )
    gate = harness.gate("charge-gate")
    await gate.wait_until_reached()

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    key = f"{session_id}/turn-1/turn-1-call-0"
    assert snapshot.messages[-1].text == f"charge: charged 9 with key {key}"
    assert harness.idempotency_keys.names() == [f"{key} attempt 1", f"{key} attempt 2"]


async def test_worker_killed_during_an_approved_single_attempt_write_records_outcome_unknown(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call gated_publish {"message": "once"}')
    await _approve_pending_interrupt(harness, session_id)
    gate = harness.gate(GATED_PUBLISH_GATE)
    await gate.wait_until_reached()

    worker.kill()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"gated_publish: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("gated_publish") == 1
    assert gate.reached_count() == 1


async def test_async_flow_durability_never_reruns_a_single_attempt_write(
    harness: ScriptedAgentHarness,
) -> None:
    for step in harness.flow.get_steps():
        options = step.step.get_step_options()
        assert options is not None
        assert options.execute_durability is StepDurability.SYNC, step.step.get_step_type()
        if type(step.step).wait_for is not Step.wait_for:
            assert options.wait_for_durability is StepDurability.SYNC, step.step.get_step_type()
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call publish {"message": "async"}',
        StartFlowOptions(config_override=FlowConfig(step_durability=StepDurability.ASYNC)),
    )
    await _approve_pending_interrupt(harness, session_id)
    approved = await harness.wait_for_turns(session_id, 1)
    assert approved.messages[-1].text == "publish: published async"
    assert harness.invocations.count("publish") == 1

    assert await harness.sessions.send_user_message(
        session_id, 'call slow_write {"message": "async"}'
    )
    gate = harness.gate(SLOW_WRITE_GATE)
    await gate.wait_until_reached()
    worker.kill()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.messages[-1].text == f"slow_write: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1


async def _approve_pending_interrupt(harness: ScriptedAgentHarness, session_id: str) -> None:
    interrupt = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None),
        description="a pending interrupt",
    )
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )


async def _text_delta(
    harness: ScriptedAgentHarness,
    session_id: str,
    attempt: int,
) -> AgentEvent | None:
    for event in await harness.agent_events(session_id):
        if event.type == AgentEventType.TEXT_DELTA and event.attempt == attempt:
            assert json.loads(event.payload)["text"] == "thinking after found stall"
            return event
    return None
