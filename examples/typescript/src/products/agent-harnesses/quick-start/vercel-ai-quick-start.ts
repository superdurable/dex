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

// The Agent Quick Start agent on the Vercel AI SDK, before and after Dex.

import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { createVercelAiAgentSessionFlow } from "@superdurable/dex-agents-vercel-ai";
import { ToolLoopAgent, tool, type LanguageModel } from "ai";
import { z } from "zod";

import { orderSupportInstructions, type OrderDesk } from "../order-support/order-desk.js";

/** Returns the two order tools, as the Vercel AI SDK defines tools. */
export function buildOrderTools(orderDesk: OrderDesk) {
  return {
    look_up_order: tool({
      description: "Look up one order by its order ID and return its items and status.",
      inputSchema: z.object({ order_id: z.string() }),
      execute: ({ order_id }) => orderDesk.describeOrder(order_id),
    }),
    cancel_order: tool({
      description: "Cancel one order that has not shipped yet.",
      inputSchema: z.object({ order_id: z.string(), reason: z.string() }),
      execute: ({ order_id, reason }) => orderDesk.cancelOrder(order_id, reason),
    }),
  };
}

/** Returns the agent as the Vercel AI SDK alone writes it. */
export function buildPlainAgent(orderDesk: OrderDesk, model: LanguageModel) {
  return new ToolLoopAgent({ model, instructions: orderSupportInstructions, tools: buildOrderTools(orderDesk) });
}

/** Returns the same agent as a durable Dex agent session: the same settings, plus tool policies. */
export function buildDurableAgent(orderDesk: OrderDesk, model: LanguageModel): AgentSessionFlow {
  return createVercelAiAgentSessionFlow(
    {
      model,
      instructions: orderSupportInstructions,
      tools: buildOrderTools(orderDesk),
      toolPolicies: {
        look_up_order: { effect: "read" },
        cancel_order: { effect: "non_idempotent_write" },
      },
    },
    "VercelAiQuickStart",
  );
}
