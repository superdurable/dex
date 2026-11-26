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

"""A fake email service that prints each message instead of sending it."""

from __future__ import annotations

import threading
from dataclasses import dataclass


@dataclass(frozen=True)
class Notification:
    idempotency_key: str
    recipient: str
    subject: str


class FakeNotificationService:
    """Records one message per idempotency key, so a retried Step sends no duplicate."""

    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._sent: dict[str, Notification] = {}

    def send(self, recipient: str, subject: str, idempotency_key: str) -> None:
        with self._lock:
            if idempotency_key in self._sent:
                return
            self._sent[idempotency_key] = Notification(idempotency_key, recipient, subject)
        print(f"[email] to {recipient}: {subject}", flush=True)

    def list_sent(self, key_prefix: str) -> list[Notification]:
        """Returns the messages whose idempotency key starts with key_prefix, in send order."""
        with self._lock:
            return [item for key, item in self._sent.items() if key.startswith(key_prefix)]
