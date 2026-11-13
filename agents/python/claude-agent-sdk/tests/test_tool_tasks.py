# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Tool tasks of a tool source: the session polls them and the resumed CLI gets the result."""

from __future__ import annotations

import json
from typing import Any

from claude_agent_sdk import ClaudeAgentOptions

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TURN_CANCELLED_NOTE_HEADER,
    AgentEventType,
    AgentToolCall,
    ToolCallStatus,
    ToolTaskCanceller,
    ToolTaskPoller,
)
from dex_agents.testing import wait_for_agent_snapshot
from dex_agents_claude_agent_sdk import ClaudeAgentRunner
from tests.conftest import ClaudeAgentHarness


def _call_report(arguments: dict[str, Any]) -> str:
    return f"call report {json.dumps(arguments)}"


def test_the_runner_polls_and_cancels_tool_tasks() -> None:
    runner = ClaudeAgentRunner(lambda context: ClaudeAgentOptions())
    assert isinstance(runner, ToolTaskPoller)
    assert isinstance(runner, ToolTaskCanceller)


async def test_a_source_task_is_polled_until_its_result_reaches_the_resumed_session(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _call_report({"topic": "q1", "polls": 3}) + " ;; pause held"
    )
    record = await _held_record(harness, session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "report: report on q1"
    assert (record.status, record.output) == (ToolCallStatus.SUCCEEDED, "report on q1")
    task_id = f"task-{record.tool_call_id}"
    assert record.task_id == task_id
    assert _task_invocations(harness) == [
        f"start {task_id}",
        f"poll {task_id} 1",
        f"poll {task_id} 2",
        f"poll {task_id} 3",
    ]
    steps = await harness.executed_step_types(session_id)
    assert steps.count("PollToolTask") == 3
    assert steps[-1] == "RunModel"


async def test_a_failed_task_records_the_call_failed(harness: ClaudeAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _call_report({"topic": "q2", "polls": 2, "fail": True}) + " ;; pause held"
    )
    record = await _held_record(harness, session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert record.status is ToolCallStatus.FAILED
    assert snapshot.messages[-1].text == ("report: The tool call failed: the report on q2 failed")


async def test_cancel_turn_cancels_the_source_task_and_the_next_turn_answers(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, _call_report({"topic": "q3", "hold": True}))
    waiting = await _wait_for_tool_call(harness, session_id, ToolCallStatus.WAITING_FOR_TASK)
    task_id = f"task-{waiting.tool_call_id}"

    assert await harness.sessions.cancel_turn(session_id)
    snapshot = await harness.wait_for_turns(session_id, 1)
    note = snapshot.messages[-1].text or ""
    assert note.startswith(TURN_CANCELLED_NOTE_HEADER)
    assert f"-> outcome_unknown: {OUTCOME_UNKNOWN_TOOL_RESULT}" in note
    # The source sees the abandoned record with its task ID and cancellation reason.
    assert harness.invocations.count(f"cancel-task {task_id} waiting_for_task turn_cancelled") == 1
    finished = [
        json.loads(event.payload)
        for event in await harness.list_events(session_id)
        if event.type == AgentEventType.TOOL_CALL_FINISHED
    ]
    assert [payload["status"] for payload in finished] == ["outcome_unknown"]
    assert "CancelToolTask" in await harness.executed_step_types(session_id)

    assert await harness.sessions.send_user_message(session_id, "say after")
    after = await harness.wait_for_turns(session_id, 2)
    assert after.messages[-1].text == "said: after"


def _task_invocations(harness: ClaudeAgentHarness) -> list[str]:
    return [
        name
        for name in harness.invocations.names()
        if name.startswith(("start task-", "poll task-", "cancel-task "))
    ]


async def _held_record(harness: ClaudeAgentHarness, session_id: str) -> AgentToolCall:
    """Return the turn's only record while the final answer waits at the ``held`` gate."""
    await harness.gate("held").wait_until_reached()
    snapshot = await harness.sessions.get_agent_snapshot(session_id)
    harness.gate("held").release()
    [record] = snapshot.tool_calls
    return record


async def _wait_for_tool_call(
    harness: ClaudeAgentHarness, session_id: str, status: ToolCallStatus
) -> AgentToolCall:
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(
            (tool_call for tool_call in snapshot.tool_calls if tool_call.status is status), None
        ),
        description=f"a tool call {status}",
    )
