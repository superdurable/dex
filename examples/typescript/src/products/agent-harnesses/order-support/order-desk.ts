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

/** System instructions of every order support agent, whatever its harness. */
export const orderSupportInstructions =
  "You are an order support agent. Look up an order before you answer a " +
  "question about it. Cancel an order only when the customer asks, and say " +
  "whether the cancellation succeeded.";

/** One order of the order desk. */
export interface Order {
  readonly orderId: string;
  readonly items: string;
  status: string;
}

/**
 * An in-memory order system that records every cancellation it performs. A real application calls its order
 * service here.
 */
export class OrderDesk {
  /** IDs of the orders this desk cancelled, in order. */
  public readonly cancelledOrderIds: string[] = [];
  private readonly toolDelayMs: number;
  private readonly orders = new Map<string, Order>([
    ["1001", { orderId: "1001", items: "2 notebooks", status: "shipped" }],
    ["1002", { orderId: "1002", items: "1 desk lamp", status: "processing" }],
  ]);

  /** @param toolDelayMs How long each tool call waits first, so a demo can stop the Worker mid-call. */
  public constructor(toolDelayMs = 0) {
    this.toolDelayMs = toolDelayMs;
  }

  public async describeOrder(orderId: string): Promise<string> {
    await this.waitForToolDelay();
    const order = this.orders.get(orderId);
    if (order === undefined) {
      return `Order ${orderId} does not exist.`;
    }
    return `Order ${order.orderId}: ${order.items}, ${order.status}.`;
  }

  public async cancelOrder(orderId: string, reason: string): Promise<string> {
    await this.waitForToolDelay();
    const order = this.orders.get(orderId);
    if (order === undefined) {
      return `Order ${orderId} does not exist.`;
    }
    if (order.status !== "processing") {
      return `Order ${orderId} is ${order.status} and cannot be cancelled.`;
    }
    order.status = "cancelled";
    this.cancelledOrderIds.push(orderId);
    return `Order ${orderId} is cancelled: ${reason}.`;
  }

  private async waitForToolDelay(): Promise<void> {
    if (this.toolDelayMs > 0) {
      await new Promise((resolve) => setTimeout(resolve, this.toolDelayMs));
    }
  }
}
