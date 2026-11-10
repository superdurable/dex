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

import { mkdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

import { tool } from "@anthropic-ai/claude-agent-sdk";
import type { AgentSessionFlow } from "@superdurable/dex-agents";
import { createClaudeAgentSessionFlow, currentDeferredToolExecution } from "@superdurable/dex-agents-claude-agent-sdk";
import { z } from "zod";

import { environmentOr } from "../../../config/env.js";
import {
  OrderNotFoundError,
  describeRefund,
  orderSupportInstructions,
  type OrderService,
} from "../order-support/order-service.js";

/** Flow type of the Claude Agent SDK order-support session. */
export const claudeAgentSdkOrderSupportFlowType = "ClaudeAgentSdkOrderSupportAgent";

/** Worker configuration of the Claude Agent SDK example, shared by every session. */
export interface ClaudeAgentSdkOrderSupportConfig {
  /** Claude model of every turn, such as `claude-opus-5-5`. */
  readonly model: string;
  /** Directory under which each session gets its own working directory for the Claude Code CLI. */
  readonly workspaceRoot: string;
  /** The whole CLI environment. Unset, the CLI inherits the Worker's, which holds `ANTHROPIC_API_KEY`. */
  readonly cliEnvironment?: Readonly<Record<string, string>>;
}

/**
 * Reads `DEX_CLAUDE_AGENT_MODEL` and `DEX_CLAUDE_AGENT_WORKSPACE`. The Claude Code CLI authenticates with
 * `ANTHROPIC_API_KEY` from the Worker's environment.
 */
export function loadClaudeAgentSdkOrderSupportConfig(): ClaudeAgentSdkOrderSupportConfig {
  return {
    model: environmentOr("DEX_CLAUDE_AGENT_MODEL", "claude-opus-5-5"),
    workspaceRoot: environmentOr("DEX_CLAUDE_AGENT_WORKSPACE", join(tmpdir(), "dex-claude-agent-sdk-order-support")),
  };
}

/**
 * Builds the order-support session Flow from Claude Agent SDK options and custom tools. One `RunModel` Step runs the
 * Claude Code CLI for one turn segment; `lookup_order` runs inside it, and `refund_order` waits for approval and then
 * runs once in its own Step.
 */
export function createClaudeAgentSdkOrderSupportFlow(
  orders: OrderService,
  config: ClaudeAgentSdkOrderSupportConfig,
): AgentSessionFlow {
  const orderInput = { orderId: z.string().describe("The order ID, such as A-1001") };
  const lookupOrder = tool(
    "lookup_order",
    "Look up an order's item, amount, and status",
    orderInput,
    async ({ orderId }) => runOrderTool(() => orders.describeOrder(orderId)),
    { annotations: { readOnlyHint: true } },
  );
  const refundOrder = tool("refund_order", "Refund an order in full", orderInput, async ({ orderId }) => {
    // The key is stable across the attempts of this call's tool Step.
    const idempotencyKey = currentDeferredToolExecution()?.idempotencyKey ?? orderId;
    return runOrderTool(() => describeRefund(orders.refundOrder(orderId, idempotencyKey)));
  });
  return createClaudeAgentSessionFlow(
    {
      // A resumed segment must see the same options, so derive them from the session ID only.
      options: (context) => {
        const cwd = join(config.workspaceRoot, context.flowId);
        mkdirSync(cwd, { recursive: true });
        return {
          model: config.model,
          systemPrompt: orderSupportInstructions,
          // No built-in Claude Code tools: the agent only looks up and refunds orders.
          tools: [],
          cwd,
          // Settings files could approve a tool before the adapter is asked.
          settingSources: [],
          ...(config.cliEnvironment === undefined ? {} : { env: { ...config.cliEnvironment } }),
        };
      },
      customTools: [lookupOrder, refundOrder],
      toolPolicies: {
        lookup_order: { effect: "read" },
        refund_order: { effect: "idempotent_write", requiresApproval: true },
      },
    },
    claudeAgentSdkOrderSupportFlowType,
    { interruptDeadlineMs: 24 * 60 * 60 * 1000 },
  );
}

/** Reports an unknown order to the model as a failed call instead of retrying it. */
async function runOrderTool(action: () => string): Promise<{ content: { type: "text"; text: string }[]; isError?: boolean }> {
  try {
    return { content: [{ type: "text", text: action() }] };
  } catch (error) {
    if (error instanceof OrderNotFoundError) {
      return { content: [{ type: "text", text: error.message }], isError: true };
    }
    throw error;
  }
}
