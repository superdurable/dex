# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Worker restarts with a deploy that renamed a tool the open turn already called."""

from __future__ import annotations

import json

from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT, MessageRole
from tests.conftest import AdkAgentHarness
from tests.scripted_adk_agent import RENAMED_TOOLS_ENVIRONMENT_VARIABLE, SLOW_WRITE_GATE


async def test_renamed_completed_tool_stays_history_and_the_turn_uses_the_new_tool(
    harness: AdkAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call lookup {"query": "q"} then await model-gate then call find {"query": "r"}',
    )
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()

    worker.kill()
    gate.release()
    await harness.stack.start_worker({RENAMED_TOOLS_ENVIRONMENT_VARIABLE: "lookup=find"})
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == "lookup: found q; find: found r"
    tool_results = [
        (message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ]
    assert tool_results == [("lookup", "found q"), ("find", "found r")]
    # The renamed function records its original name: it ran once per call.
    assert harness.invocations.count("lookup") == 2


async def test_renamed_in_flight_single_attempt_tool_records_outcome_unknown(
    harness: AdkAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call slow_write {"message": "ledger"}')
    gate = harness.gate(SLOW_WRITE_GATE)
    await gate.wait_until_reached()

    worker.kill()
    await harness.stack.start_worker({RENAMED_TOOLS_ENVIRONMENT_VARIABLE: "slow_write=save"})
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == f"slow_write: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [json.loads(message.tool_output or "") for message in tool_results] == [
        {"error": OUTCOME_UNKNOWN_TOOL_RESULT}
    ]
    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1
    executed = await harness.executed_step_types(session_id)
    assert "RecordToolOutcomeUnknown" in executed
    assert "CallSingleAttemptTool" not in executed


async def test_renamed_idempotent_write_retried_after_a_kill_records_outcome_unknown(
    harness: AdkAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5, "gate": "charge-gate"}'
    )
    gate = harness.gate("charge-gate")
    await gate.wait_until_reached()

    worker.kill()
    gate.release()
    await harness.stack.start_worker({RENAMED_TOOLS_ENVIRONMENT_VARIABLE: "charge=bill"})
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    # The killed attempt may have charged, so the retry that finds no tool is not a failure.
    assert snapshot.messages[-1].text == f"charge: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("charge") == 1
    # Every retry finds no tool, so the failure route records the write's unknown outcome.
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "RecordToolFailure",
        "ResumeTurn",
        "RunModel",
    ]
