# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A Worker restarts with a deploy that renamed a tool the open turn already called."""

from __future__ import annotations

import asyncio
import json

import pytest
from claude_agent_sdk import ClaudeAgentOptions

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    AgentEventType,
    AgentEventWriter,
    AgentToolCall,
    DeferredToolExecution,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
    ToolExecutionResult,
    ToolOutcomeUnknownError,
)
from dex_agents_claude_agent_sdk import APPROVED_CLI_TOOL_OUTPUT, ClaudeAgentRunner
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
        session_id, 'call charge {"amount": 5, "gate": "charge-gate"} ;; pause model-gate'
    )
    gate = harness.gate("charge-gate")
    await gate.wait_until_reached()

    worker.kill()
    await harness.stack.start_worker({RENAMED_TOOLS_ENVIRONMENT_VARIABLE: "charge=bill"})
    model_gate = harness.gate("model-gate")
    await model_gate.wait_until_reached()

    # The turn's records leave the snapshot when it ends, so read the record while the model waits.
    [record] = (await harness.sessions.get_agent_snapshot(session_id)).tool_calls
    assert record.tool_name == "charge"
    assert record.status is ToolCallStatus.OUTCOME_UNKNOWN
    assert record.output is None
    assert record.error == _removed_custom_tool_retry_error("charge")
    model_gate.release()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == f"charge: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert [
        (message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ] == [("charge", OUTCOME_UNKNOWN_TOOL_RESULT)]
    assert harness.invocations.count("charge") == 1
    assert gate.reached_count() == 1
    finished_statuses = [
        json.loads(event.payload)["status"]
        for event in await harness.list_events(session_id)
        if event.type == AgentEventType.TOOL_CALL_FINISHED
    ]
    assert finished_statuses == ["outcome_unknown"]
    assert_unique_transcript_entries(snapshot.messages)


async def test_renamed_write_approved_on_the_new_worker_keeps_its_placeholder_record(
    harness: ClaudeAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "hello"} ;; pause model-gate'
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id)

    worker.kill()
    await harness.stack.start_worker({RENAMED_TOOLS_ENVIRONMENT_VARIABLE: "publish=post"})
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    model_gate = harness.gate("model-gate")
    await model_gate.wait_until_reached()

    # A call that needed approval looks like a Claude Code tool to its tool Step.
    tool_calls = (await harness.sessions.get_agent_snapshot(session_id)).tool_calls
    assert [(record.tool_name, record.status, record.output) for record in tool_calls] == [
        ("publish", ToolCallStatus.SUCCEEDED, APPROVED_CLI_TOOL_OUTPUT)
    ]
    model_gate.release()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == f"publish: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert [
        (message.tool_name, message.tool_output)
        for message in snapshot.messages
        if message.role is MessageRole.TOOL
    ] == [("publish", OUTCOME_UNKNOWN_TOOL_RESULT)]
    assert harness.invocations.count("publish") == 0
    finished_statuses = [
        json.loads(event.payload)["status"]
        for event in await harness.list_events(session_id)
        if event.type == AgentEventType.TOOL_CALL_FINISHED
    ]
    assert finished_statuses == ["outcome_unknown"]
    assert_unique_transcript_entries(snapshot.messages)


async def test_only_a_retry_without_approval_treats_a_missing_tool_as_a_removed_custom_tool() -> (
    None
):
    runner = ClaudeAgentRunner(lambda context: ClaudeAgentOptions())
    removed_custom_tool_call = _tool_call_record("charge", requires_approval=False)
    approved = ToolExecutionResult(
        output=APPROVED_CLI_TOOL_OUTPUT, is_finished_by_resumed_turn=True
    )

    retry = _deferred_tool_execution(removed_custom_tool_call, attempt=2)
    with pytest.raises(ToolOutcomeUnknownError) as raised:
        await runner.execute_deferred_tool_call(retry)
    assert str(raised.value) == _removed_custom_tool_retry_error("charge")
    # No earlier attempt ran the call.
    first_attempt = _deferred_tool_execution(removed_custom_tool_call, attempt=1)
    assert await runner.execute_deferred_tool_call(first_attempt) == approved
    # A Claude Code tool defers only for approval, so a call with approval may be one.
    for tool_name in ("Bash", "charge"):
        record = _tool_call_record(tool_name, requires_approval=True)
        approved_retry = _deferred_tool_execution(record, attempt=2)
        assert await runner.execute_deferred_tool_call(approved_retry) == approved


def _removed_custom_tool_retry_error(tool_name: str) -> str:
    return (
        f"{tool_name} did not run on this attempt: this Worker no longer offers it, "
        "and an earlier attempt may have run it."
    )


def _tool_call_record(tool_name: str, *, requires_approval: bool) -> AgentToolCall:
    return AgentToolCall(
        tool_call_id=f"toolu_{tool_name}",
        turn_id="turn-1",
        tool_name=tool_name,
        arguments="{}",
        effect=ToolEffect.IDEMPOTENT_WRITE,
        requires_approval=requires_approval,
        status=ToolCallStatus.APPROVED,
    )


def _deferred_tool_execution(tool_call: AgentToolCall, *, attempt: int) -> DeferredToolExecution:
    async def heartbeat() -> None:
        await asyncio.sleep(0)

    return DeferredToolExecution(
        flow_id="renamed-tools",
        tool_call=tool_call,
        idempotency_key=f"renamed-tools/{tool_call.turn_id}/{tool_call.tool_call_id}",
        attempt=attempt,
        events=AgentEventWriter(lambda event: None, tool_call.turn_id, attempt),
        heartbeat=heartbeat,
    )
