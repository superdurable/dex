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

import { Agent, tool, type Model } from "@openai/agents";
import {
  ToolExecutionError,
  createOpenAIAgentsSessionFlow,
  getDeferredToolExecution,
  type AgentSessionFlow,
} from "@superdurable/dex-agents-openai-agents";
import { z } from "zod";

import { environmentOr } from "../../../config/env.js";
import {
  OrderNotFoundError,
  describeRefund,
  orderSupportInstructions,
  type OrderService,
} from "../order-support/order-service.js";

/** Flow type of the OpenAI Agents SDK order-support session. */
export const openAIAgentsOrderSupportFlowType = "OpenAIAgentsOrderSupportAgent";

/**
 * The model name from `DEX_OPENAI_AGENTS_MODEL`. The OpenAI Agents SDK reads `OPENAI_API_KEY` from the Worker's
 * environment.
 */
export function loadOpenAIAgentsOrderSupportModel(): string {
  return environmentOr("DEX_OPENAI_AGENTS_MODEL", "gpt-5-mini");
}

/**
 * Builds the order-support session Flow from an OpenAI Agents SDK agent. Every model call is a `RunModel` Step and
 * every function tool call is its own Step, so `refund_order` waits for approval and then runs once.
 */
export function createOpenAIAgentsOrderSupportFlow(orders: OrderService, model: string | Model): AgentSessionFlow {
  const orderInput = z.object({ orderId: z.string().describe("The order ID, such as A-1001") });
  const lookupOrder = tool({
    name: "lookup_order",
    description: "Look up an order's item, amount, and status",
    parameters: orderInput,
    // Without errorFunction: null, tool() turns every thrown error into model-visible text.
    errorFunction: null,
    execute: ({ orderId }) => runOrderTool(() => orders.describeOrder(orderId)),
  });
  const refundOrder = tool({
    name: "refund_order",
    description: "Refund an order in full",
    parameters: orderInput,
    errorFunction: null,
    execute: ({ orderId }) => {
      // The key is stable across the attempts of this call's tool Step.
      const idempotencyKey = getDeferredToolExecution()?.idempotencyKey ?? orderId;
      return runOrderTool(() => describeRefund(orders.refundOrder(orderId, idempotencyKey)));
    },
  });
  const agent = new Agent({
    name: "Order support",
    instructions: orderSupportInstructions,
    model,
    tools: [lookupOrder, refundOrder],
  });
  return createOpenAIAgentsSessionFlow(agent, openAIAgentsOrderSupportFlowType, {
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
