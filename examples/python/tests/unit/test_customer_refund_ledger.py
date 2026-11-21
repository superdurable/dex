# Copyright (c) 2022-2026 Super Durable, Inc.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

"""Checks that the refund example's fake services keep one record per key under concurrency."""

from __future__ import annotations

import threading
from collections.abc import Callable
from pathlib import Path
from typing import TypeVar

import pytest

from dex_examples.products.customer_refund.billing_service import ORDERS, FakeBillingService
from dex_examples.products.customer_refund.email_service import FakeEmailService

CALLERS = 16
ResultT = TypeVar("ResultT")


def run_together(calls: list[Callable[[], ResultT]]) -> list[ResultT]:
    """Starts every call at the same moment, each on its own thread."""
    barrier = threading.Barrier(len(calls))
    results: list[ResultT | None] = [None] * len(calls)

    def run(index: int) -> None:
        barrier.wait()
        results[index] = calls[index]()

    threads = [threading.Thread(target=run, args=(index,)) for index in range(len(calls))]
    for thread in threads:
        thread.start()
    for thread in threads:
        thread.join()
    return [result for result in results if result is not None]


def test_same_refund_key_records_one_refund(tmp_path: Path) -> None:
    order = ORDERS["INV-1249"]

    def refund() -> str:
        billing = FakeBillingService(tmp_path / "ledger.sqlite3")
        return billing.refund(order, "refund-case-1", simulate_timeout=False)

    refund_ids = run_together([refund] * CALLERS)

    refunds = FakeBillingService(tmp_path / "ledger.sqlite3").list_refunds()
    assert [refund.idempotency_key for refund in refunds] == ["refund-case-1"]
    assert refund_ids == [refunds[0].refund_id] * CALLERS


def test_different_refund_keys_keep_every_refund(tmp_path: Path) -> None:
    order = ORDERS["INV-0049"]

    def refund_for(key: str) -> Callable[[], str]:
        def refund() -> str:
            billing = FakeBillingService(tmp_path / "ledger.sqlite3")
            return billing.refund(order, key, simulate_timeout=False)

        return refund

    keys = [f"refund-case-{index}" for index in range(CALLERS)]
    refund_ids = run_together([refund_for(key) for key in keys])

    refunds = FakeBillingService(tmp_path / "ledger.sqlite3").list_refunds()
    assert sorted(refund.idempotency_key for refund in refunds) == sorted(keys)
    assert len(set(refund_ids)) == CALLERS
    assert sorted(refund_ids) == sorted(refund.refund_id for refund in refunds)


def test_same_email_key_sends_one_email(tmp_path: Path, capsys: pytest.CaptureFixture[str]) -> None:
    def send() -> bool:
        email = FakeEmailService(tmp_path / "ledger.sqlite3")
        email.send("billing@northstar.example", "Hello", "email-case-1")
        return True

    run_together([send] * CALLERS)

    assert FakeEmailService(tmp_path / "ledger.sqlite3").list_sent_keys() == ["email-case-1"]
    assert capsys.readouterr().out.count("Emailed billing@northstar.example") == 1


def test_different_email_keys_send_every_email(tmp_path: Path) -> None:
    def send_for(key: str) -> Callable[[], bool]:
        def send() -> bool:
            FakeEmailService(tmp_path / "ledger.sqlite3").send("a@example.com", "Hello", key)
            return True

        return send

    keys = [f"email-case-{index}" for index in range(CALLERS)]
    run_together([send_for(key) for key in keys])

    sent = FakeEmailService(tmp_path / "ledger.sqlite3").list_sent_keys()
    assert sorted(sent) == sorted(keys)
