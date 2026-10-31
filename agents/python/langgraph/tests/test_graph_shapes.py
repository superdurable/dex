# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Dynamic tools, tool body errors, subgraphs without checkpoints, and Worker clock skew."""

from __future__ import annotations

import pytest
from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT, AgentInterrupt, MessageRole, ToolEffect
from dex_agents.testing import wait_for_agent_snapshot

from tests.conftest import AGENT_FLOW_TYPES, ScriptedAgentHarness
from tests.scripted_agents import (
    CHECKPOINT_CLOCK_SKEW_ENVIRONMENT_VARIABLE,
    DYNAMIC_TOOL_CREATE_AGENT_FLOW_TYPE,
    DYNAMIC_TOOL_STATE_GRAPH_FLOW_TYPE,
    SHADOWED_TOOL_CREATE_AGENT_FLOW_TYPE,
    SHADOWED_TOOL_STATE_GRAPH_FLOW_TYPE,
    UNCHECKPOINTED_CREATE_AGENT_FLOW_TYPE,
    UNCHECKPOINTED_STATE_GRAPH_FLOW_TYPE,
)


@pytest.mark.parametrize(
    "agent_flow_type", [DYNAMIC_TOOL_CREATE_AGENT_FLOW_TYPE, DYNAMIC_TOOL_STATE_GRAPH_FLOW_TYPE]
)
async def test_a_dynamic_tool_passed_to_the_runner_runs_once_in_its_own_step(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call send_email {"to": "bob"}')
    [interrupt] = await harness.wait_for_pending_interrupts(session_id)
    # send_email has no policy, so the default unclassified effect asks for approval.
    assert interrupt.tool_name == "send_email"
    assert harness.tool_invocations() == []

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "send_email: emailed bob"
    assert harness.tool_invocations() == ["send_email"]
    assert "CallSingleAttemptTool" in await harness.executed_step_types(session_id)


@pytest.mark.parametrize(
    "agent_flow_type", [SHADOWED_TOOL_CREATE_AGENT_FLOW_TYPE, SHADOWED_TOOL_STATE_GRAPH_FLOW_TYPE]
)
async def test_the_tool_the_step_runs_decides_the_policy_of_a_shadowed_tool(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call create_ticket {"title": "t"}')
    [interrupt] = await harness.wait_for_pending_interrupts(session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    # The node's tool says readOnlyHint, but the Step runs the unannotated write from tools=.
    assert [
        (call.tool_name, call.effect, call.requires_approval) for call in waiting.tool_calls
    ] == [("create_ticket", ToolEffect.UNCLASSIFIED, True)]
    assert harness.tool_invocations() == []

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "create_ticket: support ticket t"
    assert harness.tool_invocations() == ["create_support_ticket"]
    executed = await harness.executed_step_types(session_id)
    assert executed.count("AwaitToolApproval") == 1
    assert executed.count("CallSingleAttemptTool") == 1


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_an_unknown_tool_fails_in_its_step_without_approval(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call send_email {"to": "bob"}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        "send_email: The tool call failed: the agent has no tool named send_email"
    )
    assert harness.tool_invocations() == []
    executed = await harness.executed_step_types(session_id)
    assert "AwaitToolApproval" not in executed
    assert "CallTool" in executed


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_a_validation_error_inside_a_tool_body_is_an_unknown_outcome(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call refund {"order_id": "o-1"} && call refund {"order": 7}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    [body_error, argument_error] = [
        message.tool_output for message in snapshot.messages if message.role is MessageRole.TOOL
    ]
    # The body raised after its write, so the outcome is unknown, not a model-visible failure.
    assert body_error == OUTCOME_UNKNOWN_TOOL_RESULT
    assert (argument_error or "").startswith("The tool call failed: invalid tool arguments:")
    assert harness.tool_invocations() == ["refund"]


@pytest.mark.parametrize(
    "agent_flow_type", [UNCHECKPOINTED_CREATE_AGENT_FLOW_TYPE, UNCHECKPOINTED_STATE_GRAPH_FLOW_TYPE]
)
async def test_a_subgraph_without_checkpoints_fails_the_turn_instead_of_repeating_tools(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call charge {"amount": 5}')
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "checkpointer=False" in (failed.state.last_turn_error or "")
    assert harness.tool_invocations() == ["charge"]
    # The restarted subgraph called the model again on each of the three RunModel attempts.
    assert harness.model_call_count() == 4
    [_, note] = failed.messages
    assert "- charge" in (note.text or "")


@pytest.mark.parametrize("agent_flow_type", AGENT_FLOW_TYPES)
async def test_a_worker_with_a_clock_behind_resumes_from_the_latest_checkpoint(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "a"} then call publish {"message": "b"}'
    )
    first = await _wait_for_interrupt_of(harness, session_id, "call-0-0-0")

    worker.kill()
    await harness.stack.start_worker({CHECKPOINT_CLOCK_SKEW_ENVIRONMENT_VARIABLE: "120"})
    assert await harness.sessions.resolve_interrupt(session_id, first.interrupt_id, approved=True)
    second = await _wait_for_interrupt_of(harness, session_id, "call-0-1-0")
    assert await harness.sessions.resolve_interrupt(session_id, second.interrupt_id, approved=True)

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == "publish: published a; publish: published b"
    assert harness.tool_invocations() == ["publish", "publish"]


async def _wait_for_interrupt_of(
    harness: ScriptedAgentHarness,
    session_id: str,
    tool_call_id: str,
) -> AgentInterrupt:
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(
            (
                interrupt
                for interrupt in snapshot.pending_interrupts
                if interrupt.tool_call_id == tool_call_id
            ),
            None,
        ),
        description=f"a pending interrupt for {tool_call_id}",
    )
