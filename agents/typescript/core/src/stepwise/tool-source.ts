// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type {
  AgentTurnContext,
  DeferredToolCall,
  DeferredToolExecution,
  DeferredToolOutcome,
  ToolCallStepOptions,
  ToolTaskCancellation,
  ToolTaskPoll,
  ToolTaskPollOutcome,
} from "../agent-framework-runner.js";
import type { AgentToolCall } from "../agent-session-values.js";

/**
 * One tool that a {@link ToolSource} lists, as the model sees it. `McpTool` satisfies it; the runner reads only
 * these three fields.
 */
export interface SourceToolDefinition {
  /**
   * Tool name the model calls. It must not repeat a function tool, an enabled built-in tool, or another
   * source's tool.
   */
  readonly name: string;
  /** Tool description for the model. */
  readonly description: string;
  /** JSON Schema of the tool's arguments object. */
  readonly inputSchema: Readonly<Record<string, unknown>>;
}

/**
 * Tools that a Stepwise agent lists at run time, such as an MCP server's tools, run one tool Step per call.
 *
 * Put a source in `AgentDefinition.toolSources`. `McpToolSource` from `@superdurable/dex-agents/mcp`
 * implements it. In every `RunModel` of a chat or executing turn, the runner awaits {@link listTools} and
 * offers the listed tools after the function tools and before the built-in tools, waiting at most
 * `AgentDefinition.toolSourceListingTimeoutMs`. A source whose listing throws or takes longer offers no tools in
 * that model call, so a down server never fails a turn; cache the listing per Worker and keep it cheap, as
 * `McpToolSource` does. A planning turn and a turn past its model call limit list nothing.
 *
 * When the model calls a listed tool, the runner returns the source's {@link deferredToolCall}, so the source
 * picks the policy, the effect, and the `toolSourceReference`. The runner passes the tool definition it offered as
 * `listedTool`, so a refresh during the model call cannot change it. The runner stores the source's
 * {@link sourceName} with that reference, so the tool Step and `PollToolTask` reach the same source, with the
 * source's own reference, on any Worker, without a listing, and whatever the order of `toolSources`. Keep
 * `sourceName` stable across deploys: a stored call whose source name no Worker has throws a `RangeError`, so its
 * effect decides its outcome. The call then runs in its own tool Step through {@link executeDeferredToolCall},
 * after an approval when its policy needs one, and a tool task that it starts is polled through
 * {@link pollToolTask}. A name that no listing offered fails as an unknown tool without an approval; it never
 * reaches a source.
 *
 * @example
 * ```ts
 * const tools = new McpToolSource([{ name: "files", transport: "stdio", command: "mcp-files" }]);
 * const flow = createStepwiseAgentSessionFlow(
 *   { name: "files", instructions: "You manage reports.", model, toolSources: [tools] },
 *   "FilesAgent",
 * );
 * ```
 */
export interface ToolSource {
  /** The source's stable, non-blank name, unique within one agent definition. Stored calls are routed by it. */
  readonly sourceName: string;
  /**
   * Returns the tools to offer now, in the order the model sees them.
   * @returns The tools.
   * @throws Any error, which fails the `RunModel` attempt so that it retries.
   */
  listTools(): Promise<readonly SourceToolDefinition[]>;
  /**
   * Builds the deferred call for one model call of a listed tool. Resolve the policy with
   * `turn.resolveToolPolicy`, so session policies win.
   * @param turn - The `RunModel` context.
   * @param call - Model tool call ID, the listed tool name the model called, the arguments as JSON text, and
   * `listedTool`, the definition this source listed for this model call. Build from it, not from a cache a refresh
   * may change.
   * @returns The deferred call.
   */
  deferredToolCall(
    turn: AgentTurnContext,
    call: {
      readonly toolCallId: string;
      readonly toolName: string;
      readonly arguments: string;
      readonly listedTool: SourceToolDefinition;
    },
  ): DeferredToolCall;
  /**
   * Runs one call of a listed tool in its tool Step.
   * @param call - The approved call.
   * @returns The tool's outcome.
   * @throws {ToolExecutionError} For a failure the model sees.
   */
  executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome>;
  /**
   * Polls a tool task that {@link executeDeferredToolCall} started, once.
   * @param poll - The call's record.
   * @returns The outcome of this poll.
   */
  pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome>;
  /**
   * Optional: names this source always lists, whatever its servers offer, so the runner refuses a collision when
   * it is constructed instead of on every model call. `McpToolSource` returns its resource and prompt tool names.
   * @returns The names.
   */
  declaredToolNames?(): ReadonlySet<string>;
  /**
   * Optional: the last listing without I/O, so `estimateInputTokens` counts the source's tools before the runner
   * has listed them on this Worker.
   * @returns The cached tools, or none.
   */
  cachedTools?(): readonly SourceToolDefinition[];
  /**
   * Optional: cancels a task that `cancel_turn` abandoned, as {@link AgentFrameworkRunner.cancelToolTask}
   * describes. A source without it leaves the task running. `McpToolSource` sends `tasks/cancel`.
   * @param cancellation - The abandoned call's record.
   * @returns A Promise that resolves once the remote system took the cancellation.
   */
  cancelToolTask?(cancellation: ToolTaskCancellation): Promise<void>;
  /**
   * Optional: the timeout and retry policy of one call's tool Step, or `undefined` for the defaults, as
   * {@link AgentFrameworkRunner.toolCallStepOptions} describes. `ConnectorToolSource` uses it for each
   * operation's timeout and retry policy.
   * @param toolCall - The record whose tool Step starts, with this source's own reference.
   * @returns The options, or `undefined`.
   * @since 0.3.0
   */
  toolCallStepOptions?(toolCall: AgentToolCall): ToolCallStepOptions | undefined;
}
