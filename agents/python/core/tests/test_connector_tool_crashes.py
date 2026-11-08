# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""Worker kills during connector writes: the provider never takes a second effect.

Each case kills the Worker at one point of a write and checks the call's final
record, the provider's writes, and the requests that reached it. ``create_payout``
is a Stripe-like retry-safe write: the provider drops a repeated idempotency
key. ``post_note_retried`` is a fenced write in ``CallTool``; ``post_note`` is
the same fenced write in ``CallSingleAttemptTool``.
"""

from __future__ import annotations

import json

import pytest

from dex_agents import OUTCOME_UNKNOWN_TOOL_RESULT
from dex_agents.connectors import CONNECTOR_NOT_SENT_RETRY_LATER
from tests.conftest import ScriptedAgentHarness
from tests.fake_ledger import FakeLedgerProvider


async def _write_with_kill(
    harness: ScriptedAgentHarness, tool_name: str, arguments: dict[str, object], gate: str | None
) -> str:
    """Run one write, kill the Worker at the gate, and return the model's answer."""
    worker = await harness.stack.start_worker()
    session_id = harness.new_session_id()
    await harness.sessions.start_session(
        session_id, "calls " + json.dumps([[tool_name, arguments]])
    )
    if gate is not None:
        await harness.gate(gate).wait_until_reached()
        worker.kill()
        await harness.stack.start_worker()
    snapshot = await harness.wait_for_turns(session_id, 1)
    text = snapshot.messages[-1].text
    assert text is not None
    return text.removeprefix(f"{tool_name}: ")


# A retry-safe, keyed write in CallTool.


@pytest.mark.parametrize("point", ["gate-before-send", "gate-after-accept"])
async def test_a_killed_keyed_write_retries_with_the_same_key_and_takes_one_effect(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider, point: str
) -> None:
    answer = await _write_with_kill(
        connector_harness,
        "create_payout",
        {"accountId": "a-1", "amount": 5, "memo": f"{point}:kill"},
        "kill",
    )
    assert answer == '{"amount":5,"payoutId":"createPayout-1"}'
    assert len(ledger.writes("createPayout")) == 1
    keys = {send["key"] for send in ledger.sends("createPayout")}
    # The Call ID, and so the key, is the same on every attempt of the tool Step.
    assert len(keys) == 1
    assert len(ledger.sends("createPayout")) == (2 if point == "gate-after-accept" else 1)


async def test_a_proven_unsent_keyed_write_is_retried_and_sent(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    answer = await _write_with_kill(
        connector_harness,
        "create_payout",
        {"accountId": "a-1", "amount": 5, "memo": "rate-limit-once"},
        None,
    )
    assert answer == '{"amount":5,"payoutId":"createPayout-1"}'
    assert len(ledger.writes("createPayout")) == 1
    assert len(ledger.sends("rate-limited")) == 1


# A fenced write in CallTool: the fence and the session's heartbeats across retries.


@pytest.mark.parametrize(
    ("words", "expected_answer", "expected_writes"),
    [
        # No marker reached Dex, so the retry reads back and never sends.
        ("gate-before-send:kill", OUTCOME_UNKNOWN_TOOL_RESULT, 0),
        ("gate-after-claim:kill", OUTCOME_UNKNOWN_TOOL_RESULT, 0),
        # The provider accepted it, and read-back finds it.
        ("gate-after-accept:kill", '{"noteId":"postNote-1"}', 1),
        # The provider accepted it, and read-back cannot find it.
        ("hide gate-after-accept:kill", OUTCOME_UNKNOWN_TOOL_RESULT, 1),
    ],
)
async def test_a_killed_fenced_write_in_call_tool_never_sends_twice(
    connector_harness: ScriptedAgentHarness,
    ledger: FakeLedgerProvider,
    words: str,
    expected_answer: str,
    expected_writes: int,
) -> None:
    answer = await _write_with_kill(
        connector_harness, "post_note_retried", {"accountId": "a-1", "text": words}, "kill"
    )
    assert answer == expected_answer
    assert len(ledger.writes("postNote")) == expected_writes
    assert len(ledger.sends("postNote")) == expected_writes


async def test_a_fenced_write_marked_not_sent_is_retried_and_sent_once(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    # The retry reads the fence marker back through the session's text checkpoint.
    answer = await _write_with_kill(
        connector_harness,
        "post_note_retried",
        {"accountId": "a-1", "text": "rate-limit-once"},
        None,
    )
    assert answer == '{"noteId":"postNote-1"}'
    assert len(ledger.writes("postNote")) == 1
    assert len(ledger.sends("rate-limited")) == 1


# A fenced write in CallSingleAttemptTool.


@pytest.mark.parametrize(
    ("words", "expected_writes"),
    [("gate-before-send:kill", 0), ("gate-after-claim:kill", 0), ("gate-after-accept:kill", 1)],
)
async def test_a_killed_single_attempt_write_ends_outcome_unknown_and_never_sends_again(
    connector_harness: ScriptedAgentHarness,
    ledger: FakeLedgerProvider,
    words: str,
    expected_writes: int,
) -> None:
    answer = await _write_with_kill(
        connector_harness, "post_note", {"accountId": "a-1", "text": words}, "kill"
    )
    assert answer == OUTCOME_UNKNOWN_TOOL_RESULT
    assert len(ledger.writes("postNote")) == expected_writes
    assert len(ledger.sends("postNote")) == expected_writes


@pytest.mark.parametrize(
    ("words", "provider_refusals"),
    [
        # The fence recorded notSent before the claim, so the request never left the process.
        ("rate-limit-once", 1),
        # The provider refused the sent note without acting, and the fence recorded canResend.
        ("rate-limit-after-send", 1),
    ],
)
async def test_a_single_attempt_write_proven_unsent_fails_with_a_retry_later_error(
    connector_harness: ScriptedAgentHarness,
    ledger: FakeLedgerProvider,
    words: str,
    provider_refusals: int,
) -> None:
    answer = await _write_with_kill(
        connector_harness, "post_note", {"accountId": "a-1", "text": words}, None
    )
    assert answer == (
        f"The tool call failed: fake-ledger postNote {CONNECTOR_NOT_SENT_RETRY_LATER}: "
        "RATE_LIMIT: rate limited"
    )
    assert ledger.writes("postNote") == []
    assert len(ledger.sends("rate-limited")) == provider_refusals


async def test_a_kill_after_the_provider_refused_but_before_the_marker_stays_unknown(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    # The claim committed and the request left; no canResend proof reached the session.
    answer = await _write_with_kill(
        connector_harness,
        "post_note",
        {"accountId": "a-1", "text": "rate-limit-after-send gate-before-can-resend:kill"},
        "kill",
    )
    assert answer == OUTCOME_UNKNOWN_TOOL_RESULT
    assert ledger.writes("postNote") == []
    assert len(ledger.sends("rate-limited")) == 1


async def test_a_retry_safe_fenced_write_refused_after_send_is_retried_and_sent_once(
    connector_harness: ScriptedAgentHarness, ledger: FakeLedgerProvider
) -> None:
    answer = await _write_with_kill(
        connector_harness,
        "post_note_retried",
        {"accountId": "a-1", "text": "rate-limit-after-send"},
        None,
    )
    assert answer == '{"noteId":"postNote-1"}'
    assert len(ledger.writes("postNote")) == 1
