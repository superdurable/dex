# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A tool use ID that a later turn reuses names a new call, not the earlier one."""

from __future__ import annotations

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT,
    AgentInterrupt,
)
from dex_agents.testing import wait_for_agent_snapshot
from tests.conftest import ClaudeAgentHarness
from tests.fake_claude_cli import FIXED_TOOL_USE_ID_SUFFIX_FILE_NAME
from tests.older_workers import end_turn_on_an_older_worker
from tests.transcript_assertions import (
    assert_unique_transcript_entries,
    deferrals_of,
    results_of,
    stored_transcript_entries,
)

REUSED_TOOL_USE_ID = "toolu_0_reusedacrossturns"


def _reuse_tool_use_ids(harness: ClaudeAgentHarness) -> None:
    (harness.state_dir / FIXED_TOOL_USE_ID_SUFFIX_FILE_NAME).write_text("reusedacrossturns")


async def test_an_approved_call_reusing_an_earlier_turns_id_runs_and_resumes(
    harness: ClaudeAgentHarness,
) -> None:
    _reuse_tool_use_ids(harness)
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "one"}')
    first = await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.resolve_interrupt(session_id, first.interrupt_id, approved=True)
    await harness.wait_for_turns(session_id, 1)

    assert await harness.sessions.send_user_message(session_id, 'call publish {"message": "two"}')
    second = await harness.wait_for_pending_interrupt(session_id)
    assert second.tool_call_id == first.tool_call_id == REUSED_TOOL_USE_ID
    assert await harness.sessions.resolve_interrupt(session_id, second.interrupt_id, approved=True)
    snapshot = await harness.wait_for_turns(session_id, 2)

    # The resumed CLI gets the second call's own result, not a refusal for the first call's ID.
    assert snapshot.messages[-1].text == "publish: published two"
    assert harness.invocations.count("publish") == 2
    assert results_of(snapshot.messages, REUSED_TOOL_USE_ID) == 2
    assert_unique_transcript_entries(snapshot.messages)


async def test_two_cancelled_calls_with_one_id_get_results_with_distinct_uuids(
    harness: ClaudeAgentHarness,
) -> None:
    _reuse_tool_use_ids(harness)
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "one"}')
    await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.steer_user_message(
        session_id, text='call publish {"message": "two"}'
    )
    await _wait_for_interrupt_of_turn(harness, session_id, "turn-2")
    assert await harness.sessions.cancel_turn(session_id)
    cancelled = await harness.wait_for_turns(session_id, 2)

    results = [
        entry
        for entry in stored_transcript_entries(cancelled.messages)
        if any(
            isinstance(block, dict) and block.get("type") == "tool_result"
            for block in (entry.get("message") or {}).get("content") or []
        )
    ]
    assert [entry["message"]["content"][0]["content"] for entry in results] == [
        TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
        TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT,
    ]
    assert len({entry["uuid"] for entry in results}) == 2
    assert len({entry["sourceToolAssistantUUID"] for entry in results}) == 2
    assert_unique_transcript_entries(cancelled.messages)

    assert await harness.sessions.send_user_message(session_id, "say next")
    snapshot = await harness.wait_for_turns(session_id, 3)
    assert snapshot.messages[-1].text == "said: next"
    assert_unique_transcript_entries(snapshot.messages)
    assert harness.invocations.count("publish") == 0


async def test_a_later_call_reusing_an_answered_id_is_answered_after_an_older_worker_ends_it(
    harness: ClaudeAgentHarness,
) -> None:
    _reuse_tool_use_ids(harness)
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "one"}')
    first = await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.resolve_interrupt(session_id, first.interrupt_id, approved=True)
    await harness.wait_for_turns(session_id, 1)
    assert await harness.sessions.send_user_message(session_id, 'call publish {"message": "two"}')
    await harness.wait_for_pending_interrupt(session_id)
    ended = await end_turn_on_an_older_worker(harness, worker, session_id, 2)
    assert results_of(ended.messages, REUSED_TOOL_USE_ID) == 1

    await harness.stack.start_worker()
    assert await harness.sessions.send_user_message(session_id, "say next")
    snapshot = await harness.wait_for_turns(session_id, 3)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert not snapshot.pending_interrupts
    answer = snapshot.messages[len(ended.messages) + 1]
    assert (answer.turn_id, answer.tool_call_id, answer.tool_output) == (
        "turn-3",
        REUSED_TOOL_USE_ID,
        OUTCOME_UNKNOWN_TOOL_RESULT,
    )
    assert snapshot.messages[-1].text == "said: next"
    assert results_of(snapshot.messages, REUSED_TOOL_USE_ID) == 2
    assert_unique_transcript_entries(snapshot.messages)
    assert harness.invocations.count("publish") == 1


async def test_a_live_deferral_reusing_a_healed_calls_id_still_resumes(
    harness: ClaudeAgentHarness,
) -> None:
    _reuse_tool_use_ids(harness)
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "one"}')
    await harness.wait_for_pending_interrupt(session_id)
    ended = await end_turn_on_an_older_worker(harness, worker, session_id, 1)

    # The next turn answers the first call, then defers a new call under the same ID.
    await harness.stack.start_worker()
    assert await harness.sessions.send_user_message(session_id, 'call publish {"message": "two"}')
    second = await harness.wait_for_pending_interrupt(session_id)
    assert second.tool_call_id == REUSED_TOOL_USE_ID
    assert await harness.sessions.resolve_interrupt(session_id, second.interrupt_id, approved=True)
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[len(ended.messages) + 1].tool_output == OUTCOME_UNKNOWN_TOOL_RESULT
    assert snapshot.messages[-1].text == "publish: published two"
    assert harness.invocations.count("publish") == 1
    assert deferrals_of(snapshot.messages, REUSED_TOOL_USE_ID) == 2
    assert results_of(snapshot.messages, REUSED_TOOL_USE_ID) == 2
    assert_unique_transcript_entries(snapshot.messages)


async def _wait_for_interrupt_of_turn(
    harness: ClaudeAgentHarness, session_id: str, turn_id: str
) -> AgentInterrupt:
    # Interrupt IDs repeat with the tool use ID, so wait for the turn too.
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(iter(snapshot.pending_interrupts), None)
        if snapshot.state is not None and snapshot.state.active_turn_id == turn_id
        else None,
        description=f"a pending interrupt of {turn_id}",
    )
