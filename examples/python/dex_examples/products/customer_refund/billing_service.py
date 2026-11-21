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

"""A fake billing service. Run this module to list the refunds it recorded."""

from __future__ import annotations

from contextlib import closing
from dataclasses import dataclass
from pathlib import Path

from dex_examples.products.customer_refund.ledger_database import (
    DEFAULT_LEDGER_DATABASE,
    connect_ledger,
)


class PaymentTimeout(Exception):
    """The payment provider did not answer in time; a retry may succeed."""


@dataclass(frozen=True)
class Order:
    order_id: str
    description: str
    amount_usd: float
    days_since_charge: int
    outage_hours: int


@dataclass(frozen=True)
class RecordedRefund:
    refund_id: str
    order_id: str
    amount_usd: float
    idempotency_key: str


ORDERS = {
    "INV-1249": Order("INV-1249", "Studio plan renewal", 1249.00, 6, 31),
    "INV-0388": Order("INV-0388", "Studio plan renewal", 1249.00, 47, 31),
    "INV-0455": Order("INV-0455", "Studio plan renewal", 1249.00, 5, 0),
    "INV-0049": Order("INV-0049", "Extra seat", 49.00, 3, 31),
}


class FakeBillingService:
    """Keeps refunds in a shared SQLite ledger, one per idempotency key."""

    def __init__(self, ledger_path: Path = DEFAULT_LEDGER_DATABASE) -> None:
        self.ledger_path = ledger_path

    def get_order(self, order_id: str) -> Order:
        return ORDERS[order_id]

    def refund(self, order: Order, idempotency_key: str, simulate_timeout: bool) -> str:
        if simulate_timeout:
            raise PaymentTimeout(f"no answer from the payment provider for {order.order_id}")
        with closing(connect_ledger(self.ledger_path)) as ledger:
            inserted = ledger.execute(
                "INSERT OR IGNORE INTO refunds (idempotency_key, order_id, amount_usd)"
                " VALUES (?, ?, ?)",
                (idempotency_key, order.order_id, order.amount_usd),
            ).rowcount
            row = ledger.execute(
                "SELECT id FROM refunds WHERE idempotency_key = ?", (idempotency_key,)
            ).fetchone()
        refund_id = refund_id_for(row[0])
        if inserted:
            print(f"Refunded {order.amount_usd:,.2f} USD as {refund_id}", flush=True)
        else:
            print(f"Refund {refund_id} already issued (key {idempotency_key})", flush=True)
        return refund_id

    def list_refunds(self) -> list[RecordedRefund]:
        with closing(connect_ledger(self.ledger_path)) as ledger:
            rows = ledger.execute(
                "SELECT id, order_id, amount_usd, idempotency_key FROM refunds ORDER BY id"
            ).fetchall()
        return [RecordedRefund(refund_id_for(row[0]), row[1], row[2], row[3]) for row in rows]


def refund_id_for(ledger_row_id: int) -> str:
    """Returns the refund ID of a ledger row, such as rf_1042 for the first row."""
    return f"rf_{1041 + ledger_row_id}"


class CallMissingBillingService(FakeBillingService):
    """Stands in for a release whose Issue refund Step forgot the billing call."""

    def refund(self, order: Order, idempotency_key: str, simulate_timeout: bool) -> str:
        print(f"Billing call missing: no refund for {order.order_id}", flush=True)
        return ""


def main() -> None:
    refunds = FakeBillingService().list_refunds()
    if not refunds:
        print("No refunds recorded.")
    for refund in refunds:
        print(
            f"{refund.refund_id}  {refund.order_id}  {refund.amount_usd:,.2f} USD  key {refund.idempotency_key}"
        )


if __name__ == "__main__":
    main()
