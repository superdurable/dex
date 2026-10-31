# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""SIGKILL the ADK Worker child at the exact moment under test."""

from __future__ import annotations

import json

from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT, MessageRole
from tests.conftest import AdkAgentHarness
from tests.scripted_adk_agent import MODEL_CALL_LOG_NAME, SLOW_WRITE_GATE


async def test_worker_killed_during_a_single_attempt_tool_step_records_outcome_unknown(
    harness: AdkAgentHarness,
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
    final_tool_result = [
        message for message in snapshot.messages if message.role is MessageRole.TOOL
    ][-1]
    assert json.loads(final_tool_result.tool_output or "") == {"error": OUTCOME_UNKNOWN_TOOL_RESULT}
    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1
    executed = await harness.executed_step_types(session_id)
    assert "CallSingleAttemptTool" not in executed
    assert executed.count("RecordToolOutcomeUnknown") == 1


async def test_worker_killed_during_run_model_reruns_only_that_model_call(
    harness: AdkAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall turn-gate")
    gate = harness.gate("turn-gate")
    await gate.wait_until_reached()
    assert harness.invocations.names() == [MODEL_CALL_LOG_NAME, "lookup", MODEL_CALL_LOG_NAME]

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert [(message.role, message.tool_name) for message in snapshot.messages] == [
        (MessageRole.USER, None),
        (MessageRole.ASSISTANT, "lookup"),
        (MessageRole.TOOL, "lookup"),
        (MessageRole.ASSISTANT, None),
    ]
    assert snapshot.messages[-1].text == "stalled at turn-gate"
    # The first model call and the tool Step committed; only the killed model call ran again.
    assert harness.invocations.names() == [
        MODEL_CALL_LOG_NAME,
        "lookup",
        MODEL_CALL_LOG_NAME,
        MODEL_CALL_LOG_NAME,
    ]
    assert gate.reached_count() == 2
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]
