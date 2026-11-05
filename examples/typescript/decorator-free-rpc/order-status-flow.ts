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
  StepList,
  Wait,
  declareRPCMethod,
  gracefulComplete,
  stringCodec,
  voidCodec,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
  type Step,
  type StepDecision,
} from "@superdurable/dex";

export const DELIVERED_ORDER_STATUS = "delivered";

const orderStatus = new Attribute("order-status", stringCodec);
const orderDelivered = new Channel("order-delivered", voidCodec);

class WaitForOrderDeliveryStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public getStepType(): string {
    return "WaitForOrderDeliveryStep";
  }

  public waitFor(_context: Context, _orderId: string): Wait {
    return Wait.until(orderDelivered.forOne());
  }

  public execute(context: Context, orderId: string): StepDecision {
    return gracefulComplete(`${orderId}:${orderStatus.get(context)}`);
  }
}

export class OrderStatusFlow implements Flow<string> {
  public getFlowType(): string {
    return "OrderStatusFlow";
  }

  public getSteps() {
    return StepList.startStep(new WaitForOrderDeliveryStep());
  }

  public getPersistenceSchema(): PersistenceSchema {
    return { attributes: [orderStatus], channels: [orderDelivered] };
  }

  public getOrderStatus(context: Context): RPCResult<string> {
    return { output: orderStatus.get(context) ?? "created" };
  }

  public updateOrderStatus(context: Context, status: string): RPCResult<string> {
    orderStatus.set(context, status);
    if (status === DELIVERED_ORDER_STATUS) {
      orderDelivered.publish(context, undefined);
    }
    return { output: status };
  }
}

// Decorator-free RPC declarations: these load under Node.js type stripping.
declareRPCMethod(OrderStatusFlow, "getOrderStatus", { outputCodec: stringCodec });
declareRPCMethod(OrderStatusFlow, "updateOrderStatus", {
  inputCodec: stringCodec,
  outputCodec: stringCodec,
  lockAttributes: [orderStatus.lock()],
});

export const orderStatusFlow = new OrderStatusFlow();
