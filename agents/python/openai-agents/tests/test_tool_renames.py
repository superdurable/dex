# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Worker restarts with a deploy that renamed a tool the open turn already called."""

from __future__ import annotations

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    MessageRole,
)
from tests.conftest import SupportAgentHarness
from tests.support_agent import RENAMED_TOOLS_ENVIRONMENT_VARIABLE


async def test_renamed_completed_tool_stays_history_and_the_turn_uses_the_new_tool(
    harness: SupportAgentHarness,
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
    assert harness.invocations.count("lookup") == 2
    assert harness.model_calls() == ["model:1:1", "model:1:2", "model:1:2", "model:1:3"]


async def test_renamed_in_flight_single_attempt_tool_records_outcome_unknown(
    harness: SupportAgentHarness,
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


async def test_steering_a_turn_rebuilt_after_a_rename_stores_every_call_once(
    harness: SupportAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call lookup {"query": "x"} then await model-gate then call publish {"message": "note"}',
    )
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()

    worker.kill()
    gate.release()
    await harness.stack.start_worker({RENAMED_TOOLS_ENVIRONMENT_VARIABLE: "lookup=find"})
    await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.steer_user_message(session_id, text="recall")
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert [(message.role, message.tool_name) for message in snapshot.messages[1:5]] == [
        (MessageRole.ASSISTANT, "lookup"),
        (MessageRole.TOOL, "lookup"),
        (MessageRole.ASSISTANT, "publish"),
        (MessageRole.TOOL, "publish"),
    ]
    assert snapshot.messages[-1].text == (
        'history: call lookup {"query": "x"} then await model-gate then call publish '
        f'{{"message": "note"}}; tools: found x | {TOOL_CALL_CANCELLED_BY_STEERING_RESULT}'
    )
    assert harness.tool_runs() == ["lookup"]
