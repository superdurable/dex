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

/** The idempotency key already belongs to a refund of another order or amount. */
export class RefundKeyConflict extends Error {
  public constructor(message: string) {
    super(message);
    this.name = "RefundKeyConflict";
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
  try {
    ledger.exec(schema);
  } catch (failure) {
    ledger.close();
    throw failure;
  }
  return ledger;
}

// AUTOINCREMENT never reuses a number, even after a row is deleted.
const REFUNDS_SCHEMA = `
CREATE TABLE IF NOT EXISTS refunds (
  refund_number INTEGER PRIMARY KEY AUTOINCREMENT,
  order_id TEXT NOT NULL,
  amount_usd REAL NOT NULL,
  idempotency_key TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS refund_idempotency_keys (
  idempotency_key TEXT NOT NULL PRIMARY KEY,
  refund_number INTEGER NOT NULL,
  order_id TEXT NOT NULL,
  amount_usd REAL NOT NULL
);`;

/** Shows refund number 1 as rf_1042. */
const REFUND_NUMBER_OFFSET = 1041;

function refundIdOf(refundNumber: unknown): string {
  return `rf_${REFUND_NUMBER_OFFSET + Number(refundNumber)}`;
}

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
      // The write lock makes the key check, the numbering, and both inserts one atomic step.
      ledger.exec("BEGIN IMMEDIATE");
      try {
        const earlier = ledger.prepare(
          "SELECT refund_number, order_id, amount_usd FROM refund_idempotency_keys WHERE idempotency_key = ?",
        ).get(idempotencyKey);
        if (earlier !== undefined) {
          ledger.exec("COMMIT");
          const refundId = refundIdOf(earlier.refund_number);
          if (earlier.order_id !== order.orderId || Number(earlier.amount_usd) !== order.amountUsd) {
            throw new RefundKeyConflict(
              `key ${idempotencyKey} already refunded ${earlier.order_id} (${formatUsd(Number(earlier.amount_usd))} USD) ` +
                `as ${refundId}, not ${order.orderId} (${formatUsd(order.amountUsd)} USD); ` +
                "give each Dex environment its own billing ledger",
            );
          }
          console.log(`Refund ${refundId} already issued (key ${idempotencyKey})`);
          return refundId;
        }
        const inserted = ledger.prepare(
          "INSERT INTO refunds (order_id, amount_usd, idempotency_key) VALUES (?, ?, ?)",
        ).run(order.orderId, order.amountUsd, idempotencyKey);
        ledger.prepare(
          "INSERT INTO refund_idempotency_keys (idempotency_key, refund_number, order_id, amount_usd) VALUES (?, ?, ?, ?)",
        ).run(idempotencyKey, inserted.lastInsertRowid, order.orderId, order.amountUsd);
        ledger.exec("COMMIT");
        const refundId = refundIdOf(inserted.lastInsertRowid);
        console.log(`Refunded ${formatUsd(order.amountUsd)} USD as ${refundId}`);
        return refundId;
      } catch (failure) {
        if (ledger.isTransaction) {
          ledger.exec("ROLLBACK");
        }
        throw failure;
      }
    } finally {
      ledger.close();
    }
  }

  /** Returns the refund ID recorded for the key, or undefined when billing has none. */
  public findRefundId(idempotencyKey: string): string | undefined {
    const ledger = openLedger(this.ledgerPath, REFUNDS_SCHEMA);
    try {
      const row = ledger.prepare(
        "SELECT refund_number FROM refund_idempotency_keys WHERE idempotency_key = ?",
      ).get(idempotencyKey);
      return row === undefined ? undefined : refundIdOf(row.refund_number);
    } finally {
      ledger.close();
    }
  }

  public listRefunds(): RecordedRefund[] {
    const ledger = openLedger(this.ledgerPath, REFUNDS_SCHEMA);
    try {
      const rows = ledger.prepare(
        "SELECT refund_number, order_id, amount_usd, idempotency_key FROM refunds ORDER BY refund_number",
      ).all();
      return rows.map((row) => ({
        refundId: refundIdOf(row.refund_number),
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
