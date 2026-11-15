# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The bundled Claude Code CLI resumes a complete session after a turn ends early."""

from __future__ import annotations

import json
import re
from typing import Any

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TOOL_CALL_CANCELLED_BY_STEERING_RESULT,
    TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT,
)
from tests.claude_test_agent import (
    FAIL_FIRST_ATTEMPT_FILE_PREFIX,
    RUN_MODEL_OUTCOMES_LOG_NAME,
    WITHOUT_ENDED_TURN_ANSWERS_FILE_NAME,
)
from tests.conftest import RealClaudeCliHarness
from tests.fake_claude_cli import CONTINUE_PROMPT
from tests.older_workers import (
    OPEN_WRITE_PROMPT,
    cancel_on_an_older_worker,
    end_turn_on_an_older_worker,
)
from tests.transcript_assertions import (
    assert_no_turn_note,
    assert_unique_transcript_entries,
    deferrals_of,
    results_of,
)

CLI_MISSING_RESULT_TEXT = "Tool result missing"
INTERRUPTED_TOOL_USE_TEXT = "[Tool use interrupted]"


async def test_real_cli_resumes_a_complete_session_after_a_turn_steered_at_an_approval(
    real_cli_harness: RealClaudeCliHarness,
) -> None:
    harness = real_cli_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    command = 'call lookup {"query": "docs"} && call publish {"message": "draft"}'
    await harness.sessions.start_session(session_id, command)
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.tool_name == "publish"
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    # One assistant message holds both calls; the CLI deferred publish after it ran lookup.
    assert [(message.role, message.tool_name) for message in waiting.messages] == [
        ("user", None),
        ("assistant", None),
        ("tool", "lookup"),
    ]
    assert f'"id": "{interrupt.tool_call_id}"' in waiting.messages[1].framework_item
    assert waiting.messages[2].tool_output == "found docs"

    assert await harness.sessions.steer_user_message(session_id, text="recall")
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[: len(waiting.messages)] == waiting.messages
    steered_result = snapshot.messages[len(waiting.messages)]
    assert (
        steered_result.role,
        steered_result.tool_name,
        steered_result.tool_call_id,
        steered_result.tool_output,
    ) == ("tool", "publish", interrupt.tool_call_id, TOOL_CALL_CANCELLED_BY_STEERING_RESULT)
    assert [
        (message.role, message.text) for message in snapshot.messages[len(waiting.messages) + 1 :]
    ] == [
        ("user", "recall"),
        # Claude Code's own placeholder reply after a turn that ended at a tool result.
        ("assistant", "No response requested."),
        ("assistant", f"history: {command}"),
    ]
    assert_no_turn_note(snapshot.messages)
    assert_unique_transcript_entries(snapshot.messages)
    assert harness.invocations.count("publish") == 0
    assert harness.invocations.count("lookup") == 1
    # The next turn's request answers the abandoned call; the CLI neither resumed it nor prompted.
    [recall_request] = _requests_mentioning(harness, interrupt.tool_call_id, "recall")
    assert _result_after_tool_use(recall_request, interrupt.tool_call_id) == [
        (TOOL_CALL_CANCELLED_BY_STEERING_RESULT, True)
    ]
    assert CONTINUE_PROMPT not in json.dumps(recall_request["messages"])
    # The CLI's placeholder reply is no model call.
    assert snapshot.usage.model_calls == 2


async def test_real_cli_next_turn_answers_a_call_that_an_older_worker_left_open(
    real_cli_harness: RealClaudeCliHarness,
) -> None:
    harness = real_cli_harness
    session_id = harness.new_session_id()
    interrupt, ended = await cancel_on_an_older_worker(harness, session_id)

    await harness.stack.start_worker()
    assert await harness.sessions.send_user_message(session_id, "recall")
    snapshot = await harness.wait_for_turns(session_id, 2)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == f"history: {OPEN_WRITE_PROMPT}"
    answer = snapshot.messages[len(ended.messages) + 1]
    assert (answer.role, answer.turn_id, answer.tool_call_id, answer.tool_output) == (
        "tool",
        "turn-2",
        interrupt.tool_call_id,
        OUTCOME_UNKNOWN_TOOL_RESULT,
    )
    assert_unique_transcript_entries(snapshot.messages)
    # The CLI did not resume the call again: one deferral, and one stored result.
    assert deferrals_of(snapshot.messages, interrupt.tool_call_id) == 1
    assert results_of(snapshot.messages, interrupt.tool_call_id) == 1
    assert harness.invocations.count("publish") == 0
    # The model sees the unknown outcome right after the call, never the CLI's filler.
    [recall_request] = _requests_mentioning(harness, interrupt.tool_call_id, "recall")
    assert CLI_MISSING_RESULT_TEXT not in json.dumps(recall_request["messages"])
    assert _result_after_tool_use(recall_request, interrupt.tool_call_id) == [
        (OUTCOME_UNKNOWN_TOOL_RESULT, True)
    ]


async def test_real_cli_call_left_open_behind_a_later_turn_is_answered_after_its_tool_use(
    real_cli_harness: RealClaudeCliHarness,
) -> None:
    harness = real_cli_harness
    session_id = harness.new_session_id()
    interrupt, _ = await cancel_on_an_older_worker(harness, session_id)
    # Without ended-turn answers the CLI resumes the open call, which defers again.
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
    first_new_request = len(harness.messages_api.requests)

    await harness.stack.start_worker()
    fail_first_attempt_flag = harness.state_dir / f"{FAIL_FIRST_ATTEMPT_FILE_PREFIX}turn-3"
    fail_first_attempt_flag.touch()
    assert await harness.sessions.send_user_message(session_id, "recall")
    snapshot = await harness.wait_for_turns(session_id, 3)
    fail_first_attempt_flag.unlink()

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == f"history: {OPEN_WRITE_PROMPT}"
    answer = snapshot.messages[len(after_older_turn.messages) + 1]
    assert (answer.role, answer.turn_id, answer.tool_call_id, answer.tool_output) == (
        "tool",
        "turn-3",
        interrupt.tool_call_id,
        OUTCOME_UNKNOWN_TOOL_RESULT,
    )
    outcomes = [
        json.loads(line)
        for line in (harness.state_dir / RUN_MODEL_OUTCOMES_LOG_NAME).read_text().splitlines()
    ]
    assert [outcome["attempt"] for outcome in outcomes] == [1, 2]
    assert outcomes[1]["first_new_message"] == outcomes[0]["first_new_message"]
    assert results_of(snapshot.messages, interrupt.tool_call_id) == 1
    assert deferrals_of(snapshot.messages, interrupt.tool_call_id) == 2
    assert_unique_transcript_entries(snapshot.messages)
    assert harness.invocations.count("publish") == 0
    # Both attempts sent the model the answer right after the call, and never the CLI's filler.
    turn_three_requests = _requests_mentioning(
        harness, interrupt.tool_call_id, "recall", first_new_request
    )
    assert len(turn_three_requests) == 2
    for request in turn_three_requests:
        assert CLI_MISSING_RESULT_TEXT not in json.dumps(request["messages"])
        assert _result_after_tool_use(request, interrupt.tool_call_id) == [
            (OUTCOME_UNKNOWN_TOOL_RESULT, True)
        ]


async def test_real_cli_two_cancelled_calls_with_one_id_store_results_with_distinct_uuids(
    real_cli_harness: RealClaudeCliHarness,
) -> None:
    harness = real_cli_harness
    harness.messages_api.fixed_tool_use_id_suffix = "reusedacrossturns"
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "one"}')
    first = await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.cancel_turn(session_id)
    await harness.wait_for_turns(session_id, 1)
    assert await harness.sessions.send_user_message(session_id, 'call publish {"message": "two"}')
    second = await harness.wait_for_pending_interrupt(session_id)
    assert second.tool_call_id == first.tool_call_id
    assert await harness.sessions.cancel_turn(session_id)
    cancelled = await harness.wait_for_turns(session_id, 2)
    assert results_of(cancelled.messages, first.tool_call_id) == 2
    assert_unique_transcript_entries(cancelled.messages)
    first_new_request = len(harness.messages_api.requests)

    assert await harness.sessions.send_user_message(session_id, "recall")
    snapshot = await harness.wait_for_turns(session_id, 3)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert_unique_transcript_entries(snapshot.messages)
    [recall_request] = _requests_mentioning(
        harness, first.tool_call_id, "recall", first_new_request
    )
    assert CLI_MISSING_RESULT_TEXT not in json.dumps(recall_request["messages"])
    assert _result_after_tool_use(recall_request, first.tool_call_id) == [
        (TOOL_CALL_CANCELLED_BY_TURN_CANCELLATION_RESULT, True)
    ]
    # Claude Code 2.1.316 keeps one tool use per ID and shows a later one as interrupted text.
    assert INTERRUPTED_TOOL_USE_TEXT in _texts(recall_request)


async def test_real_cli_later_call_reusing_an_answered_id_is_answered_after_an_older_worker(
    real_cli_harness: RealClaudeCliHarness,
) -> None:
    harness = real_cli_harness
    harness.messages_api.fixed_tool_use_id_suffix = "reusedacrossturns"
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "one"}')
    first = await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.resolve_interrupt(session_id, first.interrupt_id, approved=True)
    await harness.wait_for_turns(session_id, 1)
    assert await harness.sessions.send_user_message(session_id, 'call publish {"message": "two"}')
    await harness.wait_for_pending_interrupt(session_id)
    ended = await end_turn_on_an_older_worker(harness, worker, session_id, 2)
    assert results_of(ended.messages, first.tool_call_id) == 1
    first_new_request = len(harness.messages_api.requests)

    await harness.stack.start_worker()
    assert await harness.sessions.send_user_message(session_id, "recall")
    snapshot = await harness.wait_for_turns(session_id, 3)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    assert not snapshot.pending_interrupts
    answer = snapshot.messages[len(ended.messages) + 1]
    assert (answer.turn_id, answer.tool_call_id, answer.tool_output) == (
        "turn-3",
        first.tool_call_id,
        OUTCOME_UNKNOWN_TOOL_RESULT,
    )
    assert results_of(snapshot.messages, first.tool_call_id) == 2
    assert_unique_transcript_entries(snapshot.messages)
    assert harness.invocations.count("publish") == 1
    [recall_request] = _requests_mentioning(
        harness, first.tool_call_id, "recall", first_new_request
    )
    assert CLI_MISSING_RESULT_TEXT not in json.dumps(recall_request["messages"])
    # The CLI resumed no deferral; the reused call shows as interrupted.
    assert [
        content for content, _ in _result_after_tool_use(recall_request, first.tool_call_id)
    ] == ["published one"]
    assert INTERRUPTED_TOOL_USE_TEXT in _texts(recall_request)


def _texts(request: dict[str, Any]) -> list[str]:
    return [
        str(block.get("text", ""))
        for message in request["messages"]
        for block in _content(message)
        if block.get("type") == "text"
    ]


def _requests_mentioning(
    harness: RealClaudeCliHarness, tool_use_id: str, command: str, first_request: int = 0
) -> list[dict[str, Any]]:
    """Return the model requests after ``first_request`` whose last prompt is ``command``."""
    return [
        request.body
        for request in harness.messages_api.requests[first_request:]
        if request.body.get("messages")
        and tool_use_id in json.dumps(request.body["messages"])
        and _last_prompt(request.body["messages"]) == command
    ]


def _last_prompt(messages: list[dict[str, Any]]) -> str | None:
    prompts: list[str] = []
    for message in messages:
        if message.get("role") != "user":
            continue
        content = message.get("content")
        blocks = (
            [{"type": "text", "text": content}] if isinstance(content, str) else _content(message)
        )
        if any(block.get("type") == "tool_result" for block in blocks):
            continue
        texts = [
            str(block.get("text", "")).strip() for block in blocks if block.get("type") == "text"
        ]
        prompts.extend(text for text in texts if text and not text.startswith(("<", "[Image")))
    return prompts[-1] if prompts else None


def _result_after_tool_use(request: dict[str, Any], tool_use_id: str) -> list[tuple[Any, Any]]:
    """Return the tool's results in the message right after its tool use, as (content, is_error)."""
    messages = request["messages"]
    tool_use_index = next(
        index
        for index, message in enumerate(messages)
        if any(
            block.get("type") == "tool_use" and block.get("id") == tool_use_id
            for block in _content(message)
        )
    )
    return [
        (_without_system_reminders(block.get("content")), block.get("is_error"))
        for block in _content(messages[tool_use_index + 1])
        if block.get("type") == "tool_result" and block.get("tool_use_id") == tool_use_id
    ]


def _content(message: dict[str, Any]) -> list[dict[str, Any]]:
    content = message.get("content")
    if not isinstance(content, list):
        return []
    return [block for block in content if isinstance(block, dict)]


def _without_system_reminders(content: object) -> object:
    # The real CLI may append reminders to the last tool result it sends.
    if isinstance(content, str):
        return re.sub(r"<system-reminder>.*?</system-reminder>", "", content, flags=re.S).strip()
    if isinstance(content, list):
        texts = [
            str(block.get("text", ""))
            for block in content
            if isinstance(block, dict)
            and block.get("type") == "text"
            and not str(block.get("text", "")).lstrip().startswith("<system-reminder>")
        ]
        return "\n".join(texts).strip()
    return content
