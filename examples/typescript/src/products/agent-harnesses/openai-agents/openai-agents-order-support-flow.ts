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
import { createOpenAIAgentsSessionFlow, type AgentSessionFlow } from "@superdurable/dex-agents-openai-agents";
import { z } from "zod";

import { environmentOr } from "../../../config/env.js";
import { orderSupportInstructions, type OrderDesk } from "../order-support/order-desk.js";

/** Flow type of the OpenAI Agents SDK order support session. */
export const openAIAgentsOrderSupportFlowType = "OpenAIAgentsOrderSupportAgent";

/**
 * The model name from `DEX_OPENAI_AGENTS_MODEL`. The OpenAI Agents SDK reads `OPENAI_API_KEY` from the Worker's
 * environment.
 */
export function loadOpenAIAgentsOrderSupportModel(): string {
  return environmentOr("DEX_OPENAI_AGENTS_MODEL", "gpt-5-mini");
}

/**
 * Builds the order support session Flow. Each model call and each tool call is its own Step.
 * look_up_order only reads, so it runs without approval. cancel_order waits for approval and runs at most once.
 */
export function createOpenAIAgentsOrderSupportFlow(orderDesk: OrderDesk, model: string | Model): AgentSessionFlow {
  const lookUpOrder = tool({
    name: "look_up_order",
    description: "Look up one order by its order ID and return its items and status.",
    parameters: z.object({ order_id: z.string() }),
    // Without errorFunction: null, tool() turns every thrown error into model-visible text.
    errorFunction: null,
    execute: ({ order_id }) => orderDesk.describeOrder(order_id),
  });
  const cancelOrder = tool({
    name: "cancel_order",
    description: "Cancel one order that has not shipped yet.",
    parameters: z.object({ order_id: z.string(), reason: z.string() }),
    errorFunction: null,
    execute: ({ order_id, reason }) => orderDesk.cancelOrder(order_id, reason),
  });
  const agent = new Agent({
    name: "OrderSupport",
    instructions: orderSupportInstructions,
    model,
    tools: [lookUpOrder, cancelOrder],
  });
  return createOpenAIAgentsSessionFlow(agent, openAIAgentsOrderSupportFlowType, {
    toolPolicies: {
      look_up_order: { effect: "read" },
      cancel_order: { effect: "non_idempotent_write" },
    },
  });
}
