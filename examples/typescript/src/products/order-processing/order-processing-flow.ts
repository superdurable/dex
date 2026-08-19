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

export class OrderProcessingFlow implements Flow<OrderRequest> {
  public readonly orderStatus = new Attribute("order-status", stringCodec, {
    type: IndexType.KEYWORD,
  });
  public readonly sellerOk = new Channel("seller-ok", stringCodec);

  public readonly charge = new ChargeStep(this);
  public readonly ship = new ShipStep(this);
  public readonly refund = new RefundStep(this);

  public constructor(public readonly service: MyDependencyService) {}

  public getFlowType(): string {
    return "OrderProcessingFlow";
  }

  public getSteps() {
    return StepList.startStep(this.charge).otherSteps(this.ship, this.refund);
  }

  public getPersistenceSchema(): PersistenceSchema {
    return {
      attributes: [this.orderStatus],
      channels: [this.sellerOk],
    };
  }

  @rpc({ inputCodec: stringCodec, outputCodec: stringCodec })
  public approve(context: Context, _note: string): RPCResult<string> {
    this.sellerOk.publish(context, "approved");
    return { output: "ok" };
  }

  @rpc({ outputCodec: stringCodec })
  public describe(context: Context): RPCResult<string> {
    return { output: this.orderStatus.get(context) };
  }
}

class ChargeStep implements Step<OrderRequest> {
  public readonly inputCodec = orderRequestCodec;

  public constructor(private readonly flow: OrderProcessingFlow) {}

  public getStepType(): string {
    return "ChargeStep";
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
    this.flow.service.chargeUser(input.email, input.customerId, input.amount);
    this.flow.orderStatus.set(context, "charged");
    return goTo(this.flow.ship, input);
  }
}

class ShipStep implements Step<OrderRequest> {
  public readonly inputCodec = orderRequestCodec;

  public constructor(private readonly flow: OrderProcessingFlow) {}

  public getStepType(): string {
    return "ShipStep";
  }

  public getStepOptions() {
    return {
      executeRetry: {
        // totalDurationMs: 60 * 60 * 1000,
        totalDurationMs: 3_000,
      },
      executeFailure: ExecuteFailure.proceedTo(this.flow.refund, {
        executeRetry: {
          // totalDurationMs: 60 * 60 * 1000,
          totalDurationMs: 3_000,
        },
      }),
    };
  }

  public waitFor(_context: Context, _input: OrderRequest): Wait {
    return Wait.anyOf(
      this.flow.sellerOk.forOne(),
      Timer.byDuration(DAY_MS),
    );
  }

  public execute(context: Context, input: OrderRequest): StepDecision {
    if (context.hasTimerFired()) {
      this.flow.service.sendEmail(
        input.email,
        "Reminder: approve shipment",
        "Please approve or provide a tracking number.",
      );
      return goTo(this, input);
    }
    this.flow.service.shipItem(input.orderId, input.testFailAtShipping);
    this.flow.orderStatus.set(context, "shipped");
    return gracefulComplete(`shipped:${input.orderId}`);
  }
}

class RefundStep implements Step<OrderRequest> {
  public readonly inputCodec = orderRequestCodec;

  public constructor(private readonly flow: OrderProcessingFlow) {}

  public getStepType(): string {
    return "RefundStep";
  }

  public execute(context: Context, input: OrderRequest): StepDecision {
    this.flow.service.updateExternalSystem(`refund ${input.orderId}`);
    this.flow.orderStatus.set(context, "refunded");
    return gracefulComplete(`refunded:${input.orderId}`);
  }
}
