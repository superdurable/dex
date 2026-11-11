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

import type { AgentTool, AgentToolResult } from "@earendil-works/pi-agent-core";
import type { Api, Model } from "@earendil-works/pi-ai";
import { getBuiltinModels, type BuiltinProvider } from "@earendil-works/pi-ai/providers/all";
import { obj, str } from "@earendil-works/pi-schema";
import type { Connection } from "@superdurable/connector-slack";
import { createBuiltinAgentSessionFlow } from "@superdurable/dex-agents/builtin-loop";
import { createPiAiModelClient, piAgentFunctionTool } from "@superdurable/dex-agents-pi";

import { environmentOr } from "../../../config/env.js";
import { createSlackConnectorToolSources } from "../../../shared/slack-connector-tools.js";
import { orderSupportInstructions, type OrderDesk } from "../order-support/order-desk.js";
import type { OrderSupportSessionFlow } from "../order-support/order-support-routes.js";

/** Flow type of the Pi order support session. */
export const piOrderSupportFlowType = "PiOrderSupportAgent";

const lookUpOrderArguments = obj({ order_id: str() });
const cancelOrderArguments = obj({ order_id: str(), reason: str() });

/**
 * The pi-ai model from `DEX_PI_MODEL`, a `<provider>/<model ID>` of pi-ai's built-in catalog. pi-ai reads the
 * provider's key, such as `OPENAI_API_KEY`, from the Worker's environment.
 */
export function loadPiOrderSupportModel(): Model<Api> {
  const name = environmentOr("DEX_PI_MODEL", "openai/gpt-5-mini");
  const separator = name.indexOf("/");
  const provider = name.slice(0, separator) as BuiltinProvider;
  const modelId = name.slice(separator + 1);
  const model = separator > 0 ? getBuiltinModels(provider).find((candidate) => candidate.id === modelId) : undefined;
  if (model === undefined) {
    throw new Error(`DEX_PI_MODEL ${name} is not a <provider>/<model ID> of pi-ai's built-in catalog`);
  }
  return model;
}

/**
 * Builds the order support session Flow. Dex runs the agent loop: each model call and each tool call is its own Step.
 * look_up_order only reads, so it runs without approval. cancel_order waits for approval and runs at most once.
 * With a Slack connection, slack_post_thread_reply also waits for approval and runs at most once.
 */
export function createPiOrderSupportFlow(
  orderDesk: OrderDesk,
  model: Model<Api>,
  slack: Connection | undefined,
): OrderSupportSessionFlow {
  const lookUpOrder: AgentTool<typeof lookUpOrderArguments> = {
    name: "look_up_order",
    label: "Look up order",
    description: "Look up one order by its order ID and return its items and status.",
    parameters: lookUpOrderArguments,
    execute: async (_toolCallId, { order_id }) => textResult(orderDesk.describeOrder(order_id)),
  };
  const cancelOrder: AgentTool<typeof cancelOrderArguments> = {
    name: "cancel_order",
    label: "Cancel order",
    description: "Cancel one order that has not shipped yet.",
    parameters: cancelOrderArguments,
    // Not safe to repeat, so Dex asks for approval first and runs it at most once.
    replay: "never",
    execute: async (_toolCallId, { order_id, reason }) => textResult(orderDesk.cancelOrder(order_id, reason)),
  };
  return createBuiltinAgentSessionFlow(
    {
      name: "order-support",
      instructions: orderSupportInstructions,
      model: createPiAiModelClient(model),
      tools: [piAgentFunctionTool(lookUpOrder, { effect: "read" }), piAgentFunctionTool(cancelOrder)],
      toolSources: createSlackConnectorToolSources(slack),
    },
    piOrderSupportFlowType,
  );
}

function textResult(text: string): AgentToolResult<undefined> {
  return { content: [{ type: "text", text }], details: undefined };
}
