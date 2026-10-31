// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * Status of an agent session, stored in the `status` field of {@link AgentState}.
 *
 * - `waiting_for_user_message`: the session is idle in `AwaitUserMessage`.
 * - `running_turn`: `RunAgentTurn` runs or resumes the framework loop.
 * - `waiting_for_interrupt`: `AwaitInterruptResolution` waits for tool approvals.
 * - `executing_tool`: a tool Step runs one deferred tool call.
 */
export type AgentSessionStatus =
  | "waiting_for_user_message"
  | "running_turn"
  | "waiting_for_interrupt"
  | "executing_tool";

/**
 * Input of the `StartAgentSession` Step, passed to `Client.startFlow`.
 *
 * @example
 * ```ts
 * await client.startFlow(flow, "session-42", { initialMessage: "Summarize my inbox" });
 * ```
 */
export interface AgentSessionStartInput {
  /**
   * First user message, or `null` to start idle in `AwaitUserMessage`.
   * Blank text is treated as `null`. Wire field: `initial_message`.
   */
  readonly initialMessage: string | null;
}

/** Small durable summary of an agent session, stored in the `AgentState` Attribute. */
export interface AgentState {
  /** Current session status. Wire field: `status`. */
  readonly status: AgentSessionStatus;
  /** Number of completed or failed turns. Wire field: `turn_count`. */
  readonly turnCount: number;
  /** ID of the running turn, `turn-<n>`, or `null` when idle. Wire field: `active_turn_id`. */
  readonly activeTurnId: string | null;
  /** Sequence assigned to the next stored message; starts at 1. Wire field: `next_message_sequence`. */
  readonly nextMessageSequence: number;
  /** Tool call that the next tool Step runs, or `null`. Wire field: `executing_tool_call_id`. */
  readonly executingToolCallId: string | null;
  /** Error text of the most recent failed turn, or `null`. Wire field: `last_turn_error`. */
  readonly lastTurnError: string | null;
}

/** Author of one stored agent message. */
export type AgentMessageRole = "user" | "assistant" | "tool";

/**
 * Framework-neutral projection of one framework message, produced by an {@link AgentFrameworkRunner}.
 *
 * The session stores it as an {@link AgentMessage} after adding a sequence, turn ID, and time.
 * Only `frameworkItem` is replayed to the framework; the other fields are display projections.
 */
export interface FrameworkMessage {
  /** Author of the message. */
  readonly role: AgentMessageRole;
  /** Display text, or `null` for a message without text, such as a bare tool call. */
  readonly text: string | null;
  /** Tool call ID for a tool call or tool result message, otherwise `null`. */
  readonly toolCallId: string | null;
  /** Tool name for a tool call or tool result message, otherwise `null`. */
  readonly toolName: string | null;
  /** Tool call arguments as JSON text, otherwise `null`. */
  readonly toolArguments: string | null;
  /** Tool result text, otherwise `null`. */
  readonly toolOutput: string | null;
  /** Framework-native JSON for this message; the session replays it to the framework unchanged. */
  readonly frameworkItem: string;
}

/** One message stored in the `AgentMessages` AttributeMap under its zero-padded sequence. */
export interface AgentMessage extends FrameworkMessage {
  /** Monotonic session-wide sequence, starting at 1. Wire field: `sequence`. */
  readonly sequence: number;
  /** Turn that produced the message. Wire field: `turn_id`. */
  readonly turnId: string;
  /** RFC 3339 time at which the session stored the message. Wire field: `created_at`. */
  readonly createdAt: string;
}

/**
 * Side-effect class of a tool, which decides where and how often the session runs it.
 *
 * - `read`: runs inside `RunAgentTurn` and reruns with the turn segment.
 * - `idempotent_write`: runs in `ExecuteToolCall`, up to 3 attempts with one idempotency key.
 * - `non_idempotent_write`: runs once in `ExecuteSingleAttemptToolCall`; a crash records `outcome_unknown`.
 * - `unclassified`: treated like `non_idempotent_write`; the default for tools without a policy.
 */
export type ToolEffect = "read" | "idempotent_write" | "non_idempotent_write" | "unclassified";

/**
 * Status of one deferred tool call record.
 *
 * `awaiting_approval` and `approved` are open. The other statuses are final: the session
 * never runs a call whose record is final, and the runner reports the result to the model.
 */
export type AgentToolCallStatus =
  | "awaiting_approval"
  | "approved"
  | "rejected"
  | "succeeded"
  | "failed"
  | "outcome_unknown";

/** Durable record of one deferred tool call, stored in `AgentToolCalls` under its tool call ID. */
export interface AgentToolCall {
  /** Framework tool call ID; non-empty and slash-free. Wire field: `tool_call_id`. */
  readonly toolCallId: string;
  /** Turn that deferred the call. Wire field: `turn_id`. */
  readonly turnId: string;
  /** Tool name. Wire field: `tool_name`. */
  readonly toolName: string;
  /** Tool arguments as JSON text. Wire field: `arguments`. */
  readonly arguments: string;
  /** Effect that selected the tool Step. Wire field: `effect`. */
  readonly effect: ToolEffect;
  /** Whether the call waited for `resolve_interrupt`. Wire field: `requires_approval`. */
  readonly requiresApproval: boolean;
  /** Current status. Wire field: `status`. */
  readonly status: AgentToolCallStatus;
  /** Tool output text after `succeeded`, otherwise `null`. Wire field: `output`. */
  readonly output: string | null;
  /** Error text after `failed` or `outcome_unknown`, otherwise `null`. Wire field: `error`. */
  readonly error: string | null;
  /** Rejection text after `rejected`, otherwise `null`. Wire field: `rejection_message`. */
  readonly rejectionMessage: string | null;
}

/** One pending approval request, stored in the `PendingInterrupts` Attribute. */
export interface AgentInterrupt {
  /** Interrupt ID; equals the tool call ID for tool approvals. Wire field: `interrupt_id`. */
  readonly interruptId: string;
  /** Interrupt kind. Wire field: `kind`. */
  readonly kind: "tool_approval";
  /** Tool call awaiting approval. Wire field: `tool_call_id`. */
  readonly toolCallId: string;
  /** Tool name. Wire field: `tool_name`. */
  readonly toolName: string;
  /** Tool arguments as JSON text. Wire field: `arguments`. */
  readonly arguments: string;
  /** RFC 3339 time at which the interrupt was raised. Wire field: `created_at`. */
  readonly createdAt: string;
  /** RFC 3339 approval deadline, or `null` for no deadline. Wire field: `deadline`. */
  readonly deadline: string | null;
}

/** Resolution of one interrupt, published to the `InterruptResolutions` ChannelMap. */
export interface AgentInterruptResolution {
  /** Whether the tool call may run. Wire field: `approved`. */
  readonly approved: boolean;
  /** Text shown to the model when rejected, or `null`. Wire field: `rejection_message`. */
  readonly rejectionMessage: string | null;
}

/**
 * Model usage counters, stored cumulatively in the `AgentUsage` Attribute.
 *
 * Runners report the usage of one turn segment in an {@link AgentTurnOutcome}; the session adds it.
 * Use {@link emptyAgentUsage} and {@link addAgentUsage} to accumulate per-model-call usage.
 */
export interface AgentUsage {
  /** Number of model calls. Wire field: `model_calls`. */
  readonly modelCalls: number;
  /** Input tokens, including cached input tokens. Wire field: `input_tokens`. */
  readonly inputTokens: number;
  /** Output tokens, including reasoning tokens when the provider counts them so. Wire field: `output_tokens`. */
  readonly outputTokens: number;
  /** Input tokens served from a provider cache. Wire field: `cached_input_tokens`. */
  readonly cachedInputTokens: number;
  /** Reasoning tokens reported by the provider. Wire field: `reasoning_tokens`. */
  readonly reasoningTokens: number;
}

/** One user message, the input of the `send_user_message` RPC and the `QueuedUserMessages` Channel value. */
export interface AgentUserMessage {
  /** Message text. Blank text is rejected. Wire field: `text`. */
  readonly text: string;
}

/**
 * Type of one {@link AgentEvent}.
 *
 * Payloads, as JSON text:
 * - `turn_started`: `{}`.
 * - `turn_completed`: `{"new_message_count"}`.
 * - `turn_failed`: `{"error"}`.
 * - `text_delta` and `reasoning_delta`: `{"text"}`.
 * - `tool_call_started`: `{"tool_call_id","tool_name"}`.
 * - `tool_call_finished`: `{"tool_call_id","tool_name","status"}`.
 * - `tool_call_deferred`: `{"tool_call_id","tool_name","effect","requires_approval"}`.
 * - `interrupt_raised`: `{"interrupt_id","tool_call_id","tool_name","deadline"}`.
 * - `interrupt_resolved`: `{"interrupt_id","approved","rejection_message"}`.
 * - `usage_recorded`: the segment's usage with {@link AgentUsage} wire fields.
 */
export type AgentEventType =
  | "turn_started"
  | "turn_completed"
  | "turn_failed"
  | "text_delta"
  | "reasoning_delta"
  | "tool_call_started"
  | "tool_call_finished"
  | "tool_call_deferred"
  | "interrupt_raised"
  | "interrupt_resolved"
  | "usage_recorded";

/** One best-effort live event, written to the `AgentEvents` Stream. */
export interface AgentEvent {
  /** Event type. Wire field: `type`. */
  readonly type: AgentEventType;
  /** Turn the event belongs to, or `null`. Wire field: `turn_id`. */
  readonly turnId: string | null;
  /**
   * One-based attempt of the Step that wrote the event. Wire field: `attempt`.
   * A UI drops partial text of an earlier attempt when a later attempt of the same turn appears.
   */
  readonly attempt: number;
  /** Event payload as JSON text. Wire field: `payload`. */
  readonly payload: string;
}

/** Input of the `resolve_interrupt` RPC. */
export interface ResolveInterruptInput {
  /** Pending interrupt to resolve. Wire field: `interrupt_id`. */
  readonly interruptId: string;
  /** Whether the tool call may run. Wire field: `approved`. */
  readonly approved: boolean;
  /** Text shown to the model when rejected, or `null`. Wire field: `rejection_message`. */
  readonly rejectionMessage: string | null;
}

/** Output of the `get_agent_snapshot` RPC, enough for a UI to rebuild its view. */
export interface AgentSnapshot {
  /** Current session state. Wire field: `state`. */
  readonly state: AgentState;
  /** Up to the last 200 messages in ascending sequence order. Wire field: `messages`. */
  readonly messages: readonly AgentMessage[];
  /** Interrupts that `resolve_interrupt` still accepts. Wire field: `pending_interrupts`. */
  readonly pendingInterrupts: readonly AgentInterrupt[];
  /** Deferred tool call records of the active turn, ordered by tool call ID. Wire field: `tool_calls`. */
  readonly toolCalls: readonly AgentToolCall[];
  /** Cumulative usage. Wire field: `usage`. */
  readonly usage: AgentUsage;
  /** User messages queued and not yet consumed. Wire field: `queued_message_count`. */
  readonly queuedMessageCount: number;
}

/** Input of the `list_agent_messages` RPC. */
export interface ListAgentMessagesInput {
  /** Return messages with a smaller sequence, or `null` for the newest. Wire field: `before_sequence`. */
  readonly beforeSequence: number | null;
  /** Page size; the session clamps it to 1..200. Wire field: `limit`. */
  readonly limit: number;
}

/** Output of the `list_agent_messages` RPC. */
export interface AgentMessagesPage {
  /** Messages in ascending sequence order. Wire field: `messages`. */
  readonly messages: readonly AgentMessage[];
  /**
   * Value to pass as `beforeSequence` for the next older page, or `null` when no older message exists.
   * Wire field: `next_before_sequence`.
   */
  readonly nextBeforeSequence: number | null;
}

/**
 * Returns usage with every counter at zero.
 *
 * @returns A new {@link AgentUsage} whose counters are all `0`.
 */
export function emptyAgentUsage(): AgentUsage {
  return {
    modelCalls: 0,
    inputTokens: 0,
    outputTokens: 0,
    cachedInputTokens: 0,
    reasoningTokens: 0,
  };
}

/**
 * Adds two usage values counter by counter.
 *
 * @example
 * ```ts
 * let usage = emptyAgentUsage();
 * usage = addAgentUsage(usage, { ...emptyAgentUsage(), modelCalls: 1, inputTokens: 120, outputTokens: 30 });
 * ```
 * @param left - First usage value.
 * @param right - Second usage value.
 * @returns A new {@link AgentUsage} holding the sums; neither input is changed.
 */
export function addAgentUsage(left: AgentUsage, right: AgentUsage): AgentUsage {
  return {
    modelCalls: left.modelCalls + right.modelCalls,
    inputTokens: left.inputTokens + right.inputTokens,
    outputTokens: left.outputTokens + right.outputTokens,
    cachedInputTokens: left.cachedInputTokens + right.cachedInputTokens,
    reasoningTokens: left.reasoningTokens + right.reasoningTokens,
  };
}

/**
 * Reports whether a tool call status is final.
 *
 * @param status - Status to classify.
 * @returns `true` for `rejected`, `succeeded`, `failed`, and `outcome_unknown`; `false` for
 * `awaiting_approval` and `approved`.
 */
export function isFinalToolCallStatus(status: AgentToolCallStatus): boolean {
  return status !== "awaiting_approval" && status !== "approved";
}

/**
 * Returns the tool result text a runner shows the model for a final tool call record.
 *
 * Use it when resuming a framework whose tool result must be text. A runner whose framework has a
 * native rejection, such as a rejection message argument, may use `rejectionMessage` directly instead.
 *
 * @example
 * ```ts
 * const record = turn.toolCalls.find((call) => call.toolCallId === pendingCallId);
 * if (record !== undefined && isFinalToolCallStatus(record.status)) {
 *   frameworkToolResult = describeToolCallResultForModel(record);
 * }
 * ```
 * @param toolCall - Record whose status is final.
 * @returns The output for `succeeded`; otherwise one sentence naming the failure, rejection, or
 * unknown outcome, followed by the recorded error or rejection text.
 * @throws {TypeError} When the record status is `awaiting_approval` or `approved`.
 */
export function describeToolCallResultForModel(toolCall: AgentToolCall): string {
  switch (toolCall.status) {
    case "succeeded":
      return toolCall.output ?? "";
    case "failed":
      return `The tool call failed: ${toolCall.error ?? "no error text was recorded"}`;
    case "rejected":
      return `The user rejected the tool call: ${toolCall.rejectionMessage ?? "no reason was given"}`;
    case "outcome_unknown":
      return (
        "The tool call outcome is unknown: the Worker stopped while the tool was running, " +
        "so the call may or may not have taken effect. Do not assume it succeeded. " +
        (toolCall.error ?? "")
      ).trimEnd();
    case "awaiting_approval":
    case "approved":
      throw new TypeError(`tool call ${toolCall.toolCallId} is not final: ${toolCall.status}`);
  }
}
