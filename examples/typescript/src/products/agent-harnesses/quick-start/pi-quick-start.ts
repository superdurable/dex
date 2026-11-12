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

// The Agent Quick Start agent on Pi, before and after Dex.

import { Agent, type AgentTool, type AgentToolResult } from "@earendil-works/pi-agent-core";
import type { Api, Model } from "@earendil-works/pi-ai";
import { streamSimple } from "@earendil-works/pi-ai/compat";
import { obj, str } from "@earendil-works/pi-schema";
import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { createBuiltinAgentSessionFlow, type BuiltinLoopAgentRunner } from "@superdurable/dex-agents/builtin-loop";
import { createPiAiModelClient, piAgentFunctionTool } from "@superdurable/dex-agents-pi";

import { orderSupportInstructions, type OrderDesk } from "../order-support/order-desk.js";

const lookUpOrderArguments = obj({ order_id: str() });
const cancelOrderArguments = obj({ order_id: str(), reason: str() });

/** Returns the two order tools, as Pi defines tools. */
export function buildOrderTools(orderDesk: OrderDesk) {
  const lookUpOrder: AgentTool<typeof lookUpOrderArguments> = {
    name: "look_up_order",
    label: "Look up order",
    description: "Look up one order by its order ID and return its items and status.",
    parameters: lookUpOrderArguments,
    execute: async (_toolCallId, { order_id }) => textResult(await orderDesk.describeOrder(order_id)),
  };
  const cancelOrder: AgentTool<typeof cancelOrderArguments> = {
    name: "cancel_order",
    label: "Cancel order",
    description: "Cancel one order that has not shipped yet.",
    parameters: cancelOrderArguments,
    execute: async (_toolCallId, { order_id, reason }) => textResult(await orderDesk.cancelOrder(order_id, reason)),
  };
  return { lookUpOrder, cancelOrder };
}

/** Returns the agent as Pi alone writes it. Pi's Agent runs the loop in this process. */
export function buildPlainAgent(orderDesk: OrderDesk, model: Model<Api>): Agent {
  const { lookUpOrder, cancelOrder } = buildOrderTools(orderDesk);
  return new Agent({
    initialState: { systemPrompt: orderSupportInstructions, model, tools: [lookUpOrder, cancelOrder] },
    streamFn: streamSimple,
  });
}

/**
 * Returns the same agent as a durable Dex agent session: the same model and tools, plus tool effects.
 * Without apiKey, pi-ai reads the provider's key from the environment.
 */
export function buildDurableAgent(
  orderDesk: OrderDesk,
  model: Model<Api>,
  apiKey?: string,
): AgentSessionFlow<BuiltinLoopAgentRunner> {
  const { lookUpOrder, cancelOrder } = buildOrderTools(orderDesk);
  return createBuiltinAgentSessionFlow(
    {
      name: "order-support",
      instructions: orderSupportInstructions,
      model: createPiAiModelClient(model, { apiKey }),
      tools: [
        piAgentFunctionTool(lookUpOrder, { effect: "read" }),
        piAgentFunctionTool(cancelOrder, { effect: "non_idempotent_write" }),
      ],
    },
    "PiQuickStart",
  );
}

function textResult(text: string): AgentToolResult<undefined> {
  return { content: [{ type: "text", text }], details: undefined };
}
