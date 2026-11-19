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
from tests.claude_test_agent import RENAMED_TOOLS_ENVIRONMENT_VARIABLE, SLOW_WRITE_GATE
from tests.conftest import ClaudeAgentHarness
from tests.transcript_assertions import assert_unique_transcript_entries


async def test_renamed_completed_write_replays_its_recorded_result(
    harness: ClaudeAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5} ;; pause model-gate'
    )
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()

    worker.kill()
    gate.release()
    await harness.stack.start_worker({RENAMED_TOOLS_ENVIRONMENT_VARIABLE: "charge=bill"})
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == (
        f"charge: charged 5 with key {session_id}/turn-1/{snapshot.messages[1].tool_call_id}"
    )
    tool_results = [
        (message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ]
    assert len(tool_results) == 1
    assert tool_results[0][0] == "charge"
    assert harness.invocations.count("charge") == 1
    assert_unique_transcript_entries(snapshot.messages)


async def test_renamed_in_flight_single_attempt_write_records_outcome_unknown(
    harness: ClaudeAgentHarness,
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
    assert [(message.tool_name, message.tool_output) for message in tool_results] == [
        ("slow_write", OUTCOME_UNKNOWN_TOOL_RESULT)
    ]
    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1
    executed = await harness.executed_step_types(session_id)
    assert "RecordToolOutcomeUnknown" in executed
    assert_unique_transcript_entries(snapshot.messages)


async def test_renamed_idempotent_write_retried_after_a_kill_records_outcome_unknown(
    harness: ClaudeAgentHarness,
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
    assert snapshot.messages[-1].text == f"charge: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("charge") == 1
