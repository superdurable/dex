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
 * - `running_model`: `RunModel` makes the turn's next model call.
 * - `running_tools`: the requested tool calls run in parallel Steps, some possibly waiting for
 *   approval, and `ResumeTurn` waits for their results.
 */
export type AgentSessionStatus = "waiting_for_user_message" | "running_model" | "running_tools";

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
 * - `read`: runs in `CallTool`, up to 3 attempts; no approval by default.
 * - `idempotent_write`: runs in `CallTool`, up to 3 attempts with one idempotency key; when they run
 *   out, the session records `outcome_unknown`.
 * - `non_idempotent_write`: runs once in `CallSingleAttemptTool`; a crash records `outcome_unknown`.
 * - `unclassified`: treated like `non_idempotent_write`; the default for tools without a policy.
 */
export type ToolEffect = "read" | "idempotent_write" | "non_idempotent_write" | "unclassified";

/**
 * Status of one deferred tool call record, as a runner sees it.
 *
 * `awaiting_approval` and `approved` are open. The other statuses are final: the session
 * never runs a call whose record is final, and the runner reports the result to the model.
 * `failed` means the tool threw {@link ToolExecutionError}, or a `read` call ended without a result after
 * its attempts ran out. `outcome_unknown` means a write's tool Step ended without a result, through a
 * Worker crash, a timeout, or another error after its attempts ran out.
 *
 * The set of final statuses never grows, so an exhaustive mapping of final statuses stays complete.
 * A stored record can also hold an {@link AgentToolCallWaitingStatus}; runners never see one.
 */
export type AgentToolCallStatus =
  | "awaiting_approval"
  | "approved"
  | "rejected"
  | "succeeded"
  | "failed"
  | "outcome_unknown";

/**
 * Non-final status of a stored record while a session Step waits on the call's behalf.
 *
 * - `awaiting_answer`: `AwaitQuestionAnswer` waits for `answer_question`. The call is an `ask_user`
 *   call, or a tool call whose tool returned {@link ToolInputRequested}.
 * - `waiting_for_timer`: `AwaitTimer` waits until {@link AgentToolCallRecord.waitUntil} for a
 *   `durable_wait` call.
 *
 * These statuses appear in {@link AgentSnapshot.toolCalls} and in records read with
 * `Client.getAttributes`. A runner never receives a record with one of them: `RunModel` runs only after
 * every record of the turn is final, and a tool Step runs only an `approved` record.
 */
export type AgentToolCallWaitingStatus = "awaiting_answer" | "waiting_for_timer";

/** Status of a stored tool call record: an {@link AgentToolCallStatus} or an {@link AgentToolCallWaitingStatus}. */
export type AgentToolCallRecordStatus = AgentToolCallStatus | AgentToolCallWaitingStatus;

/**
 * One answer to a question that a tool asked by returning {@link ToolInputRequested}.
 *
 * `AwaitQuestionAnswer` appends one to the call's `inputAnswers` when the user answers. The tool then
 * runs again and receives every answer in {@link DeferredToolExecution.inputAnswers}.
 */
export interface QuestionAnswer {
  /** Interrupt ID of the answered question, `<toolCallId>-input-<n>`. Wire field: `interrupt_id`. */
  readonly interruptId: string;
  /** Question text the tool asked. Wire field: `question`. */
  readonly question: string;
  /** The user's answer, exactly as sent to `answer_question`. Wire field: `answer`. */
  readonly answer: string;
}

/** Durable record of one deferred tool call, as a runner sees it; stored in `AgentToolCalls` under its tool call ID. */
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
  /**
   * The `ToolExecutionError` message, or the failed Step's recovery detail, after `failed` or
   * `outcome_unknown`; otherwise `null`. Wire field: `error`.
   */
  readonly error: string | null;
  /** Rejection text after `rejected`, otherwise `null`. Wire field: `rejection_message`. */
  readonly rejectionMessage: string | null;
  /**
   * Zero-based order in which the turn deferred the call; approved calls run in this order.
   * It extends the planned contract, as in the Python core. Wire field: `deferral_order`.
   */
  readonly deferralOrder: number;
  /**
   * {@link DeferredToolCall.toolSourceReference}: opaque text the tool's source stored with the call,
   * absent when it stored none. Wire field: `tool_source_reference`, `null` when absent.
   */
  readonly toolSourceReference?: string;
  /**
   * W3C `traceparent` of the activation root that the call's `execute_tool` spans join; absent when
   * none recorded. The session sets it when `RunModel` defers the call and again when an approval
   * starts a new activation, so parallel activations never swap tool spans. Wire field:
   * `trace_context`, `null` when absent.
   */
  readonly traceContext?: string;
  /**
   * RFC 3339 time at which a `durable_wait` call's Timer ends, otherwise `null`. Wire field: `wait_until`.
   *
   * Records the session reads always carry this field. It is optional only so that code that builds
   * records by hand, such as a unit test, keeps compiling.
   */
  readonly waitUntil?: string | null;
  /**
   * Opaque text from the tool's last {@link ToolInputRequested}, returned to the tool when it runs
   * again after the answer; `null` when the tool gave none. Wire field: `input_continuation`.
   * Optional for the same reason as {@link waitUntil}.
   */
  readonly inputContinuation?: string | null;
  /**
   * Answers to the questions the tool asked through {@link ToolInputRequested}, oldest first; empty
   * for other calls. Wire field: `input_answers`. Optional for the same reason as {@link waitUntil}.
   */
  readonly inputAnswers?: readonly QuestionAnswer[];
  /**
   * Question text of the tool's pending {@link ToolInputRequested} while the record is
   * `awaiting_answer`, otherwise `null`. It is `null` for an `ask_user` call, whose question is in
   * {@link arguments}. `AwaitQuestionAnswer` raises the question interrupt from it under the
   * `PendingInterrupts` lock. Wire field: `input_question`. Optional for the same reason as
   * {@link waitUntil}.
   */
  readonly inputQuestion?: string | null;
  /**
   * Choices of that pending {@link ToolInputRequested}; empty for a free-text question or when no tool
   * question is pending. Wire field: `input_choices`. Optional for the same reason as {@link waitUntil}.
   */
  readonly inputChoices?: readonly string[];
  /**
   * Whether that pending {@link ToolInputRequested} accepts free text; `true` when no tool question is
   * pending. Wire field: `input_allows_free_text`. Optional for the same reason as {@link waitUntil}.
   */
  readonly inputAllowsFreeText?: boolean;
}

/**
 * A stored tool call record, as `get_agent_snapshot` and `Client.getAttributes` return it.
 *
 * It is an {@link AgentToolCall} whose status may also be an {@link AgentToolCallWaitingStatus}, and
 * whose fields added in 0.2.0 are always present.
 */
export interface AgentToolCallRecord
  extends Omit<
    AgentToolCall,
    "status" | "waitUntil" | "inputContinuation" | "inputAnswers" | "inputQuestion" | "inputChoices" | "inputAllowsFreeText"
  > {
  /** Current status. Wire field: `status`. */
  readonly status: AgentToolCallRecordStatus;
  /** See {@link AgentToolCall.waitUntil}. Wire field: `wait_until`. */
  readonly waitUntil: string | null;
  /** See {@link AgentToolCall.inputContinuation}. Wire field: `input_continuation`. */
  readonly inputContinuation: string | null;
  /** See {@link AgentToolCall.inputAnswers}. Wire field: `input_answers`. */
  readonly inputAnswers: readonly QuestionAnswer[];
  /** See {@link AgentToolCall.inputQuestion}. Wire field: `input_question`. */
  readonly inputQuestion: string | null;
  /** See {@link AgentToolCall.inputChoices}. Wire field: `input_choices`. */
  readonly inputChoices: readonly string[];
  /** See {@link AgentToolCall.inputAllowsFreeText}. Wire field: `input_allows_free_text`. */
  readonly inputAllowsFreeText: boolean;
}

/**
 * Kind of one {@link AgentInterrupt}.
 *
 * - `tool_approval`: a tool call waits for approval. Answer it with `resolve_interrupt`.
 * - `question`: the model, through `ask_user`, or a tool, through {@link ToolInputRequested}, asked the
 *   user a question. Answer it with `answer_question`, or decline it with `resolve_interrupt` and
 *   `approved` `false`.
 */
export type AgentInterruptKind = "tool_approval" | "question";

/** One pending approval or question, stored in the `PendingInterrupts` Attribute. */
export interface AgentInterrupt {
  /**
   * Interrupt ID passed to `resolve_interrupt` or `answer_question`. It equals the tool call ID for a
   * tool approval and an `ask_user` question, and is `<toolCallId>-input-<n>` for the `n`-th question a
   * tool asked through {@link ToolInputRequested}. Wire field: `interrupt_id`.
   */
  readonly interruptId: string;
  /** Interrupt kind. Wire field: `kind`. */
  readonly kind: AgentInterruptKind;
  /** Tool call that waits. Wire field: `tool_call_id`. */
  readonly toolCallId: string;
  /** Tool name; `ask_user` for an `ask_user` question. Wire field: `tool_name`. */
  readonly toolName: string;
  /** Tool arguments as JSON text. Wire field: `arguments`. */
  readonly arguments: string;
  /** RFC 3339 time at which the interrupt was raised. Wire field: `created_at`. */
  readonly createdAt: string;
  /**
   * RFC 3339 deadline, or `null` for no deadline. After it, the session rejects the call with
   * {@link approvalDeadlinePassed} or {@link questionDeadlinePassed}. Wire field: `deadline`.
   */
  readonly deadline: string | null;
  /**
   * Question text of a `question` interrupt, otherwise `null`. Wire field: `question`.
   *
   * Interrupts the session reads always carry this field and the two below. They are optional only so
   * that code written against 0.1.0, which builds interrupts by hand, keeps compiling; treat `undefined`
   * as `null`, an empty list, and `true`.
   */
  readonly question?: string | null;
  /** Answers to offer for a `question` interrupt; empty for a free-text question. Wire field: `choices`. */
  readonly choices?: readonly string[];
  /**
   * Whether `answer_question` accepts an answer that is not one of {@link choices}. Always `true` when
   * `choices` is empty and for a tool approval. Wire field: `allows_free_text`.
   */
  readonly allowsFreeText?: boolean;
}

/** Resolution of one interrupt, published to the `InterruptResolutions` ChannelMap. */
export interface AgentInterruptResolution {
  /**
   * Whether the tool call may run, or, for a question, whether the user answered it. Wire field:
   * `approved`.
   */
  readonly approved: boolean;
  /** Text shown to the model when rejected or declined, or `null`. Wire field: `rejection_message`. */
  readonly rejectionMessage: string | null;
  /**
   * The answer to a `question` interrupt, otherwise `null`. Wire field: `answer`. Resolutions the
   * session reads always carry it; it is optional so that 0.1.0 code keeps compiling.
   */
  readonly answer?: string | null;
}

/**
 * Model usage counters, stored cumulatively in the `AgentUsage` Attribute.
 *
 * Runners report the usage of one model call in an {@link AgentTurnOutcome}; the session adds it.
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

/**
 * Notice that one tool call of the current batch reached a final record, published to the internal
 * `ToolCallResults` Channel. `ResumeTurn` waits for one per requested tool call; the result itself is
 * in the call's `AgentToolCalls` record.
 */
export interface ToolCallCompleted {
  /** Tool call whose record is final. Wire field: `tool_call_id`. */
  readonly toolCallId: string;
}

/** One user message, the input of the `send_user_message` RPC and the `QueuedUserMessages` Channel value. */
export interface AgentUserMessage {
  /** Message text. Blank text is rejected. Wire field: `text`. */
  readonly text: string;
}

/**
 * Standard type of one {@link AgentEvent}. Adapters may write other types with an adapter prefix.
 *
 * Payloads are JSON objects with snake_case keys, serialized with sorted keys:
 * - `turn_started`: `{"sequence"}`, the sequence of the user message that started the turn.
 * - `turn_completed`: `{"message_count"}`, the number of messages the turn appended.
 * - `turn_failed`: `{"error"}`.
 * - `text_delta` and `reasoning_delta`: `{"text"}`.
 * - `tool_call_started`: `{"tool_call_id","tool_name"}`.
 * - `tool_call_finished`: `{"tool_call_id","tool_name","status"}`, when a record becomes final,
 *   including a rejection.
 * - `tool_call_deferred`: `{"tool_call_id","tool_name","effect","requires_approval"}`, when the model
 *   asks for a call, which then runs in its own Step or, for a built-in tool, waits in the session.
 * - `interrupt_raised`: `{"interrupt_id","kind","tool_call_id","tool_name","deadline","question",
 *   "choices","allows_free_text"}`. For a tool approval, `question` is `null`, `choices` is empty, and
 *   `allows_free_text` is `true`.
 * - `interrupt_resolved`: `{"interrupt_id","kind","approved","answered","rejection_message",
 *   "cancellation_reason"}`. `answered` is `true` only for an answered question. `cancellation_reason`
 *   is always `null` in this release.
 * - `timer_started`: `{"tool_call_id","wait_until","reason"}`, when a `durable_wait` call starts its Timer.
 * - `usage_recorded`: `{"recorded","session"}`: one model call's and the cumulative usage, each with
 *   {@link AgentUsage} wire fields.
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
  | "timer_started"
  | "usage_recorded";

/** One best-effort live event, written to the `AgentEvents` Stream. */
export interface AgentEvent {
  /** An {@link AgentEventType} or an adapter-specific type such as `"vercel_ai.step"`. Wire field: `type`. */
  readonly type: string;
  /** Turn the event belongs to, or `null`. Wire field: `turn_id`. */
  readonly turnId: string | null;
  /** One-based attempt of the Step that wrote the event. Wire field: `attempt`. */
  readonly attempt: number;
  /**
   * Dex Step execution ID of the Step that wrote the event; all attempts of one Step execution share
   * it. Events written before this field existed decode with `""`. Wire field: `step_execution_id`.
   *
   * Group live text by `(turnId, stepExecutionId, attempt)`. Within one `stepExecutionId`, keep only the
   * events of the highest `attempt` seen and drop the partial text of lower attempts. Different
   * `stepExecutionId` values of one turn are its model calls and tool Steps, and each keeps its
   * own events. After `turn_completed` or `turn_failed`, replace the turn's live text with the stored
   * messages.
   */
  readonly stepExecutionId: string;
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

/**
 * Input of the `answer_question` RPC.
 *
 * @example
 * ```ts
 * const question = snapshot.pendingInterrupts.find((interrupt) => interrupt.kind === "question");
 * if (question !== undefined) {
 *   await client.invokeRPC(flow.answerQuestion, sessionId, {
 *     interruptId: question.interruptId, answer: question.choices[0] ?? "yes" });
 * }
 * ```
 */
export interface AnswerQuestionRequest {
  /** {@link AgentInterrupt.interruptId} of a pending `question` interrupt. Wire field: `interrupt_id`. */
  readonly interruptId: string;
  /**
   * The answer. It must not be blank. When the interrupt has `choices` and `allowsFreeText` is `false`,
   * it must equal one of the choices exactly. Wire field: `answer`.
   */
  readonly answer: string;
}

/** Output of the `get_agent_snapshot` RPC, enough for a UI to rebuild its view. */
export interface AgentSnapshot {
  /** Current session state, or `null` before `StartAgentSession` committed. Wire field: `state`. */
  readonly state: AgentState | null;
  /** Up to the last 200 messages in ascending sequence order. Wire field: `messages`. */
  readonly messages: readonly AgentMessage[];
  /** Interrupts that `resolve_interrupt` still accepts. Wire field: `pending_interrupts`. */
  readonly pendingInterrupts: readonly AgentInterrupt[];
  /** Deferred tool call records of the active turn, in deferral order. Wire field: `tool_calls`. */
  readonly toolCalls: readonly AgentToolCallRecord[];
  /** Cumulative usage. Wire field: `usage`. */
  readonly usage: AgentUsage;
  /** User messages queued and not yet consumed. Wire field: `queued_message_count`. */
  readonly queuedMessageCount: number;
}

/** Input of the `list_agent_messages` RPC. */
export interface ListAgentMessagesInput {
  /** Return messages with a smaller sequence, or `null` for the newest. Wire field: `before_sequence`. */
  readonly beforeSequence: number | null;
  /** Page size; the session clamps it to 1..200. A missing wire value means 50. Wire field: `limit`. */
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
 * `awaiting_approval`, `approved`, and every {@link AgentToolCallWaitingStatus}.
 */
export function isFinalToolCallStatus(status: AgentToolCallRecordStatus): boolean {
  return (
    status === "rejected" || status === "succeeded" || status === "failed" || status === "outcome_unknown"
  );
}

/** Tool result text the model sees for an `outcome_unknown` call; the same text as the Python core. */
export const outcomeUnknownToolResult =
  "The tool call's outcome is unknown: it stopped without returning a result, so it may " +
  "or may not have taken effect. It will not be retried automatically.";

/** First line of the note `RecordTurnFailure` stores after a failed turn ran tools. */
export const turnFailureNoteHeader = "[Dex] The previous turn failed after these tool calls ran:";

/** Last line of that note. */
export const turnFailureNoteFooter = "Do not repeat these tool calls unless the user asks again.";

/** Tool result text the model sees for a rejection without a message; the same text as the Python core. */
export const defaultRejectionToolResult = "The user rejected this tool call.";

/** Rejection message recorded when an approval deadline passes. */
export const approvalDeadlinePassed = "approval deadline passed";

/** Rejection message recorded when a question's deadline passes. */
export const questionDeadlinePassed = "question deadline passed";

/** Tool result text the model sees when the user declines a question without a message. */
export const defaultQuestionDeclinedToolResult = "The user declined to answer.";

/**
 * Returns the note text that tells the model what a failed turn already did.
 *
 * `RecordTurnFailure` stores this note through {@link AgentFrameworkRunner.buildUserMessage} when a
 * failed turn has final tool call records, as the failed turn's last message, so the next turn's model
 * does not repeat a write. The text matches the Python core's `build_turn_failure_note`:
 *
 * ```text
 * [Dex] The previous turn failed after these tool calls ran:
 * - charge {"amount": 5} -> succeeded: charged 5
 * Do not repeat these tool calls unless the user asks again.
 * ```
 *
 * @param toolCalls - Final records of the failed turn; they are listed in `deferralOrder`.
 * @returns {@link turnFailureNoteHeader}, one `- <toolName> <arguments> -> <status>: <outcome>` line
 * per record, with the outcome from {@link describeToolCallOutcome}, and {@link turnFailureNoteFooter},
 * joined with newlines.
 * @throws {TypeError} When a record is not final.
 */
export function buildTurnFailureNote(toolCalls: readonly (AgentToolCall | AgentToolCallRecord)[]): string {
  const lines = [turnFailureNoteHeader];
  for (const toolCall of [...toolCalls].sort((left, right) => left.deferralOrder - right.deferralOrder)) {
    const outcome = describeToolCallOutcome(toolCall);
    lines.push(`- ${toolCall.toolName} ${toolCall.arguments} -> ${toolCall.status}: ${outcome}`);
  }
  lines.push(turnFailureNoteFooter);
  return lines.join("\n");
}

/**
 * Returns the tool result text a runner shows the model for a final tool call record.
 *
 * Use it when resuming a framework whose tool result must be text. A runner whose framework has a
 * native rejection, such as a rejection message argument, may pass `rejectionMessage` there instead.
 * The texts match the Python core's `describe_tool_call_outcome`.
 *
 * @example
 * ```ts
 * const record = turn.recordedToolCall(pendingCallId);
 * if (record !== undefined && isFinalToolCallStatus(record.status)) {
 *   frameworkToolResult = describeToolCallOutcome(record);
 * }
 * ```
 * @param toolCall - Record whose status is final.
 * @returns The output for `succeeded`; `The tool call failed: <error>` for `failed`; the rejection
 * message, or {@link defaultRejectionToolResult}, for `rejected`; {@link outcomeUnknownToolResult}
 * for `outcome_unknown`.
 * @throws {TypeError} When the record status is not final.
 */
export function describeToolCallOutcome(toolCall: AgentToolCall | AgentToolCallRecord): string {
  switch (toolCall.status) {
    case "succeeded":
      return toolCall.output ?? "";
    case "failed":
      return `The tool call failed: ${toolCall.error ?? "unknown error"}`;
    case "rejected":
      return toolCall.rejectionMessage ?? defaultRejectionToolResult;
    case "outcome_unknown":
      return outcomeUnknownToolResult;
    case "awaiting_approval":
    case "approved":
    case "awaiting_answer":
    case "waiting_for_timer":
      throw new TypeError(`tool call ${toolCall.toolCallId} has no final outcome yet`);
  }
}
