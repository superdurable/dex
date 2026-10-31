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

import pytest
from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT, AgentEvent, AgentEventType, MessageRole
from dex_agents.testing import wait_until

from tests.conftest import AGENT_FLOW_TYPES, ScriptedAgentHarness
from tests.scripted_agents import SLOW_WRITE_GATE
from tests.scripted_model import STALL_TEXT


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
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
    executed = await harness.executed_step_types(session_id)
    assert executed[1:] == [
        "RunModel",
        "RecordToolOutcomeUnknown",
        "ResumeTurn",
        "RunModel",
    ]


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_worker_killed_during_run_model_reruns_only_that_model_call(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call lookup {"query": "q"} then stall model-gate'
    )
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()

    async def stalled_text_delta(attempt: int) -> AgentEvent | None:
        # Only the exact stalled chunk counts, never an earlier partial text.
        for event in await harness.list_agent_events(session_id):
            if (
                event.type == AgentEventType.TEXT_DELTA
                and event.attempt == attempt
                and json.loads(event.payload)["text"] == STALL_TEXT
            ):
                return event
        return None

    first_attempt = await wait_until(
        "the stalled model call's text delta", lambda: stalled_text_delta(1)
    )

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"{STALL_TEXT} lookup: found q"
    assert harness.model_call_count(1) == 1
    assert harness.invocations.count("lookup") == 1
    assert harness.model_call_count(2) == 2
    assert gate.reached_count() == 2
    assert snapshot.usage.model_calls == 2
    history = await harness.read_step_history(session_id)
    model_steps = [entry for entry in history if entry.step_type == "RunModel"]
    assert [entry.final_attempt for entry in model_steps] == [1, 2]
    second_attempt = await stalled_text_delta(2)
    assert second_attempt is not None
    assert first_attempt.step_execution_id == second_attempt.step_execution_id
    assert first_attempt.step_execution_id == model_steps[1].step_execution_id
