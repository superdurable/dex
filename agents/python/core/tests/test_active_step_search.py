# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A session is searchable by the Step it waits in, and leaves the search when it moves on."""

from __future__ import annotations

from dex import ActiveStepSearchMode, FlowConfig, StartFlowOptions, StepDurability

from dex_agents import (
    AGENT_SESSION_ACTIVE_STEP_SEARCH_MODE,
    AgentInterrupt,
    AgentSessionStartInput,
)
from dex_agents.testing import wait_for_agent_snapshot, wait_until
from tests.conftest import ScriptedAgentHarness

APPROVAL_QUERY = 'ActiveStepTypes = "AwaitToolApproval"'
QUESTION_QUERY = 'ActiveStepTypes = "AwaitQuestionAnswer"'
IDLE_QUERY = 'ActiveStepTypes = "AwaitUserMessage"'
MODEL_QUERY = 'ActiveStepTypes = "RunModel"'


async def test_a_session_is_found_while_it_waits_for_approval_and_leaves_when_it_moves_on(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "publish launch notes")
    interrupt = await _wait_for_interrupt(harness, session_id)

    await _wait_for_search_match(harness, APPROVAL_QUERY, session_id, is_expected=True)
    assert not await _is_found(harness, QUESTION_QUERY, session_id)

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    await harness.wait_for_turns(session_id, 1)
    await _wait_for_search_match(harness, APPROVAL_QUERY, session_id, is_expected=False)
    await _wait_for_search_match(harness, IDLE_QUERY, session_id, is_expected=True)


async def test_a_session_is_found_while_it_waits_for_a_question_and_leaves_when_answered(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "ask-strict Which color? | red | blue")
    question = await _wait_for_interrupt(harness, session_id)

    await _wait_for_search_match(harness, QUESTION_QUERY, session_id, is_expected=True)
    assert not await _is_found(harness, APPROVAL_QUERY, session_id)

    assert await harness.sessions.answer_question(session_id, question.interrupt_id, "red")
    await harness.wait_for_turns(session_id, 1)
    await _wait_for_search_match(harness, QUESTION_QUERY, session_id, is_expected=False)


async def test_a_step_with_no_wait_is_left_out_so_a_model_call_does_not_rewrite_the_entry(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall model-gate")
    gate = harness.gate("model-gate")
    await gate.wait_until_reached()

    assert not await _is_found(harness, MODEL_QUERY, session_id)
    assert not await _is_found(harness, IDLE_QUERY, session_id)

    gate.release()
    await harness.wait_for_turns(session_id, 1)
    await _wait_for_search_match(harness, IDLE_QUERY, session_id, is_expected=True)


async def test_an_application_chooses_the_mode_and_other_config_keeps_the_default_mode(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    disabled_id = harness.new_session_id()
    await harness.sessions.start_session(
        disabled_id,
        "publish launch notes",
        StartFlowOptions(
            config_override=FlowConfig(active_step_search_mode=ActiveStepSearchMode.DISABLED)
        ),
    )
    await _wait_for_interrupt(harness, disabled_id)

    kept_id = harness.new_session_id()
    await harness.sessions.start_session(
        kept_id,
        "publish launch notes",
        StartFlowOptions(config_override=FlowConfig(step_durability=StepDurability.SYNC)),
    )
    await _wait_for_interrupt(harness, kept_id)

    await _wait_for_search_match(harness, APPROVAL_QUERY, kept_id, is_expected=True)
    assert not await _is_found(harness, APPROVAL_QUERY, disabled_id)


async def test_update_flow_config_turns_the_search_on_for_a_session_already_waiting(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    # As a session started on 0.2.0: plain start_flow keeps the server's disabled default.
    await harness.stack.client.start_flow(
        harness.flow,
        session_id,
        AgentSessionStartInput(initial_message="publish launch notes"),
    )
    interrupt = await _wait_for_interrupt(harness, session_id)
    assert not await _is_found(harness, APPROVAL_QUERY, session_id)

    await harness.stack.client.update_flow_config(
        session_id, FlowConfig(active_step_search_mode=AGENT_SESSION_ACTIVE_STEP_SEARCH_MODE)
    )

    await _wait_for_search_match(harness, APPROVAL_QUERY, session_id, is_expected=True)
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    await harness.wait_for_turns(session_id, 1)
    await _wait_for_search_match(harness, APPROVAL_QUERY, session_id, is_expected=False)


async def _wait_for_interrupt(harness: ScriptedAgentHarness, session_id: str) -> AgentInterrupt:
    snapshot = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda current: current if current.pending_interrupts else None,
        description="a pending interrupt",
    )
    return snapshot.pending_interrupts[0]


async def _wait_for_search_match(
    harness: ScriptedAgentHarness, query: str, session_id: str, *, is_expected: bool
) -> None:
    async def matches() -> bool | None:
        return True if await _is_found(harness, query, session_id) is is_expected else None

    await wait_until(f"{query} to {'find' if is_expected else 'leave'} {session_id}", matches)


async def _is_found(harness: ScriptedAgentHarness, query: str, session_id: str) -> bool:
    page = await harness.stack.client.search_flows(query, 100)
    return any(entry.flow_id == session_id for entry in page.flows)
