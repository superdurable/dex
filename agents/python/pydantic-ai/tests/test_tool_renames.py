# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Worker restarts with a deploy that renamed a tool the open turn already called."""

from __future__ import annotations

from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT, MessageRole
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import RENAMED_TOOLS_ENVIRONMENT_VARIABLE


async def test_renamed_completed_tool_stays_history_and_the_turn_uses_the_new_tool(
    harness: ScriptedAgentHarness,
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
    # The model sees the recorded result under the old name, not an unknown-tool retry prompt.
    assert snapshot.messages[-1].text == "lookup: found q; find: found r"
    tool_results = [
        (message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ]
    assert tool_results == [("lookup", "found q"), ("find", "found r")]
    assert harness.invocations.count("lookup") == 2
    assert [name for name in harness.invocations.names() if name.startswith("model:")] == [
        "model:1:1",
        "model:1:2",
        "model:1:2",
        "model:1:3",
    ]


async def test_renamed_in_flight_single_attempt_tool_records_outcome_unknown(
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
    await harness.stack.start_worker({RENAMED_TOOLS_ENVIRONMENT_VARIABLE: "slow_write=save"})
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == f"slow_write: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.tool_output for message in tool_results] == [OUTCOME_UNKNOWN_TOOL_RESULT]
    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1
    executed = await harness.executed_step_types(session_id)
    assert "RecordToolOutcomeUnknown" in executed
    assert "CallSingleAttemptTool" not in executed


async def test_renamed_in_flight_idempotent_write_records_outcome_unknown_on_its_retry(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5, "gate": "charge-gate"}'
    )
    gate = harness.gate("charge-gate")
    await gate.wait_until_reached()

    worker.kill()
    await harness.stack.start_worker({RENAMED_TOOLS_ENVIRONMENT_VARIABLE: "charge=bill"})
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    # The retry finds no tool named charge; the killed attempt may still have charged.
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert len(tool_results) == 1
    assert (tool_results[0].tool_output or "").startswith(OUTCOME_UNKNOWN_TOOL_RESULT)
    assert (snapshot.messages[-1].text or "").startswith(f"charge: {OUTCOME_UNKNOWN_TOOL_RESULT}")
    assert harness.invocations.count("charge") == 1
    assert gate.reached_count() == 1
    # The retry's refusal records the outcome itself; no failure route runs.
    executed = await harness.executed_step_types(session_id)
    assert executed.count("CallTool") == 1
    assert "RecordToolFailure" not in executed
