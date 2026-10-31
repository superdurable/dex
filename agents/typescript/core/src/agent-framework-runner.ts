// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type {
  AgentMessage,
  AgentToolCall,
  AgentToolCallStatus,
  AgentUsage,
  FrameworkMessage,
  ToolEffect,
} from "./agent-session-values.js";

/**
 * Per-tool override of the effect and approval defaults.
 *
 * Pass policies by tool name in {@link AgentSessionFlowOptions.toolPolicies}. A policy wins over
 * framework metadata such as MCP annotations and over {@link AgentSessionFlowOptions.defaultToolEffect}.
 *
 * @example
 * ```ts
 * const toolPolicies = {
 *   search_orders: { effect: "read" },
 *   refund_order: { effect: "idempotent_write", requiresApproval: true },
 * } satisfies Record<string, ToolPolicy>;
 * ```
 */
export interface ToolPolicy {
  /** Effect of the tool. */
  readonly effect: ToolEffect;
  /**
   * Whether every call waits for `resolve_interrupt` before it runs. When omitted, calls with effect
   * `non_idempotent_write` or `unclassified` require approval and the others do not.
   */
  readonly requiresApproval?: boolean;
}

/** Effective policy of one tool, returned by {@link AgentTurnContext.resolveToolPolicy}. */
export interface ResolvedToolPolicy {
  /** Effective effect after policies, framework metadata, and the session default. */
  readonly effect: ToolEffect;
  /** Effective approval requirement. */
  readonly requiresApproval: boolean;
  /**
   * Whether the runner must defer calls of this tool instead of running them inside `runTurn`.
   * It is `true` when the effect is not `read` or the tool requires approval.
   */
  readonly isDeferred: boolean;
}

/**
 * Writes best-effort live events to the session's `AgentEvents` Stream.
 *
 * Every event carries the current turn ID and Step attempt. Consecutive `text_delta` or
 * `reasoning_delta` writes are coalesced for up to 100 milliseconds or 1,024 characters before they
 * reach the Stream. Writes after the handler returns are dropped. Stream delivery is best effort: a
 * retried turn writes its events again under a new attempt, so a UI rebuilds durable state with
 * `get_agent_snapshot`.
 */
export interface AgentEventWriter {
  /**
   * Writes visible assistant text as a `text_delta` event.
   * @param text - Text chunk; empty chunks are ignored.
   */
  writeTextDelta(text: string): void;
  /**
   * Writes a provider-authored reasoning summary chunk as a `reasoning_delta` event.
   * Never label ordinary text or hidden chain of thought as reasoning.
   * @param text - Text chunk; empty chunks are ignored.
   */
  writeReasoningDelta(text: string): void;
  /**
   * Writes a `tool_call_started` event for a tool the runner runs inside `runTurn`.
   * @param toolCallId - Framework tool call ID.
   * @param toolName - Tool name.
   */
  writeToolCallStarted(toolCallId: string, toolName: string): void;
  /**
   * Writes a `tool_call_finished` event for a tool the runner ran inside `runTurn`.
   * @param toolCallId - Framework tool call ID.
   * @param toolName - Tool name.
   * @param status - `succeeded` or `failed` for an inline call.
   */
  writeToolCallFinished(toolCallId: string, toolName: string, status: AgentToolCallStatus): void;
}

/**
 * Everything a runner needs to run or resume one turn segment inside `RunAgentTurn`.
 *
 * A turn segment starts when the session begins a turn or resumes it after deferred tool calls, and
 * ends when the runner returns an {@link AgentTurnOutcome}. A crash or thrown error reruns the
 * segment from the same context on a later attempt, so model calls and `read` tools may run again.
 * Do not retain the context after `runTurn` settles.
 */
export interface AgentTurnContext {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** Turn ID, `turn-<n>`. */
  readonly turnId: string;
  /** One-based `RunAgentTurn` attempt; a value above 1 means the segment is being rerun. */
  readonly attempt: number;
  /**
   * Every stored session message in ascending sequence order, including the user message that
   * started this turn. Replay each `frameworkItem` to the framework unchanged.
   */
  readonly messages: readonly AgentMessage[];
  /**
   * Framework run state from the previous `TurnSuspended` outcome of this turn, or `null` when the
   * turn starts fresh.
   */
  readonly frameworkRunState: string | null;
  /**
   * Deferred tool call records of this turn, ordered by tool call ID. When resuming, every record is
   * final; return its recorded result to the framework instead of calling the tool again. Use
   * {@link describeToolCallResultForModel} when the framework needs result text.
   */
  readonly toolCalls: readonly AgentToolCall[];
  /** Events writer for this segment. */
  readonly events: AgentEventWriter;
  /** Signal aborted when Dex cancels the attempt; pass it to model and tool calls. */
  readonly cancellationSignal: AbortSignal;
  /** Absolute deadline of the current attempt, or `undefined` when Dex supplied none. */
  readonly deadline: Date | undefined;
  /**
   * Returns the effective policy of a tool.
   *
   * Precedence: the tool's {@link ToolPolicy}, then `frameworkEffect`, then the session's default
   * tool effect. Pass `frameworkEffect` when the framework declares one, for example with
   * {@link toolEffectFromMcpToolAnnotations}.
   *
   * @param toolName - Tool name as the model calls it.
   * @param frameworkEffect - Effect declared by framework metadata, or `undefined`.
   * @returns The effective policy.
   */
  resolveToolPolicy(toolName: string, frameworkEffect?: ToolEffect): ResolvedToolPolicy;
  /**
   * Records a heartbeat for the current attempt.
   *
   * The session already records one every 30 seconds while `runTurn` is pending, so call this only
   * for extra liveness reporting.
   *
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  recordHeartbeat(): Promise<void>;
}

/** One tool call that the runner deferred, returned in {@link TurnSuspended}. */
export interface DeferredToolCall {
  /** Framework tool call ID; must be non-empty, slash-free, and unique within the turn. */
  readonly toolCallId: string;
  /** Tool name. */
  readonly toolName: string;
  /** Tool arguments as JSON text. */
  readonly arguments: string;
  /** Effect from {@link AgentTurnContext.resolveToolPolicy}. */
  readonly effect: ToolEffect;
  /** Approval requirement from {@link AgentTurnContext.resolveToolPolicy}. */
  readonly requiresApproval: boolean;
}

/** Outcome of a turn segment that produced the final answer. */
export interface TurnCompleted {
  /** Discriminator. */
  readonly kind: "completed";
  /**
   * Every message the turn produced after its user message, across all of its segments, in order.
   * The session appends them to `AgentMessages` and deletes the turn's tool call records.
   */
  readonly newMessages: readonly FrameworkMessage[];
  /** Usage of this segment only; the session adds it to the cumulative usage. */
  readonly usage: AgentUsage;
}

/** Outcome of a turn segment that stopped at deferred tool calls. */
export interface TurnSuspended {
  /** Discriminator. */
  readonly kind: "suspended";
  /**
   * Serialized framework state from which a later segment resumes the turn, including the messages
   * this turn has produced so far. The session stores it opaquely in `FrameworkRunState`.
   */
  readonly frameworkRunState: string;
  /**
   * New deferred calls, at least one. Do not repeat a call that already has a record in
   * {@link AgentTurnContext.toolCalls}; the session rejects that as a runner defect.
   */
  readonly deferredToolCalls: readonly DeferredToolCall[];
  /** Usage of this segment only. */
  readonly usage: AgentUsage;
}

/** Result of {@link AgentFrameworkRunner.runTurn}. */
export type AgentTurnOutcome = TurnCompleted | TurnSuspended;

/** One deferred tool call that a tool Step asks the runner to run once. */
export interface DeferredToolExecution {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** Turn that deferred the call. */
  readonly turnId: string;
  /** Framework tool call ID. */
  readonly toolCallId: string;
  /** Tool name. */
  readonly toolName: string;
  /** Tool arguments as JSON text. */
  readonly arguments: string;
  /** Recorded effect; it selected `ExecuteToolCall` or `ExecuteSingleAttemptToolCall`. */
  readonly effect: ToolEffect;
  /**
   * Stable key `<flowId>/<toolCallId>`, identical across attempts. Pass it to services that
   * deduplicate requests; `ExecuteToolCall` retries an `idempotent_write` call with the same key.
   */
  readonly idempotencyKey: string;
  /** One-based attempt of the tool Step. */
  readonly attempt: number;
  /** Events writer of the tool Step. */
  readonly events: AgentEventWriter;
  /** Signal aborted when Dex cancels the attempt. */
  readonly cancellationSignal: AbortSignal;
  /** Absolute deadline of the current attempt, or `undefined`. */
  readonly deadline: Date | undefined;
  /**
   * Records a heartbeat. The session already records one every 30 seconds.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  recordHeartbeat(): Promise<void>;
}

/** Successful result of one deferred tool call. */
export interface ToolExecutionResult {
  /** Output text recorded on the call and shown to the model. */
  readonly output: string;
}

/**
 * Tool failure that the model should see, thrown from {@link AgentFrameworkRunner.executeDeferredToolCall}.
 *
 * The session records the call as `failed` with this error's message and does not retry it. Throw any
 * other error for an unexpected failure: `ExecuteToolCall` retries it, and `ExecuteSingleAttemptToolCall`
 * records `outcome_unknown` because the effect may have happened.
 *
 * @example
 * ```ts
 * if (response.status === 404) {
 *   throw new ToolExecutionError(`order ${orderId} does not exist`);
 * }
 * ```
 */
export class ToolExecutionError extends Error {
  /**
   * Creates a tool failure.
   * @param message - Error text recorded on the call and shown to the model.
   * @param options - Standard error options, such as `cause`.
   */
  public constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "ToolExecutionError";
  }
}

/**
 * Framework-specific logic that the shared agent session Flow calls.
 *
 * An adapter package implements one runner per framework and passes it to
 * {@link createAgentSessionFlow}. The Flow owns durability; the runner only translates between
 * the framework and the session values.
 *
 * Contract:
 * - `runTurn` runs the framework loop. Model calls and `read` tools run inside it. Every tool whose
 *   {@link ResolvedToolPolicy.isDeferred} is `true` must be deferred with the framework's own
 *   mechanism and returned in {@link TurnSuspended}; it must never run inside `runTurn`.
 * - When `frameworkRunState` is set, `runTurn` restores the framework and resumes it. Every deferred
 *   call then has a final record in `toolCalls`, and the framework's tool wrapper returns that
 *   recorded result instead of calling the tool.
 * - `runTurn` and `executeDeferredToolCall` must not keep state between calls; a later call may run in
 *   another Worker process.
 * - Throwing from `runTurn` retries the segment, up to 3 attempts within 30 minutes; then the turn
 *   fails, the session records `last_turn_error`, and it waits for the next user message.
 *
 * @example
 * ```ts
 * class EchoRunner implements AgentFrameworkRunner {
 *   public buildUserMessage(text: string): FrameworkMessage {
 *     return { role: "user", text, toolCallId: null, toolName: null, toolArguments: null,
 *       toolOutput: null, frameworkItem: JSON.stringify({ role: "user", content: text }) };
 *   }
 *   public async runTurn(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
 *     const last = turn.messages.at(-1)?.text ?? "";
 *     turn.events.writeTextDelta(last);
 *     return { kind: "completed", usage: emptyAgentUsage(), newMessages: [{ role: "assistant", text: last,
 *       toolCallId: null, toolName: null, toolArguments: null, toolOutput: null,
 *       frameworkItem: JSON.stringify({ role: "assistant", content: last }) }] };
 *   }
 *   public async executeDeferredToolCall(): Promise<ToolExecutionResult> {
 *     throw new ToolExecutionError("this agent has no tools");
 *   }
 * }
 * ```
 */
export interface AgentFrameworkRunner {
  /**
   * Converts user text into the framework's user message.
   *
   * The session calls it when it consumes a user message and stores the result before `runTurn`.
   * It must be synchronous, deterministic, and free of side effects.
   *
   * @param text - User message text, never blank.
   * @returns The framework message, with `role` `"user"`.
   */
  buildUserMessage(text: string): FrameworkMessage;
  /**
   * Runs or resumes one turn segment.
   *
   * @param turn - Turn context; see {@link AgentTurnContext}.
   * @returns {@link TurnCompleted} with every new message of the turn, or {@link TurnSuspended}
   * with the framework run state and the new deferred calls.
   * @throws Any error to retry the segment; after the last attempt the turn fails.
   */
  runTurn(turn: AgentTurnContext): Promise<AgentTurnOutcome>;
  /**
   * Runs the real tool for one deferred call exactly once per attempt.
   *
   * @param call - Deferred call to run.
   * @returns The tool output text.
   * @throws {ToolExecutionError} For a failure the model should see; the call is recorded as `failed`.
   * @throws Any other error for an unexpected failure; see {@link ToolExecutionError} for retry rules.
   */
  executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult>;
}

/** MCP tool annotations that classify a tool's side effects. */
export interface McpToolAnnotations {
  /** MCP `readOnlyHint`: the tool does not modify its environment. */
  readonly readOnlyHint?: boolean;
  /** MCP `idempotentHint`: repeated calls with the same arguments have no additional effect. */
  readonly idempotentHint?: boolean;
  /** MCP `destructiveHint`: the tool may perform destructive updates. */
  readonly destructiveHint?: boolean;
}

/**
 * Maps MCP tool annotations to a framework effect for {@link AgentTurnContext.resolveToolPolicy}.
 *
 * Annotations are hints from the tool server. Pass the result only for servers the application
 * trusts; otherwise leave the effect to policies and the session default.
 *
 * @example
 * ```ts
 * const policy = turn.resolveToolPolicy(tool.name, toolEffectFromMcpToolAnnotations(tool.annotations ?? {}));
 * ```
 * @param annotations - The tool's MCP annotations.
 * @returns `read` when `readOnlyHint` is `true`; `idempotent_write` when `idempotentHint` is `true`
 * and `destructiveHint` is not `true`; otherwise `undefined`, so the session default applies.
 */
export function toolEffectFromMcpToolAnnotations(
  annotations: McpToolAnnotations,
): ToolEffect | undefined {
  if (annotations.readOnlyHint === true) {
    return "read";
  }
  if (annotations.idempotentHint === true && annotations.destructiveHint !== true) {
    return "idempotent_write";
  }
  return undefined;
}
