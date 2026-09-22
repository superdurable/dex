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
  Attribute,
  Channel,
  ExecuteFailure,
  IndexType,
  StepList,
  Timer,
  Wait,
  goTo,
  gracefulComplete,
  rpc,
  stringCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

import { DAY_MS } from "../../config/env.js";
import { type MyDependencyService } from "../../shared/my-dependency-service.js";
import { orderRequestCodec, type OrderRequest } from "./models.js";

const orderStatus = new Attribute("order-status", stringCodec, {
  type: IndexType.KEYWORD,
  indexKey: "CustomKeyword",
});
const sellerOk = new Channel("seller-ok", stringCodec);

export class OrderProcessingFlow implements Flow<OrderRequest> {
  public readonly charge: Charge;
  public readonly ship: Ship;
  public readonly refund: Refund;

  public constructor(public readonly service: MyDependencyService) {
    this.refund = new Refund(service);
    this.ship = new Ship(service, this.refund);
    this.charge = new Charge(service, this.ship);
  }

  public getFlowType(): string {
    return "OrderProcessingFlow";
  }

  public getSteps() {
    return StepList.startStep(this.charge).otherSteps(this.ship, this.refund);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [orderStatus],
      channels: [sellerOk],
    };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec })
  public approve(context: Context, _note: string): RPCResult<string> {
    sellerOk.publish(context, "approved");
    return { output: "ok" };
  }

  @rpc({ outputCodec: stringCodec })
  public describe(context: Context): RPCResult<string> {
    return { output: orderStatus.get(context) };
  }
}

class Charge implements Step<OrderRequest> {
  public readonly inputCodec = orderRequestCodec;

  public constructor(
    private readonly service: MyDependencyService,
    private readonly ship: Ship,
  ) {}

  public getStepType(): string {
    return "Charge";
  }

  public getStepOptions() {
    return {
      executeRetry: {
        // totalDurationMs: 60 * 60 * 1000,
        totalDurationMs: 3_000,
      },
    };
  }

  public execute(context: Context, input: OrderRequest): StepDecision {
    this.service.chargeUser(input.email, input.customerId, input.amount);
    orderStatus.set(context, "charged");
    return goTo(Ship, input);
  }
}

class Ship implements Step<OrderRequest> {
  public readonly inputCodec = orderRequestCodec;

  public constructor(
    private readonly service: MyDependencyService,
    private readonly refund: Refund,
  ) {}

  public getStepType(): string {
    return "Ship";
  }

  public getStepOptions() {
    return {
      executeRetry: {
        // totalDurationMs: 60 * 60 * 1000,
        totalDurationMs: 3_000,
      },
      executeFailure: ExecuteFailure.proceedTo(Refund, {
        executeRetry: {
          // totalDurationMs: 60 * 60 * 1000,
          totalDurationMs: 3_000,
        },
      }),
    };
  }

  public waitFor(_context: Context, _input: OrderRequest): Wait {
    return Wait.anyOf(sellerOk.forOne(), Timer.byDuration(DAY_MS));
  }

  public execute(context: Context, input: OrderRequest): StepDecision {
    if (context.hasTimerFired()) {
      this.service.sendEmail(
        input.email,
        "Reminder: approve shipment",
        "Please approve or provide a tracking number.",
      );
      return goTo(Ship, input);
    }
    this.service.shipItem(input.orderId, input.testFailAtShipping);
    orderStatus.set(context, "shipped");
    return gracefulComplete(`shipped:${input.orderId}`);
  }
}

class Refund implements Step<OrderRequest> {
  public readonly inputCodec = orderRequestCodec;

  public constructor(private readonly service: MyDependencyService) {}

  public getStepType(): string {
    return "Refund";
  }

  public execute(context: Context, input: OrderRequest): StepDecision {
    this.service.updateExternalSystem(`refund ${input.orderId}`);
    orderStatus.set(context, "refunded");
    return gracefulComplete(`refunded:${input.orderId}`);
  }
}
