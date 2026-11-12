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

// The Agent Quick Start agent on the Claude Agent SDK, before and after Dex.

import { createSdkMcpServer, tool, type Options } from "@anthropic-ai/claude-agent-sdk";
import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { createClaudeAgentSessionFlow } from "@superdurable/dex-agents-claude-agent-sdk";
import { z } from "zod";

import { orderSupportInstructions, type OrderDesk } from "../order-support/order-desk.js";

/** Returns the two order tools, as the Claude Agent SDK defines tools. */
export function buildOrderTools(orderDesk: OrderDesk) {
  const lookUpOrder = tool(
    "look_up_order",
    "Look up one order by its order ID and return its items and status.",
    { order_id: z.string() },
    async ({ order_id }) => ({ content: [{ type: "text", text: await orderDesk.describeOrder(order_id) }] }),
  );
  const cancelOrder = tool(
    "cancel_order",
    "Cancel one order that has not shipped yet.",
    { order_id: z.string(), reason: z.string() },
    async ({ order_id, reason }) => ({ content: [{ type: "text", text: await orderDesk.cancelOrder(order_id, reason) }] }),
  );
  return [lookUpOrder, cancelOrder];
}

/** Returns the agent's options as the Claude Agent SDK alone writes them. */
export function buildPlainAgent(orderDesk: OrderDesk, model: string): Options {
  const orders = createSdkMcpServer({ name: "orders", tools: buildOrderTools(orderDesk) });
  return {
    model,
    systemPrompt: orderSupportInstructions,
    mcpServers: { orders },
    allowedTools: ["mcp__orders__look_up_order", "mcp__orders__cancel_order"],
  };
}

/** Returns the same agent as a durable Dex agent session. Dex passes the tools to the CLI itself. */
export function buildDurableAgent(
  orderDesk: OrderDesk,
  model: string,
  cliEnvironment: Readonly<Record<string, string>>,
): AgentSessionFlow {
  return createClaudeAgentSessionFlow(
    {
      options: { model, systemPrompt: orderSupportInstructions, tools: [], settingSources: [], env: { ...cliEnvironment } },
      customTools: buildOrderTools(orderDesk),
      toolPolicies: {
        look_up_order: { effect: "read" },
        cancel_order: { effect: "non_idempotent_write" },
      },
    },
    "ClaudeAgentSdkQuickStart",
  );
}
