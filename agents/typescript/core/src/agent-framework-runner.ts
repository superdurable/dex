// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AsyncContext, RetryPolicy } from "@superdurable/dex";

import type { BuiltInTool } from "./agent-built-in-tools.js";
import type { ContextSummaryRequest, ContextSummaryResult } from "./agent-context-compaction.js";
import type {
  AgentImageReference,
  AgentMessage,
  AgentPlan,
  AgentToolCall,
  AgentToolCallRecord,
  AgentToolCallStatus,
  AgentUsage,
  ContextSummary,
  SessionSettings,
  FrameworkMessage,
  InteractionMode,
  QuestionAnswer,
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
 * const label = policy.requiresApproval ? "needs approval" : "runs without approval";
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
  return { effect, requiresApproval };
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
   * Writes a `tool_call_started` event. The session writes it for every tool Step; a runner writes it
   * only for a tool its framework cannot defer and runs inside `runModelStep`.
   * @param toolCallId - Framework tool call ID.
   * @param toolName - Tool name.
   */
  writeToolCallStarted(toolCallId: string, toolName: string): void;
  /**
   * Writes a `tool_call_finished` event.
   * @param toolCallId - Framework tool call ID.
   * @param toolName - Tool name.
   * @param status - `succeeded` or `failed` for a call the runner ran itself, or the final status the
   * session recorded.
   */
  writeToolCallFinished(toolCallId: string, toolName: string, status: AgentToolCallStatus): void;
}

/**
 * Everything a runner needs for one model call of a turn, inside the `RunModel` Step.
 *
 * Each `RunModel` Step makes at most one model call. The first `RunModel` of a turn starts from the
 * stored messages. Every later one resumes from {@link frameworkRunState} after the tool calls of the
 * previous model response reached final records. A crash or thrown error reruns only this model call
 * from the same committed state. Do not retain the context after `runModelStep` settles.
 */
export interface AgentTurnContext {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** Turn ID, `turn-<n>`. */
  readonly turnId: string;
  /**
   * One-based `RunModel` attempt. A rerun after a Worker crash gets the next attempt, but an attempt whose
   * stream half-closed before any output returns as not run and keeps its number, so do not rely on it to
   * detect reruns.
   */
  readonly attempt: number;
  /**
   * Dex Step execution ID of this `RunModel` Step, shared by all of its attempts.
   *
   * The session always sets this field. It is optional only so that code that builds a context by hand
   * keeps compiling.
   */
  readonly stepExecutionId?: string;
  /**
   * Every stored session message in ascending sequence order, including the user message that
   * started this turn. Replay each `frameworkItem` to the framework unchanged. After compaction, only the
   * messages after {@link contextSummary}.
   */
  readonly messages: readonly AgentMessage[];
  /**
   * Framework run state from this turn's previous {@link ToolCallsRequested} or {@link TurnContinued} outcome,
   * or `null` for the turn's first model call.
   */
  readonly frameworkRunState: string | null;
  /**
   * Whether this model call continues the turn after tool calls or after a {@link TurnContinued} outcome, that
   * is, `frameworkRunState` is set.
   */
  readonly isResuming: boolean;
  /**
   * Tool call records of this turn, in deferral order; every record is final. Give the framework the
   * recorded result of each call from the previous model response instead of calling the tool. Use
   * {@link describeToolCallOutcome} when the framework needs result text.
   */
  readonly toolCalls: readonly AgentToolCall[];
  /** Events writer bound to this turn and attempt. */
  readonly events: AgentEventWriter;
  /** Signal aborted when Dex cancels the attempt; pass it to the model call. */
  readonly cancellationSignal: AbortSignal;
  /** Absolute deadline of the current attempt, or `undefined` when Dex supplied none. */
  readonly deadline: Date | undefined;
  /**
   * Built-in tools enabled for this session through {@link AgentSessionOptions.builtInTools}. Offer
   * their definitions from {@link builtInToolDefinitions} to the model; the session handles their calls.
   *
   * The session always sets this field. It is optional only so that code that builds a context by hand,
   * such as a unit test, keeps compiling; treat `undefined` as no built-in tools.
   */
  readonly enabledBuiltInTools?: ReadonlySet<BuiltInTool>;
  /**
   * Explicit policies by tool name from {@link AgentSessionOptions.toolPolicies}, the same map
   * {@link resolveToolPolicy} applies first. A runner whose framework sets its own approval default, such as
   * a tool's `requiresApproval`, applies that default only to tools without an explicit policy here.
   * Optional for the same reason as {@link enabledBuiltInTools}; treat `undefined` as no policies.
   */
  readonly toolPolicies?: Readonly<Record<string, ToolPolicy>>;
  /**
   * {@link AgentSessionOptions.defaultToolEffect}, the effect of a tool without a policy or framework
   * metadata. Optional for the same reason as {@link enabledBuiltInTools}.
   */
  readonly defaultToolEffect?: ToolEffect;
  /**
   * The turn's {@link AgentState.interactionMode}. A runner with {@link AgentFrameworkRunner.supportsPlanMode}
   * offers only `write_todos` in a `planning` turn. The session always sets it; treat `undefined` as `chat`.
   */
  readonly interactionMode?: InteractionMode;
  /**
   * The {@link AgentPlan} as committed before this model call, or `null` before `write_todos` first
   * succeeded. Show it to the model with {@link buildPlanNote}. The session always sets it; treat
   * `undefined` as `null`.
   */
  readonly plan?: AgentPlan | null;
  /**
   * The summary of the messages before {@link messages}, or `null`. Only a runner that implements the
   * compaction methods, such as {@link AgentFrameworkRunner.summarizeContext}, receives one; put its text
   * before {@link messages} in the model input. Optional for the same reason as {@link enabledBuiltInTools};
   * treat `undefined` as no summary.
   */
  readonly contextSummary?: ContextSummary | null;
  /**
   * The {@link SessionSettings} this turn started with, such as `{ model: "gpt-5-mini" }`. Use them to choose
   * the model or the instructions of each model call. `update_session_settings` changes them from the next turn
   * on, so every model call of one turn sees the same value, retries included. Empty when the session has no
   * settings, such as a session started on dex-agents 0.1.0. The session always sets it; it is optional for the
   * same reason as {@link enabledBuiltInTools}, so treat `undefined` as `{}`.
   */
  readonly sessionSettings?: SessionSettings;
  /**
   * Returns the effective policy of a tool; see {@link resolveToolPolicy} for the precedence.
   * Pass it unchanged in {@link DeferredToolCall.policy}.
   * @param toolName - Tool name as the model calls it.
   * @param frameworkEffect - Effect declared by framework metadata, for example from
   * {@link toolEffectFromMcpToolAnnotations}, or `undefined`.
   * @returns The effective policy.
   */
  resolveToolPolicy(toolName: string, frameworkEffect?: ToolEffect): ResolvedToolPolicy;
  /**
   * Returns this turn's record for a tool call ID.
   * @param toolCallId - Framework tool call ID.
   * @returns The record, or `undefined` when the turn never requested that call.
   */
  recordedToolCall(toolCallId: string): AgentToolCall | undefined;
  /**
   * Records a heartbeat for the current attempt.
   *
   * The session already records one every 20 seconds while `runModelStep` is pending. Call it only
   * from a long synchronous loop that could otherwise starve the event loop.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  heartbeat(): Promise<void>;
}

/** One tool call the model requested, returned in {@link ToolCallsRequested}; it runs in its own Step. */
export interface DeferredToolCall {
  /**
   * Framework tool call ID. It must be non-empty, contain no `/`, and be unique within the turn;
   * frameworks may reuse IDs across turns.
   */
  readonly toolCallId: string;
  /** Tool name. */
  readonly toolName: string;
  /** Tool arguments as JSON text. */
  readonly arguments: string;
  /** Policy from {@link AgentTurnContext.resolveToolPolicy}; it decides approval and the tool Step. */
  readonly policy: ResolvedToolPolicy;
  /**
   * Opaque text that the tool's source stores on the call's record, such as which MCP server and tool
   * to call. The tool Step returns it in {@link DeferredToolExecution.toolCall}, so the source resolves
   * the tool without listing it again. Omit it to store nothing.
   */
  readonly toolSourceReference?: string;
  /**
   * Whether the call is a write that runs in the model's order after the turn's earlier ordered calls.
   * The session starts one ordered call at a time, raises its approval only when its turn comes, and
   * records later ordered calls as skipped once one does not succeed. `read` calls ignore it. When
   * omitted, the call runs in parallel with the turn's other calls. `ConnectorToolSource` sets it for
   * connector writes.
   * @since 0.4.0
   */
  readonly ordered?: boolean;
}

/**
 * Model and response of one model call, returned in {@link TurnCompleted} or {@link ToolCallsRequested}
 * for tracing.
 *
 * The session never stores it: it only names the `chat` span and sets its GenAI attributes. Omit a
 * field when the framework does not expose it.
 *
 * @example
 * ```ts
 * const modelCallMetadata: ModelCallMetadata = {
 *   requestModel: "gpt-5",
 *   responseModel: "gpt-5-2026-08-07",
 *   providerName: "openai",
 *   responseId: "resp_123",
 *   finishReasons: ["tool_calls"],
 * };
 * return { kind: "tool_calls_requested", frameworkRunState, toolCalls, usage, modelCallMetadata };
 * ```
 */
export interface ModelCallMetadata {
  /**
   * Model name the call requested, such as `"gpt-5"`. It becomes `gen_ai.request.model` and the span
   * name `chat <requestModel>`.
   */
  readonly requestModel?: string;
  /** Model name the provider reported. */
  readonly responseModel?: string;
  /**
   * GenAI provider, such as `"openai"`, `"anthropic"`, or `"gcp.gemini"`. Use the well-known
   * `gen_ai.provider.name` value when one applies.
   */
  readonly providerName?: string;
  /** Provider response ID. */
  readonly responseId?: string;
  /** Provider finish reasons, one per returned choice. */
  readonly finishReasons?: readonly string[];
}

/** Outcome of a model call that produced the turn's final answer. */
export interface TurnCompleted {
  /** Discriminator. */
  readonly kind: "completed";
  /**
   * Every message of the turn that the session has not stored yet, in order: model output, tool
   * requests, tool results, and the final answer. The session appends them in one commit and deletes the
   * turn's tool call records.
   *
   * A runner that never sets {@link ToolCallsRequested.newMessages} returns every message the turn
   * produced after its user message, across all of its model calls. A runner that stores messages
   * incrementally returns only the messages after the ones it already returned.
   */
  readonly newMessages: readonly FrameworkMessage[];
  /** Usage of this model call only; the session adds it to the cumulative usage. */
  readonly usage: AgentUsage;
  /** Model and response of this model call, for the `chat` span. When omitted, the span is named `chat`. */
  readonly modelCallMetadata?: ModelCallMetadata;
  /**
   * Error text that ends the turn as failed instead of completed.
   *
   * When it is set, the session still stores {@link newMessages} and records {@link usage} in the same commit.
   * It then sets the turn's `lastTurnError` to this text and writes a `turn_failed` event instead of
   * `turn_completed`. No retry follows, so use it only for a deliberate stop that a retry would repeat, such as a
   * limit the runner enforces. Throw instead for a failure that a retry may fix. Omit it, the default, to complete
   * the turn. Snapshots and the UI stream show the text, so keep secrets out of it.
   *
   * @since 0.5.0
   */
  readonly turnError?: string;
}

/**
 * Outcome of a model call that answered without tool calls, after which the turn continues with another model
 * call.
 *
 * The session stores {@link newMessages}, records {@link usage}, keeps {@link frameworkRunState}, and moves to
 * `RunModel` again, all in one commit, within the same turn. That next `RunModel` is its own Step, so a retry there
 * repeats only its own model call. Its {@link AgentTurnContext.messages} include these messages. End them with a
 * user message when the next request must end with one, as Anthropic models from Claude 4.6 on require. The
 * built-in loop returns this outcome when `AgentDefinition.decideTurnContinuation` returns a continuation, with its
 * message as the last of {@link newMessages}.
 *
 * A runner that returns it with messages must implement {@link AgentFrameworkRunner.buildUnfinishedTurnMessages},
 * as for {@link ToolCallsRequested.newMessages}; the session fails the turn otherwise. A runner that never returns
 * it needs no change.
 *
 * @example
 * ```ts
 * return {
 *   kind: "turn_continued",
 *   frameworkRunState: JSON.stringify({ model_calls: modelCalls + 1 }),
 *   newMessages: [...previousToolResults, ...thisResponse, continuationUserMessage],
 *   usage,
 * };
 * ```
 * @since 0.5.0
 */
export interface TurnContinued {
  /** Discriminator. */
  readonly kind: "turn_continued";
  /**
   * Non-empty serialized framework state from which the next `RunModel` continues the turn. The session keeps it
   * opaquely in `FrameworkRunState`, as it keeps {@link ToolCallsRequested.frameworkRunState}.
   */
  readonly frameworkRunState: string;
  /**
   * Messages of the turn to store now: the previous model response's unstored tool results, if any, this model
   * response, and any user message for the next call. The next {@link AgentTurnContext.messages} include them.
   */
  readonly newMessages: readonly FrameworkMessage[];
  /** Usage of this model call only. */
  readonly usage: AgentUsage;
  /** Model and response of this model call, for the `chat` span. When omitted, the span is named `chat`. */
  readonly modelCallMetadata?: ModelCallMetadata;
}

/** Outcome of a model call that requested tool calls. */
export interface ToolCallsRequested {
  /** Discriminator. */
  readonly kind: "tool_calls_requested";
  /**
   * Non-empty serialized framework state from which the next `RunModel` continues the turn. The
   * session keeps this value opaquely in `FrameworkRunState`. A runner without
   * {@link newMessages} includes the messages this turn has produced so far; a runner with them keeps
   * only what the stored messages cannot rebuild, so the value does not grow with the turn.
   */
  readonly frameworkRunState: string;
  /**
   * Messages of the turn to store now, in the same commit that records the tool calls. Defaults to none.
   *
   * Return the previous model response's tool results and this model response, so the turn's
   * transcript is stored while its tools run. A runner that sets this field must implement
   * {@link AgentFrameworkRunner.buildUnfinishedTurnMessages}; the session fails the model call
   * otherwise. The next {@link AgentTurnContext.messages} include these messages.
   */
  readonly newMessages?: readonly FrameworkMessage[];
  /**
   * Every tool call of the model response, at least one. Each runs in its own Step, in parallel. Do not
   * repeat a call that already has a record in {@link AgentTurnContext.toolCalls}; the session rejects
   * that as a runner defect.
   */
  readonly toolCalls: readonly DeferredToolCall[];
  /** Usage of this model call only. */
  readonly usage: AgentUsage;
  /** Model and response of this model call, for the `chat` span. When omitted, the span is named `chat`. */
  readonly modelCallMetadata?: ModelCallMetadata;
}

/**
 * Result of {@link AgentFrameworkRunner.runModelStep}.
 *
 * Changed in 0.5.0: {@link TurnContinued} joined the union, so code that handles every kind handles it too.
 */
export type AgentTurnOutcome = TurnCompleted | ToolCallsRequested | TurnContinued;

/** One tool call that a tool Step asks the runner to run. */
export interface DeferredToolExecution {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** The call's record; its `effect` selected `CallTool` or `CallSingleAttemptTool`. */
  readonly toolCall: AgentToolCall;
  /**
   * Stable key `<flowId>/<turnId>/<toolCallId>`, identical across attempts of the call. Pass it to
   * services that deduplicate requests; `CallTool` retries an `idempotent_write` call with the same
   * key. The turn ID keeps keys distinct when a framework reuses tool call IDs across turns.
   */
  readonly idempotencyKey: string;
  /**
   * One-based attempt of the tool Step. `read` and `idempotent_write` calls get up to three attempts;
   * other calls get one.
   */
  readonly attempt: number;
  /** Events writer bound to the turn and attempt. */
  readonly events: AgentEventWriter;
  /** Signal aborted when Dex cancels the attempt. */
  readonly cancellationSignal: AbortSignal;
  /** Absolute deadline of the current attempt, or `undefined`. */
  readonly deadline: Date | undefined;
  /**
   * Answers to the questions this call asked by returning {@link ToolInputRequested}, oldest first, or
   * empty. The opaque continuation of the last question is in `toolCall.inputContinuation`.
   *
   * The session always sets this field. It is optional only so that code that builds an execution by
   * hand, such as a unit test, keeps compiling; treat `undefined` as no answers.
   */
  readonly inputAnswers?: readonly QuestionAnswer[];
  /**
   * Whether the runner implements {@link AgentFrameworkRunner.pollToolTask}, so the tool may return
   * {@link ToolTaskStarted}. When it is not `true`, run remote work to its end inside the call instead.
   * Optional for the same reason as {@link inputAnswers}; treat `undefined` as `false`.
   */
  readonly supportsToolTasks?: boolean;
  /**
   * Records a heartbeat. The session already records one every 20 seconds.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  heartbeat(): Promise<void>;
  /**
   * The checkpoint an earlier attempt of this call saved with {@link saveHeartbeatCheckpoint}, absent
   * when none did. Use it to resume work that the earlier attempt started, such as polling a
   * long-running job, instead of starting it again.
   */
  readonly heartbeatCheckpoint?: string;
  /**
   * Records a heartbeat carrying a checkpoint. Every later heartbeat of the attempt, including the
   * session's background heartbeats, carries it too, so the next attempt reads it as
   * {@link heartbeatCheckpoint}. Absent when the caller cannot save checkpoints.
   * @param checkpoint - Text to hand to the next attempt.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  saveHeartbeatCheckpoint?(checkpoint: string): Promise<void>;
  /**
   * The tool Step's live context, absent when the caller has none, such as a unit test. It gives the Step
   * execution ID, the attempt deadline, and heartbeats. Use it only during this call and never store it.
   * Record heartbeats only through {@link heartbeat} and {@link saveHeartbeatCheckpoint}, because a bare
   * `context.recordHeartbeat()` would drop the checkpoint. It is never part of stored state.
   * `ConnectorToolSource` derives the connector Call ID from its Step execution ID.
   * @since 0.4.0
   */
  readonly context?: AsyncContext;
}

/** Successful result of one deferred tool call. A result without `kind` is a `ToolExecutionResult`. */
export interface ToolExecutionResult {
  /** Output text recorded on the call and shown to the model. */
  readonly output: string;
  /**
   * Size in bytes of the full output when {@link output} is a truncated copy; omit it when the output is
   * whole. The session stores it as {@link AgentToolCall.truncatedOutputBytes}.
   * @since 0.4.0
   */
  readonly truncatedOutputBytes?: number;
  /**
   * Images the tool returns with {@link output}, uploaded with `MediaStore.putImage` inside the call, at most
   * `maxImagesPerMessage`. The session stores them as {@link AgentToolCall.images}; a reference that
   * `validateImageReference` refuses fails the attempt. Upload before returning: a retried attempt uploads the same
   * bytes to the same object. Omit it for none.
   * @since 0.5.0
   */
  readonly images?: readonly AgentImageReference[];
}

/**
 * Timeout and retry policy of one tool call's Step, returned by
 * {@link AgentFrameworkRunner.toolCallStepOptions}.
 *
 * The session applies it to the movement that starts the call's tool Step, and to no other Step. A
 * `CallSingleAttemptTool` call takes only {@link executeMethodTimeout}; it still runs one attempt.
 *
 * @example
 * ```ts
 * const options: ToolCallStepOptions = {
 *   executeMethodTimeout: 30_000,
 *   executeRetry: { maximumAttempts: 3, totalDuration: 3_600_000 },
 * };
 * ```
 * @since 0.4.0
 */
export interface ToolCallStepOptions {
  /** Timeout of each attempt in milliseconds; omit it for the tool Step's default. */
  readonly executeMethodTimeout?: number;
  /** Retry policy of a `CallTool` call; omit it for the default of three attempts. */
  readonly executeRetry?: RetryPolicy;
}

/**
 * Result of a deferred tool call that needs an answer from the user before it can take effect.
 *
 * Return it from {@link AgentFrameworkRunner.executeDeferredToolCall} only when the call has had no
 * effect yet. The tool Step records `awaiting_answer` and stores {@link continuation}. `AwaitQuestionAnswer`
 * then raises a `question` interrupt with ID `<toolCallId>-input-<n>`, where `n` counts the call's
 * questions from 1. After `answer_question`, the record returns to `approved` and the same tool Step
 * type runs the call again, with every answer in {@link DeferredToolExecution.inputAnswers} and the
 * continuation in `toolCall.inputContinuation`. The tool's later output, not the answer, is the result.
 * A declined question or a passed deadline records `rejected`, and the tool never runs again.
 *
 * Because the call took no effect, a single-attempt tool that runs again after an answer still never
 * repeats an effect. A crash during the second run records `outcome_unknown`, as for any run.
 *
 * @example
 * ```ts
 * public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
 *   const confirmation = call.inputAnswers?.at(-1)?.answer;
 *   if (confirmation === undefined) {
 *     return { kind: "tool_input_requested", question: "Ship to the saved address?", choices: ["yes", "no"],
 *       allowsFreeText: false, continuation: null };
 *   }
 *   return { output: confirmation === "yes" ? await shipOrder(call.idempotencyKey) : "not shipped" };
 * }
 * ```
 */
export interface ToolInputRequested {
  /** Discriminator. */
  readonly kind: "tool_input_requested";
  /** Question for the user; it must not be blank. */
  readonly question: string;
  /** Answers to offer. Defaults to none, which makes the question free text. */
  readonly choices?: readonly string[];
  /** Whether an answer outside {@link choices} is accepted. Defaults to `true`; ignored without choices. */
  readonly allowsFreeText?: boolean;
  /** Opaque text returned to the tool with the answer, such as a server request ID, or `null`. */
  readonly continuation: string | null;
}

/**
 * Result of a deferred tool call that started remote work and returned a handle to poll.
 *
 * Return it from {@link AgentFrameworkRunner.executeDeferredToolCall} when the tool's work runs
 * elsewhere and outlives the call, such as an MCP task. The tool Step records `waiting_for_task` with
 * {@link taskId} and the poll interval, writes `tool_task_started`, and ends. `PollToolTask` then waits
 * on a Timer for the interval and calls {@link AgentFrameworkRunner.pollToolTask} once per execution, so
 * no Step or connection is held while the work runs, and a Worker restart only delays the next poll. The
 * runner must implement `pollToolTask`; without it the session records the call `failed` for a `read`
 * tool and `outcome_unknown` otherwise.
 *
 * The tool Step commits the handle with its result. A `CallTool` attempt that dies after the remote work
 * started runs again, so save the handle with {@link DeferredToolExecution.saveHeartbeatCheckpoint}
 * before you return it, and return the saved handle when {@link DeferredToolExecution.heartbeatCheckpoint}
 * holds one. Once the checkpoint has reached Dex, a retried attempt resumes the same work. Dex may drop a
 * heartbeat checkpoint, though, so an attempt that dies before its checkpoint is saved and before the tool
 * Step commits can still start the work a second time on a `CallTool` retry; pass the idempotency key to
 * the remote system so it can drop the duplicate. Steering waits for a started task. `cancel_turn` abandons
 * it: the session records `outcome_unknown` and asks {@link AgentFrameworkRunner.cancelToolTask} to cancel it.
 *
 * @example
 * ```ts
 * public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
 *   if (call.heartbeatCheckpoint !== undefined) {
 *     return { kind: "tool_task_started", taskId: call.heartbeatCheckpoint, pollIntervalSeconds: 5 };
 *   }
 *   const jobId = await reports.start(call.idempotencyKey);
 *   await call.saveHeartbeatCheckpoint?.(jobId);
 *   return { kind: "tool_task_started", taskId: jobId, pollIntervalSeconds: 5 };
 * }
 * ```
 */
export interface ToolTaskStarted {
  /** Discriminator. */
  readonly kind: "tool_task_started";
  /** Handle of the remote work; it must not be blank. The session stores it as the record's `taskId`. */
  readonly taskId: string;
  /**
   * Seconds to wait before the first poll. The session rounds it up to whole seconds, at least 1,
   * because Dex Timers take whole seconds, and shortens it to at most a day.
   */
  readonly pollIntervalSeconds: number;
}

/** Result of {@link AgentFrameworkRunner.pollToolTask} while the polled task still runs. */
export interface ToolTaskPending {
  /** Discriminator. */
  readonly kind: "tool_task_pending";
  /** Seconds to wait before the next poll. The session rounds it up to whole seconds, from 1 to a day. */
  readonly pollIntervalSeconds: number;
  /**
   * Text the next poll reads as `toolCall.inputContinuation`, such as which question the task took an answer
   * to and when. Omitted or `null` clears it: keep what the next poll needs, and drop a question's
   * continuation once the task took its answer, so an identical later question reads as new.
   */
  readonly continuation?: string | null;
}

/** One task that `PollToolTask` asks the runner to poll. */
export interface ToolTaskPoll {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** The call's record, `waiting_for_task`, with `taskId`, `taskPollIntervalSeconds`, and `toolSourceReference`. */
  readonly toolCall: AgentToolCallRecord;
  /** `<flowId>/<turnId>/<toolCallId>`, the key the tool Step passed when it started the task. */
  readonly idempotencyKey: string;
  /** One-based `PollToolTask` attempt. Each poll gets up to three attempts. */
  readonly attempt: number;
  /** How the questions this call asked were resolved, oldest first, whether its tool or its task asked them. */
  readonly inputAnswers: readonly QuestionAnswer[];
  /**
   * The resolution of the task's last question, which has not reached the task yet, or `null`. It is set
   * only on the first poll after `AwaitQuestionAnswer` resolved the question, which runs at once. Deliver it
   * to the task: the answer when `resolution` is `answered`, a decline when it is `declined`, and a
   * cancelled request when it is `deadline_passed`. `toolCall.inputContinuation` holds the question's
   * continuation, so recognize the request it answers and deliver only to that request. Later polls read the
   * continuation the last {@link ToolTaskPending} returned. A retried attempt gets it again; when it cannot
   * tell whether an earlier attempt delivered it, return the task's question as {@link ToolInputRequested}
   * again rather than send it to what may be a later question. When the task cannot take the resolution,
   * cancel the task best-effort and throw an error other than {@link ToolExecutionError}, so the session
   * records the call by its effect.
   */
  readonly pendingAnswer: QuestionAnswer | null;
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

/** One task that `cancel_turn` abandoned, which `CancelToolTask` asks the runner to cancel. */
export interface ToolTaskCancellation {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /**
   * The call's record with `taskId` and `toolSourceReference`. Its `cancellationReason` is `turn_cancelled`.
   */
  readonly toolCall: AgentToolCallRecord;
  /** `<flowId>/<turnId>/<toolCallId>`, the key the tool Step passed when it started the task. */
  readonly idempotencyKey: string;
  /** One-based `CancelToolTask` attempt. Each cancellation gets up to three attempts. */
  readonly attempt: number;
  /** Signal aborted when Dex cancels the attempt, such as when its method timeout passes. */
  readonly cancellationSignal: AbortSignal;
  /**
   * Records a heartbeat. The session already records one every 20 seconds.
   * @returns A Promise that resolves when the Worker accepted the heartbeat.
   */
  heartbeat(): Promise<void>;
}

/** Result of {@link AgentFrameworkRunner.executeDeferredToolCall}. */
export type DeferredToolOutcome = ToolExecutionResult | ToolInputRequested | ToolTaskStarted;

/** Result of {@link AgentFrameworkRunner.pollToolTask}. */
export type ToolTaskPollOutcome = ToolExecutionResult | ToolInputRequested | ToolTaskPending;

/**
 * Tool failure whose effect may or may not have happened, thrown from
 * {@link AgentFrameworkRunner.executeDeferredToolCall}.
 *
 * Throw it when the tool sent a write and cannot tell whether it took effect, such as a connector
 * operation's `uncertain` branch. The session records `outcome_unknown` with this error's message as
 * `error`, does not retry, and the model sees {@link outcomeUnknownToolResult}. A later ordered write of
 * the turn is skipped.
 *
 * {@link AgentFrameworkRunner.pollToolTask} may throw it too, for a task that ended without a known
 * outcome. The session then records `outcome_unknown` the same way and stops polling. Changed in 0.5.0:
 * `pollToolTask` may throw it.
 *
 * @example
 * ```ts
 * throw new ToolOutcomeUnknownError("the provider timed out after receiving the refund");
 * ```
 * @since 0.4.0
 */
export class ToolOutcomeUnknownError extends Error {
  /**
   * Creates an unknown-outcome failure.
   * @param message - Error text recorded on the call.
   * @param options - Standard error options, such as `cause`.
   */
  public constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "ToolOutcomeUnknownError";
  }
}

/**
 * Tool failure that the model should see, thrown from {@link AgentFrameworkRunner.executeDeferredToolCall}.
 *
 * The session records the call as `failed` with this error's message and does not retry it. Throw any
 * other error for an unexpected failure: `CallTool` retries it and, once its attempts run out, records
 * `failed` for a `read` tool and `outcome_unknown` for a write; `CallSingleAttemptTool` records
 * `outcome_unknown` at once, because the write may have taken effect.
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
 * - `runModelStep` makes at most one model call. It must stop before running any tool the framework
 *   can defer, and return every requested tool call in {@link ToolCallsRequested}. Each call then runs
 *   in its own Step: `CallTool` for `read` and `idempotent_write`, `CallSingleAttemptTool` for the
 *   other effects, after `AwaitToolApproval` when the policy requires approval.
 * - When `frameworkRunState` is set, `runModelStep` restores the framework and gives it the recorded
 *   result of every call from the previous response before making the next model call.
 * - A tool the framework cannot defer runs inside `runModelStep` and repeats when the model call
 *   reruns; the adapter documents such tools as at-least-once.
 * - `runModelStep` and `executeDeferredToolCall` must not keep state between calls; a later call may
 *   run in another Worker process.
 * - Throwing from `runModelStep` retries the model call, up to 3 attempts within 30 minutes; then the
 *   turn fails, the session records `last_turn_error`, and it waits for the next user message.
 * - A runner may store the turn's transcript incrementally with {@link ToolCallsRequested.newMessages};
 *   it then implements {@link buildUnfinishedTurnMessages}. A runner may offer the
 *   {@link AgentTurnContext.enabledBuiltInTools} to its model; the session handles their calls.
 *
 * @example
 * ```ts
 * class EchoRunner implements AgentFrameworkRunner {
 *   public buildUserMessage(text: string): FrameworkMessage {
 *     return { role: "user", text, toolCallId: null, toolName: null, toolArguments: null,
 *       toolOutput: null, frameworkItem: JSON.stringify({ role: "user", content: text }) };
 *   }
 *   public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
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
 * @typeParam ToolOutcome - What {@link executeDeferredToolCall} returns. It defaults to
 * {@link ToolExecutionResult}; a runner whose tools can ask the user a question first or start a remote
 * task implements `AgentFrameworkRunner<DeferredToolOutcome>`. The session accepts both.
 */
export interface AgentFrameworkRunner<ToolOutcome extends DeferredToolOutcome = ToolExecutionResult> {
  /**
   * Converts user text into the framework's user message.
   *
   * The session calls it when it consumes a user message and stores the result before `runModelStep`.
   * It must be synchronous, deterministic, and free of side effects.
   *
   * @param text - User message text, never blank.
   * @returns The framework message, with `role` `"user"`.
   */
  buildUserMessage(text: string): FrameworkMessage;
  /**
   * Converts a user message with images into the framework's user message. Optional.
   *
   * Implement it when the runner can show images to its model. `send_user_message` accepts images only for a runner
   * with this method, and the session calls it instead of {@link buildUserMessage} for a message with images. Put
   * the references, never the bytes, in `frameworkItem`; read the bytes with `loadImages` when the model call
   * builds its input. The session sets the stored message's `images` itself. It must be synchronous,
   * deterministic, and free of side effects.
   *
   * @example
   * ```ts
   * public buildUserMessageWithImages(text: string, images: readonly AgentImageReference[]): FrameworkMessage {
   *   return { role: "user", text, toolCallId: null, toolName: null, toolArguments: null, toolOutput: null,
   *     frameworkItem: JSON.stringify({ role: "user", content: text, images }) };
   * }
   * ```
   * @param text - User message text, never blank.
   * @param images - The message's images, at least one, each valid for `validateImageReference`.
   * @returns The framework message, with `role` `"user"`.
   * @since 0.5.0
   */
  buildUserMessageWithImages?(text: string, images: readonly AgentImageReference[]): FrameworkMessage;
  /**
   * Makes the turn's next model call, at most one.
   *
   * @param turn - Turn context; see {@link AgentTurnContext}.
   * @returns {@link TurnCompleted} with every new message of the turn, {@link ToolCallsRequested}
   * with the framework run state and every tool call of the model response, or {@link TurnContinued} to make
   * another model call within the same turn.
   * @throws Any error to retry the model call; after the last attempt the turn fails.
   */
  runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome>;
  /**
   * Runs the real tool for one tool call, in `CallTool` or `CallSingleAttemptTool`.
   *
   * @param call - Deferred call to run.
   * @returns The tool output text; {@link ToolInputRequested} when the tool needs an answer from the
   * user before it takes any effect; or {@link ToolTaskStarted} when the tool started remote work that
   * {@link pollToolTask} polls.
   * @throws {ToolExecutionError} For a failure the model should see; the call is recorded as `failed`.
   * @throws Any other error for an unexpected failure; after the last attempt the record becomes
   * `failed` for a `read` tool and `outcome_unknown` for a write. See {@link ToolExecutionError}.
   */
  executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolOutcome>;
  /**
   * Returns the turn's messages that the session has not stored yet, when the turn ends without a final
   * answer. Optional.
   *
   * Implement it when the runner sets {@link ToolCallsRequested.newMessages}. `RecordTurnFailure` calls
   * it before it appends the turn-failure note, with every record of the turn final. Return the tool
   * result of every tool call already stored or returned that has no stored result, so no tool call is
   * left without a result. It must be synchronous and make no model call. When it throws, the session
   * stores only the turn-failure note, so a runner defect never blocks the failure route.
   *
   * @param turn - The ended turn; {@link AgentTurnContext.toolCalls} holds its final records.
   * @returns The messages to append, in order, possibly none.
   */
  buildUnfinishedTurnMessages?(turn: AgentTurnContext): readonly FrameworkMessage[];
  /**
   * Whether the runner honors plan mode. Optional; {@link AgentSessionOptions.planModeEnabled} needs `true`,
   * and `createAgentSessionFlow` throws otherwise.
   *
   * Such a runner reads {@link AgentTurnContext.interactionMode} in every `runModelStep`. In a `planning`
   * turn it offers only the `write_todos` definition from {@link builtInToolDefinitions}. It may force
   * `write_todos` on the turn's first model call while the plan is not a draft yet, where the model accepts a
   * forced tool choice; the built-in loop does with `planningToolChoice: "required"`. In every
   * turn it shows the plan with {@link buildPlanNote} as user text before the turn's messages. The built-in loop
   * runner implements it; a framework adapter that cannot change the tools it offers between turns leaves
   * plan mode off.
   */
  readonly supportsPlanMode?: boolean;
  /**
   * Polls one remote task that a tool started with {@link ToolTaskStarted}, once. Optional.
   *
   * Implement it when a tool returns `ToolTaskStarted`. `PollToolTask` calls it once per execution,
   * after a Timer for the call's poll interval, until it returns a result. Polling must only read the
   * task, and deliver `poll.pendingAnswer` when it is set, because the session may poll again after a
   * crash. Steering never stops a poll. `cancel_turn` aborts `poll.cancellationSignal` of a running poll,
   * and the session drops whatever it returns.
   *
   * @example
   * ```ts
   * public async pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
   *   const job = await reports.get(poll.toolCall.taskId!);
   *   return job.done ? { output: job.summary } : { kind: "tool_task_pending", pollIntervalSeconds: 10 };
   * }
   * ```
   * @param poll - The call's record and its idempotency key.
   * @returns {@link ToolExecutionResult} when the task finished, which the model sees;
   * {@link ToolTaskPending} while it runs; or {@link ToolInputRequested} when it needs the user's answer.
   * The session asks the question through `AwaitQuestionAnswer` and polls again at once with its
   * resolution, answered, declined, or past its deadline, as `pendingAnswer`. The task's later result is
   * the call's result.
   * @throws {ToolExecutionError} When the task failed in a way the model should see; the call is
   * recorded as `failed`.
   * @throws {ToolOutcomeUnknownError} When the task ended and may or may not have taken effect; the call is
   * recorded as `outcome_unknown` and not polled again. Changed in 0.5.0: this error is recorded instead of
   * failing the poll attempt.
   * @throws Any other error to fail the poll attempt; after three attempts the record becomes `failed`
   * for a `read` call and `outcome_unknown` otherwise, because the task may still run.
   */
  pollToolTask?(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome>;
  /**
   * Asks the remote system to stop one task that `cancel_turn` abandoned. Optional.
   *
   * `cancel_turn` does not wait for a started task: the session records the call `outcome_unknown` with
   * `cancellationReason` `turn_cancelled`, and the `CancelToolTask` Step calls this method best-effort before
   * it records the call. A runner without it leaves the task running. Steering never cancels a task. The
   * session may call it more than once for one task, after a crash or a failed attempt, so make it idempotent
   * by `taskId`. A task that already finished, or that the remote system no longer knows, is cancelled: return
   * normally. The record is final whatever this method does, and a result the task returns later is dropped.
   *
   * @example
   * ```ts
   * public async cancelToolTask(cancellation: ToolTaskCancellation): Promise<void> {
   *   await reports.cancel(cancellation.toolCall.taskId!);
   * }
   * ```
   * @param cancellation - The abandoned call's record and its idempotency key.
   * @returns A Promise that resolves once the remote system took the cancellation.
   * @throws Any error when the cancellation did not reach the remote system; the session retries it up to
   * three times, then records the call `outcome_unknown` anyway, so the turn always ends.
   */
  cancelToolTask?(cancellation: ToolTaskCancellation): Promise<void>;
  /**
   * Returns the timeout and retry policy of one call's tool Step. Optional.
   *
   * Implement it when some tools need options other than the tool Steps' defaults, as the built-in loop runner
   * does for a tool source that defines `toolCallStepOptions`. The session calls it whenever it starts a
   * call's tool Step, including after an approval, so it must answer from the record alone. A method that
   * throws leaves the defaults.
   *
   * @example
   * ```ts
   * public toolCallStepOptions(toolCall: AgentToolCall): ToolCallStepOptions | undefined {
   *   return toolCall.toolName === "export_report" ? { executeMethodTimeout: 120_000 } : undefined;
   * }
   * ```
   * @param toolCall - The record whose tool Step starts.
   * @returns The options, or `undefined` for the defaults.
   * @since 0.4.0
   */
  toolCallStepOptions?(toolCall: AgentToolCall): ToolCallStepOptions | undefined;
  /**
   * Estimates the size of the model input that the next {@link runModelStep} sends. Optional; one of the
   * three compaction methods that {@link AgentSessionOptions.compaction} requires.
   *
   * `RunModel` calls it before every model call of a session with a compaction policy. At the policy's
   * threshold it calls {@link selectCompactionCutoff}, and when that returns a sequence after the current
   * summary, `CompactContext` calls {@link summarizeContext} once and stores the result as the
   * `ContextSummary`. From then on, {@link AgentTurnContext.messages} start after the summary and
   * {@link AgentTurnContext.contextSummary} holds it. A runner without these methods never compacts.
   *
   * @example
   * ```ts
   * class CompactingRunner extends MyRunner {
   *   public estimateInputTokens(turn: AgentTurnContext): number {
   *     return countTokens(buildModelInput(turn));
   *   }
   *   public selectCompactionCutoff(turn: AgentTurnContext, retainedTokenBudget: number): number | null {
   *     return lastSequenceBeforeNewestMessages(turn.messages, retainedTokenBudget);
   *   }
   *   public async summarizeContext(request: ContextSummaryRequest): Promise<ContextSummaryResult> {
   *     const response = await model.summarize(request.previousSummary, request.messages);
   *     return { text: response.text, usage: response.usage };
   *   }
   * }
   * ```
   * @param turn - The same context the next {@link runModelStep} receives.
   * @returns The estimated input tokens, in the unit of `CompactionPolicy.contextWindowTokens`.
   */
  estimateInputTokens?(turn: AgentTurnContext): number | Promise<number>;
  /**
   * Chooses the last message to summarize, so the newest messages fit a token budget. Optional; see
   * {@link estimateInputTokens}.
   *
   * Never separate a tool call from its result: the messages after the cutoff must not start with a tool
   * result or hold a result whose call is before the cutoff. Keep the turn's unstored tool results
   * answerable.
   *
   * @param turn - The context of the model call that crossed the threshold.
   * @param retainedTokenBudget - Tokens that the messages after the cutoff may use.
   * @returns The sequence of the last message to summarize, one of {@link AgentTurnContext.messages}; or
   * `null` when nothing can be summarized, which lets the model call proceed.
   */
  selectCompactionCutoff?(turn: AgentTurnContext, retainedTokenBudget: number): number | null | Promise<number | null>;
  /**
   * Folds the previous summary and a range of messages into a new summary, with at most one model call.
   * Optional; see {@link estimateInputTokens}. A crash during the call reruns only this call.
   *
   * @param request - The previous summary and the messages to summarize.
   * @returns The new summary text and the usage of the call.
   * @throws Any error to fail the `CompactContext` attempt; it retries up to 3 attempts within 30
   * minutes, then the turn fails through `RecordTurnFailure`.
   */
  summarizeContext?(request: ContextSummaryRequest): Promise<ContextSummaryResult>;
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
 * @returns `read` when `readOnlyHint` is `true`; otherwise `idempotent_write` when `idempotentHint` is
 * `true`, whatever `destructiveHint` says, as in the Python core; otherwise `undefined`, so the session
 * default applies.
 */
export function toolEffectFromMcpToolAnnotations(
  annotations: McpToolAnnotations,
): ToolEffect | undefined {
  if (annotations.readOnlyHint === true) {
    return "read";
  }
  if (annotations.idempotentHint === true) {
    return "idempotent_write";
  }
  return undefined;
}
