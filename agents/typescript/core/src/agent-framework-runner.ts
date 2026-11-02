// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { BuiltInTool } from "./agent-built-in-tools.js";
import type {
  AgentMessage,
  AgentToolCall,
  AgentToolCallStatus,
  AgentUsage,
  FrameworkMessage,
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
   * One-based `RunModel` attempt. A rerun after a Worker crash may keep the same attempt when the
   * crashed attempt sent no output, so do not rely on it to detect reruns.
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
   * started this turn. Replay each `frameworkItem` to the framework unchanged.
   */
  readonly messages: readonly AgentMessage[];
  /**
   * Framework run state from this turn's previous {@link ToolCallsRequested} outcome, or `null` for
   * the turn's first model call.
   */
  readonly frameworkRunState: string | null;
  /** Whether this model call continues the turn after tool calls, that is, `frameworkRunState` is set. */
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

/** Result of {@link AgentFrameworkRunner.runModelStep}. */
export type AgentTurnOutcome = TurnCompleted | ToolCallsRequested;

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
}

/** Successful result of one deferred tool call. A result without `kind` is a `ToolExecutionResult`. */
export interface ToolExecutionResult {
  /** Output text recorded on the call and shown to the model. */
  readonly output: string;
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

/** Result of {@link AgentFrameworkRunner.executeDeferredToolCall}. */
export type DeferredToolOutcome = ToolExecutionResult | ToolInputRequested;

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
 * {@link ToolExecutionResult}; a runner whose tools can ask the user a question first implements
 * `AgentFrameworkRunner<DeferredToolOutcome>`. The session accepts both.
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
   * Makes the turn's next model call, at most one.
   *
   * @param turn - Turn context; see {@link AgentTurnContext}.
   * @returns {@link TurnCompleted} with every new message of the turn, or {@link ToolCallsRequested}
   * with the framework run state and every tool call of the model response.
   * @throws Any error to retry the model call; after the last attempt the turn fails.
   */
  runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome>;
  /**
   * Runs the real tool for one tool call, in `CallTool` or `CallSingleAttemptTool`.
   *
   * @param call - Deferred call to run.
   * @returns The tool output text, or {@link ToolInputRequested} when the tool needs an answer from the
   * user before it takes any effect.
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
