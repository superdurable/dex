# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A graceful complete request ends a session at its next wait for a user message."""

from __future__ import annotations

from dex import FlowStatus, StopFlowOptions, StopType

from dex_agents import MessageRole
from dex_agents.testing import FileGate, wait_until
from tests.conftest import ScriptedAgentHarness


async def test_a_tool_that_stops_its_own_session_completes_it_after_the_turn(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "call complete_session {}")

    assert await _wait_for_closed_status(harness, session_id) is FlowStatus.COMPLETED
    snapshot = await harness.sessions.get_agent_snapshot(session_id)
    assert snapshot.state is not None
    assert snapshot.state.turn_count == 1
    assert snapshot.state.active_turn_id is None
    assert [(message.role, message.tool_name) for message in snapshot.messages] == [
        (MessageRole.USER, None),
        (MessageRole.ASSISTANT, "complete_session"),
        (MessageRole.TOOL, "complete_session"),
        (MessageRole.ASSISTANT, None),
    ]
    tool_result, final_answer = snapshot.messages[-2:]
    assert tool_result.tool_output is not None
    assert f"completing {session_id}" in tool_result.tool_output
    assert final_answer.text is not None
    assert final_answer.text.startswith("complete_session: ")


async def test_a_message_queued_before_the_request_still_runs_its_turn(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    gate = FileGate(harness.state_dir, "graceful-first-turn")
    await harness.sessions.start_session(session_id, "stall graceful-first-turn")
    await gate.wait_until_reached()

    assert await harness.sessions.send_user_message(session_id, "say queued")
    await harness.stack.client.stop_flow(
        session_id, StopFlowOptions(type=StopType.GRACEFUL_COMPLETE)
    )
    gate.release()

    assert await _wait_for_closed_status(harness, session_id) is FlowStatus.COMPLETED
    snapshot = await harness.sessions.get_agent_snapshot(session_id)
    assert snapshot.state is not None
    assert snapshot.state.turn_count == 2
    assert snapshot.queued_message_count == 0
    assert [(message.role, message.text) for message in snapshot.messages] == [
        (MessageRole.USER, "stall graceful-first-turn"),
        (MessageRole.ASSISTANT, "stalled at graceful-first-turn"),
        (MessageRole.USER, "say queued"),
        (MessageRole.ASSISTANT, "said: queued"),
    ]


async def test_without_a_request_the_session_keeps_waiting_for_messages(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "say one")
    await harness.wait_for_turns(session_id, 1)
    assert (await harness.stack.client.describe_flow(session_id)).status is FlowStatus.RUNNING

    assert await harness.sessions.send_user_message(session_id, "say two")
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert snapshot.messages[-1].text == "said: two"
    assert (await harness.stack.client.describe_flow(session_id)).status is FlowStatus.RUNNING


async def _wait_for_closed_status(harness: ScriptedAgentHarness, session_id: str) -> FlowStatus:
    async def read_closed_status() -> FlowStatus | None:
        info = await harness.stack.client.describe_flow(session_id)
        return None if info.status is FlowStatus.RUNNING else info.status

    return await wait_until("the session Flow to close", read_closed_status)
