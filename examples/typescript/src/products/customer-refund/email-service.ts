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

import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

export const DEFAULT_EMAIL_LEDGER = ".customer-refund/sent-emails.json";

/** Keeps sent keys in a JSON file, so a restarted Worker sees earlier emails. */
export class FakeEmailService {
  public constructor(public readonly ledgerPath: string = DEFAULT_EMAIL_LEDGER) {}

  public send(to: string, body: string, idempotencyKey: string): void {
    const sent = this.listSentKeys();
    if (sent.includes(idempotencyKey)) {
      console.log(`Email to ${to} already sent (key ${idempotencyKey})`);
      return;
    }
    mkdirSync(dirname(this.ledgerPath), { recursive: true });
    writeFileSync(this.ledgerPath, JSON.stringify([...sent, idempotencyKey], null, 2));
    console.log(`Emailed ${to}: ${body}`);
  }

  public listSentKeys(): string[] {
    if (!existsSync(this.ledgerPath)) {
      return [];
    }
    return JSON.parse(readFileSync(this.ledgerPath, "utf8")) as string[];
  }
}
