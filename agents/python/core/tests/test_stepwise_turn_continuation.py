# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Built-in loop ``decide_turn_continuation``: end after tools and continue after an answer."""

from __future__ import annotations

import json
from typing import Any

import pytest

from dex_agents import AgentEventType, AgentMessage, MessageRole
from dex_agents.builtin_loop import (
    MAX_CONSECUTIVE_TURN_CONTINUATIONS,
    TURN_CONTINUATION_LIMIT_ERROR,
)
from dex_agents.testing import EXECUTE_COMPLETED_EVENT
from dex_agents.ui_stream import read_agent_event_stream_snapshot
from tests.conftest import ScriptedAgentHarness
from tests.stepwise_agent import (
    COMPACTION_WINDOW_ENVIRONMENT_VARIABLE,
    LONG_DRAFT_COUNT,
    TURN_CONTINUATION_HOOK_ENVIRONMENT_VARIABLE,
)

WITH_HOOK = {TURN_CONTINUATION_HOOK_ENVIRONMENT_VARIABLE: "1"}
COMPACTION_WINDOW_TOKENS = 1000


async def test_end_after_a_tool_completes_the_turn_without_a_model_call(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker(WITH_HOOK)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "lookup end-after-tools-1")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    assert [_describe(message) for message in snapshot.messages] == [
        "1 user lookup end-after-tools-1",
        "2 assistant lookup_order",
        "3 tool lookup_order found end-after-tools-1",
    ]
    assert snapshot.usage.model_calls == 1
    assert list(snapshot.tool_calls) == []
    assert harness.invocations.count("model:turn-1:1") == 1
    assert harness.invocations.count("model:turn-1:2") == 0
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]
    events = await harness.agent_events(session_id)
    assert _payloads(events, AgentEventType.TURN_COMPLETED) == [{"message_count": 2}]
    assert _payloads(events, AgentEventType.TURN_FAILED) == []
    assert _payloads(events, AgentEventType.USAGE_RECORDED)[-1]["recorded"]["model_calls"] == 0
    # The browser's opening snapshot shows a finished turn whose last message is the tool result.
    opening = (
        await read_agent_event_stream_snapshot(harness.stack.client, harness.flow, session_id)
    ).to_json()
    assert opening["snapshot"]["state"]["status"] == "waiting_for_user_message"
    assert [message["role"] for message in opening["snapshot"]["messages"]] == [
        "user",
        "assistant",
        "tool",
    ]

    # The next turn's model input holds the tool result that ended the previous turn.
    assert await harness.sessions.send_user_message(session_id, "count-results")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.state is not None and second.state.last_turn_error is None
    assert second.messages[-1].text == "tool results in history: 1"


async def test_a_hook_that_returns_default_behaves_exactly_like_no_hook(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    outcomes: list[dict[str, Any]] = []
    for environment in ({}, WITH_HOOK):
        worker = await harness.stack.start_worker(environment)
        session_id = harness.new_session_id()
        await harness.sessions.start_session(session_id, "lookup order-1")
        await harness.wait_for_turns(session_id, 1)
        assert await harness.sessions.send_user_message(session_id, "say hello")
        snapshot = await harness.wait_for_turns(session_id, 2)
        assert snapshot.state is not None
        outcomes.append(
            {
                "last_turn_error": snapshot.state.last_turn_error,
                "messages": [_describe(message) for message in snapshot.messages],
                "model_calls": snapshot.usage.model_calls,
                "steps": await harness.executed_step_types(session_id),
            }
        )
        worker.kill()
    assert outcomes[0] == outcomes[1]
    assert outcomes[0]["messages"] == [
        "1 user lookup order-1",
        "2 assistant lookup_order",
        "3 tool lookup_order found order-1",
        "4 assistant lookup_order: found order-1",
        "5 user say hello",
        "6 assistant None",
        "7 assistant said: hello",
    ]
    assert outcomes[0]["model_calls"] == 3


async def test_a_hook_that_raises_fails_the_run_model_attempt_which_is_retried(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker(WITH_HOOK)
    session_id = harness.new_session_id()
    # After the tool, the hook raises before any model call, then ends the turn.
    await harness.sessions.start_session(session_id, "lookup end-after-tools-hook-fail-once-a")
    first = await harness.wait_for_turns(session_id, 1)
    assert first.state is not None and first.state.last_turn_error is None
    assert harness.invocations.count("model:turn-1:1") == 1
    assert harness.invocations.count("model:turn-1:2") == 0
    # After an answer, the hook raises after the model call, so the retry repeats the call.
    assert await harness.sessions.send_user_message(session_id, "say hook-fail-once-b")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.state is not None and second.state.last_turn_error is None
    assert harness.invocations.count("model:turn-2:1") == 2
    assert second.messages[-1].text == "said: hook-fail-once-b"
    # Only committed attempts record usage, so the failed attempt's call is not counted.
    assert second.usage.model_calls == 2

    history = await harness.stack.read_step_history(session_id)
    assert [
        entry.final_attempt
        for entry in history
        if entry.step_type == "RunModel" and entry.event_type == EXECUTE_COMPLETED_EVENT
    ] == [1, 2, 2]


async def test_continue_after_an_answer_stores_its_message_and_runs_run_model_again(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker(WITH_HOOK)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "revise r1")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    assert snapshot.state.turn_count == 1
    assert [_describe(message) for message in snapshot.messages] == [
        "1 user revise r1",
        "2 assistant draft for r1",
        "3 user [continue] after draft for",
        "4 assistant final for r1",
    ]
    assert snapshot.usage.model_calls == 2
    assert harness.invocations.count("revise:r1:0") == 1
    assert harness.invocations.count("revise:r1:1") == 1
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "RunModel",
    ]
    events = await harness.agent_events(session_id)
    assert _payloads(events, AgentEventType.TURN_COMPLETED) == [{"message_count": 3}]
    assert len(_payloads(events, AgentEventType.USAGE_RECORDED)) == 2


async def test_continue_after_tools_adds_its_message_after_the_tool_results(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker(WITH_HOOK)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "lookup continue-after-tools-1")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    assert [_describe(message) for message in snapshot.messages] == [
        "1 user lookup continue-after-tools-1",
        "2 assistant lookup_order",
        "3 tool lookup_order found continue-after-tools-1",
        "4 user [continue] summarize the lookup",
        "5 assistant lookup_order: found continue-after-tools-1",
    ]
    # One model call after the tool, which received the message after the result.
    assert harness.invocations.count("model:turn-1:2") == 1
    assert snapshot.usage.model_calls == 2
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]


@pytest.mark.parametrize("invalid", ["bare-continue", "blank-continue"])
async def test_an_invalid_continuation_fails_the_run_model_attempt_like_a_hook_error(
    stepwise_harness: ScriptedAgentHarness, invalid: str
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker(WITH_HOOK)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, f"say {invalid}")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert "decide_turn_continuation of agent stepwise-test must return" in (
        snapshot.state.last_turn_error or ""
    )
    # Each attempt repeats the model call, and nothing of the failed attempts is stored.
    assert harness.invocations.count("model:turn-1:1") == 3
    assert [_describe(message) for message in snapshot.messages] == [f"1 user say {invalid}"]


async def test_worker_killed_during_the_second_run_model_repeats_only_that_call(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    worker = await harness.stack.start_worker(WITH_HOOK)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "revise-stall revise-gate r2")
    gate = harness.gate("revise-gate")
    await gate.wait_until_reached()
    continued = await harness.sessions.get_agent_snapshot(session_id)
    assert continued.state is not None
    assert continued.state.status.value == "running_model"
    assert [_describe(message) for message in continued.messages] == [
        "1 user revise-stall revise-gate r2",
        "2 assistant draft for r2",
        "3 user [continue] after draft for",
    ]

    worker.kill()
    gate.release()
    await harness.stack.start_worker(WITH_HOOK)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    assert [_describe(message) for message in snapshot.messages] == [
        "1 user revise-stall revise-gate r2",
        "2 assistant draft for r2",
        "3 user [continue] after draft for",
        "4 assistant final for r2",
    ]
    # The draft call ran once; only the killed second call ran again.
    assert harness.invocations.count("revise:r2:0") == 1
    assert harness.invocations.count("revise:r2:1") == 2
    assert snapshot.usage.model_calls == 2
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "RunModel",
    ]


async def test_after_a_continued_answer_and_a_tool_call_the_hook_sees_only_that_response(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker(WITH_HOOK)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "draft-then-lookup order-5")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    assert [
        name for name in harness.invocations.names() if name.startswith("hook-after-tools:")
    ] == ["hook-after-tools:turn-1:lookup_order"]
    assert [_describe(message) for message in snapshot.messages] == [
        "1 user draft-then-lookup order-5",
        "2 assistant draft for order-5",
        "3 user [continue] after draft for",
        "4 assistant lookup_order",
        "5 tool lookup_order found order-5",
        "6 assistant lookup_order: found order-5",
    ]
    assert snapshot.usage.model_calls == 3


async def test_continued_answers_that_cross_the_compaction_threshold_compact_earlier_responses(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker(
        {**WITH_HOOK, COMPACTION_WINDOW_ENVIRONMENT_VARIABLE: str(COMPACTION_WINDOW_TOKENS)}
    )
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "long-draft")
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    assert snapshot.messages[-1].text == "final long"
    request_tokens = [
        int(name.removeprefix("long-draft-tokens:"))
        for name in harness.invocations.names()
        if name.startswith("long-draft-tokens:")
    ]
    assert len(request_tokens) == LONG_DRAFT_COUNT + 1
    assert all(tokens <= COMPACTION_WINDOW_TOKENS for tokens in request_tokens), request_tokens
    events = await harness.agent_events(session_id)
    compactions = len(_payloads(events, AgentEventType.COMPACTION_FINISHED))
    assert compactions >= 2
    assert snapshot.context_summary is not None
    assert snapshot.context_summary.generation == compactions


async def test_more_continues_in_a_row_than_the_limit_end_the_turn_with_an_error(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    await harness.stack.start_worker(WITH_HOOK)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "stubborn-draft s1")
    snapshot = await harness.wait_for_turns(session_id, 1)

    limit_error = TURN_CONTINUATION_LIMIT_ERROR.format(limit=MAX_CONSECUTIVE_TURN_CONTINUATIONS)
    assert snapshot.state is not None and snapshot.state.last_turn_error == limit_error
    model_calls = MAX_CONSECUTIVE_TURN_CONTINUATIONS + 1
    assert snapshot.usage.model_calls == model_calls
    expected: list[tuple[MessageRole, str | None]] = [(MessageRole.USER, "stubborn-draft s1")]
    for index in range(1, model_calls + 1):
        expected.append((MessageRole.ASSISTANT, f"draft {index} for s1"))
        # The answer that crosses the limit gets no continuation message.
        if index < model_calls:
            expected.append((MessageRole.USER, f"[continue] after draft {index}"))
    assert [(message.role, message.text) for message in snapshot.messages] == expected
    events = await harness.agent_events(session_id)
    assert _payloads(events, AgentEventType.TURN_FAILED) == [{"error": limit_error}]
    assert _payloads(events, AgentEventType.TURN_COMPLETED) == []

    # The count belongs to one turn, so the next turn continues again.
    assert await harness.sessions.send_user_message(session_id, "revise r3")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.state is not None and second.state.last_turn_error is None
    assert second.messages[-1].text == "final for r3"


async def test_a_session_parked_by_a_worker_without_the_hook_continues_on_one_with_it(
    stepwise_harness: ScriptedAgentHarness,
) -> None:
    harness = stepwise_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    # The run state and the tool call record are persisted by the Worker without the hook.
    await harness.sessions.start_session(session_id, "lookup-gated upgrade-gate end-after-tools-2")
    gate = harness.gate("upgrade-gate")
    await gate.wait_until_reached()
    worker.kill()
    gate.release()
    await harness.stack.start_worker(WITH_HOOK)
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    assert [_describe(message) for message in snapshot.messages] == [
        "1 user lookup-gated upgrade-gate end-after-tools-2",
        "2 assistant lookup_order",
        "3 tool lookup_order found end-after-tools-2",
    ]
    # The hook ended the turn after the tool, so no model call followed it.
    assert harness.invocations.count("model:turn-1:2") == 0
    assert snapshot.usage.model_calls == 1

    assert await harness.sessions.send_user_message(session_id, "revise r4")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == "final for r4"


def _describe(message: AgentMessage) -> str:
    if message.role is MessageRole.TOOL:
        return f"{message.sequence} tool {message.tool_name} {message.tool_output}"
    detail = message.text if message.text is not None else message.tool_name
    return f"{message.sequence} {message.role.value} {detail}"


def _payloads(events: list[Any], event_type: AgentEventType) -> list[dict[str, Any]]:
    return [json.loads(event.payload) for event in events if event.type == event_type]
