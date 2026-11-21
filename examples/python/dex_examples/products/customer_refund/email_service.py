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

"""A fake email service that prints each message and sends one email per key."""

from __future__ import annotations

import json
from pathlib import Path

DEFAULT_EMAIL_LEDGER = Path(".customer-refund/sent-emails.json")


class FakeEmailService:
    """Keeps sent keys in a JSON file, so a restarted Worker sees earlier emails."""

    def __init__(self, ledger_path: Path = DEFAULT_EMAIL_LEDGER) -> None:
        self.ledger_path = ledger_path

    def send(self, to: str, body: str, idempotency_key: str) -> None:
        sent = self.list_sent_keys()
        if idempotency_key in sent:
            print(f"Email to {to} already sent (key {idempotency_key})", flush=True)
            return
        self.ledger_path.parent.mkdir(parents=True, exist_ok=True)
        self.ledger_path.write_text(json.dumps([*sent, idempotency_key], indent=2))
        print(f"Emailed {to}: {body}", flush=True)

    def list_sent_keys(self) -> list[str]:
        if not self.ledger_path.exists():
            return []
        keys: list[str] = json.loads(self.ledger_path.read_text())
        return keys
