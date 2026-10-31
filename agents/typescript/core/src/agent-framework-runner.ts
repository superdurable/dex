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
 * Pass policies by tool name in {@link AgentSessionOptions.toolPolicies}. A policy wins over
 * framework metadata such as MCP annotations and over {@link AgentSessionOptions.defaultToolEffect}.
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

/** Effective policy of one tool, returned by {@link resolveToolPolicy}. */
export interface ResolvedToolPolicy {
  /** Effective effect after policies, framework metadata, and the session default. */
  readonly effect: ToolEffect;
  /** Effective approval requirement. */
  readonly requiresApproval: boolean;
  /**
   * Whether the runner must defer calls of this tool instead of running them inside `runTurn`.
   * It is `false` only for a `read` tool that needs no approval.
   */
  readonly shouldDefer: boolean;
}

/**
 * Returns the effective policy of one tool.
 *
 * Runners normally call {@link AgentTurnContext.resolveToolPolicy}. Call this function when an adapter
 * classifies tools outside a turn, for example while building framework tool wrappers. Precedence: the
 * tool's {@link ToolPolicy}, then `frameworkEffect`, then `defaultToolEffect`.
 *
 * @example
 * ```ts
 * const policy = resolveToolPolicy("refund_order", flow.options.toolPolicies, flow.options.defaultToolEffect);
 * if (policy.shouldDefer) {
 *   // wrap the tool so the framework defers it
 * }
 * ```
 * @param toolName - Tool name as the model calls it.
 * @param toolPolicies - Explicit policies by tool name.
 * @param defaultToolEffect - Effect of a tool without a policy or framework metadata.
 * @param frameworkEffect - Effect declared by framework metadata, or `undefined`.
 * @returns The effective policy.
 */
export function resolveToolPolicy(
  toolName: string,
  toolPolicies: Readonly<Record<string, ToolPolicy>>,
  defaultToolEffect: ToolEffect,
  frameworkEffect?: ToolEffect,
): ResolvedToolPolicy {
  const policy = Object.hasOwn(toolPolicies, toolName) ? toolPolicies[toolName] : undefined;
  const effect = policy?.effect ?? frameworkEffect ?? defaultToolEffect;
  const requiresApproval =
    policy?.requiresApproval ?? (effect === "non_idempotent_write" || effect === "unclassified");
  return { effect, requiresApproval, shouldDefer: effect !== "read" || requiresApproval };
}

/**
 * Writes best-effort live events to the session's `AgentEvents` Stream.
 *
 * Every event carries {@link turnId} and the Step {@link attempt}. Payload objects are written as JSON
 * with sorted keys. Consecutive `text_delta` or `reasoning_delta` writes are coalesced for up to 100
 * milliseconds or 1,024 characters before they reach the Stream. Writes after the handler returns are
 * dropped. Stream delivery is best effort: a retried turn writes its events again under a new attempt,
 * so a UI rebuilds durable state with `get_agent_snapshot`.
 */
export interface AgentEventWriter {
  /** Turn stamped on every event, or `null` outside a turn. */
  readonly turnId: string | null;
  /** One-based attempt of the Step that owns this writer. */
  readonly attempt: number;
  /**
   * Writes one event with a JSON object payload.
   *
   * Prefer the typed methods for standard types. For an adapter-specific type, use an adapter prefix,
   * such as `"vercel_ai.step_finished"`, so it never collides with an {@link AgentEventType}.
   * @param type - Event type stored in `AgentEvent.type`.
   * @param payload - JSON-compatible object stored as `AgentEvent.payload` text.
   * @throws {TypeError} When the payload cannot be serialized as JSON.
   */
  writeEvent(type: string, payload: Readonly<Record<string, unknown>>): void;
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
   * Writes a `tool_call_started` event. The session writes it for deferred calls; a runner writes it
   * for `read` tools that run inside `runTurn`.
   * @param toolCallId - Framework tool call ID.
   * @param toolName - Tool name.
   */
  writeToolCallStarted(toolCallId: string, toolName: string): void;
  /**
   * Writes a `tool_call_finished` event.
   * @param toolCallId - Framework tool call ID.
   * @param toolName - Tool name.
   * @param status - `succeeded` or `failed` for an inline call, or the final status the session recorded.
   */
  writeToolCallFinished(toolCallId: string, toolName: string, status: AgentToolCallStatus): void;
}

/**
 * Everything a runner needs to run or resume one turn segment inside `RunAgentTurn`.
 *
 * A turn segment starts when the session begins a turn or resumes it after deferred tool calls, and
 * ends when the runner returns an {@link AgentTurnOutcome}. A crash or thrown error reruns the
 * segment from the same committed state, so model calls and `read` tools may run again. Do not
 * retain the context after `runTurn` settles.
 */
export interface AgentTurnContext {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** Turn ID, `turn-<n>`. */
  readonly turnId: string;
  /**
   * One-based `RunAgentTurn` attempt. A rerun after a Worker crash may keep the same attempt when the
   * crashed attempt sent no output, so do not rely on it to detect reruns.
   */
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
  /** Whether this segment resumes a suspended framework run, that is, `frameworkRunState` is set. */
  readonly isResuming: boolean;
  /**
   * Deferred tool call records of this turn, in deferral order. When resuming, every record is final;
   * return its recorded result to the framework instead of calling the tool again. Use
   * {@link describeToolCallOutcome} when the framework needs result text.
   */
  readonly toolCalls: readonly AgentToolCall[];
  /** Events writer bound to this turn and attempt. */
  readonly events: AgentEventWriter;
  /** Signal aborted when Dex cancels the attempt; pass it to model and tool calls. */
  readonly cancellationSignal: AbortSignal;
  /** Absolute deadline of the current attempt, or `undefined` when Dex supplied none. */
  readonly deadline: Date | undefined;
  /**
   * Returns the effective policy of a tool; see {@link resolveToolPolicy} for the precedence.
   * Run the call inside `runTurn` only when {@link ResolvedToolPolicy.shouldDefer} is `false`.
   * @param toolName - Tool name as the model calls it.
   * @param frameworkEffect - Effect declared by framework metadata, for example from
   * {@link toolEffectFromMcpToolAnnotations}, or `undefined`.
   * @returns The effective policy.
   */
  resolveToolPolicy(toolName: string, frameworkEffect?: ToolEffect): ResolvedToolPolicy;
  /**
   * Returns this turn's record for a tool call ID.
   * @param toolCallId - Framework tool call ID.
   * @returns The record, or `undefined` when the turn never deferred that call.
   */
  recordedToolCall(toolCallId: string): AgentToolCall | undefined;
  /**
   * Records a heartbeat for the current attempt.
   *
   * The session already records one every 20 seconds while `runTurn` is pending. Call it only from
   * a long synchronous loop that could otherwise starve the event loop.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  heartbeat(): Promise<void>;
}

/** One tool call that the runner deferred, returned in {@link TurnSuspended}. */
export interface DeferredToolCall {
  /** Framework tool call ID; must be non-empty, slash-free, and unique within the turn. */
  readonly toolCallId: string;
  /** Tool name. */
  readonly toolName: string;
  /** Tool arguments as JSON text. */
  readonly arguments: string;
  /** Policy from {@link AgentTurnContext.resolveToolPolicy}; it decides approval and the tool Step. */
  readonly policy: ResolvedToolPolicy;
}

/** Outcome of a turn segment that produced the final answer. */
export interface TurnCompleted {
  /** Discriminator. */
  readonly kind: "completed";
  /**
   * Every message the turn produced after its user message, across all of its segments, in order:
   * model output, tool requests, tool results including deferred ones, and the final answer. The
   * session appends them in one commit and deletes the turn's tool call records.
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
   * Non-empty serialized framework state from which a later segment resumes the turn, including the
   * messages this turn has produced so far. The session stores no partial messages; it keeps this
   * value opaquely in `FrameworkRunState`.
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

/** One approved deferred tool call that a tool Step asks the runner to run once. */
export interface DeferredToolExecution {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** The approved record; its `effect` selected `ExecuteToolCall` or `ExecuteSingleAttemptToolCall`. */
  readonly toolCall: AgentToolCall;
  /**
   * Stable key `<flowId>/<toolCallId>`, identical across attempts. Pass it to services that
   * deduplicate requests; `ExecuteToolCall` retries an `idempotent_write` call with the same key.
   */
  readonly idempotencyKey: string;
  /**
   * One-based attempt of the tool Step. `idempotent_write` and approved `read` calls get up to three
   * attempts; other calls get one.
   */
  readonly attempt: number;
  /** Events writer bound to the turn and attempt. */
  readonly events: AgentEventWriter;
  /** Signal aborted when Dex cancels the attempt. */
  readonly cancellationSignal: AbortSignal;
  /** Absolute deadline of the current attempt, or `undefined`. */
  readonly deadline: Date | undefined;
  /**
   * Records a heartbeat. The session already records one every 20 seconds.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  heartbeat(): Promise<void>;
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
 *   {@link ResolvedToolPolicy.shouldDefer} is `true` must be deferred with the framework's own
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
   * Runs the real tool for one approved deferred call.
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
