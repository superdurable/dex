# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Deferred tool calls: custom tools run in Dex Steps, CLI tools wait for approval."""

from __future__ import annotations

import json

from dex_agents import (
    FRAMEWORK_RUN_STATE,
    OUTCOME_UNKNOWN_TOOL_RESULT,
    PENDING_INTERRUPTS,
    TURN_FAILURE_NOTE_FOOTER,
    TURN_FAILURE_NOTE_HEADER,
    AgentSessionStatus,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
)
from dex_agents.testing import wait_for_agent_snapshot
from dex_agents_claude_agent_sdk import APPROVED_CLI_TOOL_OUTPUT, CLAUDE_TRANSCRIPT_ITEM_KIND
from tests.conftest import ClaudeAgentHarness
from tests.fake_claude_cli import RESUME_SKIPS_HOOKS_ENVIRONMENT_VARIABLE


async def test_approved_custom_write_survives_a_worker_restart_and_runs_once(
    harness: ClaudeAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "launch"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert waiting.state is not None
    assert waiting.state.status is AgentSessionStatus.RUNNING_TOOLS
    assert [(call.tool_name, call.effect, call.status) for call in waiting.tool_calls] == [
        ("publish", ToolEffect.NON_IDEMPOTENT_WRITE, ToolCallStatus.AWAITING_APPROVAL)
    ]
    assert interrupt.tool_name == "publish"
    assert interrupt.tool_call_id.startswith("toolu_")
    assert interrupt.arguments == '{"message": "launch"}'
    assert [message.text for message in waiting.messages] == ['call publish {"message": "launch"}']

    worker.kill()
    await harness.stack.start_worker()
    after_restart = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot,
        description="a snapshot from the replacement Worker",
    )
    assert after_restart.pending_interrupts == [interrupt]
    assert harness.invocations.count("publish") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published launch"
    assert [(message.role, message.tool_name) for message in snapshot.messages] == [
        (MessageRole.USER, None),
        (MessageRole.ASSISTANT, "publish"),
        (MessageRole.TOOL, "publish"),
        (MessageRole.ASSISTANT, None),
    ]
    assert snapshot.messages[2].tool_call_id == interrupt.tool_call_id
    assert snapshot.messages[2].tool_output == "published launch"
    assert harness.invocations.count("publish") == 1
    assert harness.invocations.names()[:2] == ["cli:new", "model_call"]
    assert harness.invocations.count("cli:resume") == 1
    assert harness.invocations.count("cli:auto_resume") == 1
    assert harness.invocations.count("cli:unexpected_prompt") == 0
    assert snapshot.tool_calls == []
    values = await harness.stack.client.get_attributes(
        session_id, FRAMEWORK_RUN_STATE, PENDING_INTERRUPTS
    )
    assert FRAMEWORK_RUN_STATE not in values
    assert values[PENDING_INTERRUPTS] == []


async def test_rejected_custom_write_never_runs_and_the_model_sees_the_rejection(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "risky"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="not today"
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: not today"
    assert harness.invocations.count("publish") == 0


async def test_resumed_call_that_skips_the_hook_is_answered_from_the_record(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker({RESUME_SKIPS_HOOKS_ENVIRONMENT_VARIABLE: "1"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "early"}')
    approved = await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.resolve_interrupt(
        session_id, approved.interrupt_id, approved=True
    )
    first = await harness.wait_for_turns(session_id, 1)
    assert first.messages[-1].text == "publish: published early"

    assert await harness.sessions.send_user_message(session_id, 'call publish {"message": "late"}')
    rejected = await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.resolve_interrupt(
        session_id, rejected.interrupt_id, approved=False, rejection_message="not now"
    )
    second = await harness.wait_for_turns(session_id, 2)
    assert second.messages[-1].text == "publish: not now"
    assert harness.invocations.count("publish") == 1


async def test_resolve_interrupt_returns_false_the_second_time(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "once"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)

    assert not await harness.sessions.resolve_interrupt(session_id, "unknown-call", approved=True)
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    assert not await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="too late"
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published once"
    assert harness.invocations.count("publish") == 1


async def test_idempotent_custom_write_retries_with_one_idempotency_key(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5, "fail_attempts": 1}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    tool_result = next(message for message in snapshot.messages if message.role is MessageRole.TOOL)
    expected_output = f"charged 5 with key {session_id}/turn-1/{tool_result.tool_call_id}"
    assert tool_result.tool_output == expected_output
    assert snapshot.messages[-1].text == f"charge: {expected_output}"
    assert harness.invocations.count("charge") == 2
    assert snapshot.pending_interrupts == []


async def test_idempotent_custom_write_that_exhausts_its_attempts_records_outcome_unknown(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5, "fail_attempts": 3}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"charge: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("charge") == 3


async def test_custom_tool_error_result_reaches_the_model_without_a_retry(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call charge {"amount": -1}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "charge: The tool call failed: amount must be positive"
    assert harness.invocations.count("charge") == 1


async def test_built_in_tool_runs_in_the_cli_after_approval(harness: ClaudeAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call Bash {"command": "echo hi"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert interrupt.tool_name == "Bash"
    assert [(call.tool_name, call.effect) for call in waiting.tool_calls] == [
        ("Bash", ToolEffect.UNCLASSIFIED)
    ]
    assert harness.invocations.count("Bash") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "Bash: ran echo hi"
    tool_outputs = [
        message.tool_output for message in snapshot.messages if message.role is MessageRole.TOOL
    ]
    assert tool_outputs == ["ran echo hi"]
    assert APPROVED_CLI_TOOL_OUTPUT not in (snapshot.messages[-1].text or "")
    assert harness.invocations.count("Bash") == 1


async def test_rejected_built_in_tool_never_runs(harness: ClaudeAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call Bash {"command": "rm -rf build"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="no shell"
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    # Claude Code prefixes a hook denial; only custom tools return their rejection themselves.
    assert snapshot.messages[-1].text == "Bash: PreToolUse:Bash hook error: no shell"
    assert harness.invocations.count("Bash") == 0


async def test_read_and_write_in_one_reply_run_inline_and_in_a_step(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call lookup {"query": "inline"} && call publish {"message": "step"}'
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.tool_name == "publish"
    assert harness.invocations.count("lookup") == 1
    assert harness.invocations.count("publish") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "lookup: found inline; publish: published step"
    assert harness.invocations.count("publish") == 1
    assert harness.invocations.count("lookup") == 1
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "AwaitToolApproval",
        "CallSingleAttemptTool",
        "ResumeTurn",
        "RunModel",
    ]


async def test_two_writes_in_one_reply_leave_the_turn_one_at_a_time(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
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
    assert harness.invocations.count("publish") == 1

    assert await harness.sessions.resolve_interrupt(session_id, second.interrupt_id, approved=True)
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published a; publish: published b"
    publish_outputs = [
        message.tool_output or ""
        for message in snapshot.messages
        if message.role is MessageRole.TOOL and message.tool_name == "publish"
    ]
    # The refusal of b is written in the first segment, before a's result on resume.
    assert "Call publish again by itself" in publish_outputs[0]
    assert publish_outputs[1:] == ["published a", "published b"]
    assert harness.invocations.count("publish") == 2


async def test_input_rewritten_by_an_application_hook_runs_once_in_the_step(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "rewrite me"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.arguments == '{"message": "rewritten"}'

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published rewritten"
    assert harness.invocations.count("publish") == 1


async def test_hook_that_rewrites_every_call_cannot_hide_a_recorded_result(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call lookup {"query": "stamp"} && call publish {"message": "stamp"}'
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.arguments == '{"message": "stamp+"}'

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    # On resume the MCP call carries "stamp++", unlike the hook's input and the record.
    assert snapshot.messages[-1].text == "lookup: found stamp+; publish: published stamp+"
    assert harness.invocations.count("publish") == 1
    assert harness.invocations.count("lookup") == 1


async def test_deferred_call_with_invalid_input_fails_without_running(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"wrong": 1}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == (
        "publish: The tool call failed: Input validation error: " "'message' is a required property"
    )
    assert harness.invocations.count("publish") == 0


async def test_failed_resumed_turn_after_a_write_leaves_a_note_for_the_next_prompt(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    first_prompt = 'call publish {"message": "draft"} ;; fail'
    await harness.sessions.start_session(session_id, first_prompt)
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    failed = await harness.wait_for_turns(session_id, 1)

    note = "\n".join(
        [
            TURN_FAILURE_NOTE_HEADER,
            '- publish {"message": "draft"} -> succeeded: published draft',
            TURN_FAILURE_NOTE_FOOTER,
        ]
    )
    assert failed.state is not None
    assert "scripted failure after tool calls" in (failed.state.last_turn_error or "")
    assert [(message.role, message.turn_id, message.text) for message in failed.messages] == [
        (MessageRole.USER, "turn-1", first_prompt),
        (MessageRole.USER, "turn-1", note),
    ]
    assert harness.invocations.count("cli:resume") == 3
    assert harness.invocations.count("publish") == 1

    assert await harness.sessions.send_user_message(session_id, "say next")
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert snapshot.messages[-1].text == "said: next"
    cli_prompts = [
        [block["text"] for block in entry["message"]["content"]]
        for message in snapshot.messages
        if message.turn_id == "turn-2"
        and json.loads(message.framework_item)["kind"] == CLAUDE_TRANSCRIPT_ITEM_KIND
        for entry in json.loads(message.framework_item)["entries"]
        if entry["type"] == "user" and isinstance(entry["message"]["content"], list)
    ]
    assert cli_prompts == [[first_prompt, note, "say next"]]
    assert harness.invocations.count("publish") == 1
