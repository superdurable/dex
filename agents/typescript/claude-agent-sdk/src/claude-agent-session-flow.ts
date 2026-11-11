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
  type MediaStore,
  type ToolPolicy,
} from "@superdurable/dex-agents";

import type { ToolSource } from "@superdurable/dex-agents/stepwise";

import {
  ClaudeAgentRunner,
  type ClaudeAgentOptionsFactory,
  type ClaudeAgentQueryOptions,
  type ClaudeAgentRunnerOptions,
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
  /**
   * Tool sources, such as `ConnectorToolSource` from `@superdurable/dex-agents/connectors`, served from the custom
   * tool server. Every call of a source tool runs in its own tool Step, with the effect, approval, timeout, and
   * retry policy its source gives it; a `toolPolicies` entry for a connector tool changes only its approval. See
   * {@link ClaudeAgentRunnerOptions.toolSources}, which also has an example. Defaults to none.
   *
   * @example
   * ```ts
   * const flow = createClaudeAgentSessionFlow(
   *   { options: buildOptions, toolSources: [new ConnectorToolSource([refundTool])] },
   *   "SupportAgent",
   * );
   * ```
   * @since 0.4.0
   */
  readonly toolSources?: readonly ToolSource[];
  /**
   * Milliseconds one `RunModel` attempt waits for each tool source's `listTools`. Defaults to 10 seconds.
   * @since 0.4.0
   */
  readonly toolSourceListingTimeoutMs?: number;
  /**
   * The store that holds the session's images, such as `S3MediaStore`; pass the store your clients upload to, on
   * every Worker. With a store, the session accepts user messages with images, and the model sees them and tool
   * result images as Claude `image` blocks; the session stores only references. Omit it to refuse images. See
   * {@link ClaudeAgentRunnerOptions.mediaStore}, which also has an example.
   *
   * @example
   * ```ts
   * const flow = createClaudeAgentSessionFlow({ options: buildOptions, mediaStore: media }, "SupportAgent");
   * ```
   * @since 0.5.0
   */
  readonly mediaStore?: MediaStore;
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
 * Tool sources add tools that always run in their own tool Step, reads included. `ConnectorToolSource` offers
 * reviewed Connector operations this way: each call gets the operation's effect class, approval, Call ID, timeout,
 * and retry policy from its manifest, as in a Stepwise agent.
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
 * Flow type is blank, or an effect is unknown; when a tool source is invalid or two share a `sourceName`; or when
 * a source's `declaredToolNames` repeat a custom tool, a Claude Code tool, or another source's tool.
 * @throws {RangeError} When `eventStreamCapacityBytes`, `interruptDeadlineMs`, or `toolSourceListingTimeoutMs` is
 * not positive.
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
    ...(agent.toolSources === undefined ? {} : { toolSources: agent.toolSources }),
    ...(agent.toolSourceListingTimeoutMs === undefined
      ? {}
      : { toolSourceListingTimeoutMs: agent.toolSourceListingTimeoutMs }),
    ...(agent.mediaStore === undefined ? {} : { mediaStore: agent.mediaStore }),
  });
  return createAgentSessionFlow(runner, flowType, {
    ...sessionOptions,
    ...(agent.toolPolicies === undefined ? {} : { toolPolicies: agent.toolPolicies }),
  });
}
