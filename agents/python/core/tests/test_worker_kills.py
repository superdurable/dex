# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""SIGKILL the Worker child at the exact moment under test."""

from __future__ import annotations

import asyncio
import json
from datetime import timedelta

from dex import FlowConfig, StartFlowOptions, Step, StepDurability

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TOOL_CALL_NOT_RUN_ERROR_PREFIX,
    AgentEvent,
    AgentEventType,
    MessageRole,
)
from dex_agents.testing import (
    MAX_STEP_TASKS_ENVIRONMENT_VARIABLE,
    FileGate,
    resolve_dexcli_path,
    wait_for_agent_snapshot,
    wait_until,
)
from tests.conftest import ScriptedAgentHarness


async def test_worker_killed_during_run_model_reruns_only_that_model_call(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call-then-stall model-gate lookup {"query": "q"}'
    )
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()
    first_attempt_delta = await wait_until(
        "the first attempt's text delta",
        lambda: _text_delta(harness, session_id, attempt=1),
    )

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "lookup: found q"
    assert harness.invocations.count("model:turn-1:1") == 1
    assert harness.invocations.count("lookup") == 1
    assert harness.invocations.count("model:turn-1:2") == 2
    history = await harness.stack.read_step_history(session_id)
    model_steps = [entry for entry in history if entry.step_type == "RunModel"]
    assert [entry.final_attempt for entry in model_steps] == [1, 2]
    second_attempt_delta = await _text_delta(harness, session_id, attempt=2)
    assert second_attempt_delta is not None
    assert first_attempt_delta.step_execution_id == second_attempt_delta.step_execution_id
    assert first_attempt_delta.step_execution_id == model_steps[1].step_execution_id


async def test_worker_killed_during_a_single_attempt_write_records_outcome_unknown(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call slow_write {"message": "ledger", "gate": "write-gate"}'
    )
    gate = harness.gate("write-gate")
    await gate.wait_until_reached()

    worker.kill()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"slow_write: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.tool_output for message in tool_results] == [OUTCOME_UNKNOWN_TOOL_RESULT]
    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1
    assert "RecordToolOutcomeUnknown" in await harness.executed_step_types(session_id)


async def test_worker_killed_while_a_blocking_single_attempt_write_starts_never_reruns_it(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call slow_write {"message": "ledger", "blocking_gate": "blocking-gate"}'
    )
    gate = harness.gate("blocking-gate")
    # The tool blocks the event loop from its first line, so no later frame leaves this Worker.
    try:
        await gate.wait_until_reached()
    except TimeoutError:
        # Shows whether the tool Step waited for a Worker, failed its mark, or never started.
        print(await _flow_history_json(harness, session_id))
        raise

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"slow_write: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1


async def test_a_single_attempt_write_no_worker_accepts_records_that_it_did_not_run(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker({MAX_STEP_TASKS_ENVIRONMENT_VARIABLE: "1"})
    session_id = harness.new_session_id()
    # Whichever call takes the Worker's only Step slot holds it past the other's dispatch wait.
    await harness.sessions.start_session(
        session_id,
        'call slow_write {"message": "a", "gate": "slot-a"}'
        ' && call slow_write {"message": "b", "gate": "slot-b"}',
    )
    gates = [harness.gate("slot-a"), harness.gate("slot-b")]
    await wait_until(
        "one call to take the Step slot",
        lambda: _any_reached(gates),
    )
    await wait_until(
        "the other tool Step to fail without a Worker",
        lambda: _has_failed_execute(harness, session_id),
        timeout=timedelta(seconds=60),
    )

    for gate in gates:
        gate.release()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert harness.invocations.count("slow_write") == 1
    assert sum(gate.reached_count() for gate in gates) == 1
    outputs = sorted(
        message.tool_output or ""
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    )
    assert outputs[0].startswith(f"The tool call failed: {TOOL_CALL_NOT_RUN_ERROR_PREFIX}: ")
    assert outputs[1] in ("wrote a", "wrote b")


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
    assert harness.idempotency_keys.names() == [
        f"charge {key} attempt 1",
        f"charge {key} attempt 2",
    ]


async def test_worker_killed_during_an_approved_single_attempt_write_records_outcome_unknown(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "once", "gate": "publish-gate"}'
    )
    await _approve_pending_interrupt(harness, session_id)
    gate = harness.gate("publish-gate")
    await gate.wait_until_reached()

    worker.kill()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"publish: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("publish") == 1
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
        session_id, 'call slow_write {"message": "async", "gate": "async-gate"}'
    )
    gate = harness.gate("async-gate")
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
            assert json.loads(event.payload)["text"] == "thinking before model-gate"
            return event
    return None


async def _has_failed_execute(harness: ScriptedAgentHarness, session_id: str) -> bool:
    history = json.loads(await _flow_history_json(harness, session_id))
    return any(event["type"] == "StepExecuteFailed" for event in history["events"])


async def _flow_history_json(harness: ScriptedAgentHarness, session_id: str) -> str:
    process = await asyncio.create_subprocess_exec(
        str(resolve_dexcli_path()),
        *("flow", "history", session_id, "--server", harness.stack.engine.address),
        *("--all", "--output", "json"),
        stdout=asyncio.subprocess.PIPE,
    )
    stdout, _ = await process.communicate()
    assert process.returncode == 0
    return stdout.decode()


async def _any_reached(gates: list[FileGate]) -> bool:
    return any(gate.reached_count() > 0 for gate in gates)
