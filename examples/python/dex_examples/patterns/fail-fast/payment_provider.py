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

from __future__ import annotations

import threading
from collections import Counter
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from dex_examples.patterns.fail_fast.expense_reimbursement_flow import (
        ExpenseClaim,
    )


class PayoutRejectedError(Exception):
    """The provider confirmed that the request can never succeed."""


class PayoutTimeoutError(Exception):
    """The provider did not answer in time; the same request may succeed later."""


class FakePaymentProvider:
    """Stands in for a payment provider API.

    It accepts only a bank account of eight ASCII digits and rejects any other
    at once. It times out on the first submission of every valid claim, then
    pays it.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._submissions: Counter[str] = Counter()

    def submit_payout(self, claim_id: str, claim: ExpenseClaim) -> str:
        """Pays a claim once per claim ID, so a retried Step does not pay twice."""
        with self._lock:
            self._submissions[claim_id] += 1
            submissions = self._submissions[claim_id]
        account = claim.bank_account
        if not (len(account) == 8 and account.isascii() and account.isdigit()):
            raise PayoutRejectedError("bank account is invalid")
        if submissions == 1:
            raise PayoutTimeoutError("payment provider timed out")
        return f"payout-{claim_id}"

    def count_submissions(self, claim_id: str) -> int:
        with self._lock:
            return self._submissions[claim_id]
