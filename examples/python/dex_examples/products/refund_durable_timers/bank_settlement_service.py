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

"""A fake bank that reports when a refund has settled to the customer's account."""

from __future__ import annotations

import threading
from dataclasses import dataclass
from enum import StrEnum
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from dex_examples.products.refund_durable_timers.refund_timers_flow import RefundSimulation


class SettlementState(StrEnum):
    PENDING = "PENDING"
    SETTLED = "SETTLED"


@dataclass(frozen=True)
class SettlementCheck:
    """One settlement status call that reached the bank."""

    refund_id: str
    check_number: int
    state: SettlementState


class FakeBankSettlementService:
    """Stands in for a bank's settlement status API.

    The caller passes its durable check number, so the answer does not depend on
    this process's memory.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._checks: list[SettlementCheck] = []

    def get_settlement_state(
        self, refund_id: str, check_number: int, simulation: RefundSimulation
    ) -> SettlementState:
        settled = (
            not simulation.bank_never_settles
            and check_number >= simulation.settle_after_checks
        )
        state = SettlementState.SETTLED if settled else SettlementState.PENDING
        with self._lock:
            self._checks.append(SettlementCheck(refund_id, check_number, state))
        print(f"[bank] {refund_id} check {check_number}: {state}", flush=True)
        return state

    def list_checks(self, refund_id: str) -> list[SettlementCheck]:
        with self._lock:
            return [check for check in self._checks if check.refund_id == refund_id]
