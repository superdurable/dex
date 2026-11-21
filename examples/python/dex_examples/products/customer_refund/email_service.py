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

from contextlib import closing
from pathlib import Path

from dex_examples.products.customer_refund.ledger_database import (
    DEFAULT_LEDGER_DATABASE,
    connect_ledger,
)


class FakeEmailService:
    """Records each email in the shared SQLite ledger; the first record is the send."""

    def __init__(self, ledger_path: Path = DEFAULT_LEDGER_DATABASE) -> None:
        self.ledger_path = ledger_path

    def send(self, to: str, body: str, idempotency_key: str) -> None:
        with closing(connect_ledger(self.ledger_path)) as ledger:
            inserted = ledger.execute(
                "INSERT OR IGNORE INTO sent_emails (idempotency_key, recipient, body)"
                " VALUES (?, ?, ?)",
                (idempotency_key, to, body),
            ).rowcount
        if inserted:
            print(f"Emailed {to}: {body}", flush=True)
        else:
            print(f"Email to {to} already sent (key {idempotency_key})", flush=True)

    def list_sent_keys(self) -> list[str]:
        with closing(connect_ledger(self.ledger_path)) as ledger:
            rows = ledger.execute("SELECT idempotency_key FROM sent_emails ORDER BY id").fetchall()
        return [row[0] for row in rows]
