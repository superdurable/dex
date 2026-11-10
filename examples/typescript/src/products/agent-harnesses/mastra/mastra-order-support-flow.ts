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

import { Agent } from "@mastra/core/agent";
import { createTool } from "@mastra/core/tools";
import { ToolExecutionError, type AgentSessionFlow } from "@superdurable/dex-agents";
import { createMastraAgentSessionFlow, idempotencyKeyRequestContextKey } from "@superdurable/dex-agents-mastra";
import { z } from "zod";

import { environmentOr } from "../../../config/env.js";
import {
  OrderNotFoundError,
  describeRefund,
  orderSupportInstructions,
  type OrderService,
} from "../order-support/order-service.js";

/** Flow type of the Mastra order-support session. */
export const mastraOrderSupportFlowType = "MastraOrderSupportAgent";

/** The Mastra model of every model call, such as a model router ID or an AI SDK language model. */
export type MastraOrderSupportModel = ConstructorParameters<typeof Agent>[0]["model"];

/**
 * The model router ID from `DEX_MASTRA_MODEL`, such as `openai/gpt-5-mini`. Mastra reads the provider key, such as
 * `OPENAI_API_KEY`, from the Worker's environment.
 */
export function loadMastraOrderSupportModel(): MastraOrderSupportModel {
  return environmentOr("DEX_MASTRA_MODEL", "openai/gpt-5-mini");
}

/**
 * Builds the order-support session Flow from a Mastra agent. Every model call is a `RunModel` Step and every tool
 * call is its own Step, so `refund_order` waits for approval and then runs once.
 */
export function createMastraOrderSupportFlow(orders: OrderService, model: MastraOrderSupportModel): AgentSessionFlow {
  const orderInput = z.object({ orderId: z.string().describe("The order ID, such as A-1001") });
  const lookupOrder = createTool({
    id: "lookup_order",
    description: "Look up an order's item, amount, and status",
    inputSchema: orderInput,
    execute: async ({ orderId }) => runOrderTool(() => orders.describeOrder(orderId)),
  });
  const refundOrder = createTool({
    id: "refund_order",
    description: "Refund an order in full",
    inputSchema: orderInput,
    execute: async ({ orderId }, context) => {
      // The same key on every attempt of this call's tool Step, so a retried refund takes effect once.
      const idempotencyKey = String(context.requestContext?.get(idempotencyKeyRequestContextKey) ?? orderId);
      return runOrderTool(() => describeRefund(orders.refundOrder(orderId, idempotencyKey)));
    },
  });
  const agent = new Agent({
    id: "order-support",
    name: "Order support",
    instructions: orderSupportInstructions,
    model,
    tools: { lookup_order: lookupOrder, refund_order: refundOrder },
  });
  return createMastraAgentSessionFlow(agent, mastraOrderSupportFlowType, {
    toolPolicies: {
      lookup_order: { effect: "read" },
      refund_order: { effect: "idempotent_write", requiresApproval: true },
    },
    interruptDeadlineMs: 24 * 60 * 60 * 1000,
  });
}

/** Reports an unknown order to the model as a failed call instead of retrying it. */
function runOrderTool(action: () => string): string {
  try {
    return action();
  } catch (error) {
    if (error instanceof OrderNotFoundError) {
      throw new ToolExecutionError(error.message);
    }
    throw error;
  }
}
