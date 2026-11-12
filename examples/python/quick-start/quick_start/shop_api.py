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

"""A mock payment and shipping API that drops a repeated idempotency key."""

from __future__ import annotations

import time
from pathlib import Path


class ShopApi:
    """Records each call. With keys_path, it remembers keys across processes."""

    def __init__(
        self, keys_path: Path | None = None, charge_delay_seconds: float = 0
    ) -> None:
        self.keys_path = keys_path
        self.charge_delay_seconds = charge_delay_seconds
        self.charged: list[str] = []
        self.shipped: list[str] = []
        self.repeated_keys: list[str] = []
        self._used_keys: set[str] = set()

    def charge(self, order_id: str, idempotency_key: str) -> None:
        if self._is_repeat(idempotency_key):
            print(
                f"Charge for {order_id} already done (key {idempotency_key});"
                " not charged again",
                flush=True,
            )
            return
        self.charged.append(order_id)
        print(f"Charged {order_id} with key {idempotency_key}", flush=True)
        # Leaves time to stop the Worker after the payment, before Dex records the Step.
        time.sleep(self.charge_delay_seconds)

    def ship(self, order_id: str, idempotency_key: str) -> None:
        if self._is_repeat(idempotency_key):
            print(
                f"Ship for {order_id} already done (key {idempotency_key});"
                " not shipped again",
                flush=True,
            )
            return
        self.shipped.append(order_id)
        print(f"Shipped {order_id} with key {idempotency_key}", flush=True)

    def _is_repeat(self, idempotency_key: str) -> bool:
        if self.keys_path is not None and self.keys_path.exists():
            self._used_keys = set(self.keys_path.read_text().split())
        if idempotency_key in self._used_keys:
            self.repeated_keys.append(idempotency_key)
            return True
        self._used_keys.add(idempotency_key)
        if self.keys_path is not None:
            self.keys_path.write_text("".join(f"{key}\n" for key in sorted(self._used_keys)))
        return False
