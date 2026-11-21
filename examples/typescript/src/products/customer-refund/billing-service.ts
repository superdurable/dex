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

import { existsSync, mkdirSync, readFileSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

export const DEFAULT_BILLING_LEDGER = ".customer-refund/billing-refunds.json";

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
};

const usdAmount = new Intl.NumberFormat("en-US", { minimumFractionDigits: 2, maximumFractionDigits: 2 });

/** Formats an amount like 1,249.00. */
export function formatUsd(amountUsd: number): string {
  return usdAmount.format(amountUsd);
}

/** Keeps refunds in a JSON file, so a restarted Worker sees earlier refunds. */
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
    const refunds = this.listRefunds();
    const earlier = refunds.find((refund) => refund.idempotencyKey === idempotencyKey);
    if (earlier !== undefined) {
      console.log(`Refund ${earlier.refundId} already issued (key ${idempotencyKey})`);
      return earlier.refundId;
    }
    const refund: RecordedRefund = {
      refundId: `rf_${1042 + refunds.length}`,
      orderId: order.orderId,
      amountUsd: order.amountUsd,
      idempotencyKey,
    };
    mkdirSync(dirname(this.ledgerPath), { recursive: true });
    writeFileSync(this.ledgerPath, JSON.stringify([...refunds, refund], null, 2));
    console.log(`Refunded ${formatUsd(order.amountUsd)} USD as ${refund.refundId}`);
    return refund.refundId;
  }

  public listRefunds(): RecordedRefund[] {
    if (!existsSync(this.ledgerPath)) {
      return [];
    }
    return JSON.parse(readFileSync(this.ledgerPath, "utf8")) as RecordedRefund[];
  }
}

/** Stands in for a release whose Issue refund Step forgot the billing call. */
export class CallMissingBillingService extends FakeBillingService {
  public override refund(order: Order, _idempotencyKey: string, _simulateTimeout: boolean): string {
    console.log(`Billing call missing: no refund for ${order.orderId}`);
    return "";
  }
}
