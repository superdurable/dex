// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  createAgentSessionFlow,
  type AgentSessionFlow,
  type AgentSessionOptions,
  type ToolPolicy,
} from "@superdurable/dex-agents";

import {
  ClaudeAgentRunner,
  type ClaudeAgentOptionsFactory,
  type ClaudeAgentQueryOptions,
} from "./claude-agent-runner.js";
import type { ClaudeCustomTool } from "./claude-tool-call-gate.js";

/** One Claude agent: its Claude Agent SDK options, custom tools, and tool policies. */
export interface ClaudeAgentDefinition {
  /**
   * The Claude Agent SDK options of every turn segment, or a factory that returns them per segment.
   * Use a factory to derive per-session values such as `cwd` from the Flow ID. See
   * {@link ClaudeAgentQueryOptions} for the options the adapter owns.
   */
  readonly options: ClaudeAgentQueryOptions | ClaudeAgentOptionsFactory;
  /**
   * Custom tools created with `tool()` from `@anthropic-ai/claude-agent-sdk`. The adapter serves them
   * from an in-process MCP server and records every call that does not run inline. Defaults to none.
   */
  readonly customTools?: readonly ClaudeCustomTool[];
  /**
   * Policies by tool name: a custom tool's own name such as `"create_refund"`, a built-in name such as
   * `"Bash"`, or `"mcp__<server>__<tool>"` for a tool of an external MCP server. A policy overrides MCP
   * annotations and the session's `defaultToolEffect`. For built-in and external MCP tools only the
   * approval requirement matters, because they always run inside the Claude Code CLI. Defaults to none.
   */
  readonly toolPolicies?: Readonly<Record<string, ToolPolicy>>;
  /**
   * Name of the in-process MCP server for custom tools. Defaults to `"tools"`, so the model sees a
   * custom tool `create_refund` as `mcp__tools__create_refund`.
   */
  readonly customToolServerName?: string;
}

/** Session options of {@link createClaudeAgentSessionFlow}; tool policies belong to the agent definition. */
export type ClaudeAgentSessionOptions = Omit<AgentSessionOptions, "toolPolicies">;

/**
 * Creates the durable agent session Flow for one Claude Agent SDK agent.
 *
 * Call it once per agent at application startup and pass the same Flow to the Registry and every
 * Client call. One Flow execution is one session, and the Flow ID is the session ID. The Claude Agent
 * SDK is a whole-turn harness: a Claude Code CLI process runs the turn inside a `RunModel` Step until it
 * defers a tool call or answers. The session stores the CLI's transcript and resumes the Claude
 * session from it on the next turn, after a deferred call, on a retried attempt, or on another Worker.
 *
 * Tool policies decide where each tool call runs. Custom tools that only read run inside the turn.
 * Every other custom tool call runs in its own Dex Step, exactly once for single-attempt effects, after
 * approval when its policy requires it. Tools that run inside the Claude Code CLI, such as `Bash`,
 * `Write`, and `Edit`, run inside the turn and wait for approval first when their policy requires it.
 * The default effect, `unclassified`, requires approval for every tool without a policy or MCP
 * annotation, built-in tools included.
 *
 * @example
 * ```ts
 * import { tool } from "@anthropic-ai/claude-agent-sdk";
 * import { z } from "zod";
 *
 * const lookupOrder = tool("lookup_order", "Look up an order", { orderId: z.string() },
 *   async (args) => ({ content: [{ type: "text", text: await orders.describe(args.orderId) }] }),
 *   { annotations: { readOnlyHint: true } });
 * const createRefund = tool("create_refund", "Refund an order", { orderId: z.string() },
 *   async (args) => ({ content: [{ type: "text", text: await payments.refund(args.orderId) }] }));
 *
 * const flow = createClaudeAgentSessionFlow(
 *   {
 *     options: (context) => ({
 *       systemPrompt: "You resolve order problems.",
 *       tools: ["Read", "Grep"],
 *       cwd: `/srv/agents/${context.flowId}`,
 *       settingSources: [],
 *     }),
 *     customTools: [lookupOrder, createRefund],
 *     toolPolicies: {
 *       create_refund: { effect: "non_idempotent_write" },
 *       Read: { effect: "read" },
 *       Grep: { effect: "read" },
 *     },
 *   },
 *   "SupportAgent",
 *   { interruptDeadlineMs: 24 * 60 * 60 * 1000 },
 * );
 * const registry = new Registry([flow]);
 * ```
 * @param agent - Options, custom tools, and tool policies of the agent.
 * @param flowType - Flow type, unique within the Registry, such as `"SupportAgent"`. Keep it stable:
 * running sessions resume by it.
 * @param sessionOptions - Default tool effect, Stream capacity, and approval deadline. Defaults to all
 * defaults.
 * @returns A new Flow that runs the agent with a {@link ClaudeAgentRunner}.
 * @throws {TypeError} When the custom tool server name is invalid, two custom tools share a name, the
 * Flow type is blank, or an effect is unknown.
 * @throws {RangeError} When `eventStreamCapacityBytes` or `interruptDeadlineMs` is not positive.
 */
export function createClaudeAgentSessionFlow(
  agent: ClaudeAgentDefinition,
  flowType: string,
  sessionOptions: ClaudeAgentSessionOptions = {},
): AgentSessionFlow {
  const runner = new ClaudeAgentRunner(agent.options, {
    ...(agent.customTools === undefined ? {} : { customTools: agent.customTools }),
    ...(agent.customToolServerName === undefined
      ? {}
      : { customToolServerName: agent.customToolServerName }),
  });
  return createAgentSessionFlow(runner, flowType, {
    ...sessionOptions,
    ...(agent.toolPolicies === undefined ? {} : { toolPolicies: agent.toolPolicies }),
  });
}
