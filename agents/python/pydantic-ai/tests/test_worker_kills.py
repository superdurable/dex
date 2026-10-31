# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""SIGKILL the Worker child while a Pydantic AI model call or tool call Step runs."""

from __future__ import annotations

import json

from dex import FlowConfig, StartFlowOptions, StepDurability

from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT, AgentEvent, AgentEventType, MessageRole
from dex_agents.testing import wait_until
from tests.conftest import ScriptedAgentHarness

STALL_TEXT = "thinking after found stall. "


async def test_worker_killed_during_a_resumed_run_model_reruns_only_that_model_call(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall model-gate")
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()
    first_attempt_delta = await wait_until(
        "the first attempt's text delta",
        lambda: _stall_text_delta(harness, session_id, attempt=1),
    )

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"{STALL_TEXT}stalled at model-gate"
    assert [message.role for message in snapshot.messages] == [
        MessageRole.USER,
        MessageRole.USER,
        MessageRole.ASSISTANT,
        MessageRole.TOOL,
        MessageRole.ASSISTANT,
    ]
    assert harness.invocations.count("model:1:1") == 1
    assert harness.invocations.count("lookup") == 1
    assert harness.invocations.count("model:1:2") == 2
    assert gate.reached_count() == 2
    history = await harness.stack.read_step_history(session_id)
    model_steps = [entry for entry in history if entry.step_type == "RunModel"]
    assert [entry.final_attempt for entry in model_steps] == [1, 2]
    second_attempt_delta = await _stall_text_delta(harness, session_id, attempt=2)
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
    executed = await harness.executed_step_types(session_id)
    assert "RecordToolOutcomeUnknown" in executed
    assert "CallSingleAttemptTool" not in executed


async def test_async_flow_durability_never_reruns_a_single_attempt_write(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call slow_write {"message": "async", "gate": "async-gate"}',
        StartFlowOptions(config_override=FlowConfig(step_durability=StepDurability.ASYNC)),
    )
    gate = harness.gate("async-gate")
    await gate.wait_until_reached()

    worker.kill()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"slow_write: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1


async def _stall_text_delta(
    harness: ScriptedAgentHarness,
    session_id: str,
    attempt: int,
) -> AgentEvent | None:
    for event in await harness.list_agent_events(session_id):
        if (
            event.type == AgentEventType.TEXT_DELTA
            and event.attempt == attempt
            and json.loads(event.payload)["text"] == STALL_TEXT
        ):
            return event
    return None
