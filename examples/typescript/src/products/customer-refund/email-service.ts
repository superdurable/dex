/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

/** A fake email service that prints each message and sends one email per key. */

import { openLedger } from "./billing-service.js";

export const DEFAULT_EMAIL_LEDGER = ".customer-refund/emails.sqlite";

// Only the key is stored, so the ledger keeps no address or message text.
const EMAILS_SCHEMA = `CREATE TABLE IF NOT EXISTS sent_emails (
  idempotency_key TEXT NOT NULL PRIMARY KEY
)`;

/** Keeps sent emails in a SQLite file, so a restarted or concurrent Worker sees earlier emails. */
export class FakeEmailService {
  public constructor(public readonly ledgerPath: string = DEFAULT_EMAIL_LEDGER) {}

  /** Returns true when this call sent the email, false when the key was already used. */
  public send(to: string, body: string, idempotencyKey: string): boolean {
    const ledger = openLedger(this.ledgerPath, EMAILS_SCHEMA);
    try {
      // The fake sends by recording; only the call that inserts the key prints the message.
      const inserted = ledger.prepare(
        "INSERT INTO sent_emails (idempotency_key) VALUES (?) ON CONFLICT DO NOTHING",
      ).run(idempotencyKey);
      if (inserted.changes === 0) {
        console.log(`Email to ${to} already sent (key ${idempotencyKey})`);
        return false;
      }
      console.log(`Emailed ${to}: ${body}`);
      return true;
    } finally {
      ledger.close();
    }
  }

  public listSentKeys(): string[] {
    const ledger = openLedger(this.ledgerPath, EMAILS_SCHEMA);
    try {
      return ledger.prepare("SELECT idempotency_key FROM sent_emails ORDER BY rowid").all()
        .map((row) => String(row.idempotency_key));
    } finally {
      ledger.close();
    }
  }
}
