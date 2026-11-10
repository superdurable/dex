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

import { openai } from "@ai-sdk/openai";
import type { AgentSessionFlow } from "@superdurable/dex-agents";
import {
  ToolExecutionError,
  createVercelAiAgentSessionFlow,
  getAgentToolExecutionScope,
} from "@superdurable/dex-agents-vercel-ai";
import { tool, type LanguageModel } from "ai";
import { z } from "zod";

import { environmentOr } from "../../../config/env.js";
import {
  OrderNotFoundError,
  describeRefund,
  orderSupportInstructions,
  type OrderService,
} from "../order-support/order-service.js";

/** Flow type of the Vercel AI SDK order-support session. */
export const vercelAiOrderSupportFlowType = "VercelAiOrderSupportAgent";

/** The model from `DEX_VERCEL_AI_MODEL`; the AI SDK reads `OPENAI_API_KEY` from the Worker's environment. */
export function loadVercelAiOrderSupportModel(): LanguageModel {
  return openai(environmentOr("DEX_VERCEL_AI_MODEL", "gpt-5-mini"));
}

/**
 * Builds the order-support session Flow from a Vercel AI SDK agent definition. Every model call is a `RunModel` Step
 * and every tool call is its own Step, so `refund_order` waits for approval and then runs once.
 */
export function createVercelAiOrderSupportFlow(orders: OrderService, model: LanguageModel): AgentSessionFlow {
  const orderInput = z.object({ orderId: z.string().describe("The order ID, such as A-1001") });
  return createVercelAiAgentSessionFlow(
    {
      model,
      instructions: orderSupportInstructions,
      tools: {
        lookup_order: tool({
          description: "Look up an order's item, amount, and status",
          inputSchema: orderInput,
          execute: ({ orderId }) => runOrderTool(() => orders.describeOrder(orderId)),
        }),
        refund_order: tool({
          description: "Refund an order in full",
          inputSchema: orderInput,
          execute: ({ orderId }) => {
            // The key is stable across the attempts of this call's tool Step.
            const idempotencyKey = getAgentToolExecutionScope()?.idempotencyKey ?? orderId;
            return runOrderTool(() => describeRefund(orders.refundOrder(orderId, idempotencyKey)));
          },
        }),
      },
      toolPolicies: {
        lookup_order: { effect: "read" },
        refund_order: { effect: "idempotent_write", requiresApproval: true },
      },
    },
    vercelAiOrderSupportFlowType,
    { interruptDeadlineMs: 24 * 60 * 60 * 1000 },
  );
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
