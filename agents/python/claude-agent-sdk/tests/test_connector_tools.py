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
from pathlib import Path
from typing import Any

import pytest
from claude_agent_sdk import ClaudeAgentOptions, tool

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    AgentSnapshot,
    AgentToolCall,
    MessageRole,
    ToolCallStatus,
    ToolEffect,
)
from dex_agents.connectors import ConnectorToolSource, connector_tool
from dex_agents_claude_agent_sdk import ClaudeAgentRunner, claude_agent_session_flow
from tests.claude_test_agent import (
    ALLOWED_TOOLS_ENVIRONMENT_VARIABLE,
    RAISED_PAYOUT_AMOUNT,
    RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE,
)
from tests.conftest import ClaudeAgentHarness
from tests.fake_ledger import (
    LEDGER_CONNECTION,
    CloseAccountOperation,
    FakeLedgerProvider,
    GetBalanceOperation,
)

BALANCE = '{"accountId":"a-1","amount":100}'
FIRST_PAYOUT = '{"amount":5,"payoutId":"createPayout-1"}'


def _call(tool_name: str, arguments: dict[str, Any]) -> str:
    return f"call {tool_name} {json.dumps(arguments)}"


def _ledger(harness: ClaudeAgentHarness) -> FakeLedgerProvider:
    return FakeLedgerProvider(harness.state_dir)


async def _held_records(
    harness: ClaudeAgentHarness, session_id: str, gate: str
) -> dict[str, AgentToolCall]:
    """Return the turn's records, by tool name, while the final answer waits at the gate."""
    await harness.gate(gate).wait_until_reached()
    snapshot = await harness.sessions.get_agent_snapshot(session_id)
    harness.gate(gate).release()
    return {record.tool_name: record for record in snapshot.tool_calls}


def _tool_outputs(snapshot: AgentSnapshot, tool_name: str) -> list[str]:
    return [
        message.tool_output or ""
        for message in snapshot.messages
        if message.role is MessageRole.TOOL and message.tool_name == tool_name
    ]


async def test_a_query_tool_is_deferred_and_runs_in_its_tool_step(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _call("get_balance", {"accountId": "a-1"}) + " ;; pause held"
    )
    record = (await _held_records(harness, session_id, "held"))["get_balance"]
    assert (record.effect, record.status, record.output) == (
        ToolEffect.READ,
        ToolCallStatus.SUCCEEDED,
        BALANCE,
    )
    assert not record.requires_approval and not record.ordered_write

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == f"get_balance: {BALANCE}"
    assert _tool_outputs(snapshot, "get_balance") == [BALANCE]
    assert await harness.executed_step_types(session_id) == [
        "StartAgentSession",
        "RunModel",
        "CallTool",
        "ResumeTurn",
        "RunModel",
    ]


async def test_a_mutation_with_manifest_approval_runs_once_after_approval(
    harness: ClaudeAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _call("approved_payout", {"accountId": "a-1", "amount": 5})
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    waiting = await harness.sessions.get_agent_snapshot(session_id)
    assert interrupt.tool_name == "approved_payout"
    assert [(call.effect, call.status, call.ordered_write) for call in waiting.tool_calls] == [
        (ToolEffect.IDEMPOTENT_WRITE, ToolCallStatus.AWAITING_APPROVAL, True)
    ]

    worker.kill()
    await harness.stack.start_worker()
    assert ledger.sends() == []
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == f"approved_payout: {FIRST_PAYOUT}"
    assert len(ledger.writes("createPayout")) == 1
    assert len(ledger.sends("createPayout")) == 1


async def test_a_session_policy_changes_only_the_approval_of_a_connector_tool(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _call("policy_note", {"accountId": "a-1", "text": "hello"})
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
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == "policy_note: not now"
    assert _ledger(harness).sends() == []


async def test_an_approval_predicate_decides_on_the_arguments_the_step_runs(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _call("payout_checked", {"accountId": "a-1", "amount": 10})
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == 'payout_checked: {"amount":10,"payoutId":"createPayout-1"}'

    # An application hook raises the amount; the predicate sees the raised amount.
    await harness.sessions.send_user_message(
        session_id, _call("payout_checked", {"accountId": "a-1", "amount": 10, "memo": "raise"})
    )
    interrupt = await harness.wait_for_pending_interrupt(session_id)
    assert json.loads(interrupt.arguments)["amount"] == RAISED_PAYOUT_AMOUNT
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert snapshot.messages[-1].text == (
        f'payout_checked: {{"amount":{RAISED_PAYOUT_AMOUNT},"payoutId":"createPayout-2"}}'
    )


async def test_a_retry_safe_mutation_retries_with_the_same_call_id(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _call("create_payout", {"accountId": "a-1", "amount": 5, "memo": "rate-limit-once"}),
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == f"create_payout: {FIRST_PAYOUT}"
    [refused] = ledger.sends("rate-limited")
    [paid] = ledger.writes("createPayout")
    # The Call ID, and so the idempotency key, is the same on both attempts of the tool Step.
    assert refused["key"] == paid["key"]
    assert "CallTool" in await harness.executed_step_types(session_id)


async def test_a_killed_retry_safe_mutation_retries_with_the_same_key_and_takes_one_effect(
    harness: ClaudeAgentHarness,
) -> None:
    worker = await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _call("create_payout", {"accountId": "a-1", "amount": 5, "memo": "gate-after-accept:kill"}),
    )
    await harness.gate("kill").wait_until_reached()
    worker.kill()
    await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == f"create_payout: {FIRST_PAYOUT}"
    assert len(ledger.writes("createPayout")) == 1
    sends = ledger.sends("createPayout")
    assert len(sends) == 2
    assert len({send["key"] for send in sends}) == 1


@pytest.mark.parametrize(
    ("tool_name", "arguments", "step_type"),
    [
        (
            "create_payout",
            {"accountId": "a-1", "amount": 5, "memo": "uncertain"},
            "CallTool",
        ),
        ("post_note", {"accountId": "a-1", "text": "uncertain"}, "CallSingleAttemptTool"),
    ],
)
async def test_an_uncertain_write_records_outcome_unknown_and_never_runs_again(
    harness: ClaudeAgentHarness, tool_name: str, arguments: dict[str, Any], step_type: str
) -> None:
    await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, _call(tool_name, arguments) + " ;; pause held")
    record = (await _held_records(harness, session_id, "held"))[tool_name]
    assert record.status is ToolCallStatus.OUTCOME_UNKNOWN
    assert record.error is not None and "uncertain" in record.error

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == f"{tool_name}: {OUTCOME_UNKNOWN_TOOL_RESULT}"
    assert len(ledger.writes()) == 1
    assert len(ledger.sends()) == 1
    assert step_type in await harness.executed_step_types(session_id)


async def test_a_rejected_branch_and_bad_arguments_fail_without_reaching_the_provider(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _call("post_note", {"accountId": "a-1", "text": "reject"})
    )
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.messages[-1].text == (
        "post_note: The tool call failed: fake-ledger postNote returned providerRejected: "
        "PROVIDER_REJECTION: channel archived"
    )

    await harness.sessions.send_user_message(session_id, _call("get_balance", {"accountId": 5}))
    snapshot = await harness.wait_for_turns(session_id, 2)
    text = snapshot.messages[-1].text or ""
    assert text.startswith("get_balance: The tool call failed: invalid arguments for get_balance")
    assert _ledger(harness).sends() == []


async def test_two_writes_in_one_reply_run_one_at_a_time_in_the_model_order(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker()
    ledger = _ledger(harness)
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _call("post_note", {"accountId": "a-1", "text": "first gate-after-accept:first-write"})
        + " && "
        + _call("create_payout", {"accountId": "a-1", "amount": 5}),
    )
    await harness.gate("first-write").wait_until_reached()
    running = await harness.sessions.get_agent_snapshot(session_id)
    # The second write never left the turn while the first one runs.
    assert [record.tool_name for record in running.tool_calls] == ["post_note"]
    assert [entry["operation"] for entry in ledger.sends()] == ["postNote"]
    harness.gate("first-write").release()

    snapshot = await harness.wait_for_turns(session_id, 1)
    assert [entry["operation"] for entry in ledger.writes()] == ["postNote", "createPayout"]
    assert snapshot.messages[-1].text == (
        'post_note: {"noteId":"postNote-1"}; '
        'create_payout: {"amount":5,"payoutId":"createPayout-2"}'
    )
    payout_outputs = _tool_outputs(snapshot, "create_payout")
    assert "Writes run one at a time in your order" in payout_outputs[0]
    assert payout_outputs[1:] == ['{"amount":5,"payoutId":"createPayout-2"}']


async def test_an_allowed_tools_entry_for_a_source_tool_fails_the_turn(
    harness: ClaudeAgentHarness,
) -> None:
    await harness.stack.start_worker(
        {
            ALLOWED_TOOLS_ENVIRONMENT_VARIABLE: "mcp__tools__get_balance",
            RUN_MODEL_LIMIT_ENVIRONMENT_VARIABLE: "60,1",
        }
    )
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, _call("get_balance", {"accountId": "a-1"}))
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert snapshot.state is not None
    assert "allowed_tools entry 'mcp__tools__get_balance'" in (snapshot.state.last_turn_error or "")
    assert _ledger(harness).sends() == []


def test_an_unexposed_operation_is_refused_when_the_agent_is_defined() -> None:
    with pytest.raises(ValueError, match="not exposed to agents"):
        claude_agent_session_flow(
            lambda context: ClaudeAgentOptions(),
            flow_type="UnexposedConnectorAgent",
            tool_sources=[
                ConnectorToolSource([connector_tool(CloseAccountOperation(), LEDGER_CONNECTION)])
            ],
        )


@pytest.mark.parametrize("tool_name", ["lookup", "Bash", "WebFetch"])
def test_a_source_tool_named_like_a_custom_or_claude_code_tool_is_refused(
    tool_name: str, tmp_path: Path
) -> None:
    @tool("lookup", "Look up a record", {"query": str})
    async def lookup(arguments: dict[str, Any]) -> dict[str, Any]:
        return {"content": [{"type": "text", "text": "never runs"}]}

    balance = GetBalanceOperation(FakeLedgerProvider(tmp_path))
    source = ConnectorToolSource([connector_tool(balance, LEDGER_CONNECTION, name=tool_name)])
    with pytest.raises(ValueError, match=f"tool {tool_name} of a tool source"):
        ClaudeAgentRunner(lambda context: ClaudeAgentOptions(), [lookup], tool_sources=[source])


def test_two_sources_with_one_name_or_one_tool_name_are_refused(tmp_path: Path) -> None:
    balance = GetBalanceOperation(FakeLedgerProvider(tmp_path))

    def source(source_name: str, tool_name: str) -> ConnectorToolSource:
        tools = [connector_tool(balance, LEDGER_CONNECTION, name=tool_name)]
        return ConnectorToolSource(tools, source_name=source_name)

    with pytest.raises(ValueError, match="share a source_name"):
        ClaudeAgentRunner(
            lambda context: ClaudeAgentOptions(),
            tool_sources=[source("ledger", "balance_a"), source("ledger", "balance_b")],
        )
    with pytest.raises(ValueError, match="two tool sources list a tool named balance"):
        ClaudeAgentRunner(
            lambda context: ClaudeAgentOptions(),
            tool_sources=[source("ledger-a", "balance"), source("ledger-b", "balance")],
        )
