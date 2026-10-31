// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Agent, RunConfig } from "@openai/agents";
import {
  createAgentSessionFlow,
  type AgentSessionFlow,
  type AgentSessionOptions,
  type ToolPolicy,
} from "@superdurable/dex-agents";

import { OpenAIAgentsRunner } from "./openai-agents-runner.js";

/**
 * Options of {@link createOpenAIAgentsSessionFlow}: the session options of `@superdurable/dex-agents`
 * plus the OpenAI Agents SDK run settings.
 *
 * @typeParam TContext - The application context type of the agent, `RunContext.context` in tools.
 */
export interface OpenAIAgentsSessionOptions<TContext = undefined> extends AgentSessionOptions {
  /**
   * Run configuration for the OpenAI Agents SDK `Runner` that makes every model call, such as a
   * model provider, model settings, guardrails, or `tracingDisabled`. Defaults to the SDK defaults.
   */
  readonly runConfig?: Partial<RunConfig>;
  /**
   * Maximum number of model calls in one turn, counted across all of its `RunModel` Steps, as the
   * SDK's `maxTurns` run option. `null` disables the limit. Defaults to the SDK default of 10. A turn
   * that exceeds it fails and the session records `last_turn_error`.
   */
  readonly maxTurns?: number | null;
  /**
   * Creates the application context value for one model call or one tool call.
   *
   * The adapter calls it at the start of every `RunModel` attempt and every tool Step attempt,
   * possibly in another Worker process, and passes the result as `RunContext.context`. Use it for
   * process-local dependencies, such as clients, and values derived from the session ID. The value
   * is never persisted, so changes a tool makes to it are lost after the call. Defaults to a
   * function returning `undefined`.
   *
   * @param flowId - Flow ID of the session.
   * @returns The context value, or a Promise of it.
   */
  readonly createContext?: (flowId: string) => TContext | Promise<TContext>;
  /**
   * Whether agents may have `mcpServers`. Defaults to `false`, which rejects them.
   *
   * The SDK lists MCP server tools at run time, so the adapter cannot give their calls Dex Steps.
   * With `true`, they run inside `RunModel`, whatever their policy: they never wait for approval and
   * repeat when the model call reruns, and the SDK then makes another model call in the same Step.
   * To run an MCP write durably, call the server from a function tool instead.
   */
  readonly shouldRunMcpServerToolsInline?: boolean;
}

/**
 * Creates a durable Dex agent session Flow that runs an OpenAI Agents SDK agent in Step mode.
 *
 * Call it once per agent at application startup, register the returned Flow in the process
 * Registry, and pass the same object to every Client call. One Flow execution is one session, and
 * its Flow ID is the session ID. The session stores the conversation as OpenAI Agents SDK input
 * items and replays them to the starting agent on every turn, so each turn starts at `agent`, not
 * at the agent a previous turn handed off to.
 *
 * In Step mode every model call is a `RunModel` Step and every tool call is its own tool Step:
 * `CallTool` for `read` and `idempotent_write` tools, `CallSingleAttemptTool` for the others, after
 * `AwaitToolApproval` when approval is required. The tool calls of one model response run in
 * parallel. The adapter copies the agent graph once, the starting agent and every agent reachable
 * through handoffs, and gives every function tool a `needsApproval` gate. The SDK therefore stops
 * after each model call with `result.interruptions`, one per tool call, and the adapter returns them
 * with the serialized `RunState`. Each tool's policy is looked up by its qualified name:
 * `<namespace>.<name>` inside a `toolNamespace`, otherwise the tool name. A tool's own
 * `needsApproval` also requires approval, unless its {@link ToolPolicy} sets `requiresApproval`.
 *
 * The tool Step runs the tool's input guardrails and the original tool once. The next `RunModel`
 * restores the `RunState`, approves every call whose record is final so that the gated tool returns
 * the recorded result, rejects every rejected call with its rejection message, and lets the SDK make
 * the next model call. A handoff is not a function tool: the SDK runs it and makes the next agent's
 * model call in the same `RunModel` Step, so a `RunModel` that hands off makes more than one model
 * call.
 *
 * Text deltas and provider reasoning summary deltas stream to the `AgentEvents` Stream as
 * `text_delta` and `reasoning_delta`. A handoff writes an `openai_agents.handoff` event with
 * `{"agent_name"}`. Usage from the SDK, including cached input and reasoning tokens, is added to
 * `AgentUsage` after each `RunModel` Step. Changes made to the agent graph after this call do not
 * reach the Flow.
 *
 * @example
 * ```ts
 * import { Agent, tool } from "@openai/agents";
 * import { Client, Registry, Worker, openBlobCache } from "@superdurable/dex";
 * import { z } from "zod";
 * import { createOpenAIAgentsSessionFlow } from "@superdurable/dex-agents-openai-agents";
 *
 * const lookupOrder = tool({
 *   name: "lookup_order",
 *   description: "Look up an order.",
 *   parameters: z.object({ orderId: z.string() }),
 *   errorFunction: null,
 *   execute: async ({ orderId }) => orders.describe(orderId),
 * });
 * const refundOrder = tool({
 *   name: "refund_order",
 *   description: "Refund an order.",
 *   parameters: z.object({ orderId: z.string() }),
 *   errorFunction: null,
 *   execute: async ({ orderId }) => orders.refund(orderId),
 * });
 * const agent = new Agent({
 *   name: "Support agent",
 *   instructions: "Help customers with their orders.",
 *   tools: [lookupOrder, refundOrder],
 * });
 *
 * const flow = createOpenAIAgentsSessionFlow(agent, "SupportAgent", {
 *   toolPolicies: {
 *     lookup_order: { effect: "read" },
 *     refund_order: { effect: "idempotent_write", requiresApproval: true },
 *   },
 * });
 * const registry = new Registry([flow]);
 * const blobCache = openBlobCache({ directory: ".dex-cache", maxBytes: 64 * 1024 * 1024 });
 * const worker = new Worker(registry, blobCache, { serverAddress: "127.0.0.1:8801" });
 * await worker.start();
 * await worker.waitPullRegistered({ timeoutMs: 10_000 });
 * const client = new Client(registry, blobCache, { serverAddress: "127.0.0.1:8801" });
 * await client.startFlow(flow, "session-42", { initialMessage: "Refund order 42" });
 * ```
 * @typeParam TContext - The application context type of the agent.
 * @param agent - The starting agent. Its function tools, its handoff agents, and their function
 * tools must have unique qualified names, because a tool call is resolved by name.
 * @param flowType - Flow type, unique within the Registry. Keep it stable: running sessions resume
 * by it.
 * @param options - Tool policies, session options, and SDK run settings. Defaults to all defaults,
 * which treat every function tool as `unclassified`: it waits for approval and runs once.
 * @returns A new Flow to register in one Registry.
 * @throws {TypeError} When `agent` is not an `Agent` from `@openai/agents`; when the graph has a
 * `SandboxAgent`, an agent used as a tool, a client tool search with an `execute` function, MCP
 * servers without `shouldRunMcpServerToolsInline`, or a local computer, shell, or apply-patch tool
 * without a `read` policy free of approval; when two
 * different function tools share a qualified name; or when `createAgentSessionFlow` rejects the Flow
 * type or an option.
 * @throws {RangeError} When `eventStreamCapacityBytes` or `interruptDeadlineMs` is not positive.
 */
export function createOpenAIAgentsSessionFlow<TContext = undefined>(
  agent: Agent<TContext, any>,
  flowType: string,
  options: OpenAIAgentsSessionOptions<TContext> = {},
): AgentSessionFlow {
  const runner = new OpenAIAgentsRunner(agent, {
    toolPolicies: options.toolPolicies ?? {},
    defaultToolEffect: options.defaultToolEffect ?? "unclassified",
    shouldRunMcpServerToolsInline: options.shouldRunMcpServerToolsInline ?? false,
    runConfig: options.runConfig ?? {},
    maxTurns: options.maxTurns,
    createContext: options.createContext,
  });
  return createAgentSessionFlow(runner, flowType, {
    ...(options.toolPolicies === undefined ? {} : { toolPolicies: options.toolPolicies }),
    ...(options.defaultToolEffect === undefined ? {} : { defaultToolEffect: options.defaultToolEffect }),
    ...(options.eventStreamCapacityBytes === undefined
      ? {}
      : { eventStreamCapacityBytes: options.eventStreamCapacityBytes }),
    ...(options.interruptDeadlineMs === undefined
      ? {}
      : { interruptDeadlineMs: options.interruptDeadlineMs }),
  });
}
