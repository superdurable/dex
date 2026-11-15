# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Claude agent turns store their transcript while they run and when they end early."""

from __future__ import annotations

import dataclasses
import json
from collections.abc import Sequence
from typing import Any, cast

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    AgentEventType,
    AgentMessage,
    AgentSessionStatus,
    AgentToolCall,
    AgentTurnContext,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
)
from dex_agents_claude_agent_sdk import ClaudeAgentRunner
from tests.claude_test_agent import (
    FAIL_FIRST_ATTEMPT_FILE_PREFIX,
    RUN_MODEL_OUTCOMES_LOG_NAME,
    WITHOUT_ENDED_TURN_ANSWERS_FILE_NAME,
)
from tests.conftest import ClaudeAgentHarness
from tests.older_workers import (
    cancel_on_an_older_worker,
    end_turn_on_an_older_worker,
    roles_of,
)
from tests.transcript_assertions import (
    assert_no_turn_note,
    assert_unique_transcript_entries,
    deferrals_of,
    results_of,
    stored_transcript_entries,
)

LOOKUP_THEN_PUBLISH = 'call lookup {"query": "a"} && call publish {"message": "b"}'
# Like the real CLI, the fake one sends both calls in one API message, so one stored message.
TURN_ONE_UNTIL_DEFERRAL = [
    (MessageRole.USER, "turn-1", None),
    (MessageRole.ASSISTANT, "turn-1", None),
    (MessageRole.TOOL, "turn-1", "lookup"),
]
PUBLISH_RESULT_INDEX = len(TURN_ONE_UNTIL_DEFERRAL)


async def test_a_deferred_call_stores_the_turn_so_far_and_completion_stores_no_entry_twice(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, LOOKUP_THEN_PUBLISH)
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert waiting.state is not None
    assert waiting.state.status is AgentSessionStatus.RUNNING_TOOLS
    assert roles_of(waiting.messages) == TURN_ONE_UNTIL_DEFERRAL
    assert waiting.messages[2].tool_output == "found a"
    assert [
        block["input"]
        for entry in stored_transcript_entries(waiting.messages[1:2])
        for block in entry.get("message", {}).get("content", [])
        if isinstance(block, dict) and block.get("id") == interrupt.tool_call_id
    ] == [{"message": "b"}]

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[: len(waiting.messages)] == waiting.messages
    assert roles_of(snapshot.messages) == [
        *TURN_ONE_UNTIL_DEFERRAL,
        (MessageRole.TOOL, "turn-1", "publish"),
        (MessageRole.ASSISTANT, "turn-1", None),
    ]
    assert snapshot.messages[PUBLISH_RESULT_INDEX].tool_call_id == interrupt.tool_call_id
    assert snapshot.messages[PUBLISH_RESULT_INDEX].tool_output == "published b"
    assert snapshot.messages[-1].text == "lookup: found a; publish: published b"
    assert_unique_transcript_entries(snapshot.messages)
    assert harness.invocations.names() == [
        "cli:new",
        "model_call",
        "lookup",
        "publish",
        "cli:resume",
        "cli:auto_resume",
        "model_call",
    ]


async def test_a_turn_steered_at_an_approval_stores_the_steering_result_instead_of_a_note(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, LOOKUP_THEN_PUBLISH)
    interrupt = await harness.wait_for_pending_interrupt(session_id)

    assert await harness.sessions.steer_user_message(session_id, text="say after")
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert roles_of(snapshot.messages) == [
        *TURN_ONE_UNTIL_DEFERRAL,
        (MessageRole.TOOL, "turn-1", "publish"),
        (MessageRole.USER, "turn-2", None),
        (MessageRole.ASSISTANT, "turn-2", None),
    ]
    steered_result = snapshot.messages[PUBLISH_RESULT_INDEX]
    assert steered_result.tool_call_id == interrupt.tool_call_id
    assert steered_result.tool_output == TOOL_CALL_CANCELLED_BY_STEERING_RESULT
    assert snapshot.messages[-1].text == "said: after"
    assert_no_turn_note(snapshot.messages)
    assert_unique_transcript_entries(snapshot.messages)
    # The next turn's CLI finds no pending deferral, so it runs only the new prompt.
    assert harness.invocations.names() == [
        "cli:new",
        "model_call",
        "lookup",
        "cli:resume",
        "model_call",
    ]
    [steered_event] = [
        event
        for event in await harness.list_events(session_id)
        if event.type == AgentEventType.TURN_STEERED
    ]
    assert json.loads(steered_event.payload) == {
        "message_count": PUBLISH_RESULT_INDEX,
        "steered_message_count": 1,
    }


async def test_a_turn_cancelled_while_the_resumed_cli_runs_stores_the_recorded_write_result(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, f"{LOOKUP_THEN_PUBLISH} ;; pause resumed")
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    await harness.gate("resumed").wait_until_reached()

    assert await harness.sessions.cancel_turn(session_id)
    cancelled = await harness.wait_for_turns(session_id, 1)

    # The cancelled segment stored nothing; the call's result comes from its record.
    assert roles_of(cancelled.messages) == [
        *TURN_ONE_UNTIL_DEFERRAL,
        (MessageRole.TOOL, "turn-1", "publish"),
    ]
    assert cancelled.messages[PUBLISH_RESULT_INDEX].tool_call_id == interrupt.tool_call_id
    assert cancelled.messages[PUBLISH_RESULT_INDEX].tool_output == "published b"
    assert_no_turn_note(cancelled.messages)

    assert await harness.sessions.send_user_message(session_id, "say next")
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert snapshot.messages[-1].text == "said: next"
    assert_unique_transcript_entries(snapshot.messages)
    assert harness.invocations.count("publish") == 1
    assert harness.invocations.count("cli:resume") == 2


async def test_a_run_state_of_an_earlier_adapter_is_stored_before_the_abandoned_result(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, LOOKUP_THEN_PUBLISH)
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    [record] = waiting.tool_calls
    rejected = dataclasses.replace(
        record, status=ToolCallStatus.REJECTED, rejection_message="not now"
    )
    claude_session_id = json.loads(waiting.messages[1].framework_item)["session_id"]
    runner = cast(ClaudeAgentRunner, harness.flow.runner)

    # This adapter stored the turn so far; an earlier one kept the same entries in the run state.
    stored = runner.build_unfinished_turn_messages(
        _ended_turn_context(
            waiting.messages,
            json.dumps({"version": 1, "session_id": claude_session_id, "turn_entries": []}),
            [rejected],
        )
    )
    legacy_run_state = json.dumps(
        {
            "version": 1,
            "session_id": claude_session_id,
            "turn_entries": stored_transcript_entries(waiting.messages),
        }
    )
    legacy = runner.build_unfinished_turn_messages(
        _ended_turn_context(waiting.messages[:1], legacy_run_state, [rejected])
    )

    assert [
        (message.role, message.tool_name, message.tool_call_id, message.tool_output)
        for message in stored
    ] == [(MessageRole.TOOL, "publish", interrupt.tool_call_id, "not now")]
    assert [(message.role, message.tool_name) for message in legacy] == [
        *[(role, tool_name) for role, _, tool_name in TURN_ONE_UNTIL_DEFERRAL[1:]],
        (MessageRole.TOOL, "publish"),
    ]
    assert (legacy[-1].tool_call_id, legacy[-1].tool_output) == (
        interrupt.tool_call_id,
        "not now",
    )
    # Message boundaries may differ, since trailing entries ride along; entries do not.
    assert stored_transcript_entries(legacy) == stored_transcript_entries(
        [*waiting.messages[1:], *stored]
    )
    # The same committed state always gives the same messages.
    assert (
        runner.build_unfinished_turn_messages(
            _ended_turn_context(waiting.messages[:1], legacy_run_state, [rejected])
        )
        == legacy
    )


async def test_the_next_turn_answers_a_call_that_a_worker_without_unfinished_messages_left_open(
    harness: ClaudeAgentHarness,
) -> None:
    session_id = harness.new_session_id()
    interrupt, ended = await cancel_on_an_older_worker(harness, session_id)

    await harness.stack.start_worker()
    assert await harness.sessions.send_user_message(session_id, "say next")
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert roles_of(snapshot.messages) == [
        *roles_of(ended.messages),
        (MessageRole.USER, "turn-2", None),
        (MessageRole.TOOL, "turn-2", "publish"),
        (MessageRole.ASSISTANT, "turn-2", None),
    ]
    assert snapshot.messages[4].tool_call_id == interrupt.tool_call_id
    assert snapshot.messages[4].tool_output == OUTCOME_UNKNOWN_TOOL_RESULT
    assert snapshot.messages[-1].text == "said: next"
    assert_unique_transcript_entries(snapshot.messages)
    # The CLI found no pending deferral, so it ran only the new prompt.
    assert harness.invocations.names() == ["cli:new", "model_call", "cli:resume", "model_call"]

    # Later turns find every tool use answered and add nothing.
    assert await harness.sessions.send_user_message(session_id, "say again")
    later = await harness.wait_for_turns(session_id, 3)
    assert roles_of(later.messages[len(snapshot.messages) :]) == [
        (MessageRole.USER, "turn-3", None),
        (MessageRole.ASSISTANT, "turn-3", None),
    ]
    assert_unique_transcript_entries(later.messages)


async def test_a_call_left_open_behind_a_later_turn_transcript_is_answered_in_place(
    harness: ClaudeAgentHarness,
) -> None:
    session_id = harness.new_session_id()
    interrupt, _ = await cancel_on_an_older_worker(harness, session_id)
    # A RunModel without ended-turn answers lets the CLI resume the open call, which defers again.
    worker = await harness.stack.start_worker()
    without_answers_flag = harness.state_dir / WITHOUT_ENDED_TURN_ANSWERS_FILE_NAME
    without_answers_flag.touch()
    assert await harness.sessions.send_user_message(session_id, "say second")
    reopened = await harness.wait_for_pending_interrupt(session_id)
    without_answers_flag.unlink()
    assert reopened.tool_call_id == interrupt.tool_call_id
    after_older_turn = await end_turn_on_an_older_worker(harness, worker, session_id, 2)
    assert deferrals_of(after_older_turn.messages, interrupt.tool_call_id) == 2
    assert results_of(after_older_turn.messages, interrupt.tool_call_id) == 0

    await harness.stack.start_worker()
    fail_first_attempt_flag = harness.state_dir / f"{FAIL_FIRST_ATTEMPT_FILE_PREFIX}turn-3"
    fail_first_attempt_flag.touch()
    assert await harness.sessions.send_user_message(session_id, "say third")
    snapshot = await harness.wait_for_turns(session_id, 3)
    fail_first_attempt_flag.unlink()

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert roles_of(snapshot.messages[len(after_older_turn.messages) :]) == [
        (MessageRole.USER, "turn-3", None),
        (MessageRole.TOOL, "turn-3", "publish"),
        (MessageRole.ASSISTANT, "turn-3", None),
    ]
    answer = snapshot.messages[len(after_older_turn.messages) + 1]
    assert answer.tool_call_id == interrupt.tool_call_id
    assert answer.tool_output == OUTCOME_UNKNOWN_TOOL_RESULT
    assert snapshot.messages[-1].text == "said: third"
    # The retried attempt built the same answer, and the session stored it once.
    outcomes = [
        json.loads(line)
        for line in (harness.state_dir / RUN_MODEL_OUTCOMES_LOG_NAME).read_text().splitlines()
    ]
    assert [outcome["attempt"] for outcome in outcomes] == [1, 2]
    assert outcomes[1]["first_new_message"] == outcomes[0]["first_new_message"]
    assert outcomes[0]["first_new_message"]["framework_item"] == answer.framework_item
    assert results_of(snapshot.messages, interrupt.tool_call_id) == 1
    assert deferrals_of(snapshot.messages, interrupt.tool_call_id) == 2
    assert_unique_transcript_entries(snapshot.messages)
    assert harness.invocations.names()[-4:] == [
        "cli:resume",
        "model_call",
        "cli:resume",
        "model_call",
    ]
    assert harness.invocations.count("publish") == 0

    assert await harness.sessions.send_user_message(session_id, "say fourth")
    later = await harness.wait_for_turns(session_id, 4)
    assert roles_of(later.messages[len(snapshot.messages) :]) == [
        (MessageRole.USER, "turn-4", None),
        (MessageRole.ASSISTANT, "turn-4", None),
    ]
    assert deferrals_of(later.messages, interrupt.tool_call_id) == 2
    assert_unique_transcript_entries(later.messages)


async def test_an_approved_cli_tool_whose_resumed_turn_was_cancelled_has_an_unknown_outcome(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call Bash {"command": "ls"} ;; pause resumed')
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.tool_name == "Bash"
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    await harness.gate("resumed").wait_until_reached()

    assert await harness.sessions.cancel_turn(session_id)
    cancelled = await harness.wait_for_turns(session_id, 1)

    # The resumed CLI ran Bash before the cancellation dropped its segment.
    assert harness.invocations.count("Bash") == 1
    assert roles_of(cancelled.messages) == [
        (MessageRole.USER, "turn-1", None),
        (MessageRole.ASSISTANT, "turn-1", "Bash"),
        (MessageRole.TOOL, "turn-1", "Bash"),
    ]
    assert cancelled.messages[2].tool_call_id == interrupt.tool_call_id
    assert cancelled.messages[2].tool_output == OUTCOME_UNKNOWN_TOOL_RESULT
    assert_no_turn_note(cancelled.messages)
    assert_unique_transcript_entries(cancelled.messages)


def _ended_turn_context(
    messages: Sequence[AgentMessage], framework_run_state: str, tool_calls: list[AgentToolCall]
) -> AgentTurnContext:
    # Only the members build_unfinished_turn_messages reads are real.
    return AgentTurnContext(
        flow_id="session-under-test",
        turn_id="turn-1",
        attempt=1,
        messages=messages,
        framework_run_state=framework_run_state,
        tool_calls={tool_call.tool_call_id: tool_call for tool_call in tool_calls},
        tool_policies={},
        default_tool_effect=ToolEffect.UNCLASSIFIED,
        events=cast(Any, None),
        heartbeat=cast(Any, None),
    )
