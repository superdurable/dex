// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  StepList,
  StepRef,
  booleanCodec,
  rpc,
  type Channel,
  type Context,
  type Flow,
  type PersistenceSchema,
  type RPCResult,
} from "@superdurable/dex";

import { builtInToolNames, isBlankText, type BuiltInTool } from "./agent-built-in-tools.js";
import {
  isContextCompactionRunner,
  resolveCompactionPolicy,
  type CompactionPolicy,
  type ResolvedCompactionPolicy,
} from "./agent-context-compaction.js";
import type {
  AgentFrameworkRunner,
  DeferredToolOutcome,
  ToolPolicy,
} from "./agent-framework-runner.js";
import {
  agentMessagesPageCodec,
  agentSnapshotCodec,
  agentUserMessageCodec,
  answerQuestionRequestCodec,
  cancelTurnRequestCodec,
  deleteQueuedMessageRequestCodec,
  executePlanRequestCodec,
  listAgentMessagesInputCodec,
  resolveInterruptInputCodec,
  steerUserMessageRequestCodec,
  updateSessionSettingsRequestCodec,
} from "./agent-session-codecs.js";
import {
  createAgentSessionPersistence,
  defaultAgentEventsCapacityBytes,
  planExecutionKey,
  type AgentSessionPersistence,
} from "./agent-session-persistence.js";
import { checkStoredSessionSettings } from "./agent-session-settings.js";
import type { ModelSpendLimit } from "./agent-session-spend-limit.js";
import { resolveRunModelLimit, type ResolvedRunModelLimit, type RunModelLimit } from "./agent-session-step-options.js";
import {
  ApplyTurnControlStep,
  AwaitToolApprovalStep,
  AwaitUserMessageStep,
  CompactContextStep,
  isExecutablePlanStatus,
  RecordTurnFailureStep,
  ResumeTurnStep,
  RunModelStep,
  StartAgentSessionStep,
  ToolCallFailureRecordingStep,
  ToolCallStep,
} from "./agent-session-steps.js";
import { AgentSessionTracer } from "./agent-session-tracer.js";
import { CancelToolTaskStep, FinishTurnCancellationStep } from "./agent-session-cancellation-steps.js";
import { AwaitQuestionAnswerStep, AwaitTimerStep, PollToolTaskStep } from "./agent-session-wait-steps.js";
import {
  AgentSessionTransitions,
  agentSessionStepTypes,
  finishTurnCancellationMovement,
  messageRolloverMaxBytesPerCommit,
  messageRolloverMaxChunksPerCommit,
  type AgentSessionConfig,
} from "./agent-session-transitions.js";
import type {
  AgentMessagesPage,
  AgentSessionStartInput,
  AgentSnapshot,
  AgentUserMessage,
  AnswerQuestionRequest,
  CancelTurnRequest,
  DeleteQueuedMessageRequest,
  ExecutePlanRequest,
  ListAgentMessagesInput,
  PendingUserMessage,
  ResolveInterruptInput,
  SteerUserMessageRequest,
  ToolEffect,
  UpdateSessionSettingsRequest,
} from "./agent-session-values.js";
import {
  genAISemanticConventionsSchemaUrls,
  type AgentTracingOptions,
  type ResolvedAgentTracingOptions,
} from "./agent-tracing-options.js";

export const snapshotMessageLimit = 200;
export const defaultMaxDurableWaitMs = 365 * 24 * 60 * 60 * 1_000;
export const messagePageLimit = 200;
/** Default {@link AgentSessionOptions.messageWindowSize}. */
export const defaultMessageWindowSize = 64;
/** Default {@link AgentSessionOptions.messageChunkSize}. */
export const defaultMessageChunkSize = 32;
/** Default {@link AgentSessionOptions.messageChunkMaxBytes}: 1 MiB. */
export const defaultMessageChunkMaxBytes = 1_048_576;
/** Default {@link AgentSessionOptions.retainedMessageLimit}. */
export const defaultRetainedMessageLimit = 1_000;
export { messageRolloverMaxBytesPerCommit, messageRolloverMaxChunksPerCommit };
const toolEffects: readonly ToolEffect[] = [
  "read",
  "idempotent_write",
  "non_idempotent_write",
  "unclassified",
];

/**
 * Tool policies, built-in tools, live events, interrupt deadline, tracing, and compaction of one agent,
 * passed to {@link createAgentSessionFlow}.
 */
export interface AgentSessionOptions {
  /**
   * Policies by tool name. A policy overrides framework metadata and {@link defaultToolEffect}.
   * Defaults to none.
   */
  readonly toolPolicies?: Readonly<Record<string, ToolPolicy>>;
  /**
   * Effect of a tool without a policy or framework metadata. Defaults to `"unclassified"`, so unknown
   * tools run once and need approval.
   */
  readonly defaultToolEffect?: ToolEffect;
  /**
   * Approximate byte budget of the `AgentEvents` Stream, shared by every session of the Flow type.
   * Defaults to 10 MiB.
   */
  readonly eventStreamCapacityBytes?: number;
  /**
   * Milliseconds an approval or a question may stay pending before the session rejects it with
   * `"approval deadline passed"` or `"question deadline passed"`. Defaults to no deadline: interrupts wait
   * indefinitely.
   */
  readonly interruptDeadlineMs?: number;
  /**
   * OpenTelemetry GenAI span options. Defaults to spans through the global tracer provider when
   * `@opentelemetry/api` is installed, and nothing otherwise. See {@link AgentTracingOptions}.
   */
  readonly tracing?: AgentTracingOptions;
  /**
   * Built-in tools the session handles, such as `["ask_user", "durable_wait"]`. Defaults to none.
   *
   * The runner offers the enabled ones to its model from {@link builtInToolDefinitions}; see
   * {@link BuiltInTool}. An enabled built-in name is reserved: a tool policy for it is refused here, and
   * the session routes every call with that name to the built-in tool. Enable a built-in tool only after
   * every Worker of the Flow type runs a release that supports it.
   */
  readonly builtInTools?: readonly BuiltInTool[];
  /**
   * Longest `durable_wait` in milliseconds; a longer `duration_seconds` fails the call at once.
   * Defaults to 365 days. It must be a positive multiple of 1,000, because Dex Timers take whole seconds.
   * The Python core's `max_durable_wait` is a `timedelta`; this option is the same limit in milliseconds.
   */
  readonly maxDurableWaitMs?: number;
  /**
   * Whether `send_user_message` accepts a message with `planMode`, which starts a `planning` turn that drafts
   * a plan for `execute_plan`. A `draft` plan makes every new turn a `planning` turn until it runs. Defaults
   * to `false`.
   *
   * It requires `write_todos` in {@link builtInTools} and a runner whose
   * {@link AgentFrameworkRunner.supportsPlanMode} is `true`, such as the Stepwise runner.
   */
  readonly planModeEnabled?: boolean;
  /**
   * When to summarize earlier messages so the model input stays under the context window. Defaults to no
   * compaction. It needs a runner that implements {@link AgentFrameworkRunner.estimateInputTokens},
   * {@link AgentFrameworkRunner.selectCompactionCutoff}, and {@link AgentFrameworkRunner.summarizeContext}.
   * The summarized messages stay stored until {@link retainedMessageLimit} deletes them. Enable it only after
   * every Worker of the Flow type runs a release with the `CompactContext` Step. See {@link CompactionPolicy}.
   */
  readonly compaction?: CompactionPolicy;
  /**
   * Most messages the `AgentMessages` window holds. Defaults to 64. When an append takes the window past it,
   * its oldest messages move into immutable `AgentMessageChunks` instances in the same commit, at most
   * {@link messageRolloverMaxChunksPerCommit} chunks per commit. Every Step that stores messages loads the
   * whole window, so it bounds that load. It must be at least {@link messageChunkSize}.
   */
  readonly messageWindowSize?: number;
  /** Most messages one archive chunk holds. Defaults to 32. A change applies to the next chunk written. */
  readonly messageChunkSize?: number;
  /**
   * Most bytes one archive chunk holds, measured on the JSON this core writes. Defaults to 1 MiB, between 1 and
   * 8,388,608. A chunk closes at {@link messageChunkSize} messages or at this size, whichever comes first. A
   * message whose chunk of one, the message plus 15 bytes, would pass it never enters the window: the Step that
   * stores it writes it straight into its own chunk, so a message too large for the server's
   * `values.maxValueBytes` fails that Step instead of a later rollover. Keep it well under that limit.
   */
  readonly messageChunkMaxBytes?: number;
  /**
   * Retention of summarized messages: the session deletes its oldest archive chunk once every message in it is
   * at or before `ContextSummary.lastSequence` and at least this many stored messages come after the chunk.
   * Defaults to 1,000. `null` keeps every message. It applies only with {@link compaction}, so Flow state stays
   * bounded however long the session runs.
   *
   * The newest `retainedMessageLimit` messages and every message after the summary always stay. Deleted
   * messages leave `list_agent_messages` and `get_agent_snapshot`, and each `list_agent_messages` page then
   * carries `deletedBeforeSequence`. An application that keeps the full transcript copies new messages with
   * `list_agent_messages` before more than `retainedMessageLimit` messages follow them.
   */
  readonly retainedMessageLimit?: number | null;
  /**
   * Method timeout and attempts of `RunModel`, for a runner whose `RunModel` takes longer than one model call, such as
   * a harness. Defaults to 15 minutes and 3 attempts in 30 minutes. See {@link RunModelLimit}.
   *
   * @since 0.3.0
   */
  readonly runModelLimit?: RunModelLimit;
  /**
   * The application's spend limit, called before and after each model call of `RunModel` and `CompactContext`.
   * Omit it to make no checks. See {@link ModelSpendLimit}.
   *
   * @since 0.3.0
   */
  readonly spendLimit?: ModelSpendLimit;
}

/** Options applied by an {@link AgentSessionFlow}, with every default filled in. */
export interface ResolvedAgentSessionOptions {
  /** Policies by tool name. */
  readonly toolPolicies: Readonly<Record<string, ToolPolicy>>;
  /** Effect of a tool without a policy or framework metadata. */
  readonly defaultToolEffect: ToolEffect;
  /** Byte budget of the `AgentEvents` Stream. */
  readonly eventStreamCapacityBytes: number;
  /** Approval and question deadline in milliseconds, or `undefined` for none. */
  readonly interruptDeadlineMs: number | undefined;
  /**
   * Tracing options. A Flow always sets this field; it is optional so that 0.1.0 code that builds these
   * options keeps compiling.
   */
  readonly tracing?: ResolvedAgentTracingOptions;
  /**
   * Enabled built-in tools, in {@link builtInToolDefinitions} order. A Flow always sets this field and
   * the one below; they are optional so that 0.1.0 code that builds these options keeps compiling.
   */
  readonly builtInTools?: readonly BuiltInTool[];
  /** Longest `durable_wait` in milliseconds. */
  readonly maxDurableWaitMs?: number;
  /** Whether plan mode is enabled. A Flow always sets it; it is optional for the same reason. */
  readonly planModeEnabled?: boolean;
  /** The compaction policy with its defaults, or `undefined` when the session never compacts. */
  readonly compaction?: ResolvedCompactionPolicy;
  /** Most messages the `AgentMessages` window holds. A Flow always sets it; optional for 0.1.0 code. */
  readonly messageWindowSize?: number;
  /** Most messages per archive chunk. A Flow always sets it; optional for 0.1.0 code. */
  readonly messageChunkSize?: number;
  /** Most bytes per archive chunk. A Flow always sets it; optional for 0.1.0 code. */
  readonly messageChunkMaxBytes?: number;
  /** The retention limit, or `null` to keep every message. A Flow always sets it; optional for 0.1.0 code. */
  readonly retainedMessageLimit?: number | null;
  /** The `RunModel` limit and its retry budget. A Flow always sets it; optional for 0.2.0 code. */
  readonly runModelLimit?: ResolvedRunModelLimit;
  /** The application's spend limit, or `undefined` for none. */
  readonly spendLimit?: ModelSpendLimit;
}

/**
 * Durable agent session Flow: one Flow execution is one agent session, and its Flow ID is the session ID.
 *
 * Create one per agent with {@link createAgentSessionFlow}, register it in the process `Registry`, and
 * call its RPCs with `Client.invokeRPC`. The RPC names are `send_user_message`, `steer_user_message`,
 * `delete_queued_message`, `cancel_turn`, `resolve_interrupt`, `answer_question`, `execute_plan`,
 * `update_session_settings`, `get_agent_snapshot`, and `list_agent_messages`, the same as the Python core. The session never completes on its
 * own: it waits for the next user message after every turn. Stop it with `Client.stopFlow`.
 *
 * `steer_user_message` ends the running turn at its next safe Step boundary. It never interrupts a model call or a
 * tool call that has already started. `cancel_turn` aborts the running model call and every wait for a person, a
 * Timer, or a remote task too, but a tool call Step that has started still runs to the end. The calls that have
 * not started record `rejected` with a `cancellationReason`, an abandoned task records `outcome_unknown`, and
 * `ApplyTurnControl` ends the turn. A steered turn is followed by a new turn with the steered messages.
 *
 * The Flow's persistence definitions are properties of the Flow; pass them to `Client.getAttributes`
 * or the Stream reads.
 *
 * @example
 * ```ts
 * const flow = createAgentSessionFlow(runner, "SupportAgent");
 * const registry = new Registry([flow]);
 * // ... start a Worker and a Client with this registry ...
 * await startAgentSession(client, flow, sessionId);
 * await client.invokeRPC(flow.sendUserMessage, sessionId, { text: "Where is order 42?" });
 * const snapshot = await client.invokeRPC(flow.getAgentSnapshot, sessionId);
 * for (const interrupt of snapshot.pendingInterrupts) {
 *   await client.invokeRPC(flow.resolveInterrupt, sessionId, {
 *     interruptId: interrupt.interruptId, approved: true, rejectionMessage: null });
 * }
 * ```
 * @typeParam Runner - Type of {@link runner}. It defaults to {@link AgentFrameworkRunner}.
 */
export interface AgentSessionFlow<
  Runner extends AgentFrameworkRunner<DeferredToolOutcome> = AgentFrameworkRunner,
> extends Flow<AgentSessionStartInput>,
    AgentSessionPersistence {
  /** The framework runner passed to {@link createAgentSessionFlow}. */
  readonly runner: Runner;
  /** The options this Flow applies, with defaults filled in. */
  readonly options: ResolvedAgentSessionOptions;
  /**
   * RPC `send_user_message`: queues one user message.
   *
   * The session starts one turn per queued message, in order, after the current turn ends.
   * The RPC requires a running session.
   *
   * @param context - RPC Context supplied by Dex.
   * @param message - Message to queue.
   * @returns `true` when queued; `false` when the text is blank, or for a `planMode` message when
   * {@link AgentSessionOptions.planModeEnabled} is not set.
   */
  sendUserMessage(context: Context, message: AgentUserMessage): RPCResult<boolean>;
  /**
   * RPC `execute_plan`: approves one plan revision, so the session starts a turn that runs it.
   *
   * Call it after the user reviewed the `draft` plan that a `planMode` message drafted, or to run an `active`
   * plan again. The next `AwaitUserMessage` starts an `executing` turn with every tool, whose user message is
   * `[Dex] The user approved plan revision <n>. Execute it and keep the task statuses current with
   * write_todos.`. A queued message that the session took first runs its turn first; when that turn changes
   * the plan, the approval is stale and never runs.
   *
   * The RPC records the approval in `PendingPlanExecution` until the session starts the turn, so a revision
   * runs once per approval. It locks `PendingPlanExecution`: a concurrent `execute_plan` can make it fail with
   * `RpcLockConflictError`, which is safe to retry.
   *
   * @example
   * ```ts
   * const snapshot = await client.invokeRPC(flow.getAgentSnapshot, sessionId);
   * if (snapshot.plan?.status === "draft") {
   *   await client.invokeRPC(flow.executePlan, sessionId, { revision: snapshot.plan.revision });
   * }
   * ```
   * @param context - RPC Context supplied by Dex.
   * @param input - The plan revision the user reviewed.
   * @returns `true` when the execution is accepted; `false` unless the session waits for a user message, the
   * plan has this revision and is `draft` or `active`, no execution of it is pending, and no queued or steered
   * message or interrupt is pending.
   */
  executePlan(context: Context, input: ExecutePlanRequest): RPCResult<boolean>;
  /**
   * RPC `steer_user_message`: steers the running turn with new text or with a queued message.
   *
   * A steered message applies at the turn's next safe Step boundary. The session never interrupts a model call or
   * a tool call that has started. It records the calls that have not started as `rejected` with
   * `cancellationReason` `"steered"`, ends the turn, and starts a new turn whose user messages are every steered
   * message, in order.
   *
   * While no turn is active, there is nothing to steer: new `text` is queued like `send_user_message`, so it
   * starts the next turn at once, and a `messageId` that is still queued stays queued. A turn that a Worker
   * without steering started or advanced cannot be steered; see {@link AgentState.acceptsTurnControl}.
   *
   * With `messageId`, the RPC moves that queued message: it deletes it from `QueuedUserMessages` and publishes it
   * to `SteeredUserMessages` in one commit. It locks nothing. When another Step or RPC consumes the message
   * between the RPC's read and its commit, the RPC fails with `ChannelMessageNotFoundError` and commits nothing;
   * reload the snapshot and let the user choose again.
   *
   * @example
   * ```ts
   * const [queued] = (await client.invokeRPC(flow.getAgentSnapshot, sessionId)).queuedMessages ?? [];
   * if (queued !== undefined) {
   *   await client.invokeRPC(flow.steerUserMessage, sessionId, { messageId: queued.messageId, text: null });
   * }
   * ```
   * @param context - RPC Context supplied by Dex.
   * @param input - Exactly one of a queued message ID and new text.
   * @returns `true` when the message is steered, or queued while no turn is active; `false` when both or neither
   * field is set, the text is blank, the message is no longer queued, or the active turn does not accept steering.
   */
  steerUserMessage(context: Context, input: SteerUserMessageRequest): RPCResult<boolean>;
  /**
   * RPC `delete_queued_message`: deletes one queued user message before it starts a turn.
   *
   * It locks nothing. When the session consumes the message between the RPC's read and its commit, the RPC fails
   * with `ChannelMessageNotFoundError` and commits nothing.
   *
   * @param context - RPC Context supplied by Dex.
   * @param input - {@link PendingUserMessage.messageId} from `get_agent_snapshot`.
   * @returns `true` when the message was deleted; `false` when it is no longer queued.
   */
  deleteQueuedMessage(context: Context, input: DeleteQueuedMessageRequest): RPCResult<boolean>;
  /**
   * RPC `cancel_turn`: stops the running turn now, with no new turn.
   *
   * The RPC cancels the turn's running model call or compaction, and every Step that waits for an approval, an
   * answer, a `durable_wait` Timer, or a remote task's next poll. A cancelled model call's `cancellationSignal`
   * aborts at once and its answer is dropped. A tool call Step that has started runs to the end, and the turn ends
   * after it. The calls that have not started record `rejected` with `cancellationReason` `"turn_cancelled"`. A
   * started remote task is abandoned: the call records `outcome_unknown`, and a runner that implements
   * {@link AgentFrameworkRunner.cancelToolTask} is asked to cancel the task. Then the session waits for the next
   * user message.
   *
   * The RPC locks `TurnCancellationRequest` and `PendingInterrupts`. No long-running Step locks them, so it never
   * waits for a model call. It can fail with `RpcLockConflictError` while a short Step holds a lock, which is safe
   * to retry. A second call for the same turn changes nothing and returns `true`.
   *
   * @param context - RPC Context supplied by Dex.
   * @param input - The turn to stop, or `turnId` `null` for the active turn.
   * @returns `true` when the request is recorded, although a turn whose model call already committed its final
   * answer completes instead; `false` when no turn is active, `turnId` names another turn, or the active turn does
   * not accept cancellation.
   */
  cancelTurn(context: Context, input: CancelTurnRequest): RPCResult<boolean>;
  /**
   * RPC `update_session_settings`: replaces the session's settings from the next turn on.
   *
   * The new settings apply when the next turn starts, the same boundary at which a `planMode` message takes
   * effect. A running turn keeps the settings it started with for every model call, retry, and compaction, so one
   * turn never mixes two models. The settings replace the old ones; nothing is merged, and `{}` clears them, so
   * `get_agent_snapshot` returns `null`.
   *
   * Settings are stored in the session and returned by `get_agent_snapshot`, and the browser snapshot JSON of
   * `@superdurable/dex-agents/ui-stream` includes them. Never put a secret or anything a browser must not see in
   * them; put a reference, such as a credential name, that the Worker resolves. Call the RPC only after every
   * Worker of the Flow type runs dex-agents 0.2.0.
   *
   * The RPC locks nothing. When two updates race, the one that commits last wins. A turn that starts between
   * this RPC's read and its commit runs with the earlier settings, and the next turn gets these.
   *
   * @example
   * ```ts
   * await client.invokeRPC(flow.updateSessionSettings, sessionId, { sessionSettings: { model: "gpt-5" } });
   * ```
   * @param context - RPC Context supplied by Dex.
   * @param input - The complete new settings.
   * @returns `true` when the settings are stored; `false` before `StartAgentSession` committed, or when the
   * settings are `null` (a missing or `null` field), are not a JSON object, or are larger than
   * `MAX_SESSION_SETTINGS_BYTES` plus `SESSION_SETTINGS_SIZE_TOLERANCE_BYTES`.
   */
  updateSessionSettings(context: Context, input: UpdateSessionSettingsRequest): RPCResult<boolean>;
  /**
   * RPC `resolve_interrupt`: approves or rejects one pending tool approval, or declines one question.
   *
   * It removes the interrupt from `PendingInterrupts` and publishes the answer in one commit, so only
   * one call succeeds per interrupt. It locks `PendingInterrupts`, so a call that races
   * `AwaitToolApproval`, `AwaitQuestionAnswer`, `ResumeTurn`, `FinishTurnCancellation`, or `cancel_turn` can fail
   * with `RpcLockConflictError`, which is safe to retry. The rejection message is dropped when `approved` is `true`. A question needs an answer, so
   * `approved` `true` on a `question` interrupt is refused; answer it with {@link answerQuestion}.
   *
   * @param context - RPC Context supplied by Dex.
   * @param input - Interrupt ID and decision.
   * @returns `true` when the interrupt was pending and is now resolved; `false` when it is unknown,
   * already resolved, past its deadline, or a question with `approved` `true`.
   */
  resolveInterrupt(context: Context, input: ResolveInterruptInput): RPCResult<boolean>;
  /**
   * RPC `answer_question`: answers one pending `question` interrupt.
   *
   * The answer becomes the result of the `ask_user` call, `{"status":"answered","answer":"<text>"}`, so
   * the model continues the same turn. For a tool's question, the tool runs again with the answer. The
   * RPC removes the interrupt and publishes the answer in one commit, so only one call succeeds per
   * question. It locks `PendingInterrupts` and can fail with `RpcLockConflictError`, which is safe to
   * retry. Decline a question with {@link resolveInterrupt} and `approved` `false`.
   *
   * @example
   * ```ts
   * const snapshot = await client.invokeRPC(flow.getAgentSnapshot, sessionId);
   * const question = snapshot.pendingInterrupts.find((interrupt) => interrupt.kind === "question");
   * if (question !== undefined) {
   *   await client.invokeRPC(flow.answerQuestion, sessionId, { interruptId: question.interruptId, answer: "blue" });
   * }
   * ```
   * @param context - RPC Context supplied by Dex.
   * @param input - Interrupt ID and answer.
   * @returns `true` when the question was pending and is now answered; `false` when the interrupt is
   * not pending, is not a question, or is past its deadline, and when the answer is blank or is not one
   * of the choices while free text is not allowed.
   */
  answerQuestion(context: Context, input: AnswerQuestionRequest): RPCResult<boolean>;
  /**
   * RPC `get_agent_snapshot`: returns the durable session view a UI renders.
   *
   * It reads committed state without locks, so it also serves a retained closed session. It loads the
   * `AgentMessages` window and every retained `AgentMessageChunks` instance.
   *
   * @param context - RPC Context supplied by Dex.
   * @returns State, the last 200 messages, pending interrupts, the active turn's tool calls, usage, the queued
   * message count, the plan, the queued and steered messages with their Channel message IDs, and the context
   * summary; summarized messages stay in `messages` until retention deletes them. Before `StartAgentSession` commits, `state` is `null` and
   * the lists are empty.
   */
  getAgentSnapshot(context: Context): RPCResult<AgentSnapshot>;
  /**
   * RPC `list_agent_messages`: returns one page of stored messages, oldest first.
   *
   * Start with `beforeSequence` `null` for the newest page, then pass each page's
   * `nextBeforeSequence` to walk back. It also serves a retained closed session. Messages that retention
   * deleted are gone: the oldest page starts at the oldest stored message.
   *
   * @param context - RPC Context supplied by Dex.
   * @param input - Exclusive upper sequence bound, or `null`, and a page size clamped to 1..200.
   * @returns Up to `limit` messages below `beforeSequence` in ascending order, and the bound of the
   * next older page, or `null` when the page starts at the oldest stored message.
   */
  listAgentMessages(context: Context, input: ListAgentMessagesInput): RPCResult<AgentMessagesPage>;
}

/**
 * Creates the shared agent session Flow for one agent and its framework runner.
 *
 * Construct it once per agent at application startup and pass the same object to the Registry and
 * every Client call. Each call creates a distinct Flow with its own Step instances, persistence
 * definitions, and RPC methods, so one Registry can hold several agents under different Flow types.
 * Adapter packages wrap this function; applications usually call the adapter's factory instead.
 *
 * It is a function rather than a class because the Dex Client identifies an RPC by its method
 * function, and two Flows of one class would share their RPC methods.
 *
 * @example
 * ```ts
 * const triage = createAgentSessionFlow(triageRunner, "TriageAgent", {
 *   toolPolicies: { lookup_customer: { effect: "read" } },
 *   interruptDeadlineMs: 24 * 60 * 60 * 1000,
 * });
 * const billing = createAgentSessionFlow(billingRunner, "BillingAgent");
 * const registry = new Registry([triage, billing]);
 * ```
 * @typeParam Runner - Type of the runner, kept as the Flow's `runner` property type.
 * @param runner - Framework runner; the Flow owns no other dependency.
 * @param flowType - Flow type, unique within the Registry, such as `"TriageAgent"`. Keep it stable:
 * running sessions resume by it.
 * @param options - Tool policies, Stream capacity, approval deadline, and tracing. Defaults to all
 * defaults.
 * @returns A new Flow to register in one Registry.
 * @throws {TypeError} When `flowType` is blank, the runner is incomplete, an effect or built-in tool is
 * unknown, a tool policy names an enabled built-in tool, a tracing option is invalid, `planModeEnabled`
 * is set without `write_todos` or for a runner without {@link AgentFrameworkRunner.supportsPlanMode}, or
 * `compaction` is set for a runner without the compaction methods.
 * @throws {RangeError} When `eventStreamCapacityBytes` or `interruptDeadlineMs` is not positive,
 * `maxDurableWaitMs` is not a positive whole number of seconds, or the compaction policy is out of range.
 */
export function createAgentSessionFlow<Runner extends AgentFrameworkRunner<DeferredToolOutcome>>(
  runner: Runner,
  flowType: string,
  options: AgentSessionOptions = {},
): AgentSessionFlow<Runner> {
  const config = resolveAgentSessionConfig(runner, flowType, options);
  const persistence = createAgentSessionPersistence(config.eventStreamCapacityBytes);
  const transitions = new AgentSessionTransitions(
    persistence,
    runner,
    config,
    new AgentSessionTracer(config.tracing, persistence),
  );
  const resolvedOptions: ResolvedAgentSessionOptions = Object.freeze({
    toolPolicies: config.toolPolicies,
    defaultToolEffect: config.defaultToolEffect,
    eventStreamCapacityBytes: config.eventStreamCapacityBytes,
    interruptDeadlineMs: config.interruptDeadlineMs,
    tracing: config.tracing,
    builtInTools: Object.freeze(builtInToolNames.filter((name) => config.builtInTools.has(name))),
    maxDurableWaitMs: config.maxDurableWaitMs,
    planModeEnabled: config.planModeEnabled,
    ...(config.compaction === undefined ? {} : { compaction: config.compaction }),
    messageWindowSize: config.messageWindowSize,
    messageChunkSize: config.messageChunkSize,
    messageChunkMaxBytes: config.messageChunkMaxBytes,
    retainedMessageLimit: config.retainedMessageLimit,
    runModelLimit: config.runModelLimit,
    ...(config.spendLimit === undefined ? {} : { spendLimit: config.spendLimit }),
  });

  // A class per Flow gives each Flow its own RPC methods, which the Dex Client identifies by function.
  class AgentSessionFlowDefinition implements AgentSessionFlow<Runner> {
    public readonly runner = runner;
    public readonly options = resolvedOptions;
    public readonly agentState = persistence.agentState;
    public readonly agentMessages = persistence.agentMessages;
    public readonly agentMessageChunks = persistence.agentMessageChunks;
    public readonly agentMessageIndex = persistence.agentMessageIndex;
    public readonly frameworkRunState = persistence.frameworkRunState;
    public readonly agentToolCalls = persistence.agentToolCalls;
    public readonly pendingInterrupts = persistence.pendingInterrupts;
    public readonly agentUsage = persistence.agentUsage;
    public readonly queuedUserMessages = persistence.queuedUserMessages;
    public readonly interruptResolutions = persistence.interruptResolutions;
    public readonly toolCallResults = persistence.toolCallResults;
    public readonly steeredUserMessages = persistence.steeredUserMessages;
    public readonly turnControlRequests = persistence.turnControlRequests;
    public readonly turnCancellationRequest = persistence.turnCancellationRequest;
    public readonly toolCallCancellations = persistence.toolCallCancellations;
    public readonly agentEvents = persistence.agentEvents;
    public readonly agentTraceContext = persistence.agentTraceContext;
    public readonly agentPlan = persistence.agentPlan;
    public readonly planExecutions = persistence.planExecutions;
    public readonly pendingPlanExecution = persistence.pendingPlanExecution;
    public readonly contextSummary = persistence.contextSummary;
    public readonly sessionSettings = persistence.sessionSettings;
    public readonly turnSessionSettings = persistence.turnSessionSettings;
    /** The `TurnReplySequence` Attribute; see {@link AgentSessionPersistence.turnReplySequence}. */
    public readonly turnReplySequence = persistence.turnReplySequence;
    private readonly steps = StepList.startStep<AgentSessionStartInput>(
      new StartAgentSessionStep(transitions),
    ).otherSteps(
      new AwaitUserMessageStep(transitions),
      new RunModelStep(transitions),
      new CompactContextStep(transitions),
      new AwaitToolApprovalStep(transitions),
      new AwaitQuestionAnswerStep(transitions),
      new AwaitTimerStep(transitions),
      new ToolCallStep(transitions, agentSessionStepTypes.callTool),
      new ToolCallStep(transitions, agentSessionStepTypes.callSingleAttemptTool),
      new PollToolTaskStep(transitions),
      new ToolCallFailureRecordingStep(transitions, agentSessionStepTypes.recordToolFailure, true),
      new ToolCallFailureRecordingStep(
        transitions,
        agentSessionStepTypes.recordToolOutcomeUnknown,
        false,
      ),
      new ResumeTurnStep(transitions),
      new ApplyTurnControlStep(transitions),
      new RecordTurnFailureStep(transitions),
      new FinishTurnCancellationStep(transitions),
      new CancelToolTaskStep(transitions),
    );

    public getFlowType(): string {
      return config.flowType;
    }

    public getSteps(): StepList<AgentSessionStartInput> {
      return this.steps;
    }

    // AgentTraceContext and ContextSummary are always registered, so Workers with and without tracing or
    // compaction serve one Flow type.
    public getPersistenceSchema(): PersistenceSchema {
      return {
        attributes: [
          persistence.agentState,
          persistence.agentMessages,
          persistence.agentMessageChunks,
          persistence.agentMessageIndex,
          persistence.frameworkRunState,
          persistence.agentToolCalls,
          persistence.pendingInterrupts,
          persistence.agentUsage,
          persistence.turnCancellationRequest,
          persistence.agentTraceContext,
          persistence.agentPlan,
          persistence.pendingPlanExecution,
          persistence.contextSummary,
          persistence.sessionSettings,
          persistence.turnSessionSettings,
          persistence.turnReplySequence,
        ],
        channels: [
          persistence.queuedUserMessages,
          persistence.interruptResolutions,
          persistence.toolCallResults,
          persistence.planExecutions,
          persistence.steeredUserMessages,
          persistence.turnControlRequests,
          persistence.toolCallCancellations,
        ],
        streams: [persistence.agentEvents],
      };
    }

    @rpc<AgentUserMessage, boolean>({
      name: "send_user_message",
      inputCodec: agentUserMessageCodec,
      outputCodec: booleanCodec,
      isTransactional: true,
    })
    public sendUserMessage(context: Context, message: AgentUserMessage): RPCResult<boolean> {
      const planMode = message.planMode ?? false;
      if (isBlankText(message.text) || (planMode && !config.planModeEnabled)) {
        return { output: false };
      }
      persistence.queuedUserMessages.publish(context, { text: message.text, planMode });
      return { output: true };
    }

    @rpc<ExecutePlanRequest, boolean>({
      name: "execute_plan",
      inputCodec: executePlanRequestCodec,
      outputCodec: booleanCodec,
      lockAttributes: [persistence.pendingPlanExecution.lock()],
    })
    public executePlan(context: Context, input: ExecutePlanRequest): RPCResult<boolean> {
      const state = transitions.readOptionalAgentState(context);
      const plan = transitions.readOptionalAgentPlan(context);
      const pending = persistence.pendingPlanExecution.get(context);
      if (
        state === null ||
        state.status !== "waiting_for_user_message" ||
        state.activeTurnId !== null ||
        plan === null ||
        plan.revision !== input.revision ||
        !isExecutablePlanStatus(plan.status) ||
        (pending !== undefined && pending.revision === plan.revision) ||
        context.channelSize(persistence.queuedUserMessages) > 0 ||
        context.channelSize(persistence.steeredUserMessages) > 0 ||
        transitions.readPendingInterrupts(context).length > 0
      ) {
        return { output: false };
      }
      const request = { revision: plan.revision };
      persistence.pendingPlanExecution.set(context, request);
      persistence.planExecutions.publish(context, planExecutionKey(plan.revision), request);
      return { output: true };
    }

    @rpc<SteerUserMessageRequest, boolean>({
      name: "steer_user_message",
      inputCodec: steerUserMessageRequestCodec,
      outputCodec: booleanCodec,
      isTransactional: true,
      loadChannels: [persistence.queuedUserMessages],
    })
    public steerUserMessage(context: Context, input: SteerUserMessageRequest): RPCResult<boolean> {
      if ((input.messageId === null) === (input.text === null)) {
        return { output: false };
      }
      const state = transitions.readOptionalAgentState(context);
      if (state?.activeTurnId == null) {
        // Any Worker version waits for queued messages while idle; a steer might wait forever.
        if (input.text === null) {
          return { output: findQueuedMessage(context, persistence, input.messageId ?? "") !== undefined };
        }
        return this.sendUserMessage(context, { text: input.text });
      }
      if (state.acceptsTurnControl !== true) {
        return { output: false };
      }
      let steered: AgentUserMessage;
      if (input.text !== null) {
        if (isBlankText(input.text)) {
          return { output: false };
        }
        steered = { text: input.text };
      } else {
        const queued = findQueuedMessage(context, persistence, input.messageId ?? "");
        if (queued === undefined) {
          return { output: false };
        }
        persistence.queuedUserMessages.delete(context, queued.messageId);
        steered = queued.message;
      }
      persistence.steeredUserMessages.publish(context, steered);
      persistence.turnControlRequests.publish(context, { kind: "steer" });
      return { output: true };
    }

    @rpc<DeleteQueuedMessageRequest, boolean>({
      name: "delete_queued_message",
      inputCodec: deleteQueuedMessageRequestCodec,
      outputCodec: booleanCodec,
      isTransactional: true,
      loadChannels: [persistence.queuedUserMessages],
    })
    public deleteQueuedMessage(context: Context, input: DeleteQueuedMessageRequest): RPCResult<boolean> {
      const queued = findQueuedMessage(context, persistence, input.messageId);
      if (queued === undefined) {
        return { output: false };
      }
      persistence.queuedUserMessages.delete(context, queued.messageId);
      return { output: true };
    }

    // PendingInterrupts orders it with ResumeTurn, and TurnCancellationRequest with turn starts.
    @rpc<CancelTurnRequest, boolean>({
      name: "cancel_turn",
      inputCodec: cancelTurnRequestCodec,
      outputCodec: booleanCodec,
      lockAttributes: [persistence.turnCancellationRequest.lock(), persistence.pendingInterrupts.lock()],
    })
    public cancelTurn(context: Context, input: CancelTurnRequest): RPCResult<boolean> {
      const state = transitions.readOptionalAgentState(context);
      const activeTurnId = state?.activeTurnId ?? null;
      if (
        activeTurnId === null ||
        state?.acceptsTurnControl !== true ||
        (input.turnId !== null && input.turnId !== activeTurnId)
      ) {
        return { output: false };
      }
      if (transitions.isTurnCancelled(context, activeTurnId)) {
        return { output: true };
      }
      persistence.turnCancellationRequest.set(context, {
        turnId: activeTurnId,
        requestedAt: new Date().toISOString(),
      });
      // Each cancelled Step has no outside effect or only waits; the model call's answer is lost.
      return {
        output: true,
        nextSteps: [finishTurnCancellationMovement(activeTurnId)],
        cancelingSteps: [
          agentSessionStepTypes.runModel,
          agentSessionStepTypes.compactContext,
          agentSessionStepTypes.recordTurnFailure,
          agentSessionStepTypes.awaitToolApproval,
          agentSessionStepTypes.awaitQuestionAnswer,
          agentSessionStepTypes.awaitTimer,
          agentSessionStepTypes.pollToolTask,
        ].map((stepType) => new StepRef(stepType)),
      };
    }

    @rpc<UpdateSessionSettingsRequest, boolean>({
      name: "update_session_settings",
      inputCodec: updateSessionSettingsRequestCodec,
      outputCodec: booleanCodec,
      isTransactional: true,
    })
    public updateSessionSettings(context: Context, input: UpdateSessionSettingsRequest): RPCResult<boolean> {
      const sessionSettings = input.sessionSettings;
      if (transitions.readOptionalAgentState(context) === null || sessionSettings === null) {
        return { output: false };
      }
      try {
        checkStoredSessionSettings(sessionSettings);
      } catch {
        return { output: false };
      }
      if (Object.keys(sessionSettings).length > 0) {
        persistence.sessionSettings.set(context, sessionSettings);
      } else if (transitions.readSessionSettings(context) !== null) {
        persistence.sessionSettings.delete(context);
      }
      return { output: true };
    }

    @rpc<ResolveInterruptInput, boolean>({
      name: "resolve_interrupt",
      inputCodec: resolveInterruptInputCodec,
      outputCodec: booleanCodec,
      lockAttributes: [persistence.pendingInterrupts.lock()],
    })
    public resolveInterrupt(context: Context, input: ResolveInterruptInput): RPCResult<boolean> {
      const pendingInterrupts = transitions.readPendingInterrupts(context);
      const interrupt = pendingInterrupts.find((pending) => pending.interruptId === input.interruptId);
      // After the deadline, AwaitToolApproval rejects the call; a late answer must not win.
      if (
        interrupt === undefined ||
        (interrupt.deadline !== null && Date.parse(interrupt.deadline) <= Date.now()) ||
        (interrupt.kind === "question" && input.approved)
      ) {
        return { output: false };
      }
      persistence.pendingInterrupts.set(
        context,
        pendingInterrupts.filter((interrupt) => interrupt.interruptId !== input.interruptId),
      );
      persistence.interruptResolutions.publish(context, input.interruptId, {
        approved: input.approved,
        rejectionMessage: input.approved ? null : input.rejectionMessage,
        answer: null,
      });
      return { output: true };
    }

    @rpc<AnswerQuestionRequest, boolean>({
      name: "answer_question",
      inputCodec: answerQuestionRequestCodec,
      outputCodec: booleanCodec,
      lockAttributes: [persistence.pendingInterrupts.lock()],
    })
    public answerQuestion(context: Context, input: AnswerQuestionRequest): RPCResult<boolean> {
      const pendingInterrupts = transitions.readPendingInterrupts(context);
      const interrupt = pendingInterrupts.find((pending) => pending.interruptId === input.interruptId);
      if (
        interrupt === undefined ||
        interrupt.kind !== "question" ||
        (interrupt.deadline !== null && Date.parse(interrupt.deadline) <= Date.now()) ||
        isBlankText(input.answer) ||
        isAnswerOutsideChoices(interrupt.choices ?? [], interrupt.allowsFreeText ?? true, input.answer)
      ) {
        return { output: false };
      }
      persistence.pendingInterrupts.set(
        context,
        pendingInterrupts.filter((pending) => pending.interruptId !== input.interruptId),
      );
      persistence.interruptResolutions.publish(context, input.interruptId, {
        approved: true,
        rejectionMessage: null,
        answer: input.answer,
      });
      return { output: true };
    }

    @rpc<void, AgentSnapshot>({
      name: "get_agent_snapshot",
      outputCodec: agentSnapshotCodec,
      loadAttributeMaps: [persistence.agentMessages, persistence.agentMessageChunks, persistence.agentToolCalls],
      loadChannels: [persistence.queuedUserMessages, persistence.steeredUserMessages],
    })
    public getAgentSnapshot(context: Context): RPCResult<AgentSnapshot> {
      const state = transitions.readOptionalAgentState(context);
      if (state === null) {
        return {
          output: {
            state: null,
            messages: [],
            pendingInterrupts: [],
            toolCalls: [],
            usage: transitions.readAgentUsage(context),
            queuedMessageCount: 0,
            plan: null,
            queuedMessages: [],
            steeredMessages: [],
            contextSummary: null,
            sessionSettings: null,
          },
        };
      }
      const end = state.nextMessageSequence;
      return {
        output: {
          state,
          messages: transitions.readMessages(context, end - snapshotMessageLimit, end),
          pendingInterrupts: transitions.readPendingInterrupts(context),
          toolCalls: transitions.readTurnToolCalls(context, state.activeTurnId),
          usage: transitions.readAgentUsage(context),
          queuedMessageCount: context.channelSize(persistence.queuedUserMessages),
          plan: transitions.readOptionalAgentPlan(context),
          queuedMessages: readPendingUserMessages(context, persistence.queuedUserMessages),
          steeredMessages: readPendingUserMessages(context, persistence.steeredUserMessages),
          contextSummary: transitions.readContextSummary(context),
          sessionSettings: transitions.readSessionSettings(context),
        },
      };
    }

    @rpc<ListAgentMessagesInput, AgentMessagesPage>({
      name: "list_agent_messages",
      inputCodec: listAgentMessagesInputCodec,
      outputCodec: agentMessagesPageCodec,
      loadAttributeMaps: [persistence.agentMessages, persistence.agentMessageChunks],
    })
    public listAgentMessages(
      context: Context,
      input: ListAgentMessagesInput,
    ): RPCResult<AgentMessagesPage> {
      const state = transitions.readOptionalAgentState(context);
      if (state === null) {
        return { output: { messages: [], nextBeforeSequence: null, deletedBeforeSequence: null } };
      }
      const limit = Math.max(1, Math.min(input.limit, messagePageLimit));
      const firstSequence = transitions.readFirstStoredSequence(context);
      let end = state.nextMessageSequence;
      if (input.beforeSequence !== null) {
        end = Math.max(firstSequence, Math.min(input.beforeSequence, end));
      }
      const start = Math.max(firstSequence, end - limit);
      return {
        output: {
          messages: transitions.readMessages(context, start, end),
          nextBeforeSequence: start > firstSequence ? start : null,
          deletedBeforeSequence: firstSequence > 1 ? firstSequence : null,
        },
      };
    }
  }

  return new AgentSessionFlowDefinition();
}

function resolveAgentSessionConfig(
  runner: AgentFrameworkRunner<DeferredToolOutcome>,
  flowType: string,
  options: AgentSessionOptions,
): AgentSessionConfig {
  if (
    typeof runner?.buildUserMessage !== "function" ||
    typeof runner.runModelStep !== "function" ||
    typeof runner.executeDeferredToolCall !== "function"
  ) {
    throw new TypeError("createAgentSessionFlow requires an AgentFrameworkRunner");
  }
  if (typeof flowType !== "string" || flowType.trim() === "") {
    throw new TypeError("flowType must be a non-blank string");
  }
  const defaultToolEffect = options.defaultToolEffect ?? "unclassified";
  requireToolEffect(defaultToolEffect, "defaultToolEffect");
  const toolPolicies: Record<string, ToolPolicy> = {};
  for (const [toolName, policy] of Object.entries(options.toolPolicies ?? {})) {
    requireToolEffect(policy.effect, `tool policy ${toolName}`);
    toolPolicies[toolName] = Object.freeze({ ...policy });
  }
  const eventStreamCapacityBytes =
    options.eventStreamCapacityBytes ?? defaultAgentEventsCapacityBytes;
  if (!Number.isSafeInteger(eventStreamCapacityBytes) || eventStreamCapacityBytes <= 0) {
    throw new RangeError("eventStreamCapacityBytes must be a positive integer");
  }
  const interruptDeadlineMs = options.interruptDeadlineMs;
  if (
    interruptDeadlineMs !== undefined &&
    (!Number.isFinite(interruptDeadlineMs) || interruptDeadlineMs <= 0)
  ) {
    throw new RangeError("interruptDeadlineMs must be positive");
  }
  const builtInTools = new Set<BuiltInTool>();
  for (const builtInTool of options.builtInTools ?? []) {
    if (!(builtInToolNames as readonly string[]).includes(builtInTool)) {
      throw new TypeError(`builtInTools has unknown built-in tool ${String(builtInTool)}`);
    }
    builtInTools.add(builtInTool);
  }
  for (const toolName of Object.keys(toolPolicies)) {
    if (builtInTools.has(toolName as BuiltInTool)) {
      throw new TypeError(`tool policy ${toolName} names an enabled built-in tool, whose policy is fixed`);
    }
  }
  const planModeEnabled = options.planModeEnabled ?? false;
  if (planModeEnabled && !builtInTools.has("write_todos")) {
    throw new TypeError("planModeEnabled requires the write_todos built-in tool");
  }
  if (planModeEnabled && runner.supportsPlanMode !== true) {
    throw new TypeError("planModeEnabled requires a runner that supports plan mode");
  }
  const maxDurableWaitMs = options.maxDurableWaitMs ?? defaultMaxDurableWaitMs;
  if (!Number.isSafeInteger(maxDurableWaitMs) || maxDurableWaitMs < 1_000 || maxDurableWaitMs % 1_000 !== 0) {
    throw new RangeError("maxDurableWaitMs must be a positive whole number of seconds, in milliseconds");
  }
  const compaction = options.compaction === undefined ? undefined : resolveCompactionPolicy(options.compaction);
  if (compaction !== undefined && !isContextCompactionRunner(runner)) {
    throw new TypeError(
      "compaction needs a runner that implements estimateInputTokens, selectCompactionCutoff, and summarizeContext",
    );
  }
  const messageChunkSize = options.messageChunkSize ?? defaultMessageChunkSize;
  if (!Number.isSafeInteger(messageChunkSize) || messageChunkSize <= 0) {
    throw new RangeError("messageChunkSize must be positive");
  }
  const messageWindowSize = options.messageWindowSize ?? defaultMessageWindowSize;
  if (!Number.isSafeInteger(messageWindowSize) || messageWindowSize < messageChunkSize) {
    throw new RangeError("messageWindowSize must be at least messageChunkSize");
  }
  const messageChunkMaxBytes = options.messageChunkMaxBytes ?? defaultMessageChunkMaxBytes;
  if (
    !Number.isSafeInteger(messageChunkMaxBytes) ||
    messageChunkMaxBytes < 1 ||
    messageChunkMaxBytes > messageRolloverMaxBytesPerCommit
  ) {
    throw new RangeError(`messageChunkMaxBytes must be between 1 and ${messageRolloverMaxBytesPerCommit}`);
  }
  const retainedMessageLimit =
    options.retainedMessageLimit === undefined ? defaultRetainedMessageLimit : options.retainedMessageLimit;
  if (retainedMessageLimit !== null && (!Number.isSafeInteger(retainedMessageLimit) || retainedMessageLimit < 0)) {
    throw new RangeError("retainedMessageLimit must not be negative");
  }
  return {
    flowType,
    toolPolicies: Object.freeze(toolPolicies),
    defaultToolEffect,
    eventStreamCapacityBytes,
    interruptDeadlineMs,
    tracing: resolveAgentTracingOptions(flowType, options.tracing ?? {}),
    builtInTools,
    maxDurableWaitMs,
    planModeEnabled,
    compaction,
    messageWindowSize,
    messageChunkSize,
    messageChunkMaxBytes,
    retainedMessageLimit,
    runModelLimit: resolveRunModelLimit(options.runModelLimit),
    spendLimit: options.spendLimit,
  };
}

function resolveAgentTracingOptions(
  flowType: string,
  tracing: AgentTracingOptions,
): ResolvedAgentTracingOptions {
  const agentName = tracing.agentName ?? flowType;
  if (typeof agentName !== "string" || agentName.trim() === "") {
    throw new TypeError("tracing.agentName must be a non-blank string");
  }
  const semanticConventionsVersion = tracing.semanticConventionsVersion ?? "1.42.0-dev";
  if (!Object.hasOwn(genAISemanticConventionsSchemaUrls, semanticConventionsVersion)) {
    throw new TypeError(`tracing.semanticConventionsVersion ${String(semanticConventionsVersion)} is unknown`);
  }
  const tracerProvider = tracing.tracerProvider;
  if (tracerProvider !== undefined && typeof tracerProvider?.getTracer !== "function") {
    throw new TypeError("tracing.tracerProvider must be an OpenTelemetry TracerProvider");
  }
  return Object.freeze({
    isEnabled: tracing.isEnabled ?? true,
    tracerProvider,
    agentName,
    shouldCaptureMessageContent: tracing.shouldCaptureMessageContent ?? false,
    semanticConventionsVersion,
  });
}

function readPendingUserMessages(context: Context, channel: Channel<AgentUserMessage>): PendingUserMessage[] {
  return channel
    .pendingMessages(context)
    .map((pending) => ({ messageId: pending.messageId, message: pending.value }));
}

function findQueuedMessage(
  context: Context,
  persistence: AgentSessionPersistence,
  messageId: string,
): PendingUserMessage | undefined {
  return readPendingUserMessages(context, persistence.queuedUserMessages).find(
    (queued) => queued.messageId === messageId,
  );
}

function isAnswerOutsideChoices(choices: readonly string[], allowsFreeText: boolean, answer: string): boolean {
  return choices.length > 0 && !allowsFreeText && !choices.includes(answer);
}

function requireToolEffect(effect: ToolEffect, description: string): void {
  if (!toolEffects.includes(effect)) {
    throw new TypeError(`${description} has unknown tool effect ${String(effect)}`);
  }
}
