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

/** System instructions shared by every order-support agent, whatever its harness. */
export const orderSupportInstructions =
  "You are an order-support agent. Look up an order before you act on it. " +
  "Refund an order only when the customer asks for it, and never claim a refund succeeded unless the tool says so.";

/** Name of the read tool that every harness example registers. */
export const lookupOrderToolName = "lookup_order";

/** Name of the approval-gated write tool that every harness example registers. */
export const refundOrderToolName = "refund_order";

/** One order the demo store knows. */
export interface Order {
  readonly orderId: string;
  readonly item: string;
  readonly amountCents: number;
  readonly status: "delivered" | "shipped" | "refunded";
}

/** The refund a `refund_order` call made. */
export interface Refund {
  readonly refundId: string;
  readonly orderId: string;
  readonly amountCents: number;
}

/** Thrown when a tool names an order that does not exist. */
export class OrderNotFoundError extends Error {
  public constructor(public readonly orderId: string) {
    super(`order ${orderId} does not exist`);
  }
}

/** An in-memory order system. `refundOrder` takes an idempotency key, as a payment provider does. */
export class OrderService {
  private readonly orders = new Map<string, Order>();
  private readonly refundsByIdempotencyKey = new Map<string, Refund>();

  public constructor(orders: readonly Order[]) {
    for (const order of orders) {
      this.orders.set(order.orderId, order);
    }
  }

  /** Returns a short description of the order for the model. */
  public describeOrder(orderId: string): string {
    const order = this.requireOrder(orderId);
    const amount = (order.amountCents / 100).toFixed(2);
    return `order ${order.orderId}: ${order.item}, $${amount}, ${order.status}`;
  }

  /** Refunds the order once per idempotency key; a repeated key returns the first refund. */
  public refundOrder(orderId: string, idempotencyKey: string): Refund {
    const earlierRefund = this.refundsByIdempotencyKey.get(idempotencyKey);
    if (earlierRefund !== undefined) {
      return earlierRefund;
    }
    const order = this.requireOrder(orderId);
    const refund = { refundId: `refund-${this.refundsByIdempotencyKey.size + 1}`, orderId, amountCents: order.amountCents };
    this.refundsByIdempotencyKey.set(idempotencyKey, refund);
    this.orders.set(orderId, { ...order, status: "refunded" });
    return refund;
  }

  /** Counts the distinct refunds made for an order. */
  public countRefunds(orderId: string): number {
    return [...this.refundsByIdempotencyKey.values()].filter((refund) => refund.orderId === orderId).length;
  }

  private requireOrder(orderId: string): Order {
    const order = this.orders.get(orderId);
    if (order === undefined) {
      throw new OrderNotFoundError(orderId);
    }
    return order;
  }
}

/** The demo orders the example starts with. */
export const demoOrders: readonly Order[] = [
  { orderId: "A-1001", item: "espresso machine", amountCents: 24_900, status: "delivered" },
  { orderId: "A-1002", item: "coffee grinder", amountCents: 8_900, status: "shipped" },
];

/** An order store that holds the demo orders. */
export function createDemoOrderService(): OrderService {
  return new OrderService(demoOrders);
}

/** Formats a refund as the text the model reads. */
export function describeRefund(refund: Refund): string {
  return `refunded order ${refund.orderId} as ${refund.refundId} for $${(refund.amountCents / 100).toFixed(2)}`;
}
