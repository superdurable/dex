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
import type { MastraModelConfig } from "@mastra/core/llm";
import { createTool } from "@mastra/core/tools";
import type { Connection } from "@superdurable/connector-slack";
import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { createMastraAgentSessionFlow } from "@superdurable/dex-agents-mastra";
import { z } from "zod";

import { environmentOr } from "../../../config/env.js";
import { createSlackConnectorToolSources } from "../../../shared/slack-connector-tools.js";
import { orderSupportInstructions, type OrderDesk } from "../order-support/order-desk.js";

/** Flow type of the Mastra order support session. */
export const mastraOrderSupportFlowType = "MastraOrderSupportAgent";

/**
 * The model router ID from `DEX_MASTRA_MODEL`, such as `openai/gpt-5-mini`. Mastra reads the provider key, such as
 * `OPENAI_API_KEY`, from the Worker's environment.
 */
export function loadMastraOrderSupportModel(): MastraModelConfig {
  return environmentOr("DEX_MASTRA_MODEL", "openai/gpt-5-mini");
}

/**
 * Builds the order support session Flow. Each model call and each tool call is its own Step.
 * look_up_order only reads, so it runs without approval. cancel_order waits for approval and runs at most once.
 * With a Slack connection, slack_post_thread_reply also waits for approval and runs at most once.
 */
export function createMastraOrderSupportFlow(
  orderDesk: OrderDesk,
  model: MastraModelConfig,
  slack: Connection | undefined,
): AgentSessionFlow {
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
  const agent = new Agent({
    id: "order-support",
    name: "OrderSupport",
    instructions: orderSupportInstructions,
    model,
    tools: { look_up_order: lookUpOrder, cancel_order: cancelOrder },
  });
  return createMastraAgentSessionFlow(agent, mastraOrderSupportFlowType, {
    toolPolicies: {
      look_up_order: { effect: "read" },
      cancel_order: { effect: "non_idempotent_write" },
    },
    toolSources: createSlackConnectorToolSources(slack),
  });
}
