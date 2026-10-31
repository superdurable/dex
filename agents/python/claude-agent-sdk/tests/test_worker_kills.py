# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""SIGKILL the Worker, and with it the CLI process, at the exact moment under test."""

from __future__ import annotations

import json

from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT, MessageRole
from tests.claude_test_agent import SLOW_WRITE_GATE
from tests.conftest import ClaudeAgentHarness


async def test_worker_killed_during_a_single_attempt_write_records_outcome_unknown(
    harness: ClaudeAgentHarness,
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
    harness: ClaudeAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stall turn-gate")
    gate = harness.gate("turn-gate")
    await gate.wait_until_reached()
    assert (1, "thinking after found stall") in await harness.text_deltas(session_id)

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert [(message.role, message.text) for message in snapshot.messages] == [
        (MessageRole.USER, "stall turn-gate"),
        (MessageRole.ASSISTANT, None),
        (MessageRole.TOOL, None),
        (MessageRole.ASSISTANT, "stalled at turn-gate"),
    ]
    session_ids = {
        json.loads(message.framework_item)["session_id"] for message in snapshot.messages[1:]
    }
    assert len(session_ids) == 1
    assert harness.invocations.count("cli:new") == 2
    assert harness.invocations.count("lookup") == 2
    assert gate.reached_count() == 2
    assert (2, "thinking after found stall") in await harness.text_deltas(session_id)


async def test_worker_killed_while_resuming_reuses_the_recorded_result(
    harness: ClaudeAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "once"} ;; pause resume-gate'
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    gate = harness.gate("resume-gate")
    await gate.wait_until_reached()
    assert harness.invocations.count("publish") == 1

    worker.kill()
    gate.release()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "publish: published once"
    assert harness.invocations.count("publish") == 1
    assert harness.invocations.count("cli:resume") == 2
    assert gate.reached_count() == 2
