# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Every ADK tool call runs in its own Step: parallel calls, approvals, rejections, keys."""

from __future__ import annotations

import json

from dex_agents import (
    FRAMEWORK_RUN_STATE,
    PENDING_INTERRUPTS,
    AgentEventType,
    AgentSessionStatus,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
)
from dex_agents.testing import wait_for_agent_snapshot
from tests.conftest import AdkAgentHarness
from tests.scripted_adk_agent import (
    FORBIDDEN_MESSAGE,
    GUARDRAIL_ERROR,
    REVIEWER_ANSWER,
    SCRIPTED_ADK_PIPELINE_FLOW_TYPE,
)


async def test_parallel_tool_calls_run_as_parallel_steps(harness: AdkAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call lookup {"query": "a", "gate": "lookup-gate"}'
        ' && call charge {"amount": 2, "gate": "charge-gate"}',
    )
    lookup_gate = harness.gate("lookup-gate")
    charge_gate = harness.gate("charge-gate")
    # Both tools wait at their gates at once, so their Steps run in parallel.
    await lookup_gate.wait_until_reached()
    await charge_gate.wait_until_reached()
    running = await harness.sessions.get_agent_snapshot(session_id)
    assert running.state is not None
    assert running.state.status is AgentSessionStatus.RUNNING_TOOLS
    assert [(call.tool_name, call.effect) for call in running.tool_calls] == [
        ("lookup", ToolEffect.READ),
        ("charge", ToolEffect.IDEMPOTENT_WRITE),
    ]

    lookup_gate.release()
    charge_gate.release()
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert (snapshot.messages[-1].text or "").startswith("lookup: found a; charge: ")
    executed = await harness.executed_step_types(session_id)
    assert executed.count("CallTool") == 2
    assert executed[-2:] == ["ResumeTurn", "RunModel"]
    assert executed.count("RunModel") == 2
    assert harness.invocations.count("model") == 2


async def test_approved_write_survives_a_worker_restart_and_runs_once(
    harness: AdkAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "launch"} && call lookup {"query": "launch"}'
    )
    waiting = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot
        if [(call.tool_name, call.status) for call in snapshot.tool_calls]
        == [("publish", ToolCallStatus.AWAITING_APPROVAL), ("lookup", ToolCallStatus.SUCCEEDED)]
        else None,
        description="the read finished while the write awaits approval",
    )
    interrupt = waiting.pending_interrupts[0]
    assert interrupt.tool_name == "publish"
    assert json.loads(interrupt.arguments) == {"message": "launch"}
    assert waiting.tool_calls[0].effect is ToolEffect.UNCLASSIFIED
    assert harness.invocations.names() == ["model", "lookup"]

    worker.kill()
    await harness.stack.start_worker()
    after_restart = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot,
        description="a snapshot from the replacement Worker",
    )
    assert after_restart.pending_interrupts == [interrupt]
    assert harness.invocations.count("publish") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published launch; lookup: found launch"
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert len(tool_results) == 1
    responses = json.loads(tool_results[0].framework_item)["content"]["parts"]
    assert [part["function_response"]["response"] for part in responses] == [
        {"result": "published launch"},
        {"result": "found launch"},
    ]
    assert harness.invocations.names() == ["model", "lookup", "publish", "model"]
    executed = await harness.executed_step_types(session_id)
    assert executed.count("AwaitToolApproval") == 1
    assert executed.count("CallSingleAttemptTool") == 1
    assert executed.count("CallTool") == 1
    values = await harness.stack.client.get_attributes(
        session_id, FRAMEWORK_RUN_STATE, PENDING_INTERRUPTS
    )
    assert FRAMEWORK_RUN_STATE not in values
    assert values[PENDING_INTERRUPTS] == []


async def test_rejected_write_never_runs_and_the_model_sees_the_rejection(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "risky"}')
    interrupt = await harness.wait_for_interrupt(session_id, "publish")

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="not today"
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: not today"
    assert harness.invocations.count("publish") == 0
    assert "CallSingleAttemptTool" not in await harness.executed_step_types(session_id)


async def test_resolve_interrupt_returns_false_the_second_time(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "once"}')
    interrupt = await harness.wait_for_interrupt(session_id, "publish")

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    assert not await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="too late"
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published once"
    assert harness.invocations.count("publish") == 1


async def test_turn_with_two_tool_batches_keeps_every_result(harness: AdkAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call publish {"message": "first"} then call lookup {"query": "second"}',
    )
    first = await harness.wait_for_interrupt(session_id, "publish")
    assert await harness.sessions.resolve_interrupt(session_id, first.interrupt_id, approved=True)

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published first; lookup: found second"
    assert [
        message.tool_name for message in snapshot.messages if message.role is MessageRole.TOOL
    ] == ["publish", "lookup"]
    executed = await harness.executed_step_types(session_id)
    assert executed.count("RunModel") == 3
    assert executed.count("ResumeTurn") == 2
    assert harness.invocations.count("model") == 3


async def test_idempotent_write_runs_without_approval_with_its_idempotency_key(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call charge {"amount": 5}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    tool_call_id = snapshot.messages[1].tool_call_id
    assert snapshot.messages[-1].text == "charge: " + json.dumps(
        {"charged": 5, "idempotency_key": f"{session_id}/turn-1/{tool_call_id}"}, sort_keys=True
    )
    assert harness.invocations.count("charge") == 1

    assert await harness.sessions.send_user_message(session_id, 'call charge {"amount": -1}')
    failed = await harness.wait_for_turns(session_id, 2)
    assert failed.messages[-1].text == "charge: The tool call failed: amount must be positive"
    assert harness.invocations.count("charge") == 2


async def test_tool_that_requires_adk_confirmation_waits_for_approval(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call refund {"order_id": "7"}')
    interrupt = await harness.wait_for_interrupt(session_id, "refund")
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert [(call.effect, call.requires_approval) for call in waiting.tool_calls] == [
        (ToolEffect.IDEMPOTENT_WRITE, True)
    ]
    assert harness.invocations.count("refund") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "refund: refunded 7"
    assert harness.invocations.count("refund") == 1


async def test_agent_before_tool_callback_answers_a_call_without_a_tool_step(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, f'call publish {{"message": "{FORBIDDEN_MESSAGE}"}}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"publish: {GUARDRAIL_ERROR}"
    assert snapshot.usage.model_calls == 2
    assert harness.invocations.count("publish") == 0
    assert all(
        event.type != AgentEventType.INTERRUPT_RAISED
        for event in await harness.agent_events(session_id)
    )
    assert "CallSingleAttemptTool" not in await harness.executed_step_types(session_id)


async def test_sequential_agent_continues_with_its_next_agent_after_a_tool_step(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    sessions = harness.sessions_for(SCRIPTED_ADK_PIPELINE_FLOW_TYPE)
    await sessions.start_session(session_id, 'call publish {"message": "draft"}')
    interrupt = await harness.wait_for_interrupt(session_id, "publish", sessions)

    assert await sessions.resolve_interrupt(session_id, interrupt.interrupt_id, approved=True)
    snapshot = await harness.wait_for_turns(session_id, 1, sessions)
    texts = [message.text for message in snapshot.messages if message.text]
    assert texts[-2:] == ["publish: published draft", REVIEWER_ANSWER]
    authors = [
        json.loads(message.framework_item)["author"]
        for message in snapshot.messages
        if message.text
    ]
    assert authors[-2:] == ["drafter", "reviewer"]
    assert harness.invocations.count("publish") == 1


async def test_control_tool_beside_a_tool_step_fails_the_turn_without_running_tools(
    harness: AdkAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "x"} && call exit_loop {}'
    )
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "called exit_loop together with tools that need Dex Steps" in (
        failed.state.last_turn_error or ""
    )
    assert harness.invocations.count("publish") == 0
    assert [message.text for message in failed.messages] == [
        'call publish {"message": "x"} && call exit_loop {}'
    ]
