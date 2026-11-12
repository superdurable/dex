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

// The Agent Quick Start agent on Mastra, before and after Dex.

import { Agent } from "@mastra/core/agent";
import type { MastraModelConfig } from "@mastra/core/llm";
import { createTool } from "@mastra/core/tools";
import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { createMastraAgentSessionFlow } from "@superdurable/dex-agents-mastra";
import { z } from "zod";

import { orderSupportInstructions, type OrderDesk } from "../order-support/order-desk.js";

/** Returns the agent as Mastra alone writes it. */
export function buildPlainAgent(orderDesk: OrderDesk, model: MastraModelConfig): Agent {
  const lookUpOrder = createTool({
    id: "look_up_order",
    description: "Look up one order by its order ID and return its items and status.",
    inputSchema: z.object({ order_id: z.string() }),
    execute: async ({ order_id }) => orderDesk.describeOrder(order_id),
  });
  const cancelOrder = createTool({
    id: "cancel_order",
    description: "Cancel one order that has not shipped yet.",
    inputSchema: z.object({ order_id: z.string(), reason: z.string() }),
    execute: async ({ order_id, reason }) => orderDesk.cancelOrder(order_id, reason),
  });
  return new Agent({
    id: "order-support",
    name: "OrderSupport",
    instructions: orderSupportInstructions,
    model,
    tools: { look_up_order: lookUpOrder, cancel_order: cancelOrder },
  });
}

/** Returns the same agent as a durable Dex agent session. */
export function buildDurableAgent(orderDesk: OrderDesk, model: MastraModelConfig): AgentSessionFlow {
  return createMastraAgentSessionFlow(buildPlainAgent(orderDesk, model), "MastraQuickStart", {
    toolPolicies: {
      look_up_order: { effect: "read" },
      cancel_order: { effect: "non_idempotent_write" },
    },
  });
}
