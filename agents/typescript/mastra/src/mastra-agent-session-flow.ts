// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Agent } from "@mastra/core/agent";
import {
  createAgentSessionFlow,
  type AgentSessionFlow,
  type AgentSessionOptions,
} from "@superdurable/dex-agents";

import { MastraAgentRunner, type MastraAgentRunnerOptions } from "./mastra-agent-runner.js";

/**
 * Options of {@link createMastraAgentSessionFlow}: the shared session options plus the Mastra runner options.
 *
 * `toolPolicies` serves both: the session uses it to classify every tool call, and the runner uses its
 * names to offer tools that Mastra adds outside the agent's `tools`.
 */
export interface MastraAgentSessionOptions
  extends AgentSessionOptions,
    Omit<MastraAgentRunnerOptions, "toolPolicies"> {}

/**
 * Creates a durable Dex agent session Flow for one Mastra `Agent`.
 *
 * Call it once per agent at application startup, register the returned Flow in the process `Registry`,
 * and pass the same object to every Client call. Each call returns a new Flow, so one Registry can hold
 * several agents under different Flow types. The Flow ID is the session ID; start a session with
 * `Client.startFlow`, then use the Flow's RPCs `send_user_message`, `resolve_interrupt`,
 * `get_agent_snapshot`, and `list_agent_messages`.
 *
 * The session runs in Step mode. Each model call is a `RunModel` Step: Mastra stops after the model call,
 * because every agent tool is passed as a client tool. Each tool call then runs in its own Step, in
 * parallel with the other calls of the response: `CallTool` for `read` and `idempotent_write` tools,
 * `CallSingleAttemptTool` for the others, after `AwaitToolApproval` when the policy requires approval.
 * The next `RunModel` gives the model the recorded results. An effect comes from
 * `toolPolicies`, then from the tool's MCP annotations (`mcp.annotations` of `createTool`), then from
 * `defaultToolEffect`. A tool created with Mastra's `requireApproval` always waits for approval.
 *
 * @example
 * ```ts
 * import { Agent } from "@mastra/core/agent";
 * import { createTool } from "@mastra/core/tools";
 * import { Registry } from "@superdurable/dex";
 * import { createMastraAgentSessionFlow } from "@superdurable/dex-agents-mastra";
 * import { z } from "zod";
 *
 * const searchOrders = createTool({
 *   id: "search_orders",
 *   description: "Find a customer's orders",
 *   inputSchema: z.object({ customerId: z.string() }),
 *   execute: async ({ customerId }) => ordersApi.search(customerId),
 * });
 * const refundOrder = createTool({
 *   id: "refund_order",
 *   description: "Refund one order",
 *   inputSchema: z.object({ orderId: z.string() }),
 *   execute: async ({ orderId }) => ordersApi.refund(orderId),
 * });
 * const supportAgent = new Agent({
 *   id: "support",
 *   name: "Support",
 *   instructions: "Help customers with their orders.",
 *   model: "openai/gpt-5",
 *   tools: { search_orders: searchOrders, refund_order: refundOrder },
 * });
 *
 * const flow = createMastraAgentSessionFlow(supportAgent, "SupportAgent", {
 *   toolPolicies: {
 *     search_orders: { effect: "read" },
 *     refund_order: { effect: "idempotent_write", requiresApproval: true },
 *   },
 *   maxSteps: 10,
 * });
 * const registry = new Registry([flow]);
 * ```
 * @param agent - The Mastra agent. The runner passes it no Memory thread, so Dex alone stores the conversation.
 * @param flowType - Flow type, unique within the Registry, such as `"SupportAgent"`. Keep it stable,
 * because running sessions resume by it.
 * @param options - Session and runner options. Defaults to all defaults: every tool without a policy or
 * MCP annotations is `unclassified`, so it runs once and needs approval.
 * @returns A new Flow to register in one Registry.
 * @throws {TypeError} When `flowType` is blank or an effect is unknown.
 * @throws {RangeError} When `maxSteps`, `eventStreamCapacityBytes`, or `interruptDeadlineMs` is not positive.
 */
export function createMastraAgentSessionFlow(
  agent: Agent,
  flowType: string,
  options: MastraAgentSessionOptions = {},
): AgentSessionFlow {
  const {
    toolPolicies,
    defaultToolEffect,
    eventStreamCapacityBytes,
    interruptDeadlineMs,
    ...runnerOptions
  } = options;
  const runner = new MastraAgentRunner(agent, {
    ...runnerOptions,
    ...(toolPolicies === undefined ? {} : { toolPolicies }),
  });
  return createAgentSessionFlow(runner, flowType, {
    ...(toolPolicies === undefined ? {} : { toolPolicies }),
    ...(defaultToolEffect === undefined ? {} : { defaultToolEffect }),
    ...(eventStreamCapacityBytes === undefined ? {} : { eventStreamCapacityBytes }),
    ...(interruptDeadlineMs === undefined ? {} : { interruptDeadlineMs }),
  });
}
