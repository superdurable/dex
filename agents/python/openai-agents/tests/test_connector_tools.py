# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Connector tools from a ``ConnectorToolSource``: every call runs in its own tool Step."""

from __future__ import annotations

import json
from dataclasses import replace
from datetime import timedelta
from pathlib import Path
from typing import Any, cast

import pytest

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TOOL_CALL_NOT_RUN_ERROR_PREFIX,
    TOOL_CALL_SKIPPED_AFTER_WRITE_DETAIL,
    AgentEventWriter,
    AgentSnapshot,
    AgentToolCall,
    AgentTurnContext,
    ToolCallStatus,
    ToolCallStepOptionsProvider,
    ToolEffect,
)
from dex_agents.builtin_loop import FrameworkToolSourceRouter
from dex_agents.connectors import ConnectorToolSource, connector_tool
from dex_agents_openai_agents import OpenAIAgentsRunner, openai_agents_session_flow
from dex_agents_openai_agents._agent_graph import ActiveTurn
from tests.conftest import SupportAgentHarness as ScriptedAgentHarness
from tests.connector_agent import WITHDRAWN_FILE_NAME, build_connector_agent, build_connector_source
from tests.fake_ledger import (
    LEDGER_CONNECTION,
    CloseAccountOperation,
    FakeLedgerProvider,
    GetBalanceOperation,
)

BALANCE = '{"accountId":"a-1","amount":100}'
SKIPPED_ERROR = f"{TOOL_CALL_NOT_RUN_ERROR_PREFIX}: {TOOL_CALL_SKIPPED_AFTER_WRITE_DETAIL}"
NOT_OFFERED = "hiding_note did not run: its tool source does not offer this tool now."
FIRST_PAYOUT = '{"amount":5,"payoutId":"createPayout-1"}'


def _calls(*calls: tuple[str, dict[str, Any]], hold: str | None = None) -> str:
    command = "calls " + json.dumps([[name, arguments] for name, arguments in calls])
    return command if hold is None else f"hold:{hold} {command}"


def _answer_lines(snapshot: AgentSnapshot) -> list[str]:
    text = snapshot.messages[-1].text
    assert text is not None
    return text.split("\n")


def _ledger(harness: ScriptedAgentHarness) -> FakeLedgerProvider:
    return FakeLedgerProvider(harness.state_dir)


async def _held_records(
    harness: ScriptedAgentHarness, session_id: str, gate: str
) -> dict[str, AgentToolCall]:
    """Return the turn's records, by tool name, while the final answer waits at the gate."""
    await harness.gate(gate).wait_until_reached()
    snapshot = await harness.sessions.get_agent_snapshot(session_id)
    harness.gate(gate).release()
    return {record.tool_name: record for record in snapshot.tool_calls}


async def test_a_query_tool_runs_in_its_tool_step(
    connector_harness: ScriptedAgentHarness,
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _calls(("get_balance", {"accountId": "a-1"}), hold="held")
    )
    record = (await _held_records(harness, session_id, "held"))["get_balance"]
    assert (record.effect, record.status, record.output) == (
        ToolEffect.READ,
        ToolCallStatus.SUCCEEDED,
        BALANCE,
    )
    assert not record.requires_approval and not record.ordered_write
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert _answer_lines(snapshot) == [f"get_balance: {BALANCE}"]
    assert "CallTool" in await harness.executed_step_types(session_id)


async def test_an_approved_mutation_runs_once(
    connector_harness: ScriptedAgentHarness,
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _calls(("approved_payout", {"accountId": "a-1", "amount": 5}))
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert interrupt.tool_name == "approved_payout"
    assert [(call.effect, call.status, call.ordered_write) for call in waiting.tool_calls] == [
        (ToolEffect.IDEMPOTENT_WRITE, ToolCallStatus.AWAITING_APPROVAL, True)
    ]
    assert ledger.sends() == []
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert _answer_lines(snapshot) == [f"approved_payout: {FIRST_PAYOUT}"]
    assert len(ledger.writes("createPayout")) == 1
    assert len(ledger.sends("createPayout")) == 1


async def test_a_session_policy_changes_only_the_approval_of_a_connector_tool(
    connector_harness: ScriptedAgentHarness,
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _calls(("policy_note", {"accountId": "a-1", "text": "hello"}))
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    # The policy claims READ, but the effect class always comes from the manifest.
    assert [(call.tool_name, call.effect) for call in waiting.tool_calls] == [
        ("policy_note", ToolEffect.NON_IDEMPOTENT_WRITE)
    ]
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="not now"
    )
    await harness.wait_for_turns(session_id, 1)
    assert _ledger(harness).sends() == []


async def test_a_retry_safe_mutation_retries_with_the_same_call_id(
    connector_harness: ScriptedAgentHarness,
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _calls(("create_payout", {"accountId": "a-1", "amount": 5, "memo": "rate-limit-once"})),
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert _answer_lines(snapshot) == [f"create_payout: {FIRST_PAYOUT}"]
    [refused] = ledger.sends("rate-limited")
    [paid] = ledger.writes("createPayout")
    # The Call ID, and so the idempotency key, is the same on both attempts of the tool Step.
    assert refused["key"] == paid["key"]
    assert "CallTool" in await harness.executed_step_types(session_id)


@pytest.mark.parametrize(
    ("tool_name", "arguments", "step_type"),
    [
        ("create_payout", {"accountId": "a-1", "amount": 5, "memo": "uncertain"}, "CallTool"),
        ("post_note", {"accountId": "a-1", "text": "uncertain"}, "CallSingleAttemptTool"),
    ],
)
async def test_an_uncertain_write_records_outcome_unknown_and_never_runs_again(
    connector_harness: ScriptedAgentHarness,
    tool_name: str,
    arguments: dict[str, Any],
    step_type: str,
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, _calls((tool_name, arguments), hold="held"))
    record = (await _held_records(harness, session_id, "held"))[tool_name]
    assert record.status is ToolCallStatus.OUTCOME_UNKNOWN
    assert record.error is not None and "uncertain" in record.error
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert OUTCOME_UNKNOWN_TOOL_RESULT in _answer_lines(snapshot)[0]
    assert len(ledger.writes()) == 1
    assert len(ledger.sends()) == 1
    assert step_type in await harness.executed_step_types(session_id)


async def test_two_writes_of_one_response_run_one_at_a_time_in_the_model_order(
    connector_harness: ScriptedAgentHarness,
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _calls(
            ("post_note", {"accountId": "a-1", "text": "first gate-after-accept:first-write"}),
            ("create_payout", {"accountId": "a-1", "amount": 5}),
        ),
    )
    await harness.gate("first-write").wait_until_reached()
    running = await harness.sessions.get_agent_snapshot(session_id)
    records = {record.tool_call_id: record for record in running.tool_calls}
    assert records["call-0"].ordered_write
    assert records["call-1"].ordered_write and records["call-1"].write_queued
    assert ledger.writes("createPayout") == []
    harness.gate("first-write").release()
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert [entry["operation"] for entry in ledger.writes()] == ["postNote", "createPayout"]
    assert _answer_lines(snapshot) == [
        'post_note: {"noteId":"postNote-1"}',
        'create_payout: {"amount":5,"payoutId":"createPayout-2"}',
    ]


async def test_an_unknown_write_skips_the_later_writes_of_the_response(
    connector_harness: ScriptedAgentHarness,
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _calls(
            ("create_payout", {"accountId": "a-1", "amount": 5, "memo": "uncertain"}),
            ("post_note", {"accountId": "a-1", "text": "paid"}),
            hold="skipped",
        ),
    )
    records = await _held_records(harness, session_id, "skipped")
    assert records["create_payout"].status is ToolCallStatus.OUTCOME_UNKNOWN
    skipped = records["post_note"]
    assert (skipped.status, skipped.skipped, skipped.error) == (
        ToolCallStatus.FAILED,
        True,
        SKIPPED_ERROR,
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    lines = _answer_lines(snapshot)
    assert OUTCOME_UNKNOWN_TOOL_RESULT in lines[0]
    assert lines[1].startswith("post_note: ") and SKIPPED_ERROR in lines[1]
    assert ledger.writes("postNote") == []


async def test_a_rejected_write_skips_the_later_writes_of_the_response(
    connector_harness: ScriptedAgentHarness,
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _calls(
            ("approved_payout", {"accountId": "a-1", "amount": 5}),
            ("post_note", {"accountId": "a-1", "text": "paid"}),
            hold="rejected",
        ),
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="not now"
    )
    records = await _held_records(harness, session_id, "rejected")
    assert records["approved_payout"].status is ToolCallStatus.REJECTED
    assert records["post_note"].skipped
    snapshot = await harness.wait_for_turns(session_id, 1)
    lines = _answer_lines(snapshot)
    assert lines[0].startswith("approved_payout: ") and "not now" in lines[0]
    assert lines[1].startswith("post_note: ") and SKIPPED_ERROR in lines[1]
    assert ledger.sends() == []


@pytest.mark.parametrize("withdrawn", ["empty", "raise"])
async def test_a_new_call_of_a_tool_its_source_withdrew_is_refused_before_any_step(
    connector_harness: ScriptedAgentHarness, withdrawn: str
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    first = {"accountId": "a-1", "text": "first gate-after-accept:withdraw"}
    second = {"accountId": "a-1", "text": "second"}
    await harness.sessions.start_session(
        session_id,
        _calls(("hiding_note", first)) + " then " + json.dumps([["hiding_note", second]]),
    )
    await harness.gate("withdraw").wait_until_reached()
    # The next RunModel's listing lacks the tool whose recorded call it must replay.
    (harness.state_dir / WITHDRAWN_FILE_NAME).write_text(withdrawn)
    harness.gate("withdraw").release()
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.state is not None and snapshot.state.last_turn_error is None
    lines = _answer_lines(snapshot)
    assert lines[0] == 'hiding_note: {"noteId":"postNote-1"}'
    assert lines[1].startswith("hiding_note: ")
    assert NOT_OFFERED in lines[1]
    assert len(ledger.sends("postNote")) == 1
    assert (await harness.executed_step_types(session_id)).count("CallSingleAttemptTool") == 1


@pytest.mark.parametrize("second_tool", ["get_balance", "post_note"])
async def test_a_reused_call_id_never_replays_a_recorded_result(
    connector_harness: ScriptedAgentHarness, second_tool: str
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    later = [[second_tool, {"accountId": "a-1", "text": "reused"}, "reused-id"]]
    if second_tool == "get_balance":
        later = [["get_balance", {"accountId": "a-1"}, "reused-id"]]
    await harness.sessions.start_session(
        session_id,
        "calls "
        + json.dumps([["get_balance", {"accountId": "a-1"}, "reused-id"]])
        + " then "
        + json.dumps(later),
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    # The turn fails instead of answering the new call with the recorded result.
    assert snapshot.state is not None and snapshot.state.last_turn_error
    assert ledger.sends("postNote") == []
    assert len(ledger.sends("getBalance")) <= 1


async def test_a_source_call_gets_its_operation_timeout_and_bounded_retries(
    tmp_path: Path,
) -> None:
    source = build_connector_source(tmp_path)
    runner = OpenAIAgentsRunner(build_connector_agent(tmp_path), tool_sources=[source])
    assert isinstance(runner, ToolCallStepOptionsProvider)
    turn = _turn()
    listing = await FrameworkToolSourceRouter([source]).list_source_tools(turn)
    deferred = listing.deferred_tool_call(
        turn, tool_call_id="call-0", tool_name="post_note", arguments='{"accountId":"a-1"}'
    )
    assert deferred is not None
    record = AgentToolCall(
        tool_call_id=deferred.tool_call_id,
        turn_id="turn-1",
        tool_name=deferred.tool_name,
        arguments=deferred.arguments,
        effect=deferred.policy.effect,
        requires_approval=deferred.policy.is_approval_required,
        status=ToolCallStatus.APPROVED,
        tool_source_reference=deferred.tool_source_reference,
    )
    options = runner.tool_call_step_options(record)
    assert options is not None
    assert options.execute_method_timeout == timedelta(seconds=30)
    assert options.execute_retry is not None and options.execute_retry.maximum_attempts == 3
    # Function tools keep the tool Steps' defaults.
    assert runner.tool_call_step_options(replace(record, tool_source_reference=None)) is None


def test_a_paused_record_replays_once_and_only_for_its_own_tool() -> None:
    record = AgentToolCall(
        tool_call_id="paused-id",
        turn_id="turn-1",
        tool_name="get_balance",
        arguments="{}",
        effect=ToolEffect.READ,
        requires_approval=False,
        status=ToolCallStatus.SUCCEEDED,
        output=BALANCE,
    )
    active_turn = ActiveTurn(
        turn=replace(_turn(), tool_calls={record.tool_call_id: record}),
        paused_call_ids={"paused-id"},
    )
    with pytest.raises(RuntimeError, match="recorded for get_balance, not post_note"):
        active_turn.take_paused_record("paused-id", "post_note")
    assert active_turn.take_paused_record("paused-id", "get_balance") is record
    # The SDK filters most repeats itself; a second replay of the same call ID is refused here.
    with pytest.raises(RuntimeError, match="not a paused call"):
        active_turn.take_paused_record("paused-id", "get_balance")
    with pytest.raises(RuntimeError, match="not a paused call"):
        active_turn.take_paused_record("unknown-id", "get_balance")


def test_an_unexposed_operation_is_refused_when_the_agent_is_defined(tmp_path: Path) -> None:
    with pytest.raises(ValueError, match="not exposed to agents"):
        openai_agents_session_flow(
            build_connector_agent(tmp_path),
            flow_type="UnexposedConnectorAgent",
            tool_sources=[
                ConnectorToolSource([connector_tool(CloseAccountOperation(), LEDGER_CONNECTION)])
            ],
        )


def test_a_source_tool_named_like_a_function_tool_is_refused(tmp_path: Path) -> None:
    balance = GetBalanceOperation(FakeLedgerProvider(tmp_path))
    source = ConnectorToolSource([connector_tool(balance, LEDGER_CONNECTION, name="lookup")])
    with pytest.raises(ValueError, match="tool lookup of a tool source uses the name"):
        openai_agents_session_flow(
            build_connector_agent(tmp_path), flow_type="CollidingAgent", tool_sources=[source]
        )


def test_two_sources_that_list_one_tool_name_are_refused(tmp_path: Path) -> None:
    balance = GetBalanceOperation(FakeLedgerProvider(tmp_path))
    sources = [
        ConnectorToolSource(
            [connector_tool(balance, LEDGER_CONNECTION, name="balance")], source_name=name
        )
        for name in ("first", "second")
    ]
    with pytest.raises(ValueError, match="two tool sources list a tool named balance"):
        openai_agents_session_flow(
            build_connector_agent(tmp_path), flow_type="CollidingAgent", tool_sources=sources
        )


def _turn() -> AgentTurnContext:
    async def heartbeat() -> None:
        return None

    return AgentTurnContext(
        flow_id="session-1",
        turn_id="turn-1",
        attempt=1,
        messages=(),
        framework_run_state=None,
        tool_calls={},
        tool_policies={},
        default_tool_effect=ToolEffect.UNCLASSIFIED,
        events=cast(AgentEventWriter, None),
        heartbeat=heartbeat,
    )
