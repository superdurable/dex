# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Every tool call of a model message runs in its own Step, in parallel."""

from __future__ import annotations

import pytest
from dex_agents import AgentSessionStatus, ToolCallStatus
from dex_agents.testing import wait_for_agent_snapshot

from tests.conftest import AGENT_FLOW_TYPES, ScriptedAgentHarness
from tests.scripted_agents import SLOW_WRITE_GATE


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_a_read_and_a_write_of_one_model_message_run_as_parallel_steps(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call wait_at_gate {"gate": "read-gate"} && call slow_write {"message": "x"}'
    )
    read_gate = harness.gate("read-gate")
    write_gate = harness.gate(SLOW_WRITE_GATE)
    # Both tools wait at their gates at the same time, so their Steps run in parallel.
    await read_gate.wait_until_reached()
    await write_gate.wait_until_reached()
    running = await harness.sessions.get_agent_snapshot(session_id)
    assert running.state is not None
    assert running.state.status is AgentSessionStatus.RUNNING_TOOLS
    assert [(call.tool_name, call.deferral_order) for call in running.tool_calls] == [
        ("wait_at_gate", 0),
        ("slow_write", 1),
    ]
    assert harness.model_call_count() == 1

    read_gate.release()
    write_gate.release()
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "wait_at_gate: passed read-gate; slow_write: wrote x"
    executed = await harness.executed_step_types(session_id)
    assert sorted(executed[2:4]) == ["CallSingleAttemptTool", "CallTool"]
    assert executed[4:] == ["ResumeTurn", "RunModel"]
    assert harness.model_call_count() == 2


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_an_approval_waits_while_a_read_of_the_same_message_finishes(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call publish {"message": "first"} && call lookup {"query": "inline"}'
        ' && call charge {"amount": 3}',
    )
    waiting = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot
        if [(call.tool_name, call.status) for call in snapshot.tool_calls]
        == [
            ("publish", ToolCallStatus.AWAITING_APPROVAL),
            ("lookup", ToolCallStatus.SUCCEEDED),
            ("charge", ToolCallStatus.SUCCEEDED),
        ]
        else None,
        description="the read and the idempotent write finished while the approval waits",
    )
    assert [interrupt.tool_name for interrupt in waiting.pending_interrupts] == ["publish"]
    assert sorted(harness.tool_invocations()) == ["charge", "lookup"]

    assert await harness.sessions.resolve_interrupt(
        session_id, waiting.pending_interrupts[0].interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == (
        "publish: published first; lookup: found inline; "
        f"charge: charged 3 with key {session_id}/turn-1/call-0-0-2"
    )
    assert sorted(harness.tool_invocations()) == ["charge", "lookup", "publish"]
    assert harness.model_call_count() == 2
    executed = await harness.executed_step_types(session_id)
    assert executed.count("AwaitToolApproval") == 1
    assert executed.count("CallSingleAttemptTool") == 1
    assert executed.count("CallTool") == 2
