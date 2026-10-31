# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""SIGKILL the Worker child at the exact moment under test."""

from __future__ import annotations

import json

from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT, AgentEvent, AgentEventType, MessageRole
from dex_agents.testing import wait_until
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import SLOW_WRITE_GATE


async def test_worker_killed_during_a_single_attempt_write_records_outcome_unknown(
    harness: ScriptedAgentHarness,
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
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.tool_output for message in tool_results] == [OUTCOME_UNKNOWN_TOOL_RESULT]
    assert harness.invocations.count("slow_write") == 1
    assert gate.reached_count() == 1


async def test_worker_killed_during_run_agent_turn_reruns_the_segment(
    harness: ScriptedAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall turn-gate")
    gate = harness.gate("turn-gate")
    await gate.wait_until_reached()
    first_attempt_delta = await wait_until(
        "the first attempt's text delta",
        lambda: _text_delta(harness, session_id, attempt=1),
    )

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "stalled at turn-gate"
    assert [message.text for message in snapshot.messages] == [
        "stall turn-gate",
        "stalled at turn-gate",
    ]
    assert harness.invocations.count("run_turn:turn-1") == 2
    assert harness.invocations.count("lookup") == 2
    assert gate.reached_count() == 2
    second_attempt_delta = await _text_delta(harness, session_id, attempt=2)
    assert second_attempt_delta is not None
    assert first_attempt_delta.turn_id == second_attempt_delta.turn_id == "turn-1"


async def _text_delta(
    harness: ScriptedAgentHarness,
    session_id: str,
    attempt: int,
) -> AgentEvent | None:
    page = await harness.stack.client.list_stream_messages(
        session_id, harness.flow.agent_events, 100
    )
    for message in page.messages:
        event = message.value
        if event.type == AgentEventType.TEXT_DELTA and event.attempt == attempt:
            assert json.loads(event.payload)["text"] == "thinking after found stall"
            return event
    return None
