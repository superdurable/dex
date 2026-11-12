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

import {
  StepList,
  Timer,
  Wait,
  goTo,
  gracefulComplete,
  stringCodec,
  type Context,
  type Flow,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { SHIP_DELAY_MS } from "./config.ts";
import type { ShopApi } from "./shop-api.ts";

export const SHIP_DELAY_TIMER = "ship-delay";

class Charge implements Step<string> {
  public readonly inputCodec = stringCodec;
  private readonly shop: ShopApi;

  public constructor(shop: ShopApi) {
    this.shop = shop;
  }

  public getStepType(): string {
    return "Charge";
  }

  public async execute(_context: Context, orderId: string): Promise<StepDecision> {
    await this.shop.charge(orderId, `charge-${orderId}`);
    return goTo(Ship, orderId);
  }
}

class Ship implements Step<string> {
  public readonly inputCodec = stringCodec;
  private readonly shop: ShopApi;

  public constructor(shop: ShopApi) {
    this.shop = shop;
  }

  public getStepType(): string {
    return "Ship";
  }

  public waitFor(_context: Context, _orderId: string): Wait {
    return Wait.until(Timer.byDuration(SHIP_DELAY_MS, SHIP_DELAY_TIMER));
  }

  public async execute(_context: Context, orderId: string): Promise<StepDecision> {
    await this.shop.ship(orderId, `ship-${orderId}`);
    return gracefulComplete(`shipped ${orderId}`);
  }
}

export class OrderFlow implements Flow<string> {
  private readonly charge: Charge;
  private readonly ship: Ship;

  public constructor(shop: ShopApi) {
    this.charge = new Charge(shop);
    this.ship = new Ship(shop);
  }

  public getFlowType(): string {
    return "OrderFlow";
  }

  public getSteps(): StepList<string> {
    return StepList.startStep(this.charge).otherSteps(this.ship);
  }
}
