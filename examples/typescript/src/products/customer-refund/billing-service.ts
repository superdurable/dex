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

/** A fake billing service. Run list-refunds.ts to list the refunds it recorded. */

import { mkdirSync } from "node:fs";
import { dirname } from "node:path";
import { DatabaseSync } from "node:sqlite";

export const DEFAULT_BILLING_LEDGER = ".customer-refund/billing.sqlite";

/** The payment provider did not answer in time; a retry may succeed. */
export class PaymentTimeout extends Error {
  public constructor(message: string) {
    super(message);
    this.name = "PaymentTimeout";
  }
}

export interface Order {
  readonly orderId: string;
  readonly description: string;
  readonly amountUsd: number;
  readonly daysSinceCharge: number;
  readonly outageHours: number;
}

export interface RecordedRefund {
  readonly refundId: string;
  readonly orderId: string;
  readonly amountUsd: number;
  readonly idempotencyKey: string;
}

const ORDERS: Readonly<Record<string, Order>> = {
  "INV-1249": { orderId: "INV-1249", description: "Studio plan renewal", amountUsd: 1249.0, daysSinceCharge: 6, outageHours: 31 },
  "INV-0388": { orderId: "INV-0388", description: "Studio plan renewal", amountUsd: 1249.0, daysSinceCharge: 47, outageHours: 0 },
  "INV-0049": { orderId: "INV-0049", description: "Extra seat", amountUsd: 49.0, daysSinceCharge: 3, outageHours: 31 },
  "INV-0412": { orderId: "INV-0412", description: "Extra seat", amountUsd: 49.0, daysSinceCharge: 47, outageHours: 31 },
  "INV-0518": { orderId: "INV-0518", description: "Extra seat", amountUsd: 49.0, daysSinceCharge: 3, outageHours: 0 },
};

const usdAmount = new Intl.NumberFormat("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });

/** Formats an amount like 1,249.00. */
export function formatUsd(amountUsd: number): string {
  return usdAmount.format(amountUsd);
}

/** Opens a SQLite ledger; concurrent Workers wait up to 10 seconds for its lock. */
export function openLedger(ledgerPath: string, schema: string): DatabaseSync {
  mkdirSync(dirname(ledgerPath), { recursive: true });
  const ledger = new DatabaseSync(ledgerPath, { timeout: 10_000 });
  ledger.exec(schema);
  return ledger;
}

const REFUNDS_SCHEMA = `CREATE TABLE IF NOT EXISTS refunds (
  refund_id TEXT NOT NULL UNIQUE,
  order_id TEXT NOT NULL,
  amount_usd REAL NOT NULL,
  idempotency_key TEXT NOT NULL UNIQUE
)`;

/** Keeps refunds in a SQLite file, so a restarted or concurrent Worker sees earlier refunds. */
export class FakeBillingService {
  public constructor(public readonly ledgerPath: string = DEFAULT_BILLING_LEDGER) {}

  public getOrder(orderId: string): Order {
    const order = ORDERS[orderId];
    if (order === undefined) {
      throw new Error(`no order ${orderId}`);
    }
    return order;
  }

  public refund(order: Order, idempotencyKey: string, simulateTimeout: boolean): string {
    if (simulateTimeout) {
      throw new PaymentTimeout(`no answer from the payment provider for ${order.orderId}`);
    }
    const ledger = openLedger(this.ledgerPath, REFUNDS_SCHEMA);
    try {
      // One statement numbers and records the refund, so concurrent calls cannot share an ID.
      const inserted = ledger.prepare(
        `INSERT INTO refunds (refund_id, order_id, amount_usd, idempotency_key)
         SELECT 'rf_' || (1042 + COUNT(*)), ?, ?, ? FROM refunds WHERE true
         ON CONFLICT (idempotency_key) DO NOTHING`,
      ).run(order.orderId, order.amountUsd, idempotencyKey);
      const row = ledger.prepare("SELECT refund_id FROM refunds WHERE idempotency_key = ?").get(idempotencyKey);
      const refundId = String(row?.refund_id);
      if (inserted.changes === 0) {
        console.log(`Refund ${refundId} already issued (key ${idempotencyKey})`);
      } else {
        console.log(`Refunded ${formatUsd(order.amountUsd)} USD as ${refundId}`);
      }
      return refundId;
    } finally {
      ledger.close();
    }
  }

  public listRefunds(): RecordedRefund[] {
    const ledger = openLedger(this.ledgerPath, REFUNDS_SCHEMA);
    try {
      const rows = ledger.prepare(
        "SELECT refund_id, order_id, amount_usd, idempotency_key FROM refunds ORDER BY rowid",
      ).all();
      return rows.map((row) => ({
        refundId: String(row.refund_id),
        orderId: String(row.order_id),
        amountUsd: Number(row.amount_usd),
        idempotencyKey: String(row.idempotency_key),
      }));
    } finally {
      ledger.close();
    }
  }
}

/** Stands in for a release whose Issue refund Step forgot the billing call. */
export class CallMissingBillingService extends FakeBillingService {
  public override refund(order: Order, _idempotencyKey: string, _simulateTimeout: boolean): string {
    console.log(`Billing call missing: no refund for ${order.orderId}`);
    return "";
  }
}
