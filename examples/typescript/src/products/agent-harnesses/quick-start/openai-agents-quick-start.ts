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

// The Agent Quick Start agent on the OpenAI Agents SDK, before and after Dex.

import { Agent, tool, type Model } from "@openai/agents";
import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { createOpenAIAgentsSessionFlow } from "@superdurable/dex-agents-openai-agents";
import { z } from "zod";

import { orderSupportInstructions, type OrderDesk } from "../order-support/order-desk.js";

/** Returns the agent as the OpenAI Agents SDK alone writes it. */
export function buildPlainAgent(orderDesk: OrderDesk, model: string | Model): Agent {
  const lookUpOrder = tool({
    name: "look_up_order",
    description: "Look up one order by its order ID and return its items and status.",
    parameters: z.object({ order_id: z.string() }),
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
  return new Agent({
    name: "OrderSupport",
    instructions: orderSupportInstructions,
    model,
    tools: [lookUpOrder, cancelOrder],
  });
}

/** Returns the same agent as a durable Dex agent session. */
export function buildDurableAgent(orderDesk: OrderDesk, model: string | Model): AgentSessionFlow {
  return createOpenAIAgentsSessionFlow(buildPlainAgent(orderDesk, model), "OpenAIAgentsQuickStart", {
    toolPolicies: {
      look_up_order: { effect: "read" },
      cancel_order: { effect: "non_idempotent_write" },
    },
  });
}
