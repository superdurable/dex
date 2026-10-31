# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Every function tool call runs in its own Step: parallel calls, approvals, and results."""

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
from dex_agents.testing import wait_for_agent_snapshot
from tests.conftest import SupportAgentHarness
from tests.scripted_model import SCRIPTED_MODEL_FAILURE
from tests.support_agent import INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE


async def test_parallel_tool_calls_run_as_parallel_steps(harness: SupportAgentHarness) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call lookup {"query": "a", "gate": "lookup-gate"}'
        ' && call charge {"amount": 2, "gate": "charge-gate"}',
    )
    lookup_gate = harness.gate("lookup-gate")
    charge_gate = harness.gate("charge-gate")
    # Both tools wait at their gates at the same time, so their Steps run in parallel.
    await lookup_gate.wait_until_reached()
    await charge_gate.wait_until_reached()
    running = await harness.sessions.get_agent_snapshot(session_id)
    assert running.state is not None
    assert running.state.status is AgentSessionStatus.RUNNING_TOOLS
    assert [(call.tool_name, call.effect) for call in running.tool_calls] == [
        ("lookup", ToolEffect.READ),
        ("charge", ToolEffect.IDEMPOTENT_WRITE),
    ]

    lookup_gate.release()
    charge_gate.release()
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == (
        f"lookup: found a; charge: charged 2 with key {session_id}/turn-1/call-1-0-1"
    )
    executed = await harness.executed_step_types(session_id)
    assert executed.count("CallTool") == 2
    assert executed[:2] == ["StartAgentSession", "RunModel"]
    assert executed[-2:] == ["ResumeTurn", "RunModel"]
    assert harness.model_calls() == ["model:1:1", "model:1:2"]


async def test_approved_write_waits_beside_a_read_survives_a_restart_and_runs_once(
    harness: SupportAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "launch"} && call lookup {"query": "y"}'
    )
    waiting = await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot
        if [(call.tool_name, call.status) for call in snapshot.tool_calls]
        == [("publish", ToolCallStatus.AWAITING_APPROVAL), ("lookup", ToolCallStatus.SUCCEEDED)]
        else None,
        description="the read finished while the write awaits approval",
    )
    interrupt = waiting.pending_interrupts[0]
    assert (interrupt.tool_call_id, interrupt.tool_name) == ("call-1-0-0", "publish")
    assert json.loads(interrupt.arguments) == {"message": "launch"}
    values = await harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE)
    run_state = json.loads(values[FRAMEWORK_RUN_STATE])
    assert run_state["current_agent"]["name"] == "Assistant"
    assert "$schemaVersion" in run_state

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
    assert snapshot.messages[-1].text == "publish: published launch; lookup: found y"
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert [message.tool_output for message in tool_results] == ["published launch", "found y"]
    assert harness.tool_runs() == ["lookup", "publish"]
    assert snapshot.usage.model_calls == 2
    executed = await harness.executed_step_types(session_id)
    assert executed.count("AwaitToolApproval") == 1
    assert executed.count("CallSingleAttemptTool") == 1
    assert executed.count("CallTool") == 1
    values = await harness.stack.client.get_attributes(
        session_id, FRAMEWORK_RUN_STATE, PENDING_INTERRUPTS
    )
    assert FRAMEWORK_RUN_STATE not in values
    assert values[PENDING_INTERRUPTS] == []


async def test_rejected_write_never_runs_and_the_model_sees_the_rejection(
    harness: SupportAgentHarness,
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
    assert "CallSingleAttemptTool" not in await harness.executed_step_types(session_id)


async def test_resolve_interrupt_returns_false_the_second_time(
    harness: SupportAgentHarness,
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


async def test_expired_approval_deadline_rejects_the_call(harness: SupportAgentHarness) -> None:
    await harness.stack.start_worker({INTERRUPT_DEADLINE_ENVIRONMENT_VARIABLE: "2"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call publish {"message": "slow human"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.deadline is not None

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == f"publish: {APPROVAL_DEADLINE_PASSED}"
    assert not await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    assert harness.invocations.count("publish") == 0


async def test_idempotent_write_retries_an_exception_with_one_idempotency_key(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5, "failed_attempts": 1}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    key = f"{session_id}/turn-1/call-1-0-0"
    assert snapshot.messages[-1].text == f"charge: charged 5 with key {key}"
    assert harness.idempotency_keys.names() == [
        f"charge {key} attempt 1",
        f"charge {key} attempt 2",
    ]


async def test_idempotent_write_that_exhausts_its_attempts_records_an_unknown_outcome(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5, "failed_attempts": 3}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == f"charge: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert harness.invocations.count("charge") == 3
    assert "RecordToolFailure" in await harness.executed_step_types(session_id)


async def test_tool_execution_error_reaches_the_model_without_a_retry(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call charge {"amount": -1}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "charge: The tool call failed: amount must be positive"
    assert harness.invocations.count("charge") == 1


async def test_invalid_tool_arguments_fail_the_call_without_running_it(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call charge {"amount": 5 && call charge {"amount": "five"}'
    )
    # The SDK cannot parse the first call's arguments, so it asks for approval.
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.tool_call_id == "call-1-0-0"
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    outputs = [message.tool_output or "" for message in snapshot.messages if message.tool_output]
    assert [output.split(":")[0] for output in outputs] == ["The tool call failed"] * 2
    assert all("Invalid JSON input for tool charge" in output for output in outputs)
    assert harness.invocations.count("charge") == 0


async def test_read_tool_that_the_application_marked_needs_approval_waits_for_approval(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call read_secret {"name": "db"}')
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert [(call.effect, call.requires_approval) for call in waiting.tool_calls] == [
        (ToolEffect.READ, True)
    ]
    assert harness.invocations.count("read_secret") == 0

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "read_secret: secret db is 42"
    assert harness.invocations.count("read_secret") == 1


async def test_callable_needs_approval_sees_defaulted_arguments_as_the_sdk_does(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call transfer {"to": "bob"} && call transfer {"to": "amy", "amount": 10}',
    )
    # The omitted amount defaults to 5000, so the SDK requires approval for it.
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.tool_call_id == "call-1-0-0"
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert [(call.tool_call_id, call.requires_approval) for call in waiting.tool_calls] == [
        ("call-1-0-0", True),
        ("call-1-0-1", False),
    ]

    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "transfer: sent 5000 to bob; transfer: sent 10 to amy"
    assert harness.invocations.count("transfer") == 2


async def test_tool_with_an_output_type_resumes_with_a_receipt_and_a_failure(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call pay {"order": "7", "amount": 5} && call pay {"order": "8", "amount": 0}',
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.state is not None
    assert snapshot.state.last_turn_error is None
    tool_results = [message for message in snapshot.messages if message.role is MessageRole.TOOL]
    assert json.loads(tool_results[0].tool_output or "") == {
        "receipt_id": "receipt-7",
        "amount": 5,
    }
    assert tool_results[1].tool_output == "The tool call failed: card declined"
    assert harness.invocations.count("pay") == 2


async def test_tool_input_guardrail_runs_once_before_the_body_in_the_tool_call_step(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        'call run_sql {"sql": "drop table users"} && call run_sql {"sql": "truncate users"}'
        ' && call run_sql {"sql": "select 1"}',
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == (
        "run_sql: The tool call failed: blocked: destructive SQL; "
        "run_sql: The tool call failed: tool input guardrail block_destructive_sql stopped "
        "the call to run_sql; run_sql: ran select 1"
    )
    # One guardrail run per call: the resumed RunModel does not run it again.
    assert harness.invocations.count("guardrail:run_sql") == 3
    assert harness.invocations.count("run_sql") == 1


async def test_tools_nested_in_an_agent_tool_get_no_idempotency_key(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call ask_researcher {"input": "call cite {\\"source\\": \\"paper\\"}"}'
    )
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "ask_researcher: cite: cited paper with key none"
    assert harness.invocations.count("cite") == 1


async def test_agent_tool_runs_its_nested_agent_in_one_tool_call_step(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, 'call ask_researcher {"input": "say deep"}')
    snapshot = await harness.wait_for_turns(session_id, 1)

    assert snapshot.messages[-1].text == "ask_researcher: said: deep"
    # The nested model call runs inside CallTool, and its usage is not reported.
    assert harness.model_calls() == ["model:1:1", "model:1:1", "model:1:2"]
    assert snapshot.usage.model_calls == 2
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]


async def test_handoff_makes_two_model_calls_in_one_run_model_and_resumes_in_the_target(
    harness: SupportAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'handoff billing then call refund {"order": "7"}'
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert interrupt.tool_name == "refund"
    values = await harness.stack.client.get_attributes(session_id, FRAMEWORK_RUN_STATE)
    assert json.loads(values[FRAMEWORK_RUN_STATE])["current_agent"]["name"] == "Billing"
    assert harness.model_calls() == ["model:1:1", "model:1:2"]

    worker.kill()
    await harness.stack.start_worker()
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "refund: refunded 7"
    assert [message.tool_name for message in snapshot.messages if message.tool_name] == [
        "transfer_to_billing",
        "transfer_to_billing",
        "refund",
        "refund",
    ]
    assert harness.invocations.count("refund") == 1
    assert snapshot.usage.model_calls == 3
    executed = await harness.executed_step_types(session_id)
    assert executed.count("RunModel") == 2


async def test_writes_in_consecutive_model_responses_run_in_two_tool_batches(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, 'call publish {"message": "a"} then call publish {"message": "b"}'
    )
    first = await harness.wait_for_pending_interrupt(session_id)
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
        description="the second pending interrupt",
    )
    assert (first.tool_call_id, second.tool_call_id) == ("call-1-0-0", "call-1-1-0")
    assert harness.invocations.count("publish") == 1

    assert await harness.sessions.resolve_interrupt(session_id, second.interrupt_id, approved=True)
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "publish: published a; publish: published b"
    assert harness.invocations.count("publish") == 2
    assert snapshot.usage.model_calls == 3
    executed = await harness.executed_step_types(session_id)
    assert executed.count("RunModel") == 3
    assert executed.count("ResumeTurn") == 2


async def test_failed_turn_after_a_write_leaves_a_note_for_the_next_turn(
    harness: SupportAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    command = 'call publish {"message": "launch"} then fail'
    await harness.sessions.start_session(session_id, command)
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    failed = await harness.wait_for_turns(session_id, 1)

    note = "\n".join(
        [
            TURN_FAILURE_NOTE_HEADER,
            '- publish {"message": "launch"} -> succeeded: published launch',
            TURN_FAILURE_NOTE_FOOTER,
        ]
    )
    assert failed.state is not None
    assert SCRIPTED_MODEL_FAILURE in (failed.state.last_turn_error or "")
    assert [(message.role, message.text) for message in failed.messages] == [
        (MessageRole.USER, command),
        (MessageRole.USER, note),
    ]
    assert json.loads(failed.messages[1].framework_item) == {"role": "user", "content": note}
    assert harness.invocations.count("publish") == 1
    assert harness.model_calls() == ["model:1:1"] + ["model:1:2"] * 3

    assert await harness.sessions.send_user_message(session_id, "recall")
    recalled = await harness.wait_for_turns(session_id, 2)
    assert recalled.messages[-1].text == f"history: {command} | {note}; tools: "
    assert harness.invocations.count("publish") == 1
