# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Pydantic AI tool call Steps: approvals, rejections, retries, deadlines, and failures."""

from __future__ import annotations

import json

from dex_agents import (
    APPROVAL_DEADLINE_PASSED,
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
from tests.conftest import ScriptedAgentHarness
from tests.scripted_agent import (
    CONNECTION_LOST_AMOUNT,
    INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE,
    SYSTEM_PROMPT,
)


async def test_approved_write_survives_a_worker_restart_and_runs_once(
    harness: ScriptedAgentHarness,
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
    assert (interrupt.tool_call_id, interrupt.tool_name) == ("call-1-0-0", "publish")
    assert json.loads(interrupt.arguments) == {"message": "launch"}
    assert interrupt.deadline is None

    worker.kill()
    await harness.stack.start_worker()
    assert await harness.sessions.get_agent_snapshot(session_id) == waiting
    assert harness.invocations.count("publish") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published launch"
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [(message.tool_name, message.tool_output) for message in tool_results] == [
        ("publish", "published launch")
    ]
    # The agent's exception-swallowing hook runs only in the tool Step, around the real call.
    assert harness.invocations.names() == ["model:1:1", "audit:publish", "publish", "model:1:2"]
    assert snapshot.tool_calls == []
    values = await harness.stack.client.get_attributes(
        session_id, FRAMEWORK_RUN_STATE, PENDING_INTERRUPTS
    )
    assert FRAMEWORK_RUN_STATE not in values
    assert values[PENDING_INTERRUPTS] == []
    executed = await harness.executed_step_types(session_id)
    assert executed[-4:] == [
        "AwaitToolApproval",
        "CallSingleAttemptTool",
        "ResumeTurn",
        "RunModel",
    ]


async def test_rejected_write_never_runs_and_the_model_sees_the_rejection(
    harness: ScriptedAgentHarness,
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
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.tool_output for message in tool_results] == ["not today"]
    assert harness.invocations.count("publish") == 0
    assert harness.invocations.count("audit:publish") == 0
    assert "CallSingleAttemptTool" not in await harness.executed_step_types(session_id)


async def test_resolve_interrupt_returns_false_the_second_time(
    harness: ScriptedAgentHarness,
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


async def test_expired_interrupt_deadline_rejects_the_call(harness: ScriptedAgentHarness) -> None:
    await harness.stack.start_worker({INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE: "2"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "slow human"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.deadline is not None

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == f"publish: {APPROVAL_DEADLINE_PASSED}"
    assert snapshot.pending_interrupts == []
    assert not await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    assert harness.invocations.count("publish") == 0


async def test_idempotent_write_uses_the_key_and_retries_to_an_unknown_outcome(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call charge {"amount": 5}')
    charged = await harness.wait_for_turns(session_id, 1)

    assert charged.messages[-1].text == (
        f"charge: charged 5 for deps-{session_id} with key {session_id}/turn-1/call-1-0-0"
    )
    assert await harness.list_event_payloads(session_id, "tool_call_deferred") == [
        {
            "tool_call_id": "call-1-0-0",
            "tool_name": "charge",
            "effect": "idempotent_write",
            "requires_approval": False,
        }
    ]
    assert await harness.list_event_payloads(session_id, "interrupt_raised") == []

    assert await harness.sessions.send_user_message(session_id, 'call charge {"amount": -1}')
    refused = await harness.wait_for_turns(session_id, 2)
    assert refused.messages[-1].text == "charge: The tool call failed: amount must be positive"
    assert harness.invocations.count("charge") == 2

    assert await harness.sessions.send_user_message(
        session_id, f'call charge {{"amount": {CONNECTION_LOST_AMOUNT}}}'
    )
    exhausted = await harness.wait_for_turns(session_id, 3)
    assert exhausted.messages[-1].text == f"charge: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("charge") == 5
    assert "RecordToolFailure" in await harness.executed_step_types(session_id)


async def test_model_retry_from_a_tool_without_retries_fails_the_call_once(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call charge_once {"amount": 0}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    final_text = snapshot.messages[-1].text or ""
    assert final_text.startswith("charge_once: The tool call failed: ")
    assert "exceeded max retries count of 0" in final_text
    assert harness.invocations.count("charge_once") == 1
    assert "RecordToolFailure" not in await harness.executed_step_types(session_id)


async def test_tool_that_pydantic_ai_marks_for_approval_waits_despite_its_policy(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call archive {"item": "report"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id, "archive")
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert [(call.effect, call.requires_approval, call.status) for call in waiting.tool_calls] == [
        (ToolEffect.IDEMPOTENT_WRITE, True, ToolCallStatus.AWAITING_APPROVAL)
    ]
    assert harness.invocations.count("archive") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "archive: archived report"
    assert harness.invocations.count("archive") == 1
    executed = await harness.executed_step_types(session_id)
    assert executed.count("AwaitToolApproval") == 1
    assert executed.count("CallTool") == 1


async def test_external_tool_without_a_policy_waits_for_approval_and_fails_clearly(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, "call ask_browser {}")
    interrupt = await harness.wait_for_pending_interrupt(session_id, "ask_browser")
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert [(call.effect, call.requires_approval) for call in waiting.tool_calls] == [
        (ToolEffect.UNCLASSIFIED, True)
    ]

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == (
        "ask_browser: The tool call failed: tool ask_browser is an external tool and has no "
        "function to run in a Dex tool Step"
    )
    finished = await harness.list_event_payloads(session_id, "tool_call_finished")
    assert [payload["status"] for payload in finished] == ["failed"]


async def test_tool_that_defers_itself_after_work_records_an_unknown_outcome(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call start_export {"name": "q3"}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"start_export: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("start_export") == 1
    assert "RecordToolOutcomeUnknown" in await harness.executed_step_types(session_id)


async def test_a_sub_agent_model_error_after_a_write_records_an_unknown_outcome(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call book_room {"room": "A"}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"book_room: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("reserve_room") == 1
    assert "RecordToolOutcomeUnknown" in await harness.executed_step_types(session_id)


async def test_failed_turn_after_a_write_leaves_a_note_that_the_next_model_input_contains(
    harness: ScriptedAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    prompt = 'call charge {"amount": 5} then fail'
    await harness.sessions.start_session(session_id, prompt)
    failed = await harness.wait_for_turns(session_id, 1)

    assert failed.state is not None
    assert "scripted model failure" in (failed.state.last_turn_error or "")
    charge_output = f"charged 5 for deps-{session_id} with key {session_id}/turn-1/call-1-0-0"
    note = "\n".join(
        [
            TURN_FAILURE_NOTE_HEADER,
            f'- charge {{"amount":5}} -> succeeded: {charge_output}',
            TURN_FAILURE_NOTE_FOOTER,
        ]
    )
    assert [(message.role, message.text) for message in failed.messages] == [
        (MessageRole.USER, prompt),
        (MessageRole.USER, note),
    ]
    assert failed.tool_calls == []
    assert harness.invocations.count("charge") == 1
    assert harness.invocations.count("model:1:2") == 3

    assert await harness.sessions.send_user_message(session_id, "recall")
    recalled = await harness.wait_for_turns(session_id, 2)
    assert recalled.messages[-1].text == f"history: {prompt} | {note}; system: {SYSTEM_PROMPT}"
    assert harness.invocations.count("charge") == 1
