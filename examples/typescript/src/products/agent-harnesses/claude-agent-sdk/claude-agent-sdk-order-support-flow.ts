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

import { tool } from "@anthropic-ai/claude-agent-sdk";
import type { Connection } from "@superdurable/connector-slack";
import { createClaudeAgentSessionFlow } from "@superdurable/dex-agents-claude-agent-sdk";
import { z } from "zod";

import { environmentOr } from "../../../config/env.js";
import { createSlackConnectorToolSources } from "../../../shared/slack-connector-tools.js";
import { orderSupportInstructions, type OrderDesk } from "../order-support/order-desk.js";

/** Flow type of the Claude Agent SDK order support session. */
export const claudeAgentSdkOrderSupportFlowType = "ClaudeAgentSdkOrderSupportAgent";

/** The Claude model from `DEX_CLAUDE_AGENT_SDK_MODEL`. */
export function loadClaudeAgentSdkOrderSupportModel(): string {
  return environmentOr("DEX_CLAUDE_AGENT_SDK_MODEL", "claude-opus-5-5");
}

/**
 * Whether the Worker can authenticate the Claude Agent SDK. The example authenticates only with
 * `ANTHROPIC_API_KEY`, so without it the Claude session Flow is not registered.
 */
export function hasAnthropicApiKey(): boolean {
  return (process.env.ANTHROPIC_API_KEY ?? "") !== "";
}

/** Anthropic settings of the Worker that reach the Claude Code CLI besides its API key, as in the Python example. */
const keptAnthropicSettingNames = ["ANTHROPIC_BASE_URL"];

/** Worker environment variables the Claude Code CLI needs to run, besides its API key. */
const claudeCliRuntimeVariableNames = ["PATH", "HOME", "TMPDIR", "HTTPS_PROXY", "HTTP_PROXY", "NO_PROXY"];

/**
 * Builds the whole environment of the Claude Code CLI: `ANTHROPIC_API_KEY`, `ANTHROPIC_BASE_URL` when it is set,
 * and the variables the CLI needs to run. Other credentials in the Worker's environment never reach the CLI.
 */
export function buildClaudeCliEnvironment(): Record<string, string> {
  const cliEnvironment: Record<string, string> = { ANTHROPIC_API_KEY: process.env.ANTHROPIC_API_KEY ?? "" };
  for (const name of [...keptAnthropicSettingNames, ...claudeCliRuntimeVariableNames]) {
    const value = process.env[name];
    if (value !== undefined) {
      cliEnvironment[name] = value;
    }
  }
  return cliEnvironment;
}

/**
 * Builds the order support session Flow. The Claude Code CLI runs each turn, and look_up_order runs inside it.
 * cancel_order waits for approval, runs at most once in its own Step, and then the Claude session continues.
 * With a Slack connection, slack_post_thread_reply does the same.
 *
 * @param cliEnvironment - The whole environment of the Claude Code CLI, from {@link buildClaudeCliEnvironment}.
 */
export function createClaudeAgentSdkOrderSupportFlow(
  orderDesk: OrderDesk,
  model: string,
  cliEnvironment: Readonly<Record<string, string>>,
  slack: Connection | undefined,
) {
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
  return createClaudeAgentSessionFlow(
    {
      options: {
        model,
        systemPrompt: orderSupportInstructions,
        // No Claude Code built-in tools: the agent uses only the order tools.
        tools: [],
        // Settings files could approve a tool before Dex asks for approval.
        settingSources: [],
        // Only the variables in cliEnvironment reach the CLI, so no other Claude credential can take over.
        env: { ...cliEnvironment },
      },
      customTools: [lookUpOrder, cancelOrder],
      toolPolicies: {
        look_up_order: { effect: "read" },
        cancel_order: { effect: "non_idempotent_write" },
      },
      toolSources: createSlackConnectorToolSources(slack),
    },
    claudeAgentSdkOrderSupportFlowType,
  );
}
