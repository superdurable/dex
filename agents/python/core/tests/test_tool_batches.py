# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Turns whose model asks for tools twice, in two batches."""

from __future__ import annotations

import json

from dex_agents import (
    FRAMEWORK_RUN_STATE,
    TURN_FAILURE_NOTE_FOOTER,
    TURN_FAILURE_NOTE_HEADER,
    MessageRole,
    ToolCallStatus,
)
from dex_agents.testing import wait_for_agent_snapshot
from tests.conftest import ScriptedAgentHarness


async def test_a_turn_with_two_tool_batches_runs_both_and_resumes_twice(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'chain lookup {"query": "a"} && charge {"amount": 2}'
        ' then publish {"message": "p"} && lookup {"query": "b"}',
    )
    waiting = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot if snapshot.pending_interrupts else None,
        description="the second batch's approval",
    )
    assert [(call.tool_call_id, call.deferral_order) for call in waiting.tool_calls] == [
        ("turn-1-a-0", 0),
        ("turn-1-a-1", 1),
        ("turn-1-b-0", 2),
        ("turn-1-b-1", 3),
    ]
    assert [call.status for call in waiting.tool_calls[:2]] == [ToolCallStatus.SUCCEEDED] * 2
    assert waiting.tool_calls[2].status is ToolCallStatus.AWAITING_APPROVAL
    values = await harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE)
    run_state = json.loads(values[FRAMEWORK_RUN_STATE])
    assert run_state["batches"] == 2
    assert len(run_state["calls"]) == 4

    assert await harness.sessions.resolve_interrupt(
        session_id, waiting.pending_interrupts[0].interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    key = f"{session_id}/turn-1/turn-1-a-1"
    assert snapshot.messages[-1].text == (
        f"lookup: found a; charge: charged 2 with key {key}; publish: published p; lookup: found b"
    )
    assert [
        message.tool_call_id for message in snapshot.messages if message.role is MessageRole.TOOL
    ] == [
        "turn-1-a-0",
        "turn-1-a-1",
        "turn-1-b-0",
        "turn-1-b-1",
    ]
    assert snapshot.usage.model_calls == 3
    executed = await harness.executed_step_types(session_id)
    assert executed.count("RunModel") == 3
    assert executed.count("ResumeTurn") == 2
    assert executed.count("CallTool") == 3
    assert executed.count("CallSingleAttemptTool") == 1
    assert executed[-1] == "RunModel"
    assert [name for name in harness.invocations.names() if name.startswith("model:")] == [
        "model:turn-1:1",
        "model:turn-1:2",
        "model:turn-1:3",
    ]
    values = await harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE)
    assert FRAMEWORK_RUN_STATE not in values


async def test_a_turn_that_fails_after_two_batches_notes_every_tool_call(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'chain lookup {"query": "c"} then charge {"amount": 3} then fail'
    )
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "scripted failure after two tool batches" in (failed.state.last_turn_error or "")
    key = f"{session_id}/turn-1/turn-1-b-0"
    assert failed.messages[-1].text == "\n".join(
        [
            TURN_FAILURE_NOTE_HEADER,
            '- lookup {"query": "c"} -> succeeded: found c',
            f'- charge {{"amount": 3}} -> succeeded: charged 3 with key {key}',
            TURN_FAILURE_NOTE_FOOTER,
        ]
    )
    assert harness.invocations.count("charge") == 1
    assert failed.tool_calls == []


async def test_a_second_batch_that_repeats_a_tool_call_id_fails_the_turn(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'chain-reusing-ids lookup {"query": "d"} then lookup {"query": "e"}'
    )
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "turn-1-a-0 was already requested in this turn" in (failed.state.last_turn_error or "")
    assert failed.messages[-1].text == "\n".join(
        [
            TURN_FAILURE_NOTE_HEADER,
            '- lookup {"query": "d"} -> succeeded: found d',
            TURN_FAILURE_NOTE_FOOTER,
        ]
    )
    assert harness.invocations.count("lookup") == 1
    assert harness.invocations.count("model:turn-1:2") == 3
