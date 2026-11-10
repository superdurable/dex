# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Connector tools on a real Dex engine: mapping, validation, ordering, approval, and crashes."""

from __future__ import annotations

import asyncio
import json
from typing import Any
from uuid import uuid4

from dex_agents import (
    OUTCOME_UNKNOWN_TOOL_RESULT,
    TOOL_CALL_NOT_RUN_ERROR_PREFIX,
    TOOL_CALL_SKIPPED_AFTER_WRITE_DETAIL,
    AgentInterrupt,
    AgentSnapshot,
    AgentToolCall,
    ToolCallStatus,
)
from dex_agents.connectors import CONNECTOR_NOT_SENT_RETRY_LATER, CONNECTOR_RESULT_TRUNCATED_NOTE
from dex_agents.testing import resolve_dexcli_path, wait_for_agent_snapshot
from dex_agents.ui_stream import agent_snapshot_to_json
from tests.conftest import ScriptedAgentHarness
from tests.connector_agent import (
    DROPPED_TOOLS_ENVIRONMENT_VARIABLE,
    MAX_RESULT_BYTES_ENVIRONMENT_VARIABLE,
)
from tests.fake_ledger import SECRET_ENVIRONMENT_VARIABLE, FakeLedgerProvider

SKIPPED_ERROR = f"{TOOL_CALL_NOT_RUN_ERROR_PREFIX}: {TOOL_CALL_SKIPPED_AFTER_WRITE_DETAIL}"
FAILED = "The tool call failed: "


def _calls(*calls: tuple[str, dict[str, Any]], hold: str | None = None) -> str:
    command = "calls " + json.dumps([[name, arguments] for name, arguments in calls])
    return command if hold is None else f"hold:{hold} {command}"


def _answer_lines(snapshot: AgentSnapshot) -> list[str]:
    text = snapshot.messages[-1].text
    assert text is not None
    return text.split("\n")


async def _run_turn(
    harness: ScriptedAgentHarness, *calls: tuple[str, dict[str, Any]]
) -> tuple[str, list[str]]:
    session_id = harness.new_session_id()
    await harness.sessions.start_session(session_id, _calls(*calls))
    return session_id, _answer_lines(await harness.wait_for_turns(session_id, 1))


async def _held_records(
    harness: ScriptedAgentHarness, session_id: str, gate: str
) -> dict[str, AgentToolCall]:
    """Return the turn's records while the model's final answer waits at the gate."""
    await harness.gate(gate).wait_until_reached()
    snapshot = await harness.sessions.get_agent_snapshot(session_id)
    harness.gate(gate).release()
    return {record.tool_call_id: record for record in snapshot.tool_calls}


async def _pending_interrupt(harness: ScriptedAgentHarness, session_id: str) -> AgentInterrupt:
    return await wait_for_agent_snapshot(
        harness.sessions,
        session_id,
        lambda snapshot: snapshot.pending_interrupts[0] if snapshot.pending_interrupts else None,
        description="a pending interrupt",
    )


# O1-1 and O1-2


async def test_a_query_tool_returns_its_result_to_the_model(
    connector_harness: ScriptedAgentHarness,
) -> None:
    await connector_harness.stack.start_worker()
    _, lines = await _run_turn(connector_harness, ("get_balance", {"accountId": "a-1"}))
    assert lines == ['get_balance: {"accountId":"a-1","amount":100}']


async def test_each_branch_kind_maps_to_its_record_and_the_session_continues(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _calls(
            ("get_balance", {"accountId": "missing"}),
            ("get_balance", {"accountId": "garbled"}),
            ("create_payout", {"accountId": "a-1", "amount": 5, "memo": "reject"}),
            hold="mapped",
        ),
    )
    records = await _held_records(harness, session_id, "mapped")
    assert [records[f"call-{index}"].status for index in range(3)] == [
        ToolCallStatus.FAILED,
        ToolCallStatus.FAILED,
        ToolCallStatus.FAILED,
    ]
    assert records["call-0"].error == (
        "fake-ledger getBalance returned notFound: PROVIDER_REJECTION: no account"
    )
    assert records["call-1"].error == (
        "fake-ledger getBalance returned invalidResponse: PROVIDER_REJECTION: bad body"
    )
    assert records["call-2"].error == (
        "fake-ledger createPayout returned providerRejected: PROVIDER_REJECTION: insufficient funds"
    )
    assert not records["call-2"].skipped
    await harness.wait_for_turns(session_id, 1)

    # An unlisted optional branch of a Mutation and uncertain both fail closed.
    await harness.sessions.send_user_message(
        session_id,
        _calls(
            ("create_payout", {"accountId": "a-1", "amount": 5, "memo": "invalid"}),
            hold="unknown",
        ),
    )
    records = await _held_records(harness, session_id, "unknown")
    assert records["call-0"].status is ToolCallStatus.OUTCOME_UNKNOWN
    assert records["call-0"].error == (
        "fake-ledger createPayout returned invalidResponse: PROVIDER_REJECTION: bad body"
    )
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert _answer_lines(snapshot) == [f"create_payout: {OUTCOME_UNKNOWN_TOOL_RESULT}"]
    assert len(ledger.writes("createPayout")) == 1

    await harness.sessions.send_user_message(
        session_id,
        _calls(("create_payout", {"accountId": "a-1", "amount": 5, "memo": "uncertain"})),
    )
    snapshot = await harness.wait_for_turns(session_id, 3)
    assert _answer_lines(snapshot) == [f"create_payout: {OUTCOME_UNKNOWN_TOOL_RESULT}"]
    assert snapshot.state is not None and snapshot.state.last_turn_error is None


# Schema validation before decoding


async def test_arguments_that_break_the_schema_fail_without_running_the_operation(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    await connector_harness.stack.start_worker()
    _, lines = await _run_turn(
        connector_harness,
        ("get_balance", {"accountId": 7}),
        ("get_balance", {"accountId": "a-1", "acountId": "typo"}),
        ("create_payout", {"accountId": "a-1"}),
    )
    assert lines == [
        f"get_balance: {FAILED}invalid arguments for get_balance: "
        "arguments.accountId must be string",
        f"get_balance: {FAILED}invalid arguments for get_balance: "
        "arguments.acountId is not allowed",
        f"create_payout: {FAILED}invalid arguments for create_payout: arguments.amount is required",
    ]
    assert ledger.sends() == []


# O1-5: ordering and skip


async def test_writes_run_in_the_model_order_and_reads_run_in_parallel(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _calls(
            ("post_note", {"accountId": "a-1", "text": "first gate-after-accept:first-write"}),
            ("get_balance", {"accountId": "a-1"}),
            ("create_payout", {"accountId": "a-1", "amount": 5}),
        ),
    )
    await harness.gate("first-write").wait_until_reached()
    snapshot = await harness.sessions.get_agent_snapshot(session_id)
    records = {record.tool_call_id: record for record in snapshot.tool_calls}
    # The read finished beside the first write; the second write has not started.
    assert records["call-1"].status is ToolCallStatus.SUCCEEDED
    assert records["call-2"].write_queued and records["call-2"].ordered_write
    assert ledger.writes("createPayout") == []
    harness.gate("first-write").release()
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert [entry["operation"] for entry in ledger.writes()] == ["postNote", "createPayout"]
    assert _answer_lines(snapshot)[2] == 'create_payout: {"amount":5,"payoutId":"createPayout-2"}'


async def test_a_failed_or_unknown_write_skips_the_later_writes_of_the_response(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _calls(
            ("create_payout", {"accountId": "a-1", "amount": 5, "memo": "reject"}),
            ("get_balance", {"accountId": "a-1"}),
            ("post_note", {"accountId": "a-1", "text": "refunded"}),
            ("create_payout", {"accountId": "a-1", "amount": 7}),
            hold="skipped",
        ),
    )
    records = await _held_records(harness, session_id, "skipped")
    assert records["call-1"].status is ToolCallStatus.SUCCEEDED
    for skipped in ("call-2", "call-3"):
        assert records[skipped].status is ToolCallStatus.FAILED
        assert records[skipped].skipped
        assert not records[skipped].write_queued
        assert records[skipped].error == SKIPPED_ERROR
    assert not records["call-0"].skipped
    await harness.wait_for_turns(session_id, 1)
    assert ledger.sends() == []

    await harness.sessions.send_user_message(
        session_id,
        _calls(
            ("create_payout", {"accountId": "a-1", "amount": 5, "memo": "uncertain"}),
            ("post_note", {"accountId": "a-1", "text": "paid"}),
        ),
    )
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert _answer_lines(snapshot) == [
        f"create_payout: {OUTCOME_UNKNOWN_TOOL_RESULT}",
        f"post_note: {FAILED}{SKIPPED_ERROR}",
    ]
    assert ledger.writes("postNote") == []


async def test_a_write_proven_unsent_still_skips_the_later_writes(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    await connector_harness.stack.start_worker()
    _, lines = await _run_turn(
        connector_harness,
        ("post_note", {"accountId": "a-1", "text": "rate-limit-once"}),
        ("create_payout", {"accountId": "a-1", "amount": 5}),
    )
    assert lines == [
        f"post_note: {FAILED}fake-ledger postNote {CONNECTOR_NOT_SENT_RETRY_LATER}: "
        "RATE_LIMIT: rate limited",
        f"create_payout: {FAILED}{SKIPPED_ERROR}",
    ]
    assert ledger.writes() == []


# O1-6: approval


async def test_the_manifest_approval_and_an_override_both_apply(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _calls(("approved_payout", {"accountId": "a-1", "amount": 5}))
    )
    interrupt = await _pending_interrupt(harness, session_id)
    assert interrupt.tool_name == "approved_payout"
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    await harness.wait_for_turns(session_id, 1)
    assert len(ledger.writes("createPayout")) == 1

    # create_payout overrides the manifest's recommendation; post_note has none.
    await harness.sessions.send_user_message(
        session_id,
        _calls(
            ("create_payout", {"accountId": "a-1", "amount": 5}),
            ("post_note", {"accountId": "a-1", "text": "done"}),
        ),
    )
    snapshot = await harness.wait_for_turns(session_id, 2)
    assert snapshot.pending_interrupts == []
    assert len(ledger.writes()) == 3


async def test_an_argument_predicate_decides_approval_per_call(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    session_id, _ = await _run_turn(harness, ("payout_checked", {"accountId": "a-1", "amount": 10}))
    assert len(ledger.writes("createPayout")) == 1

    await harness.sessions.send_user_message(
        session_id, _calls(("payout_checked", {"accountId": "a-1", "amount": 100}))
    )
    interrupt = await _pending_interrupt(harness, session_id)
    assert interrupt.tool_name == "payout_checked"
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=False, rejection_message="too much"
    )
    await harness.wait_for_turns(session_id, 2)
    assert len(ledger.writes("createPayout")) == 1


async def test_a_queued_write_asks_for_approval_only_when_its_turn_comes(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _calls(
            ("post_note", {"accountId": "a-1", "text": "first gate-after-accept:note"}),
            ("approved_payout", {"accountId": "a-1", "amount": 5}),
            ("post_note", {"accountId": "a-1", "text": "after the payout"}),
        ),
    )
    await harness.gate("note").wait_until_reached()
    assert (await harness.sessions.get_agent_snapshot(session_id)).pending_interrupts == []
    harness.gate("note").release()
    interrupt = await _pending_interrupt(harness, session_id)
    assert interrupt.tool_call_id == "call-1"
    assert len(ledger.writes("postNote")) == 1

    # A rejected approval stops the later writes too.
    assert await harness.sessions.resolve_interrupt(session_id, "call-1", approved=False)
    snapshot = await harness.wait_for_turns(session_id, 1)
    assert _answer_lines(snapshot)[2] == f"post_note: {FAILED}{SKIPPED_ERROR}"
    assert len(ledger.writes()) == 1


async def test_cancel_turn_cancels_the_queued_writes(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    harness = connector_harness
    await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id,
        _calls(
            ("post_note", {"accountId": "a-1", "text": "first gate-after-accept:cancel"}),
            ("create_payout", {"accountId": "a-1", "amount": 5}),
        ),
    )
    await harness.gate("cancel").wait_until_reached()
    assert await harness.sessions.cancel_turn(session_id)
    harness.gate("cancel").release()
    await harness.wait_for_turns(session_id, 1)
    assert [entry["operation"] for entry in ledger.writes()] == ["postNote"]


# Result size


async def test_a_long_result_reaches_the_model_truncated_and_the_record_says_so(
    connector_harness: ScriptedAgentHarness,
) -> None:
    harness = connector_harness
    await harness.stack.start_worker({MAX_RESULT_BYTES_ENVIRONMENT_VARIABLE: "40"})
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _calls(("get_balance", {"accountId": "a-1", "padding": 100}), hold="big")
    )
    records = await _held_records(harness, session_id, "big")
    full = json.dumps(
        {"accountId": "a-1", "amount": 100, "filler": "x" * 100},
        separators=(",", ":"),
        sort_keys=True,
    )
    note = CONNECTOR_RESULT_TRUNCATED_NOTE.format(total_bytes=len(full))
    assert records["call-0"].output == f"{full[:40]}\n{note}"
    assert records["call-0"].truncated_output_bytes == len(full)


# O1-7: a tool removed while a call is stored


async def test_a_removed_tool_ends_its_stored_call_by_its_effect_class(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    harness = connector_harness
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, _calls(("approved_payout", {"accountId": "a-1", "amount": 5}))
    )
    await _pending_interrupt(harness, session_id)
    worker.kill()
    await harness.stack.start_worker({DROPPED_TOOLS_ENVIRONMENT_VARIABLE: "approved_payout"})
    assert await harness.sessions.resolve_interrupt(session_id, "call-0", approved=True)
    snapshot = await harness.wait_for_turns(session_id, 1)
    # approved_payout is IDEMPOTENT_WRITE: retries run out and the write may have run.
    assert _answer_lines(snapshot) == [f"approved_payout: {OUTCOME_UNKNOWN_TOOL_RESULT}"]
    assert ledger.sends() == []


# O1-8: credentials


async def _hydrated_history(harness: ScriptedAgentHarness, session_id: str) -> str:
    process = await asyncio.create_subprocess_exec(
        str(resolve_dexcli_path()),
        "flow",
        "history",
        session_id,
        "--server",
        harness.stack.engine.address,
        "--all",
        "--output",
        "json",
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.PIPE,
    )
    stdout, stderr = await process.communicate()
    assert process.returncode == 0, stderr.decode(errors="replace")
    return stdout.decode()


async def test_a_connection_secret_never_reaches_session_state_records_streams_or_history(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    harness = connector_harness
    secret = f"ledger-secret-{uuid4().hex}"
    await harness.stack.start_worker({SECRET_ENVIRONMENT_VARIABLE: secret})
    session_id = harness.new_session_id()
    seen: list[str] = []

    async def turn(command: str, turn_count: int) -> None:
        if turn_count == 1:
            await harness.sessions.start_session(session_id, command)
        else:
            await harness.sessions.send_user_message(session_id, command)
        snapshot = await harness.wait_for_turns(session_id, turn_count)
        seen.append(json.dumps(agent_snapshot_to_json(snapshot)))

    await turn(_calls(("get_balance", {"accountId": "a-1"})), 1)
    await turn(_calls(("create_payout", {"accountId": "a-1", "amount": 5})), 2)
    await turn(_calls(("post_note", {"accountId": "a-1", "text": "paid"})), 3)
    await turn(
        _calls(
            ("create_payout", {"accountId": "a-1", "amount": 5, "memo": "reject"}),
            ("create_payout", {"accountId": "a-1", "amount": 5, "memo": "uncertain"}),
        ),
        4,
    )
    await harness.sessions.send_user_message(
        session_id, _calls(("approved_payout", {"accountId": "a-1", "amount": 6}), hold="held")
    )
    interrupt = await _pending_interrupt(harness, session_id)
    seen.append(
        json.dumps(agent_snapshot_to_json(await harness.sessions.get_agent_snapshot(session_id)))
    )
    assert await harness.sessions.resolve_interrupt(
        session_id, interrupt.interrupt_id, approved=True
    )
    records = await _held_records(harness, session_id, "held")
    seen.extend(repr(record) for record in records.values())
    await harness.wait_for_turns(session_id, 5)

    seen.extend(repr(event) for event in await harness.agent_events(session_id))
    seen.append(await _hydrated_history(harness, session_id))
    assert all(secret not in text for text in seen)
    # The secret was in use: every request the provider received was authorized with it.
    # Turn 4's second payout was skipped after the rejected one, so three requests were sent.
    sends = ledger.sends()
    assert [send["operation"] for send in sends] == ["createPayout", "postNote", "createPayout"]
    assert all(send["authorized"] == "True" for send in sends)
