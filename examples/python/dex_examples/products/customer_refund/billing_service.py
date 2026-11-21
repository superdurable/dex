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

import json
from dataclasses import asdict, dataclass
from pathlib import Path

DEFAULT_BILLING_LEDGER = Path(".customer-refund/billing-refunds.json")

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
    "INV-0388": Order("INV-0388", "Studio plan renewal", 1249.00, 47, 0),
    "INV-0049": Order("INV-0049", "Extra seat", 49.00, 3, 31),
}

class FakeBillingService:
    """Keeps refunds in a JSON file, so a restarted Worker sees earlier refunds."""

    def __init__(self, ledger_path: Path = DEFAULT_BILLING_LEDGER) -> None:
        self.ledger_path = ledger_path

    def get_order(self, order_id: str) -> Order:
        return ORDERS[order_id]

    def refund(self, order: Order, idempotency_key: str, simulate_timeout: bool) -> str:
        if simulate_timeout:
            raise PaymentTimeout(f"no answer from the payment provider for {order.order_id}")
        refunds = self.list_refunds()
        for earlier in refunds:
            if earlier.idempotency_key == idempotency_key:
                print(f"Refund {earlier.refund_id} already issued (key {idempotency_key})", flush=True)
                return earlier.refund_id
        refund = RecordedRefund(
            f"rf_{1042 + len(refunds)}", order.order_id, order.amount_usd, idempotency_key
        )
        self._save([*refunds, refund])
        print(f"Refunded {order.amount_usd:,.2f} USD as {refund.refund_id}", flush=True)
        return refund.refund_id

    def list_refunds(self) -> list[RecordedRefund]:
        if not self.ledger_path.exists():
            return []
        return [RecordedRefund(**item) for item in json.loads(self.ledger_path.read_text())]

    def _save(self, refunds: list[RecordedRefund]) -> None:
        self.ledger_path.parent.mkdir(parents=True, exist_ok=True)
        self.ledger_path.write_text(json.dumps([asdict(refund) for refund in refunds], indent=2))

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
        print(f"{refund.refund_id}  {refund.order_id}  {refund.amount_usd:,.2f} USD  key {refund.idempotency_key}")

if __name__ == "__main__":
    main()
