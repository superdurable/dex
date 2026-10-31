# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""The bundled Claude Code CLI against a local scripted Messages API.

These tests settle what the fake CLI copies: a resumed deferred call continues
without a prompt, the CLI adds a hidden "Continue from where you left off."
message, and one reply can defer several calls. Every request must reach the
mock with the mock key; a refused proxy stops anything else.
"""

from __future__ import annotations

from typing import Any

from dex_agents.testing import wait_for_agent_snapshot
from tests.claude_test_agent import PERMISSION_MODE_ENVIRONMENT_VARIABLE
from tests.conftest import MOCK_API_KEY, RealClaudeCliHarness
from tests.fake_claude_cli import CONTINUE_PROMPT


async def test_real_cli_resumes_an_approved_write_without_a_prompt(
    real_cli_harness: RealClaudeCliHarness,
) -> None:
    harness = real_cli_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "real"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.tool_name == "publish"
    assert harness.invocations.count("publish") == 0

    worker.kill()
    await harness.stack.start_worker()
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published real"
    assert harness.invocations.count("publish") == 1

    model_requests = _model_requests(harness)
    assert len(model_requests) == 2
    resumed_turn = model_requests[1]["messages"][-1]
    assert resumed_turn["role"] == "user"
    resumed_blocks = resumed_turn["content"]
    assert resumed_blocks[0]["type"] == "tool_result"
    assert resumed_blocks[0]["tool_use_id"] == interrupt.tool_call_id
    assert resumed_blocks[-1]["text"] == CONTINUE_PROMPT

    assert await harness.sessions.send_user_message(session_id, "recall")
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == 'history: call publish {"message": "real"}'
    assert len(_model_requests(harness)) == 3
    _assert_only_the_mock_was_reached(harness)


async def test_real_cli_defers_two_writes_of_one_reply_one_at_a_time(
    real_cli_harness: RealClaudeCliHarness,
) -> None:
    harness = real_cli_harness
    await harness.stack.start_worker({PERMISSION_MODE_ENVIRONMENT_VARIABLE: "dontAsk"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "a"} && call publish {"message": "b"}'
    )
    first = await harness.wait_for_pending_interrupt(session_id)
    assert first.arguments == '{"message": "a"}'
    assert await harness.sessions.resolve_interrupt(session_id, first.interrupt_id, approved=True)
    second = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: next(
            (
                interrupt
                for interrupt in snapshot.pending_interrupts
                if interrupt.interrupt_id != first.interrupt_id
            ),
            None,
        ),
        description="the second write's interrupt",
    )
    assert second.arguments == '{"message": "b"}'
    assert await harness.sessions.resolve_interrupt(session_id, second.interrupt_id, approved=True)

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published a; publish: published b"
    assert harness.invocations.count("publish") == 2
    denied = [
        block["content"]
        for body in _model_requests(harness)
        for message in body["messages"]
        if isinstance(message.get("content"), list)
        for block in message["content"]
        if block.get("type") == "tool_result" and block.get("is_error")
    ]
    assert denied[0].startswith("PreToolUse:mcp__tools__publish hook error: publish did not run.")
    _assert_only_the_mock_was_reached(harness)


async def test_real_cli_resumes_repeatedly_and_returns_a_rejection_as_plain_text(
    real_cli_harness: RealClaudeCliHarness,
) -> None:
    harness = real_cli_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id)
    rounds = ["one", "two", "three", "four"]
    for turn_number, message in enumerate(rounds, start=1):
        assert await harness.sessions.send_user_message(
            session_id, f'call publish {{"message": "{message}"}}'
        )
        interrupt = await harness.wait_for_pending_interrupt(session_id)
        is_rejected = message == "four"
        assert await harness.sessions.resolve_interrupt(
            session_id,
            interrupt.interrupt_id,
            approved=not is_rejected,
            rejection_message="not now" if is_rejected else None,
        )
        snapshot = await harness.wait_for_turns(session_id, turn_number)
        expected = "publish: not now" if is_rejected else f"publish: published {message}"
        assert snapshot.messages[-1].text == expected

    assert harness.invocations.count("publish") == 3
    assert len(_model_requests(harness)) == 2 * len(rounds)
    _assert_only_the_mock_was_reached(harness)


def _model_requests(harness: RealClaudeCliHarness) -> list[dict[str, Any]]:
    return [
        request.body for request in harness.messages_api.requests if request.body.get("messages")
    ]


def _assert_only_the_mock_was_reached(harness: RealClaudeCliHarness) -> None:
    assert harness.messages_api.requests
    assert {request.path.split("?", 1)[0] for request in harness.messages_api.requests} == {
        "/v1/messages"
    }
    assert {request.api_key for request in harness.messages_api.requests} == {MOCK_API_KEY}
    assert (harness.state_dir / "claude-config" / "projects").is_dir()
    assert not (harness.claude_home / ".claude" / "projects").exists()
