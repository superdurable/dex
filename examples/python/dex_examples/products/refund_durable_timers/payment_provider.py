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

"""A fake payment provider that issues refunds and fails on cue."""

from __future__ import annotations

import hashlib
import threading
import time
from dataclasses import dataclass
from typing import TYPE_CHECKING

if TYPE_CHECKING:
    from dex_examples.products.refund_durable_timers.refund_timers_flow import RefundSimulation


class PaymentProviderUnavailableError(RuntimeError):
    """A temporary provider outage; retrying later can succeed."""


class PaymentRateLimitedError(RuntimeError):
    """The provider throttled the call and said when to try again."""

    def __init__(self, retry_after_seconds: int) -> None:
        super().__init__(f"rate limited; retry after {retry_after_seconds} seconds")
        self.retry_after_seconds = retry_after_seconds


@dataclass(frozen=True)
class PaymentAttempt:
    """One call that reached the provider, with its monotonic arrival time."""

    idempotency_key: str
    attempt: int
    outcome: str
    arrived_at: float


class FakePaymentProvider:
    """Stands in for a card processor's refund API. It moves no money.

    The Step attempt number and the run's simulation knobs decide each outcome,
    so a restarted Worker sees the same sequence.
    """

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._refund_ids: dict[str, str] = {}
        self._attempts: list[PaymentAttempt] = []

    def issue_refund(
        self,
        idempotency_key: str,
        amount_cents: int,
        attempt: int,
        simulation: RefundSimulation,
    ) -> str:
        """Returns the refund ID; the same key always returns the same refund."""
        rate_limited_attempt = simulation.transient_payment_failures + 1
        if attempt <= simulation.transient_payment_failures:
            self._record(idempotency_key, attempt, "unavailable")
            raise PaymentProviderUnavailableError("payment provider is temporarily unavailable")
        if simulation.rate_limit_retry_after_seconds > 0 and attempt == rate_limited_attempt:
            self._record(idempotency_key, attempt, "rate-limited")
            raise PaymentRateLimitedError(simulation.rate_limit_retry_after_seconds)
        self._record(idempotency_key, attempt, "refunded")
        with self._lock:
            digest = hashlib.sha256(f"{idempotency_key}:{amount_cents}".encode()).hexdigest()
            return self._refund_ids.setdefault(idempotency_key, f"rf_{digest[:10]}")

    def list_attempts(self, idempotency_key: str) -> list[PaymentAttempt]:
        with self._lock:
            return [item for item in self._attempts if item.idempotency_key == idempotency_key]

    def _record(self, idempotency_key: str, attempt: int, outcome: str) -> None:
        with self._lock:
            self._attempts.append(
                PaymentAttempt(idempotency_key, attempt, outcome, time.monotonic())
            )
        print(f"[payment] {idempotency_key} attempt {attempt}: {outcome}", flush=True)
