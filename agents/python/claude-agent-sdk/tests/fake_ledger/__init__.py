# Copyright (c) 2026 Super Durable, Inc.
#
# Licensed under the Sustainable Use License 1.0.
# You may not use this file except in compliance with the License.
# See the LICENSE file in the repository root.
#
# SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

"""A fake Connector package: a ledger provider kept in files, with Connector SDK operations.

A copy of ``agents/python/core/tests/fake_ledger``.

It ships ``dex-connector.json`` as package data, as a real Connector package
does. ``getBalance`` is a Query. ``createPayout`` is a Stripe-like Mutation: the
provider drops a repeated idempotency key, so it is ``retry: safe``.
``postNote`` is a Slack-like Mutation guarded by the single-dispatch fence; the
provider does not drop repeats. ``closeAccount`` is not exposed to agents.

Space-separated words in a payout ``memo`` or a note ``text`` steer the provider:
``reject``, ``invalid``, ``uncertain``, ``rate-limit-once`` (refused before the
claim, ``notSent``), ``rate-limit-after-send`` (the provider refuses the sent
note, ``canResend``), ``hide`` (read-back finds nothing), and
``gate-before-send:<gate>``, ``gate-after-claim:<gate>``,
``gate-after-accept:<gate>``, or ``gate-before-can-resend:<gate>``, which wait at
a ``FileGate`` on attempt 1 only.
"""

from __future__ import annotations

import json
import os
from dataclasses import dataclass, field
from datetime import datetime, timedelta
from pathlib import Path
from typing import cast

from dex_connectors.sdk import (
    BranchDefinition,
    Call,
    CallID,
    ConnectionRef,
    Failure,
    FailureKind,
    IdempotencyKey,
    MutationAttempt,
    MutationDefinition,
    OperationRef,
    QueryAttempt,
    QueryDefinition,
    Receipt,
    SecretString,
    StaticCredentialProvider,
    StepDefaults,
)
from dex_connectors.sdk.singledispatch import DecisionKind, Fence

from dex_agents.testing import FileGate

CONNECTOR_ID = "fake-ledger"
STATE_DIR_ENVIRONMENT_VARIABLE = "FAKE_LEDGER_STATE_DIR"
#: The Worker reads the ledger connection secret from this variable, as from a secret store.
SECRET_ENVIRONMENT_VARIABLE = "FAKE_LEDGER_SECRET"
DEFAULT_SECRET = "unset-test-secret"
LEDGER_CONNECTION = ConnectionRef(CONNECTOR_ID, "test-ledger")
_DEFAULTS = StepDefaults(timedelta(seconds=30), 1)


@dataclass(frozen=True)
class GetBalanceInput:
    """One account to read."""

    account_id: str = field(metadata={"dex_json_name": "accountId"})
    padding: int = field(default=0, metadata={"dex_json_omit_default": True})


@dataclass(frozen=True)
class Balance:
    """An account balance."""

    account_id: str = field(metadata={"dex_json_name": "accountId"})
    amount: int
    filler: str = field(default="", metadata={"dex_json_omit_default": True})


@dataclass(frozen=True)
class CreatePayoutInput:
    """One payout."""

    account_id: str = field(metadata={"dex_json_name": "accountId"})
    amount: int
    memo: str = field(default="", metadata={"dex_json_omit_default": True})


@dataclass(frozen=True)
class Payout:
    """A payout the provider made."""

    payout_id: str = field(default="", metadata={"dex_json_name": "payoutId"})
    amount: int = 0


@dataclass(frozen=True)
class PostNoteInput:
    """One note."""

    account_id: str = field(metadata={"dex_json_name": "accountId"})
    text: str


@dataclass(frozen=True)
class Note:
    """A note the provider stored."""

    note_id: str = field(default="", metadata={"dex_json_name": "noteId"})


@dataclass(frozen=True)
class CloseAccountInput:
    """One account to close."""

    account_id: str = field(metadata={"dex_json_name": "accountId"})


def _definition(kind: type[QueryDefinition], operation_id: str, *branches: str) -> QueryDefinition:
    declared = tuple(
        BranchDefinition(branch, f"The {branch} branch.", optional=index > 0)
        for index, branch in enumerate(branches)
    )
    return kind(OperationRef(CONNECTOR_ID, operation_id), declared, _DEFAULTS)


GET_BALANCE_DEFINITION = _definition(
    QueryDefinition, "getBalance", "found", "notFound", "invalidResponse", "defect"
)
CREATE_PAYOUT_DEFINITION = _definition(
    MutationDefinition,
    "createPayout",
    "paid",
    "providerRejected",
    "uncertain",
    "invalidResponse",
    "defect",
)
POST_NOTE_DEFINITION = _definition(
    MutationDefinition, "postNote", "posted", "providerRejected", "uncertain", "defect"
)
CLOSE_ACCOUNT_DEFINITION = _definition(MutationDefinition, "closeAccount", "closed", "defect")


class FakeLedgerProvider:
    """The provider: an append-only file of writes in a state directory."""

    def __init__(self, state_dir: Path) -> None:
        self.state_dir = state_dir
        self._writes = state_dir / "fake-ledger-writes.jsonl"
        self._sends = state_dir / "fake-ledger-sends.jsonl"
        secret = os.environ.get(SECRET_ENVIRONMENT_VARIABLE, DEFAULT_SECRET)
        self._expected_secret = secret
        self.credentials = StaticCredentialProvider({LEDGER_CONNECTION: SecretString(secret)})

    async def authorize(self, call: Call) -> bool:
        """Resolve the connection secret at call time and check it, as a provider would."""
        secret = await self.credentials.resolve(call)
        return secret.reveal() == self._expected_secret

    @classmethod
    def from_environment(cls) -> FakeLedgerProvider:
        return cls(Path(os.environ[STATE_DIR_ENVIRONMENT_VARIABLE]))

    def writes(self, operation: str | None = None) -> list[dict[str, str]]:
        """Return every write that took effect, oldest first."""
        return [
            entry
            for entry in _read_lines(self._writes)
            if operation is None or entry["operation"] == operation
        ]

    def sends(self, operation: str | None = None) -> list[dict[str, str]]:
        """Return every request that reached the provider, including repeats it dropped."""
        return [
            entry
            for entry in _read_lines(self._sends)
            if operation is None or entry["operation"] == operation
        ]

    def accept(
        self, operation: str, key: str, *, deduplicate: bool, hidden: bool, authorized: bool = True
    ) -> str:
        _append_line(
            self._sends, {"operation": operation, "key": key, "authorized": str(authorized)}
        )
        if deduplicate:
            for entry in self.writes(operation):
                if entry["key"] == key:
                    return entry["id"]
        object_id = f"{operation}-{len(self.writes()) + 1}"
        _append_line(
            self._writes,
            {"operation": operation, "key": key, "id": object_id, "hidden": str(hidden)},
        )
        return object_id

    def find(self, operation: str, key: str) -> str | None:
        for entry in self.writes(operation):
            if entry["key"] == key and entry["hidden"] != "True":
                return entry["id"]
        return None

    def gate(self, name: str) -> FileGate:
        return FileGate(self.state_dir, name)


class GetBalanceOperation:
    """Read a balance."""

    def __init__(self, provider: FakeLedgerProvider) -> None:
        self._provider = provider

    def definition(self) -> QueryDefinition:
        return GET_BALANCE_DEFINITION

    async def invoke(self, call: Call, input: GetBalanceInput) -> QueryAttempt[Balance]:
        if not await self._provider.authorize(call):
            return QueryAttempt.branch_result(
                "notFound", Balance(input.account_id, 0), Receipt(), _refused("getBalance")
            )
        if input.account_id == "missing":
            return QueryAttempt.branch_result(
                "notFound",
                Balance(input.account_id, 0),
                Receipt(),
                _failure("getBalance", "no account"),
            )
        if input.account_id == "garbled":
            return QueryAttempt.branch_result(
                "invalidResponse",
                Balance(input.account_id, 0),
                Receipt(),
                _failure("getBalance", "bad body"),
            )
        payouts = sum(1 for _ in self._provider.writes("createPayout"))
        return QueryAttempt.branch_result(
            "found", Balance(input.account_id, 100 - payouts, "x" * input.padding), Receipt()
        )


class CreatePayoutOperation:
    """Pay out once per idempotency key, as a Stripe-like provider does."""

    def __init__(self, provider: FakeLedgerProvider) -> None:
        self._provider = provider

    def definition(self) -> MutationDefinition:
        return cast(MutationDefinition, CREATE_PAYOUT_DEFINITION)

    def idempotency_key(self, call_id: CallID, input: CreatePayoutInput) -> IdempotencyKey:
        return IdempotencyKey(call_id)

    async def invoke(self, call: Call, input: CreatePayoutInput) -> MutationAttempt[Payout]:
        words = input.memo.split()
        attempt = call.context.attempt if call.context is not None else 1
        if not await self._provider.authorize(call):
            return MutationAttempt.branch_result(
                "providerRejected", Payout(), Receipt(), _refused("createPayout")
            )
        if "reject" in words:
            return MutationAttempt.branch_result(
                "providerRejected",
                Payout(),
                Receipt(),
                _failure("createPayout", "insufficient funds"),
            )
        await _wait_at(self._provider, words, "gate-before-send", attempt)
        if "rate-limit-once" in words and attempt == 1:
            _append_line(
                self._provider._sends, {"operation": "rate-limited", "key": call.idempotency_key}
            )
            return MutationAttempt.retry(
                Failure(FailureKind.RATE_LIMIT, CONNECTOR_ID, "createPayout", "rate limited")
            )
        payout_id = self._provider.accept(
            "createPayout", str(call.idempotency_key), deduplicate=True, hidden=False
        )
        await _wait_at(self._provider, words, "gate-after-accept", attempt)
        if "invalid" in words:
            return MutationAttempt.branch_result(
                "invalidResponse", Payout(), Receipt(), _failure("createPayout", "bad body")
            )
        if "uncertain" in words:
            return MutationAttempt.uncertain(
                Payout(), _failure("createPayout", "server error after send"), Receipt()
            )
        return MutationAttempt.branch_result(
            "paid", Payout(payout_id, input.amount), Receipt(provider_object_id=payout_id)
        )


class PostNoteOperation:
    """Post a note once through the single-dispatch fence; the provider keeps repeats."""

    def __init__(self, provider: FakeLedgerProvider) -> None:
        self._provider = provider

    def definition(self) -> MutationDefinition:
        return cast(MutationDefinition, POST_NOTE_DEFINITION)

    def idempotency_key(self, call_id: CallID, input: PostNoteInput) -> IdempotencyKey:
        return IdempotencyKey(call_id)

    async def invoke(self, call: Call, input: PostNoteInput) -> MutationAttempt[Note]:
        words = input.text.split()
        key = str(call.idempotency_key)
        if not await self._provider.authorize(call):
            return MutationAttempt.branch_result(
                "providerRejected", Note(), Receipt(), _refused("postNote")
            )
        if "reject" in words:
            return MutationAttempt.branch_result(
                "providerRejected", Note(), Receipt(), _failure("postNote", "channel archived")
            )
        fence = Fence(call, timedelta(seconds=30))
        decision = await fence.decide()
        attempt = call.context.attempt if call.context is not None else 1
        if decision.kind is DecisionKind.FAIL_CLOSED:
            return MutationAttempt.uncertain(
                Note(), _failure("postNote", decision.error), Receipt()
            )
        if decision.kind is DecisionKind.READ_BACK_REQUIRED:
            found = self._provider.find("postNote", key)
            if found is None:
                return MutationAttempt.uncertain(
                    Note(), _failure("postNote", "read-back found no note"), Receipt()
                )
            return MutationAttempt.branch_result(
                "posted", Note(found), Receipt(provider_object_id=found)
            )
        await _wait_at(self._provider, words, "gate-before-send", attempt)
        if "rate-limit-once" in words and attempt == 1:
            await fence.record_not_sent()
            _append_line(self._provider._sends, {"operation": "rate-limited", "key": key})
            return MutationAttempt.retry(
                Failure(FailureKind.RATE_LIMIT, CONNECTOR_ID, "postNote", "rate limited")
            )

        async def send(deadline: datetime) -> str | None:
            await _wait_at(self._provider, words, "gate-after-claim", attempt)
            if "rate-limit-after-send" in words and attempt == 1:
                # The request reached the provider, which refused it without acting.
                _append_line(self._provider._sends, {"operation": "rate-limited", "key": key})
                return None
            note_id = self._provider.accept(
                "postNote", key, deduplicate=False, hidden="hide" in words
            )
            await _wait_at(self._provider, words, "gate-after-accept", attempt)
            return note_id

        note_id = await fence.dispatch(send)
        if note_id is None:
            await _wait_at(self._provider, words, "gate-before-can-resend", attempt)
            await fence.record_can_resend()
            return MutationAttempt.retry(
                Failure(FailureKind.RATE_LIMIT, CONNECTOR_ID, "postNote", "rate limited")
            )
        await fence.record_provider_id(note_id)
        if "uncertain" in words:
            return MutationAttempt.uncertain(Note(), _failure("postNote", "no response"), Receipt())
        return MutationAttempt.branch_result(
            "posted", Note(note_id), Receipt(provider_object_id=note_id)
        )


class CloseAccountOperation:
    """A Mutation no review exposed to agents."""

    def definition(self) -> MutationDefinition:
        return cast(MutationDefinition, CLOSE_ACCOUNT_DEFINITION)

    def idempotency_key(self, call_id: CallID, input: CloseAccountInput) -> IdempotencyKey:
        return IdempotencyKey(call_id)

    async def invoke(self, call: Call, input: CloseAccountInput) -> MutationAttempt[Note]:
        raise AssertionError("closeAccount must never run")


def _refused(operation: str) -> Failure:
    return Failure(
        FailureKind.AUTHENTICATION, CONNECTOR_ID, operation, "the ledger refused the credential"
    )


def _failure(operation: str, message: str) -> Failure:
    return Failure(FailureKind.PROVIDER_REJECTION, CONNECTOR_ID, operation, message)


async def _wait_at(
    provider: FakeLedgerProvider, words: list[str], prefix: str, attempt: int
) -> None:
    for word in words:
        if word.startswith(f"{prefix}:") and attempt == 1:
            gate = provider.gate(word.removeprefix(f"{prefix}:"))
            gate.mark_reached()
            await gate.wait_for_release()


def _read_lines(path: Path) -> list[dict[str, str]]:
    if not path.exists():
        return []
    return [json.loads(line) for line in path.read_text().splitlines() if line]


def _append_line(path: Path, entry: dict[str, str]) -> None:
    with path.open("a") as file:
        file.write(json.dumps(entry) + "\n")
        file.flush()
        os.fsync(file.fileno())
