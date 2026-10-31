# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Every model call and every tool call of a turn is its own Step."""

from __future__ import annotations

from dex_agents import AgentSessionStatus, MessageRole, ToolCallStatus
from dex_agents.testing import wait_for_agent_snapshot, wait_until
from tests.conftest import ScriptedAgentHarness


async def test_a_turn_with_a_read_tool_runs_model_tool_and_resume_steps(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call lookup {"query": "docs"}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert [(message.role, message.tool_name) for message in snapshot.messages] == [
        (MessageRole.USER, None),
        (MessageRole.ASSISTANT, "lookup"),
        (MessageRole.TOOL, "lookup"),
        (MessageRole.ASSISTANT, None),
    ]
    assert snapshot.messages[-1].text == "lookup: found docs"
    assert snapshot.usage.model_calls == 2
    assert snapshot.tool_calls == []
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]
    assert harness.invocations.names() == ["model:turn-1:1", "lookup", "model:turn-1:2"]


async def test_parallel_tool_calls_run_as_parallel_steps(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call lookup {"query": "a", "gate": "lookup-gate"}'
        ' && call charge {"amount": 2, "gate": "charge-gate"}',
    )
    lookup_gate = harness.gate("lookup-gate")
    charge_gate = harness.gate("charge-gate")
    # Both tools wait at their gates at the same time, so their Steps run in parallel.
    await lookup_gate.wait_until_reached()
    await charge_gate.wait_until_reached()
    running = await harness.sessions.get_agent_snapshot(session_id)
    assert running.state is not None
    assert running.state.status is AgentSessionStatus.RUNNING_TOOLS
    assert [(call.tool_name, call.deferral_order) for call in running.tool_calls] == [
        ("lookup", 0),
        ("charge", 1),
    ]

    lookup_gate.release()
    charge_gate.release()
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == (
        f"lookup: found a; charge: charged 2 with key {session_id}/turn-1/turn-1-call-1"
    )
    executed = await harness.executed_step_types(session_id)
    assert executed.count("CallTool") == 2
    assert executed[-2:] == ["ResumeTurn", "RunModel"]


async def test_an_approval_waits_in_parallel_with_an_unapproved_read(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "x"} && call lookup {"query": "y"}'
    )

    def statuses(snapshot_tool_calls: list[tuple[str, ToolCallStatus]]) -> bool:
        return snapshot_tool_calls == [
            ("publish", ToolCallStatus.AWAITING_APPROVAL),
            ("lookup", ToolCallStatus.SUCCEEDED),
        ]

    waiting = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot
        if statuses([(call.tool_name, call.status) for call in snapshot.tool_calls])
        else None,
        description="the read finished while the write awaits approval",
    )
    assert [interrupt.tool_name for interrupt in waiting.pending_interrupts] == ["publish"]
    assert harness.invocations.count("lookup") == 1
    assert harness.invocations.count("publish") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, waiting.pending_interrupts[0].interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published x; lookup: found y"
    executed = await harness.executed_step_types(session_id)
    assert executed.count("AwaitToolApproval") == 1
    assert executed.count("CallSingleAttemptTool") == 1
    assert executed.count("CallTool") == 1


async def test_a_message_sent_during_a_turn_waits_in_the_queue(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall busy-gate")
    gate = harness.gate("busy-gate")
    await gate.wait_until_reached()

    assert await harness.sessions.send_user_message(session_id, "say later")

    async def queued_count() -> int:
        return (await harness.sessions.get_agent_snapshot(session_id)).queued_message_count

    assert await wait_until("one queued message", queued_count) == 1
    during = await harness.sessions.get_agent_snapshot(session_id)
    assert during.state is not None
    assert during.state.active_turn_id == "turn-1"
    assert during.state.status is AgentSessionStatus.RUNNING_MODEL

    gate.release()
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert [message.text for message in snapshot.messages] == [
        "stall busy-gate",
        "stalled at busy-gate",
        "say later",
        "said: later",
    ]
    assert snapshot.queued_message_count == 0
