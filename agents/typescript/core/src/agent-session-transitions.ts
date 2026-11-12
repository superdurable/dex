// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  AttributeMapNotLoadedError,
  StepMovement,
  StepRef,
  goTo,
  goToMany,
  stringCodec,
  type AsyncContext,
  type Context,
  type RecoveryErrorInfo,
  type StepDecision,
} from "@superdurable/dex";

import type { BuiltInTool } from "./agent-built-in-tools.js";
import { agentSessionStepTypes } from "./agent-session-step-types.js";
import { isContextCompactionRunner, type ResolvedCompactionPolicy } from "./agent-context-compaction.js";
import { StreamAgentEventWriter } from "./agent-event-writer.js";
import { encodeAgentMessage } from "./agent-session-codecs.js";
import { validateImageReferences } from "./agent-media.js";
import {
  resolveToolPolicy,
  type AgentFrameworkRunner,
  type AgentTurnContext,
  type DeferredToolOutcome,
  type ResolvedToolPolicy,
  type ToolPolicy,
} from "./agent-framework-runner.js";
import { messageSequenceKey, type AgentSessionPersistence } from "./agent-session-persistence.js";
import { buildSessionTitle } from "./agent-session-summary.js";
import {
  modelCallRefusedErrorPrefix,
  withinSpendLimitCallTimeout,
  type ModelSpendLimit,
} from "./agent-session-spend-limit.js";
import { messageStepOptions, type MessageChunkLoads, type ResolvedRunModelLimit } from "./agent-session-step-options.js";
import type { AgentSessionTracer } from "./agent-session-tracer.js";
import {
  addAgentUsage,
  buildToolCallNote,
  emptyAgentUsage,
  isFinalToolCallStatus,
  type AgentInterrupt,
  type AgentInterruptKind,
  type AgentMessage,
  type AgentMessageChunk,
  type AgentMessageChunkRange,
  type AgentMessageIndex,
  type AgentPlan,
  type AgentState,
  type AgentToolCall,
  type AgentToolCallRecord,
  type AgentToolCallStatus,
  type AgentUsage,
  type AgentUserMessage,
  type ContextSummary,
  type SessionSettings,
  type TurnSessionSettings,
  type FrameworkMessage,
  type InteractionMode,
  type ToolCallCancellationReason,
  type ToolEffect,
  toolCallCancelledBySteeringResult,
  toolCallCancelledByTurnCancellationResult,
  turnCancelledNoteHeader,
  turnSteeredNoteHeader,
  type TurnCancellationRequest,
  type TurnReplySequence,
} from "./agent-session-values.js";
import type { ResolvedAgentTracingOptions } from "./agent-tracing-options.js";

export { agentSessionStepTypes } from "./agent-session-step-types.js";


/** Most archive chunks one commit writes, so a large 0.1.0 history migrates over several commits. */
export const messageRolloverMaxChunksPerCommit = 4;
/**
 * Most chunk bytes one commit writes, 8 MiB, which keeps a Step result far under the engine's frame limit. A
 * single chunk larger than it still moves, alone.
 */
export const messageRolloverMaxBytesPerCommit = 8_388_608;
// `{"messages":[` and `]}` around the comma-separated messages.
const messageChunkOverheadBytes = 15;
const utf8Encoder = new TextEncoder();
/** Consumes every pending message of a Channel at once; the plan's MAX. */
export const allPendingMessages = 2_147_483_647;

/** The model-visible result of a call that steering or `cancel_turn` abandoned, by reason. */
export const toolCallCancellationResults: Readonly<Record<ToolCallCancellationReason, string>> = {
  steered: toolCallCancelledBySteeringResult,
  turn_cancelled: toolCallCancelledByTurnCancellationResult,
};

/** Fields of one `interrupt_resolved` event. */
export interface InterruptResolvedEvent {
  readonly interruptId: string;
  readonly kind: AgentInterruptKind;
  readonly isApproved: boolean;
  readonly isAnswered: boolean;
  readonly rejectionMessage: string | null;
  readonly cancellationReason?: ToolCallCancellationReason;
}

// Keeps liveness well inside the shortest heartbeat timeout, one minute.
const heartbeatIntervalMs = 20_000;

export interface AgentSessionConfig {
  readonly flowType: string;
  readonly toolPolicies: Readonly<Record<string, ToolPolicy>>;
  readonly defaultToolEffect: ToolEffect;
  readonly eventStreamCapacityBytes: number;
  readonly interruptDeadlineMs: number | undefined;
  readonly tracing: ResolvedAgentTracingOptions;
  readonly builtInTools: ReadonlySet<BuiltInTool>;
  readonly maxDurableWaitMs: number;
  readonly planModeEnabled: boolean;
  readonly compaction: ResolvedCompactionPolicy | undefined;
  readonly messageWindowSize: number;
  readonly messageChunkSize: number;
  readonly messageChunkMaxBytes: number;
  readonly retainedMessageLimit: number | null;
  readonly runModelLimit: ResolvedRunModelLimit;
  readonly spendLimit: ModelSpendLimit | undefined;
}

/** Shared state reads, writes, and routing used by every session Step. */
export class AgentSessionTransitions {
  public constructor(
    public readonly persistence: AgentSessionPersistence,
    public readonly runner: AgentFrameworkRunner<DeferredToolOutcome>,
    public readonly config: AgentSessionConfig,
    public readonly tracer: AgentSessionTracer,
  ) {}

  public readOptionalAgentState(context: Context): AgentState | null {
    return (this.persistence.agentState.get(context) as AgentState | undefined) ?? null;
  }

  public readAgentState(context: Context): AgentState {
    const state = this.readOptionalAgentState(context);
    if (state === null) {
      throw new Error(`the agent session ${context.flowId} is not initialized`);
    }
    return state;
  }

  public writeAgentState(context: Context, state: AgentState): void {
    this.persistence.agentState.set(context, state);
  }

  /** Writes the session's last activity time and, when it changed, its turn count. */
  public recordSessionActivity(context: Context, turnCount: number): void {
    // The Step's scheduled time comes from Dex and stays the same across retries.
    this.persistence.agentSessionLastActivityAt.set(context, context.firstAttemptAt.toISOString());
    if (this.persistence.agentSessionTurnCount.get(context) !== turnCount) {
      this.persistence.agentSessionTurnCount.set(context, turnCount);
    }
  }

  /** Writes the session's title from its first user message, unless it already has one. */
  public recordSessionTitle(context: Context, firstMessageText: string): void {
    const title = buildSessionTitle(firstMessageText);
    if (title !== null && this.persistence.agentSessionTitle.get(context) === undefined) {
      this.persistence.agentSessionTitle.set(context, title);
    }
  }

  public readPendingInterrupts(context: Context): readonly AgentInterrupt[] {
    return (this.persistence.pendingInterrupts.get(context) as readonly AgentInterrupt[] | undefined) ?? [];
  }

  public readOptionalAgentPlan(context: Context): AgentPlan | null {
    return (this.persistence.agentPlan.get(context) as AgentPlan | undefined) ?? null;
  }

  public readAgentUsage(context: Context): AgentUsage {
    return (this.persistence.agentUsage.get(context) as AgentUsage | undefined) ?? emptyAgentUsage();
  }

  /** The settings the next turn starts with, or `null` when the session has none. */
  public readSessionSettings(context: Context): SessionSettings | null {
    return (this.persistence.sessionSettings.get(context) as SessionSettings | undefined) ?? null;
  }

  /** The settings the active turn started with; see {@link selectTurnSessionSettings}. */
  public readTurnSessionSettings(context: Context): SessionSettings {
    return selectTurnSessionSettings(
      (this.persistence.turnSessionSettings.get(context) as TurnSessionSettings | undefined) ?? null,
      this.readOptionalAgentState(context)?.activeTurnId ?? null,
    );
  }

  public readContextSummary(context: Context): ContextSummary | null {
    return (this.persistence.contextSummary.get(context) as ContextSummary | undefined) ?? null;
  }

  /** The summary the runner sees; a runner without the compaction methods keeps every message instead. */
  public readRunnerContextSummary(context: Context): ContextSummary | null {
    return isContextCompactionRunner(this.runner) ? this.readContextSummary(context) : null;
  }

  public readFrameworkRunState(context: Context): string | null {
    const runState = this.persistence.frameworkRunState.get(context) as string | undefined;
    return runState === undefined || runState === "" ? null : runState;
  }

  public readMessageIndex(context: Context): AgentMessageIndex | null {
    return (this.persistence.agentMessageIndex.get(context) as AgentMessageIndex | undefined) ?? null;
  }

  /** Sequence of the oldest stored message: retention deleted the earlier ones. */
  public readFirstStoredSequence(context: Context): number {
    return this.readMessageIndex(context)?.firstSequence ?? 1;
  }

  /**
   * Returns the stored messages with sequences in [start, end), from the `AgentMessages` window and the
   * `AgentMessageChunks` instances. It requires the whole window and every chunk it reads to be loaded.
   */
  public readMessages(context: Context, start: number, end: number): AgentMessage[] {
    const index = this.readMessageIndex(context);
    const messages: AgentMessage[] = [];
    const chunks = new Map<number, readonly AgentMessage[]>();
    for (let sequence = Math.max(index?.firstSequence ?? 1, start); sequence < end; sequence += 1) {
      const range = index === null ? undefined : findMessageChunkRange(index, sequence);
      if (range === undefined) {
        messages.push(this.readWindowMessage(context, sequence));
        continue;
      }
      let chunk = chunks.get(range.firstSequence);
      if (chunk === undefined) {
        chunk = this.readChunkMessages(context, range);
        chunks.set(range.firstSequence, chunk);
      }
      messages.push(chunk[sequence - range.firstSequence] as AgentMessage);
    }
    return messages;
  }

  private readWindowMessage(context: Context, sequence: number): AgentMessage {
    const message = this.persistence.agentMessages.get(context, messageSequenceKey(sequence)) as
      | AgentMessage
      | undefined;
    if (message === undefined) {
      throw new Error(`AgentMessages has no message ${sequence} in session ${context.flowId}`);
    }
    return message;
  }

  /** Reads one loaded chunk and checks that it holds exactly the messages its range names. */
  private readChunkMessages(context: Context, range: AgentMessageChunkRange): readonly AgentMessage[] {
    const key = messageSequenceKey(range.firstSequence);
    let chunk: AgentMessageChunk | undefined;
    try {
      chunk = this.persistence.agentMessageChunks.get(context, key) as AgentMessageChunk | undefined;
    } catch (error: unknown) {
      if (error instanceof AttributeMapNotLoadedError) {
        throw new Error(`AgentMessageChunks instance ${key} is not loaded`);
      }
      throw error;
    }
    if (chunk === undefined) {
      throw new Error(`AgentMessageChunks instance ${key} is not loaded`);
    }
    if (!isMessageChunkOfRange(chunk, range)) {
      throw new Error(
        `AgentMessageChunks instance ${key} does not hold messages ${range.firstSequence} through ${range.lastSequence}`,
      );
    }
    return chunk.messages;
  }

  /**
   * Counts the active turn's stored messages after the user messages that started it, before this Step's
   * appends. A turn without its `TurnReplySequence`, such as one a 0.1.0 Worker started, is counted from its
   * stored messages instead.
   */
  public countStoredTurnReplies(context: Context, state: AgentState, turnId: string): number {
    return state.nextMessageSequence - this.readTurnReplySequence(context, state, turnId);
  }

  /** Returns the first sequence after the turn's user messages, from `TurnReplySequence` or the stored messages. */
  private readTurnReplySequence(context: Context, state: AgentState, turnId: string): number {
    const stored = this.persistence.turnReplySequence.get(context) as TurnReplySequence | undefined;
    if (stored !== undefined && stored.turnId === turnId) {
      return stored.sequence;
    }
    const turnMessages = this.readStoredTurnMessages(context, state, turnId);
    return state.nextMessageSequence - countMessagesAfterUserMessages(turnMessages, turnId);
  }

  /**
   * Returns the turn's newest stored messages, read back until another turn's, the oldest stored message, or a
   * chunk this Step did not load.
   */
  private readStoredTurnMessages(context: Context, state: AgentState, turnId: string): AgentMessage[] {
    const index = this.readMessageIndex(context);
    const turnMessages: AgentMessage[] = [];
    for (let sequence = state.nextMessageSequence - 1; sequence >= (index?.firstSequence ?? 1); sequence -= 1) {
      const message = this.readLoadedMessage(context, index, sequence);
      if (message === undefined || message.turnId !== turnId) {
        break;
      }
      turnMessages.push(message);
    }
    return turnMessages.reverse();
  }

  /** Reads one stored message from the window or its chunk, or returns `undefined` when its chunk is not loaded. */
  private readLoadedMessage(context: Context, index: AgentMessageIndex | null, sequence: number): AgentMessage | undefined {
    const range = index === null ? undefined : findMessageChunkRange(index, sequence);
    if (range === undefined) {
      return this.readWindowMessage(context, sequence);
    }
    try {
      return this.readChunkMessages(context, range)[sequence - range.firstSequence];
    } catch (error: unknown) {
      if (error instanceof Error && error.message.endsWith("is not loaded")) {
        return undefined;
      }
      throw error;
    }
  }

  /** First sequence of the model input: after the runner's summary, and never before the oldest stored message. */
  public readModelInputFirstSequence(context: Context, summary: ContextSummary | null): number {
    return Math.max(this.readFirstStoredSequence(context), summary === null ? 1 : summary.lastSequence + 1);
  }

  /**
   * Returns the archive chunks that hold messages in [start, end): every chunk when compaction is off, since
   * the model input then covers them all, else exact instances.
   */
  public messageChunkLoads(context: Context, start: number, end: number = Number.MAX_SAFE_INTEGER): MessageChunkLoads {
    const index = this.readMessageIndex(context);
    if (index === null) {
      return { isWholeMap: false, instances: [] };
    }
    const instances = index.chunks
      .filter((range) => range.lastSequence >= start && range.firstSequence < end)
      .map((range) => this.persistence.agentMessageChunks.load(messageSequenceKey(range.firstSequence)));
    return { isWholeMap: false, instances };
  }

  /** The chunk loads of a Step that rebuilds the model input. */
  public modelInputChunkLoads(context: Context, summary: ContextSummary | null): MessageChunkLoads {
    if (this.config.compaction === undefined) {
      return { isWholeMap: true, instances: [] };
    }
    return this.messageChunkLoads(context, this.readModelInputFirstSequence(context, summary));
  }

  /** Moves to `RunModel`, loading the window and the archive chunks of the model input. */
  public goToRunModel(context: Context, summary: ContextSummary | null = this.readRunnerContextSummary(context)): StepDecision {
    return this.goToModelInputStep(context, agentSessionStepTypes.runModel, summary);
  }

  /** Moves to `ApplyTurnControl`, which may rebuild the model input for unfinished-turn messages. */
  public goToApplyTurnControl(context: Context): StepDecision {
    return this.goToModelInputStep(context, agentSessionStepTypes.applyTurnControl, this.readRunnerContextSummary(context));
  }

  private goToModelInputStep(context: Context, stepType: string, summary: ContextSummary | null): StepDecision {
    const options = messageStepOptions(
      this.persistence,
      stepType,
      this.modelInputChunkLoads(context, summary),
      this.config.runModelLimit,
    );
    return goToMany(StepMovement.of(new StepRef(stepType), undefined, options));
  }

  /** Returns the turn's tool call records in deferral order; requires the AgentToolCalls load. */
  public readTurnToolCalls(context: Context, turnId: string | null): AgentToolCallRecord[] {
    if (turnId === null) {
      return [];
    }
    const toolCalls = this.persistence.agentToolCalls;
    return toolCalls
      .getAllInstanceKeys(context)
      .map((key) => toolCalls.get(context, key))
      .filter((toolCall) => toolCall.turnId === turnId)
      .sort((left, right) => left.deferralOrder - right.deferralOrder);
  }

  /** Returns one tool call record; requires the AgentToolCalls load. */
  public readToolCall(context: Context, toolCallId: string): AgentToolCallRecord {
    const toolCall = this.persistence.agentToolCalls.get(context, toolCallId) as AgentToolCallRecord | undefined;
    if (toolCall === undefined) {
      throw new Error(`tool call ${toolCallId} has no record in session ${context.flowId}`);
    }
    return toolCall;
  }

  /** Tells `ResumeTurn` that one tool call of the batch has a final record. */
  public publishToolCallResult(context: Context, toolCallId: string): void {
    this.persistence.toolCallResults.publish(context, { toolCallId });
  }

  public writeToolCall(context: Context, toolCall: AgentToolCallRecord): void {
    this.persistence.agentToolCalls.set(context, toolCall.toolCallId, toolCall);
  }

  /**
   * Writes a final record, its `tool_call_finished` event, and its `ToolCallResults` notice in one commit.
   * Pass the Step's open events writer, if it has one, so the event keeps its order.
   */
  public finishToolCall(context: Context, toolCall: AgentToolCallRecord, events?: StreamAgentEventWriter): void {
    if (!isFinalToolCallStatus(toolCall.status)) {
      throw new TypeError(`tool call ${toolCall.toolCallId} is not final: ${toolCall.status}`);
    }
    this.writeToolCall(context, toolCall);
    const finalStatus = toolCall.status as AgentToolCallStatus;
    if (events === undefined) {
      this.writeEvents(context, toolCall.turnId, (writer) =>
        writer.writeToolCallFinished(toolCall.toolCallId, toolCall.toolName, finalStatus),
      );
    } else {
      events.writeToolCallFinished(toolCall.toolCallId, toolCall.toolName, finalStatus);
    }
    this.publishToolCallResult(context, toolCall.toolCallId);
  }

  /** Records a queued call final; `ResumeTurn` never waited for it, so no result is published. */
  public finishUnscheduledToolCall(context: Context, toolCall: AgentToolCallRecord, events: StreamAgentEventWriter): void {
    if (!isFinalToolCallStatus(toolCall.status)) {
      throw new TypeError(`tool call ${toolCall.toolCallId} is not final: ${toolCall.status}`);
    }
    this.writeToolCall(context, toolCall);
    events.writeToolCallFinished(toolCall.toolCallId, toolCall.toolName, toolCall.status as AgentToolCallStatus);
  }

  public writeInterruptRaised(events: StreamAgentEventWriter, interrupt: AgentInterrupt): void {
    events.writeEvent("interrupt_raised", {
      interrupt_id: interrupt.interruptId,
      kind: interrupt.kind,
      tool_call_id: interrupt.toolCallId,
      tool_name: interrupt.toolName,
      deadline: interrupt.deadline,
      question: interrupt.question ?? null,
      choices: interrupt.choices ?? [],
      allows_free_text: interrupt.allowsFreeText ?? true,
    });
  }

  public writeInterruptResolved(context: Context, turnId: string, resolution: InterruptResolvedEvent): void {
    this.writeEvents(context, turnId, (events) =>
      events.writeEvent("interrupt_resolved", {
        interrupt_id: resolution.interruptId,
        kind: resolution.kind,
        approved: resolution.isApproved,
        answered: resolution.isAnswered,
        rejection_message: resolution.rejectionMessage,
        cancellation_reason: resolution.cancellationReason ?? null,
      }),
    );
  }

  /** Writes the `interrupt_resolved` event of an interrupt that steering or `cancel_turn` removed. */
  public writeInterruptCancelled(
    context: Context,
    turnId: string,
    interruptId: string,
    kind: AgentInterruptKind,
    reason: ToolCallCancellationReason,
  ): void {
    this.writeInterruptResolved(context, turnId, {
      interruptId,
      kind,
      isApproved: false,
      isAnswered: false,
      rejectionMessage: toolCallCancellationResults[reason],
      cancellationReason: reason,
    });
  }

  /**
   * Returns why the turn ends at this boundary, or `null`. It reads committed state only: a matching
   * `TurnCancellationRequest`, then any pending steered message.
   */
  public readTurnControlReason(context: Context, turnId: string | null): ToolCallCancellationReason | null {
    if (turnId === null) {
      return null;
    }
    const request = this.persistence.turnCancellationRequest.get(context) as TurnCancellationRequest | undefined;
    // Turn IDs never repeat, so a request for an earlier turn never matches.
    if (request !== undefined && request.turnId === turnId) {
      return "turn_cancelled";
    }
    return context.channelSize(this.persistence.steeredUserMessages) > 0 ? "steered" : null;
  }

  /** Builds the runner's view of a turn; every record must be final. */
  public createTurnContext(
    context: Context,
    state: AgentState,
    turnId: string,
    toolCalls: readonly AgentToolCallRecord[],
    events: StreamAgentEventWriter,
    heartbeat: () => Promise<void>,
  ): AgentTurnContext {
    const runnerToolCalls = toolCalls.map(toRunnerToolCall);
    const toolCallsById = new Map(runnerToolCalls.map((toolCall) => [toolCall.toolCallId, toolCall]));
    const frameworkRunState = this.readFrameworkRunState(context);
    const contextSummary = this.readRunnerContextSummary(context);
    const firstStoredSequence = this.readFirstStoredSequence(context);
    if (contextSummary === null && firstStoredSequence > 1) {
      console.warn(
        `dex-agents: session ${context.flowId} stores no messages before ${firstStoredSequence}; retention ` +
          "deleted them, and this runner cannot use the context summary that covers them",
      );
    }
    return {
      flowId: context.flowId,
      turnId,
      attempt: context.attempt,
      stepExecutionId: context.stepExecutionId,
      messages: this.readMessages(
        context,
        this.readModelInputFirstSequence(context, contextSummary),
        state.nextMessageSequence,
      ),
      frameworkRunState,
      isResuming: frameworkRunState !== null,
      toolCalls: runnerToolCalls,
      events,
      cancellationSignal: context.cancellationSignal,
      deadline: context.deadline,
      enabledBuiltInTools: this.config.builtInTools,
      interactionMode: state.interactionMode ?? "chat",
      plan: this.readOptionalAgentPlan(context),
      contextSummary,
      sessionSettings: this.readTurnSessionSettings(context),
      toolPolicies: this.config.toolPolicies,
      defaultToolEffect: this.config.defaultToolEffect,
      resolveToolPolicy: (toolName, frameworkEffect) => this.resolveToolPolicy(toolName, frameworkEffect),
      recordedToolCall: (toolCallId) => toolCallsById.get(toolCallId),
      heartbeat,
    };
  }

  /**
   * Returns the runner's unfinished-turn messages for an ending turn, or `null` when the runner has no such
   * method or it throws, so the caller stores its note instead.
   */
  public buildUnfinishedTurnMessages(
    context: Context,
    state: AgentState,
    turnId: string,
    finalToolCalls: readonly AgentToolCallRecord[],
  ): readonly FrameworkMessage[] | null {
    if (this.runner.buildUnfinishedTurnMessages === undefined) {
      return null;
    }
    const events = this.createEventWriter(context, turnId);
    try {
      const turnContext = this.createTurnContext(context, state, turnId, finalToolCalls, events, () =>
        Promise.resolve(),
      );
      const messages: unknown = this.runner.buildUnfinishedTurnMessages(turnContext);
      if (!Array.isArray(messages) || !messages.every(isFrameworkMessage)) {
        throw new TypeError("buildUnfinishedTurnMessages must return an array of framework messages");
      }
      messages.forEach((message) => validateImageReferences(message.images ?? []));
      return messages;
    } catch (error: unknown) {
      // A runner defect must not block the end of the turn; the caller's note still tells the model.
      console.warn(`dex-agents: no unfinished-turn messages are stored for ${turnId}:`, error);
      return null;
    } finally {
      events.close();
    }
  }

  public isEnabledBuiltInTool(toolName: string): toolName is BuiltInTool {
    return this.config.builtInTools.has(toolName as BuiltInTool);
  }

  /**
   * Appends messages, writes the advanced AgentState, and returns it.
   *
   * A message goes into the `AgentMessages` window, or, when its own chunk would exceed `messageChunkMaxBytes`,
   * straight into a chunk of its own, so the first store writes the largest value it ever needs. In the same
   * commit it moves the oldest window messages into archive chunks while the window is over its size and
   * applies retention; it writes `AgentMessageIndex` only when one of them changed it. It requires the whole
   * window to be loaded. With `startsTurn`, `TurnReplySequence` records where the turn's replies begin.
   */
  public appendMessages(
    context: Context,
    state: AgentState,
    turnId: string,
    frameworkMessages: readonly FrameworkMessage[],
    startsTurn = false,
  ): AgentState {
    if (frameworkMessages.length === 0 && !startsTurn) {
      this.writeAgentState(context, state);
      return state;
    }
    // A runner's images fail the Step here, before any message of the commit is written.
    frameworkMessages.forEach((message) => validateImageReferences(message.images ?? []));
    const activeTurnId = state.activeTurnId;
    if (!startsTurn && activeTurnId !== null && activeTurnId !== undefined) {
      const stored = this.persistence.turnReplySequence.get(context) as TurnReplySequence | undefined;
      if (stored === undefined || stored.turnId !== activeTurnId) {
        // Recorded before a rollover can move the turn's first messages out of reach.
        this.persistence.turnReplySequence.set(context, {
          turnId: activeTurnId,
          sequence: this.readTurnReplySequence(context, state, activeTurnId),
        });
      }
    }
    const storedIndex = this.readMessageIndex(context);
    let index = storedIndex ?? emptyMessageIndex();
    const directChunks: AgentMessageChunkRange[] = [];
    let nextMessageSequence = state.nextMessageSequence;
    const createdAt = new Date().toISOString();
    for (const frameworkMessage of frameworkMessages) {
      const message: AgentMessage = {
        sequence: nextMessageSequence,
        turnId,
        role: frameworkMessage.role,
        text: frameworkMessage.text,
        toolCallId: frameworkMessage.toolCallId,
        toolName: frameworkMessage.toolName,
        toolArguments: frameworkMessage.toolArguments,
        toolOutput: frameworkMessage.toolOutput,
        frameworkItem: frameworkMessage.frameworkItem,
        createdAt,
        ...(frameworkMessage.images === undefined || frameworkMessage.images.length === 0
          ? {}
          : { images: frameworkMessage.images }),
      };
      const key = messageSequenceKey(nextMessageSequence);
      if (messageChunkOverheadBytes + agentMessageBytes(message) > this.config.messageChunkMaxBytes) {
        this.persistence.agentMessageChunks.set(context, key, { messages: [message] });
        directChunks.push({ firstSequence: nextMessageSequence, lastSequence: nextMessageSequence });
      } else {
        this.persistence.agentMessages.set(context, key, message);
      }
      nextMessageSequence += 1;
    }
    if (directChunks.length > 0) {
      index = { ...index, chunks: [...index.chunks, ...directChunks] };
    }
    if (startsTurn) {
      this.persistence.turnReplySequence.set(context, { turnId, sequence: nextMessageSequence });
    }
    index = this.rollOverMessageWindow(context, index, nextMessageSequence);
    index = this.applyMessageRetention(context, index, nextMessageSequence, this.readContextSummary(context));
    if (!isSameMessageIndex(storedIndex ?? emptyMessageIndex(), index)) {
      this.persistence.agentMessageIndex.set(context, index);
    }
    const advancedState = { ...state, nextMessageSequence };
    this.writeAgentState(context, advancedState);
    return advancedState;
  }

  /**
   * Moves the oldest window messages into immutable chunks while the window holds more than its size. A chunk
   * closes at `messageChunkSize` messages, at `messageChunkMaxBytes` bytes, or before a message stored in its
   * own chunk. One commit writes at most {@link messageRolloverMaxChunksPerCommit} chunks and
   * {@link messageRolloverMaxBytesPerCommit} bytes, so a backlog drains over several commits.
   */
  private rollOverMessageWindow(context: Context, index: AgentMessageIndex, nextMessageSequence: number): AgentMessageIndex {
    const chunks = [...index.chunks];
    const directStarts = new Set(
      chunks.filter((range) => range.firstSequence >= index.windowFirstSequence).map((range) => range.firstSequence),
    );
    let windowFirstSequence = skipDirectChunks(directStarts, index.windowFirstSequence);
    const windowMessageCount = (): number =>
      nextMessageSequence -
      windowFirstSequence -
      [...directStarts].filter((sequence) => sequence >= windowFirstSequence).length;
    let movedChunks = 0;
    let movedBytes = 0;
    while (windowMessageCount() > this.config.messageWindowSize && movedChunks < messageRolloverMaxChunksPerCommit) {
      const messages: AgentMessage[] = [];
      let chunkBytes = messageChunkOverheadBytes - 1;
      for (
        let sequence = windowFirstSequence;
        sequence < nextMessageSequence && messages.length < this.config.messageChunkSize && !directStarts.has(sequence);
        sequence += 1
      ) {
        const message = this.readWindowMessage(context, sequence);
        const withMessageBytes = chunkBytes + 1 + agentMessageBytes(message);
        if (messages.length > 0 && withMessageBytes > this.config.messageChunkMaxBytes) {
          break;
        }
        messages.push(message);
        chunkBytes = withMessageBytes;
      }
      if (movedChunks > 0 && movedBytes + chunkBytes > messageRolloverMaxBytesPerCommit) {
        break;
      }
      for (const message of messages) {
        this.persistence.agentMessages.delete(context, messageSequenceKey(message.sequence));
      }
      this.persistence.agentMessageChunks.set(context, messageSequenceKey(windowFirstSequence), { messages });
      const lastSequence = windowFirstSequence + messages.length - 1;
      chunks.push({ firstSequence: windowFirstSequence, lastSequence });
      windowFirstSequence = skipDirectChunks(directStarts, lastSequence + 1);
      movedChunks += 1;
      movedBytes += chunkBytes;
    }
    if (windowFirstSequence === index.windowFirstSequence && chunks.length === index.chunks.length) {
      return index;
    }
    chunks.sort((left, right) => left.firstSequence - right.firstSequence);
    return { chunks, firstSequence: firstStoredSequence(chunks, windowFirstSequence), windowFirstSequence };
  }

  /**
   * Deletes the oldest archive chunk while every message in it is summarized and at least `retainedMessageLimit`
   * stored messages come after it, so the newest `retainedMessageLimit` messages always stay. It runs only with
   * compaction and a limit.
   */
  public applyMessageRetention(
    context: Context,
    index: AgentMessageIndex,
    nextMessageSequence: number,
    summary: ContextSummary | null,
  ): AgentMessageIndex {
    const limit = this.config.retainedMessageLimit;
    if (this.config.compaction === undefined || limit === null || summary === null) {
      return index;
    }
    let deletedChunks = 0;
    for (const range of index.chunks) {
      // Only archived chunks, before the window; a message stored in its own chunk waits for the rollover.
      if (
        range.firstSequence >= index.windowFirstSequence ||
        range.lastSequence > summary.lastSequence ||
        nextMessageSequence - (range.lastSequence + 1) < limit
      ) {
        break;
      }
      this.persistence.agentMessageChunks.delete(context, messageSequenceKey(range.firstSequence));
      deletedChunks += 1;
    }
    if (deletedChunks === 0) {
      return index;
    }
    const chunks = index.chunks.slice(deletedChunks);
    return { ...index, chunks, firstSequence: firstStoredSequence(chunks, index.windowFirstSequence) };
  }

  /** Appends the turn's user messages, in order, opens the next turn, and moves to RunModel. */
  public beginTurn(
    context: Context,
    state: AgentState,
    messages: readonly AgentUserMessage[],
    stepType: string,
    interactionMode: InteractionMode = "chat",
    executedPlan?: AgentPlan,
    wasPlanDraft = false,
    sessionSettings: SessionSettings | null = this.readSessionSettings(context),
  ): StepDecision {
    const turnId = `turn-${state.turnCount + 1}`;
    // The turn keeps these until it ends; later updates wait for the next turn.
    if (sessionSettings !== null && Object.keys(sessionSettings).length > 0) {
      this.persistence.turnSessionSettings.set(context, { turnId, sessionSettings });
    } else if (this.persistence.turnSessionSettings.get(context) !== undefined) {
      this.persistence.turnSessionSettings.delete(context);
    }
    const sequence = state.nextMessageSequence;
    return this.tracer.traceUserMessageActivation(
      context,
      stepType,
      turnId,
      messageSequenceKey(sequence),
      messages.map((message) => message.text).join("\n"),
      () => {
        const userMessages = messages.map((message) => this.buildStoredUserMessage(message));
        if (userMessages.some((userMessage) => userMessage.role !== "user")) {
          throw new TypeError("buildUserMessage must return a message with role user");
        }
        const stateWithMessage = this.appendMessages(context, state, turnId, userMessages, true);
        this.writeAgentState(context, {
          ...stateWithMessage,
          status: "running_model",
          activeTurnId: turnId,
          interactionMode,
          acceptsTurnControl: true,
        });
        this.recordSessionActivity(context, state.turnCount);
        const firstMessage = messages[0];
        if (state.turnCount === 0 && firstMessage !== undefined) {
          this.recordSessionTitle(context, firstMessage.text);
        }
        this.writeEvents(context, turnId, (events) => {
          events.writeEvent("turn_started", { sequence });
          if (executedPlan !== undefined) {
            events.writeEvent("plan_execution_started", { revision: executedPlan.revision });
            if (wasPlanDraft) {
              writePlanUpdated(events, executedPlan);
            }
          }
        });
        return this.goToRunModel(context);
      },
    );
  }

  /** Builds the stored item of one user message; the session sets its images itself. */
  private buildStoredUserMessage(message: AgentUserMessage): FrameworkMessage {
    const images = message.images ?? [];
    if (images.length === 0) {
      const { images: _ignored, ...built } = this.runner.buildUserMessage(message.text);
      return built;
    }
    // A runner redeployed without image support still stores the images, for display.
    const built =
      this.runner.buildUserMessageWithImages === undefined
        ? this.runner.buildUserMessage(message.text)
        : this.runner.buildUserMessageWithImages(message.text, images);
    return { ...built, images };
  }

  /**
   * Returns the mode of a turn that these user messages start: `planning` when plan mode is enabled and a
   * message has `planMode` or a `draft` plan exists, else `chat`.
   */
  public decideInteractionMode(context: Context, messages: readonly AgentUserMessage[]): InteractionMode {
    if (!this.config.planModeEnabled) {
      return "chat";
    }
    const isPlanModeRequested = messages.some((message) => message.planMode === true);
    return isPlanModeRequested || this.readOptionalAgentPlan(context)?.status === "draft" ? "planning" : "chat";
  }

  /** Ends a steered or cancelled turn with its unfinished-turn messages, or with a note; returns the new state. */
  public endControlledTurn(
    context: Context,
    initialState: AgentState,
    turnId: string,
    steeredMessageCount: number,
  ): AgentState {
    const transitions = this;
    const persistence = this.persistence;
    const toolCalls = transitions.readTurnToolCalls(context, turnId);
    const openToolCall = toolCalls.find((toolCall) => !isFinalToolCallStatus(toolCall.status));
    if (openToolCall !== undefined) {
      throw new Error(`${turnId} ended before tool call ${openToolCall.toolCallId} finished`);
    }
    const isCancelled = transitions.readTurnControlReason(context, turnId) === "turn_cancelled";
    const storedMessageCount = transitions.countStoredTurnReplies(context, initialState, turnId);
    let messages = transitions.buildUnfinishedTurnMessages(context, initialState, turnId, toolCalls);
    if (messages === null) {
      messages = [];
      if (toolCalls.length > 0) {
        // The records are deleted below; the note keeps the next model from repeating them.
        const note = transitions.runner.buildUserMessage(
          buildToolCallNote(isCancelled ? turnCancelledNoteHeader : turnSteeredNoteHeader, toolCalls),
        );
        if (note.role !== "user") {
          throw new TypeError("buildUserMessage must return a message with role user");
        }
        messages = [note];
      }
    }
    const stateWithMessages = transitions.appendMessages(context, initialState, turnId, messages);
    for (const toolCall of toolCalls) {
      persistence.agentToolCalls.delete(context, toolCall.toolCallId);
    }
    persistence.frameworkRunState.delete(context);
    const endedState: AgentState = {
      ...stateWithMessages,
      status: "waiting_for_user_message",
      turnCount: stateWithMessages.turnCount + 1,
      activeTurnId: null,
      lastTurnError: null,
      acceptsTurnControl: false,
    };
    transitions.writeAgentState(context, endedState);
    transitions.recordSessionActivity(context, endedState.turnCount);
    const messageCount = storedMessageCount + messages.length;
    transitions.writeEvents(context, turnId, (events) =>
      isCancelled
        ? events.writeEvent("turn_cancelled", { message_count: messageCount })
        : events.writeEvent("turn_steered", {
            steered_message_count: steeredMessageCount,
            message_count: messageCount,
          }),
    );
    return endedState;
  }

  public goToAwaitUserMessage(): StepDecision {
    return goTo(new StepRef(agentSessionStepTypes.awaitUserMessage), undefined);
  }

  /** Reports whether `cancel_turn` stopped this turn; reads committed state only. */
  public isTurnCancelled(context: Context, turnId: string | null): boolean {
    return this.readTurnControlReason(context, turnId) === "turn_cancelled";
  }

  public resolveToolPolicy(toolName: string, frameworkEffect?: ToolEffect): ResolvedToolPolicy {
    return resolveToolPolicy(
      toolName,
      this.config.toolPolicies,
      this.config.defaultToolEffect,
      frameworkEffect,
    );
  }

  /**
   * Asks the application's spend limit whether a model call may run.
   * @returns The move that ends the refused turn, or `null` to call the model.
   * @throws What the check threw, which fails the attempt so no model call runs unchecked.
   */
  public async checkSpendLimit(
    context: AsyncContext,
    state: AgentState,
    turnId: string,
    stepType: string,
  ): Promise<StepDecision | null> {
    const spendLimit = this.config.spendLimit;
    if (spendLimit === undefined) {
      return null;
    }
    const decision = await withinSpendLimitCallTimeout(
      "checkModelCall",
      spendLimit.checkModelCall({
        flowId: context.flowId,
        turnId,
        stepType,
        attempt: context.attempt,
        sessionSettings: this.readTurnSessionSettings(context),
        sessionUsage: this.readAgentUsage(context),
      }),
    );
    if (typeof decision?.isAllowed !== "boolean") {
      throw new TypeError("checkModelCall must return a ModelCallDecision");
    }
    if (decision.isAllowed) {
      return null;
    }
    // RecordTurnFailure loads what ending a turn needs, which CompactContext does not.
    this.writeAgentState(context, { ...state, lastTurnError: `${modelCallRefusedErrorPrefix}: ${decision.reason}` });
    return goTo(new StepRef(agentSessionStepTypes.recordTurnFailure), undefined);
  }

  /** Reports a finished model call. A failure is logged: failing the attempt would pay for the call again. */
  public async reportModelUsage(
    context: AsyncContext,
    turnId: string,
    stepType: string,
    usage: AgentUsage,
  ): Promise<void> {
    const spendLimit = this.config.spendLimit;
    if (spendLimit === undefined) {
      return;
    }
    const reportId = `${context.flowId}/${turnId}/${context.stepExecutionId}/${context.attempt}`;
    try {
      await withinSpendLimitCallTimeout(
        "reportModelUsage",
        spendLimit.reportModelUsage({
          reportId,
          flowId: context.flowId,
          turnId,
          stepType,
          attempt: context.attempt,
          sessionSettings: this.readTurnSessionSettings(context),
          usage,
          sessionUsage: addAgentUsage(this.readAgentUsage(context), usage),
        }),
      );
    } catch (error) {
      console.warn(`dex-agents: reportModelUsage failed for ${reportId}:`, error);
    }
  }

  public recordUsage(context: Context, usage: AgentUsage, events: StreamAgentEventWriter): void {
    const sessionUsage = addAgentUsage(this.readAgentUsage(context), usage);
    this.persistence.agentUsage.set(context, sessionUsage);
    events.writeEvent("usage_recorded", {
      recorded: usagePayload(usage),
      session: usagePayload(sessionUsage),
    });
  }

  public createEventWriter(context: Context, turnId: string | null): StreamAgentEventWriter {
    return new StreamAgentEventWriter(context, this.persistence.agentEvents, turnId);
  }

  public writeEvents(
    context: Context,
    turnId: string | null,
    write: (events: StreamAgentEventWriter) => void,
  ): void {
    const events = this.createEventWriter(context, turnId);
    try {
      write(events);
    } finally {
      events.close();
    }
  }

  /** Runs an operation while recording a heartbeat every 20 seconds. */
  public async runWithHeartbeats<Result>(
    context: AsyncContext,
    operation: () => Promise<Result>,
    recordHeartbeat: () => Promise<void> = () => this.recordHeartbeat(context),
  ): Promise<Result> {
    const heartbeatTimer = setInterval(() => {
      // A rejected heartbeat means the attempt already ended; the operation result decides the outcome.
      recordHeartbeat().catch(() => undefined);
    }, heartbeatIntervalMs);
    try {
      return await operation();
    } finally {
      clearInterval(heartbeatTimer);
    }
  }

  public recordHeartbeat(context: AsyncContext): Promise<void> {
    return context.recordHeartbeat(context.stepExecutionId, stringCodec);
  }
}

/**
 * Heartbeats of one tool Step attempt. Each carries the latest checkpoint, else the Step execution ID,
 * which every attempt shares and so never reads as a checkpoint.
 */
export class ToolStepHeartbeats {
  public readonly previousCheckpoint: string | undefined;
  private checkpoint: string | undefined;

  public constructor(private readonly context: AsyncContext) {
    const previous = context.hasLastHeartbeatValue() ? context.getLastHeartbeatValue(stringCodec) : undefined;
    this.previousCheckpoint = previous === undefined || previous === context.stepExecutionId ? undefined : previous;
    // Every heartbeat replaces the saved value, so carry the earlier attempt's checkpoint forward.
    this.checkpoint = this.previousCheckpoint;
  }

  public record(): Promise<void> {
    return this.context.recordHeartbeat(this.checkpoint ?? this.context.stepExecutionId, stringCodec);
  }

  public saveCheckpoint(checkpoint: string): Promise<void> {
    this.checkpoint = checkpoint;
    return this.record();
  }
}

/** Returns a record as a runner sees it; only records that runners may see are accepted. */
export function toRunnerToolCall(record: AgentToolCallRecord): AgentToolCall {
  if (record.status === "awaiting_answer" || record.status === "waiting_for_timer" || record.status === "waiting_for_task") {
    throw new TypeError(`tool call ${record.toolCallId} is still ${record.status}`);
  }
  return record as AgentToolCall;
}

function isFrameworkMessage(value: unknown): value is FrameworkMessage {
  const message = value as Partial<Record<keyof FrameworkMessage, unknown>> | null;
  const isNullableString = (field: unknown) => field === null || typeof field === "string";
  return (
    typeof message === "object" &&
    message !== null &&
    (message.role === "user" || message.role === "assistant" || message.role === "tool") &&
    typeof message.frameworkItem === "string" &&
    isNullableString(message.text) &&
    isNullableString(message.toolCallId) &&
    isNullableString(message.toolName) &&
    isNullableString(message.toolArguments) &&
    isNullableString(message.toolOutput)
  );
}

/** Moves to `FinishTurnCancellation` for one turn; its locks come from the Step's own options. */
export function finishTurnCancellationMovement(turnId: string): StepMovement<string> {
  return StepMovement.of(new StepRef(agentSessionStepTypes.finishTurnCancellation), turnId);
}

/** Reports whether `cancel_turn` abandons this call's started task. */
export function isAbandonedToolTask(toolCall: AgentToolCallRecord): boolean {
  return (
    (toolCall.taskId ?? null) !== null &&
    (toolCall.status === "waiting_for_task" || toolCall.status === "awaiting_answer")
  );
}

/** Reports whether steering or `cancel_turn` may abandon a call that waits in a session Step. */
export function isCancellableByTurnControl(toolCall: AgentToolCallRecord): boolean {
  // Each of these waits for a person or a Timer; the call has taken no effect. A task's question
  // waits for a person too, but its task has started, and a waiting_for_task record is never listed.
  return (
    toolCall.status === "awaiting_approval" ||
    (toolCall.status === "awaiting_answer" && (toolCall.taskId ?? null) === null) ||
    toolCall.status === "waiting_for_timer"
  );
}

/** Returns a record that steering or `cancel_turn` abandoned before the call started. */
export function cancelledToolCall(
  toolCall: AgentToolCallRecord,
  reason: ToolCallCancellationReason,
): AgentToolCallRecord {
  return {
    ...toolCall,
    status: "rejected",
    rejectionMessage: toolCallCancellationResults[reason],
    cancellationReason: reason,
    inputQuestion: null,
    inputChoices: [],
    inputAllowsFreeText: true,
  };
}

/**
 * Returns the kept chunk that holds a message, found by binary search over the index's ordered ranges, or
 * `undefined` when the message is in the window.
 */
export function findMessageChunkRange(index: AgentMessageIndex, sequence: number): AgentMessageChunkRange | undefined {
  let low = 0;
  let high = index.chunks.length - 1;
  while (low <= high) {
    const middle = (low + high) >> 1;
    const range = index.chunks[middle] as AgentMessageChunkRange;
    if (sequence < range.firstSequence) {
      high = middle - 1;
    } else if (sequence > range.lastSequence) {
      low = middle + 1;
    } else {
      return range;
    }
  }
  if (sequence < index.windowFirstSequence) {
    throw new Error(`AgentMessageIndex lists no chunk that holds message ${sequence}`);
  }
  return undefined;
}

/** The index of a session whose messages are all in the window, as a new session or a 0.1.0 one stores them. */
function emptyMessageIndex(): AgentMessageIndex {
  return { chunks: [], firstSequence: 1, windowFirstSequence: 1 };
}

/** The oldest stored message: the first archived chunk's first sequence, else the window's first. */
function firstStoredSequence(chunks: readonly AgentMessageChunkRange[], windowFirstSequence: number): number {
  const first = chunks[0];
  return first !== undefined && first.firstSequence < windowFirstSequence ? first.firstSequence : windowFirstSequence;
}

/** Returns the first sequence at or after `sequence` that is not a message stored in its own chunk. */
function skipDirectChunks(directStarts: ReadonlySet<number>, sequence: number): number {
  let next = sequence;
  while (directStarts.has(next)) {
    next += 1;
  }
  return next;
}

/** Whether a chunk holds exactly the messages of its range, in order. */
export function isMessageChunkOfRange(chunk: AgentMessageChunk, range: AgentMessageChunkRange): boolean {
  return (
    chunk.messages.length === range.lastSequence - range.firstSequence + 1 &&
    chunk.messages.every((message, offset) => message.sequence === range.firstSequence + offset)
  );
}

/** The UTF-8 bytes of a message's JSON as this core stores it. */
function agentMessageBytes(message: AgentMessage): number {
  return utf8Encoder.encode(JSON.stringify(encodeAgentMessage(message))).length;
}

function isSameMessageIndex(left: AgentMessageIndex, right: AgentMessageIndex): boolean {
  return (
    left.firstSequence === right.firstSequence &&
    left.windowFirstSequence === right.windowFirstSequence &&
    left.chunks.length === right.chunks.length &&
    left.chunks.every(
      (range, offset) =>
        range.firstSequence === right.chunks[offset]?.firstSequence &&
        range.lastSequence === right.chunks[offset]?.lastSequence,
    )
  );
}

/** Counts a turn's stored messages after the user messages that started it. */
export function countMessagesAfterUserMessages(messages: readonly AgentMessage[], turnId: string): number {
  const turnMessages = messages.filter((message) => message.turnId === turnId);
  const firstNonUserIndex = turnMessages.findIndex((message) => message.role !== "user");
  return firstNonUserIndex === -1 ? 0 : turnMessages.length - firstNonUserIndex;
}

export function toolStepTypeForEffect(effect: ToolEffect): string {
  switch (effect) {
    case "read":
    case "idempotent_write":
      return agentSessionStepTypes.callTool;
    case "non_idempotent_write":
    case "unclassified":
      return agentSessionStepTypes.callSingleAttemptTool;
  }
}

/** Returns the text of the failure that selected a recovery Step. */
export function readRecoveryDetail(context: Context): string {
  return recoveryErrorDetail(context.recoveryError);
}

/** Returns the text of a recovery error: its detail, else its error type, else "unknown failure". */
export function recoveryErrorDetail(recoveryError: RecoveryErrorInfo | undefined): string {
  if (recoveryError === undefined) {
    return "unknown failure";
  }
  return recoveryError.detail !== "" ? recoveryError.detail : recoveryError.errorType;
}

/** Writes `plan_updated` with the plan's revision, status, and task count. */
export function writePlanUpdated(events: StreamAgentEventWriter, plan: AgentPlan): void {
  events.writeEvent("plan_updated", { revision: plan.revision, status: plan.status, task_count: plan.tasks.length });
}

function usagePayload(usage: AgentUsage): Record<string, number> {
  return {
    model_calls: usage.modelCalls,
    input_tokens: usage.inputTokens,
    output_tokens: usage.outputTokens,
    cached_input_tokens: usage.cachedInputTokens,
    reasoning_tokens: usage.reasoningTokens,
  };
}

/**
 * Returns the settings of the active turn from its stored copy: the copy's settings while it belongs to that
 * turn, else `{}`. A turn that a Worker without session settings started, such as 0.1.0, leaves an earlier
 * turn's copy in place, which must not apply.
 */
export function selectTurnSessionSettings(
  stored: TurnSessionSettings | null,
  activeTurnId: string | null,
): SessionSettings {
  return stored !== null && activeTurnId !== null && stored.turnId === activeTurnId ? stored.sessionSettings : {};
}
