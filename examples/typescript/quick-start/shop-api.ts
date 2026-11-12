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

import { existsSync, readFileSync, writeFileSync } from "node:fs";
import { setTimeout as sleep } from "node:timers/promises";

/**
 * A mock payment and shipping API that drops a repeated idempotency key.
 * With keysPath, it remembers keys across processes.
 */
export class ShopApi {
  public readonly charged: string[] = [];
  public readonly shipped: string[] = [];
  public readonly repeatedKeys: string[] = [];
  private readonly keysPath: string | undefined;
  private readonly chargeDelayMs: number;
  private usedKeys = new Set<string>();

  public constructor(keysPath?: string, chargeDelayMs = 0) {
    this.keysPath = keysPath;
    this.chargeDelayMs = chargeDelayMs;
  }

  public async charge(orderId: string, idempotencyKey: string): Promise<void> {
    if (this.isRepeat(idempotencyKey)) {
      console.log(`Charge for ${orderId} already done (key ${idempotencyKey}); not charged again`);
      return;
    }
    this.charged.push(orderId);
    console.log(`Charged ${orderId} with key ${idempotencyKey}`);
    // Leaves time to stop the Worker after the payment, before Dex records the Step.
    await sleep(this.chargeDelayMs);
  }

  public async ship(orderId: string, idempotencyKey: string): Promise<void> {
    if (this.isRepeat(idempotencyKey)) {
      console.log(`Ship for ${orderId} already done (key ${idempotencyKey}); not shipped again`);
      return;
    }
    this.shipped.push(orderId);
    console.log(`Shipped ${orderId} with key ${idempotencyKey}`);
  }

  private isRepeat(idempotencyKey: string): boolean {
    if (this.keysPath !== undefined && existsSync(this.keysPath)) {
      this.usedKeys = new Set(readFileSync(this.keysPath, "utf8").split(/\s+/).filter(Boolean));
    }
    if (this.usedKeys.has(idempotencyKey)) {
      this.repeatedKeys.push(idempotencyKey);
      return true;
    }
    this.usedKeys.add(idempotencyKey);
    if (this.keysPath !== undefined) {
      writeFileSync(this.keysPath, [...this.usedKeys].sort().map((key) => `${key}\n`).join(""));
    }
    return false;
  }
}
