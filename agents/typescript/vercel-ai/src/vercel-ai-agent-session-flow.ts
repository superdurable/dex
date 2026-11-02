// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  isStepCount,
  type Instructions,
  type LanguageModel,
  type LanguageModelCallOptions,
  type StopCondition,
  type Tool,
  type ToolChoice,
  type ToolSet,
  type streamText,
} from "ai";

import {
  createAgentSessionFlow,
  type AgentSessionFlow,
  type AgentSessionOptions,
  type ToolEffect,
  type ToolPolicy,
} from "@superdurable/dex-agents";

import {
  VercelAiAgentRunner,
  isProviderExecutedTool,
  type ResolvedVercelAiAgentDefinition,
} from "./vercel-ai-agent-runner.js";

/** Model step limit of one turn when {@link VercelAiAgentDefinition.stopWhen} is omitted. */
export const defaultVercelAiTurnStepCount = 20;

/**
 * The AI SDK agent that a Dex agent session runs: model, instructions, tools, and Dex tool policies.
 *
 * Pass it to {@link createVercelAiAgentSessionFlow}. The session calls AI SDK `streamText` with these
 * settings for every model call and rebuilds `messages` from the durable session history, so the
 * definition must be identical in every Worker process that serves the Flow type.
 *
 * Every tool runs in Dex, never in a browser: each tool must have an `execute` function, except a
 * provider-executed tool such as a provider web search, which runs inside the model call. The model
 * sees every tool without `execute`, so each call comes back to Dex and runs in its own Step, in
 * parallel with the other calls of the response. A tool's Dex {@link ToolPolicy} picks that Step and
 * whether the call first waits for `resolve_interrupt`.
 *
 * Throw `ToolExecutionError` from `execute` for a failure the model should see; the call is recorded as
 * `failed` and not retried. Any other error, crash, or timeout fails the Step attempt. A `read` or
 * `idempotent_write` call retries up to 3 attempts, with the same idempotency key; then a `read` call is
 * recorded as `failed` and a write as `outcome_unknown`. A single-attempt call records `outcome_unknown`
 * at once, because the write may have happened.
 *
 * @example
 * ```ts
 * import { openai } from "@ai-sdk/openai";
 * import { jsonSchema, tool } from "ai";
 *
 * const supportAgent = {
 *   model: openai("gpt-5"),
 *   instructions: "You help customers with their orders.",
 *   tools: {
 *     get_order: tool({
 *       description: "Look up an order",
 *       inputSchema: jsonSchema<{ orderId: string }>({
 *         type: "object", properties: { orderId: { type: "string" } }, required: ["orderId"] }),
 *       execute: async ({ orderId }) => orders.get(orderId),
 *     }),
 *     refund_order: tool({
 *       description: "Refund an order",
 *       inputSchema: jsonSchema<{ orderId: string }>({
 *         type: "object", properties: { orderId: { type: "string" } }, required: ["orderId"] }),
 *       execute: async ({ orderId }) => payments.refund(orderId),
 *     }),
 *   },
 *   toolPolicies: {
 *     get_order: { effect: "read" },
 *     refund_order: { effect: "non_idempotent_write" },
 *   },
 * } satisfies VercelAiAgentDefinition;
 * ```
 * @typeParam TOOLS - The tool set; its names type the keys of {@link toolPolicies}.
 */
export interface VercelAiAgentDefinition<TOOLS extends ToolSet = ToolSet> {
  /** Language model of every model call, such as `openai("gpt-5")` or a gateway model ID. */
  readonly model: LanguageModel;
  /** AI SDK `instructions` sent before the conversation on every model call. Defaults to none. */
  readonly instructions?: Instructions;
  /**
   * AI SDK tools by the name the model calls. Defaults to none. Every tool needs an `execute`
   * function unless it is provider-executed. A tool-level `needsApproval: true` requires approval
   * unless the tool's {@link ToolPolicy} sets `requiresApproval`; a `needsApproval` function is
   * rejected because Dex decides approval before it sees the call's input.
   */
  readonly tools?: TOOLS;
  /**
   * Dex policy by tool name. A policy overrides framework metadata and {@link defaultToolEffect}.
   * Defaults to none. Keys must name tools in {@link tools}.
   */
  readonly toolPolicies?: { readonly [TOOL_NAME in keyof TOOLS & string]?: ToolPolicy };
  /**
   * Effect of a tool without a policy or trusted MCP annotations. Defaults to `"unclassified"`, so such
   * a tool runs at most once, in its own Step, after approval.
   */
  readonly defaultToolEffect?: ToolEffect;
  /**
   * Whether MCP tool annotations classify tools that have no {@link ToolPolicy}. Defaults to `false`.
   * When `true`, a tool whose `metadata.annotations` (as `@ai-sdk/mcp` sets them) has `readOnlyHint`
   * is `read`, and otherwise one with `idempotentHint` is `idempotent_write`. Enable it
   * only for MCP servers the application trusts; annotations are hints from the server.
   */
  readonly shouldTrustMcpToolAnnotations?: boolean;
  /**
   * AI SDK stop conditions of one turn. Defaults to `isStepCount(`{@link defaultVercelAiTurnStepCount}`)`.
   * As in the AI SDK loop, they run after each model step whose tool calls all have results, over every
   * model step of the turn; when one is met, the turn ends with those results and no further model call.
   * Steps from earlier `RunModel` Steps are rebuilt from JSON, with `stepNumber`, `finishReason`, `text`,
   * `toolCalls`, `toolResults`, `content`, and `usage`; their other `StepResult` fields are absent.
   */
  readonly stopWhen?: StopCondition<NoInfer<TOOLS>> | readonly StopCondition<NoInfer<TOOLS>>[];
  /** AI SDK tool choice of every model call. Defaults to the AI SDK default, `"auto"`. */
  readonly toolChoice?: ToolChoice<NoInfer<TOOLS>>;
  /** AI SDK model call settings, such as `temperature` and `maxOutputTokens`. Defaults to none. */
  readonly callSettings?: LanguageModelCallOptions;
  /** AI SDK `providerOptions` of every model call. Defaults to none. */
  readonly providerOptions?: Parameters<typeof streamText>[0]["providerOptions"];
  /**
   * AI SDK retries of one model call, within one Dex attempt. Defaults to the AI SDK default, 2. Dex
   * still retries the `RunModel` Step up to 3 attempts.
   */
  readonly maxRetries?: number;
  /** Extra HTTP headers of every model call. Defaults to none. */
  readonly headers?: Readonly<Record<string, string | undefined>>;
}

/** Session options of {@link createVercelAiAgentSessionFlow}; tool policies live in the agent definition. */
export type VercelAiAgentSessionOptions = Omit<AgentSessionOptions, "toolPolicies" | "defaultToolEffect">;

/**
 * Creates a durable Dex agent session Flow that runs a Vercel AI SDK agent.
 *
 * One Flow execution is one agent session, and its Flow ID is the session ID. The session runs in
 * Step mode. Each `RunModel` Step makes one AI SDK model call with `streamText`, with the session
 * history as `messages`, and streams text and reasoning deltas to the session's `AgentEvents` Stream.
 * Every tool reaches the model without `execute`, so the call stops at the tool calls. Each call then
 * runs the tool's `execute` in its own `CallTool` or `CallSingleAttemptTool` Step, after
 * `AwaitToolApproval` when required, in parallel with its siblings. The next `RunModel` adds the
 * recorded results as AI SDK tool results and makes the next model call. A crash reruns only the Step
 * that was running.
 *
 * Construct the Flow once per agent at process startup, in every Worker and Client process, and pass
 * the same object to the Registry and to Client calls. The RPCs, persistence, and events are those of
 * `@superdurable/dex-agents`; see its README.
 *
 * @example
 * ```ts
 * import { Client, Registry, Worker, openBlobCache } from "@superdurable/dex";
 * import { createVercelAiAgentSessionFlow } from "@superdurable/dex-agents-vercel-ai";
 *
 * const flow = createVercelAiAgentSessionFlow(supportAgent, "SupportAgent", {
 *   interruptDeadlineMs: 24 * 60 * 60 * 1000,
 * });
 * const registry = new Registry([flow]);
 * const blobCache = openBlobCache({ directory: ".dex-cache", maxBytes: 64 * 1024 * 1024 });
 * const worker = new Worker(registry, blobCache, { serverAddress: "127.0.0.1:8801" });
 * await worker.start();
 * await worker.waitPullRegistered({ timeoutMs: 10_000 });
 * const client = new Client(registry, blobCache, { serverAddress: "127.0.0.1:8801" });
 * await client.startFlow(flow, "session-42", { initialMessage: "Refund order 42" });
 * ```
 * @typeParam TOOLS - The agent's tool set.
 * @param agent - Model, instructions, tools, and tool policies.
 * @param flowType - Flow type, unique within the Registry, such as `"SupportAgent"`. Keep it stable:
 * running sessions resume by it.
 * @param options - Event Stream capacity and approval deadline. Defaults to the core defaults.
 * @returns A new Flow to register in one Registry.
 * @throws {TypeError} When the model is missing, a tool has no `execute` function, a tool uses a
 * `needsApproval` function, a policy names an unknown tool, or the core rejects the Flow type or a policy.
 * @throws {RangeError} When `eventStreamCapacityBytes` or `interruptDeadlineMs` is not positive.
 */
export function createVercelAiAgentSessionFlow<TOOLS extends ToolSet>(
  agent: VercelAiAgentDefinition<TOOLS>,
  flowType: string,
  options: VercelAiAgentSessionOptions = {},
): AgentSessionFlow {
  const resolvedAgent = resolveVercelAiAgentDefinition(agent);
  return createAgentSessionFlow(new VercelAiAgentRunner(resolvedAgent), flowType, {
    ...options,
    toolPolicies: resolvedAgent.toolPolicies,
    ...(agent.defaultToolEffect === undefined ? {} : { defaultToolEffect: agent.defaultToolEffect }),
  });
}

function resolveVercelAiAgentDefinition<TOOLS extends ToolSet>(
  agent: VercelAiAgentDefinition<TOOLS>,
): ResolvedVercelAiAgentDefinition {
  if (agent?.model === undefined || agent.model === null) {
    throw new TypeError("createVercelAiAgentSessionFlow requires an agent with a model");
  }
  const tools: Record<string, Tool> = {};
  for (const [toolName, tool] of Object.entries((agent.tools ?? {}) as Record<string, Tool>)) {
    requireDexCompatibleTool(toolName, tool);
    tools[toolName] = tool;
  }
  const toolPolicies: Record<string, ToolPolicy> = {};
  for (const [toolName, policy] of Object.entries(agent.toolPolicies ?? {}) as [string, ToolPolicy | undefined][]) {
    if (policy === undefined) {
      continue;
    }
    const tool = Object.hasOwn(tools, toolName) ? tools[toolName] : undefined;
    if (tool === undefined) {
      throw new TypeError(`toolPolicies names ${toolName}, which is not one of the agent's tools`);
    }
    if (isProviderExecutedTool(tool)) {
      throw new TypeError(`tool ${toolName} is provider-executed, so a Dex tool policy cannot apply to it`);
    }
    toolPolicies[toolName] = policy;
  }
  const stopWhen = agent.stopWhen ?? isStepCount(defaultVercelAiTurnStepCount);
  return {
    model: agent.model,
    instructions: agent.instructions,
    tools,
    toolPolicies,
    shouldTrustMcpToolAnnotations: agent.shouldTrustMcpToolAnnotations ?? false,
    stopConditions: (Array.isArray(stopWhen) ? [...stopWhen] : [stopWhen]) as StopCondition<ToolSet>[],
    streamTextSettings: {
      toolChoice: agent.toolChoice as ToolChoice<ToolSet> | undefined,
      providerOptions: agent.providerOptions,
      maxRetries: agent.maxRetries,
      headers: agent.headers,
      callSettings: { ...agent.callSettings },
    },
  };
}

function requireDexCompatibleTool(toolName: string, tool: Tool): void {
  if (typeof tool !== "object" || tool === null) {
    throw new TypeError(`tool ${toolName} is not an AI SDK tool`);
  }
  if (typeof tool.needsApproval === "function") {
    throw new TypeError(
      `tool ${toolName} uses a needsApproval function; set requiresApproval in its Dex tool policy instead`,
    );
  }
  if (!isProviderExecutedTool(tool) && typeof tool.execute !== "function") {
    throw new TypeError(`tool ${toolName} has no execute function; a Dex agent session runs every tool itself`);
  }
}
