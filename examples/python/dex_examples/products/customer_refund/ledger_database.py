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

"""The SQLite ledger that the fake billing and email services share."""

from __future__ import annotations

import sqlite3
from pathlib import Path

DEFAULT_LEDGER_DATABASE = Path(".customer-refund/ledger.sqlite3")

_SCHEMA = """
CREATE TABLE IF NOT EXISTS refunds (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    idempotency_key TEXT NOT NULL UNIQUE,
    order_id TEXT NOT NULL,
    amount_usd REAL NOT NULL
);
CREATE TABLE IF NOT EXISTS sent_email_keys (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    idempotency_key TEXT NOT NULL UNIQUE
);
"""


def connect_ledger(database_path: Path) -> sqlite3.Connection:
    """Opens the ledger in autocommit mode, so each INSERT commits atomically."""
    database_path.parent.mkdir(parents=True, exist_ok=True)
    connection = sqlite3.connect(database_path, timeout=30, isolation_level=None)
    try:
        # BEGIN IMMEDIATE waits for the write lock, so concurrent first opens do not fail.
        connection.executescript(f"BEGIN IMMEDIATE; {_SCHEMA} COMMIT;")
    except BaseException:
        connection.close()
        raise
    return connection
