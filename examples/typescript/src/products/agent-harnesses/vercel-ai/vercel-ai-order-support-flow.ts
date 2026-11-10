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
import type { Connection } from "@superdurable/connector-slack";
import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { createVercelAiAgentSessionFlow } from "@superdurable/dex-agents-vercel-ai";
import { tool, type LanguageModel } from "ai";
import { z } from "zod";

import { environmentOr } from "../../../config/env.js";
import { createSlackConnectorToolSources } from "../../../shared/slack-connector-tools.js";
import { orderSupportInstructions, type OrderDesk } from "../order-support/order-desk.js";

/** Flow type of the Vercel AI SDK order support session. */
export const vercelAiOrderSupportFlowType = "VercelAiOrderSupportAgent";

/** The model from `DEX_VERCEL_AI_MODEL`; the AI SDK reads `OPENAI_API_KEY` from the Worker's environment. */
export function loadVercelAiOrderSupportModel(): LanguageModel {
  return openai(environmentOr("DEX_VERCEL_AI_MODEL", "gpt-5-mini"));
}

/**
 * Builds the order support session Flow. Each model call and each tool call is its own Step.
 * look_up_order only reads, so it runs without approval. cancel_order waits for approval and runs at most once.
 * With a Slack connection, slack_post_thread_reply also waits for approval and runs at most once.
 */
export function createVercelAiOrderSupportFlow(
  orderDesk: OrderDesk,
  model: LanguageModel,
  slack: Connection | undefined,
): AgentSessionFlow {
  return createVercelAiAgentSessionFlow(
    {
      model,
      instructions: orderSupportInstructions,
      tools: {
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
      },
      toolPolicies: {
        look_up_order: { effect: "read" },
        cancel_order: { effect: "non_idempotent_write" },
      },
      toolSources: createSlackConnectorToolSources(slack),
    },
    vercelAiOrderSupportFlowType,
  );
}
