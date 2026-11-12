// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  ExecuteFailure,
  StepMovement,
  int64Codec,
  StepRef,
  StepAttemptProgress,
  Timer,
  Wait,
  deadEnd,
  forceFail,
  goTo,
  goToMany,
  stringCodec,
  voidCodec,
  type AsyncContext,
  type Codec,
  type Condition,
  type Context,
  type RecoveryErrorInfo,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import { boundApprovalDisplay } from "./agent-approval-display.js";
import {
  buildPlanClearedOutput,
  buildPlanUpdatedOutput,
  isBlankText,
  validateAskUserArguments,
  validateDurableWaitArguments,
  validateWriteTodosArguments,
  writeTodosCalledMoreThanOnce,
  type BuiltInTool,
} from "./agent-built-in-tools.js";
import {
  isCompactionTriggered,
  isContextCompactionRunner,
  readRetainedTokenBudget,
  type ContextCompactionRunner,
  type ContextSummaryResult,
} from "./agent-context-compaction.js";
import type { StreamAgentEventWriter } from "./agent-event-writer.js";
import {
  ToolExecutionError,
  ToolOutcomeUnknownError,
  type AgentTurnContext,
  type DeferredToolCall,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type ToolCallStepOptions,
  type ToolCallsRequested,
  type ToolInputRequested,
  type ToolTaskStarted,
  type TurnCompleted,
  type TurnContinued,
} from "./agent-framework-runner.js";
import { toolResultImages } from "./agent-media.js";
import { buildPlanExecutionMessage, planningTurnToolRejection, planStatusAfterWrite } from "./agent-plans.js";
import { agentSessionStartInputCodec, turnFailureRouteInputCodec } from "./agent-session-codecs.js";
import {
  messageStepOptions,
  minuteMs,
  type MessageChunkLoads,
  modelCallStepOptions,
  recoveryRetry,
  syncDurability,
} from "./agent-session-step-options.js";
import {
  agentSessionStepTypes,
  allPendingMessages,
  cancelledToolCall,
  isAbandonedToolTask,
  isCancellableByTurnControl,
  readRecoveryDetail,
  recoveryErrorDetail,
  toRunnerToolCall,
  toolStepTypeForEffect,
  ToolStepHeartbeats,
  writePlanUpdated,
  type AgentSessionTransitions,
} from "./agent-session-transitions.js";
import {
  messageSequenceKey,
  planExecutionKey,
  type AgentSessionPersistence,
} from "./agent-session-persistence.js";
import { checkStoredSessionSettings } from "./agent-session-settings.js";
import type { ToolCallSpan } from "./agent-session-tracer.js";
import {
  approvalDeadlinePassed,
  blankToolTaskId,
  buildTurnFailureNote,
  emptyAgentUsage,
  invalidToolTaskPollInterval,
  isFinalToolCallStatus,
  missingToolTaskPoller,
  toolCallNotRunErrorPrefix,
  toolCallSkippedAfterWriteDetail,
  toolTaskAbandonedError,
  type AgentInterrupt,
  type AgentInterruptDisplay,
  type AgentPlan,
  type AgentInterruptKind,
  type AgentSessionStartInput,
  type AgentState,
  type AgentToolCallRecord,
  type AgentUserMessage,
  type CompactContextInput,
  type ContextSummary,
  type FrameworkMessage,
  type InteractionMode,
  type PlanStatus,
  type ToolCallCancellationReason,
} from "./agent-session-values.js";

/** Longest wait between two polls of a tool task, as in the Python core; a longer interval is shortened. */
const maximumToolTaskPollIntervalSeconds = 86_400;
// An int64 Value, as the Python core's Step[int] encodes it.
const expectedResultCountCodec: Codec<number> = {
  typeName: "ExpectedResultCount",
  wireKind: "int64",
  encode: (value) => int64Codec.encode(BigInt(requirePositiveResultCount(value))),
  decode: (value) => requirePositiveResultCount(Number(int64Codec.decode(value))),
};

/** Start of the Flow failure reason when `StartAgentSession` refuses the start input's settings. */
export const invalidStartSessionSettingsReason =
  "dex-agents: the start input's session_settings are not valid session settings";

export class StartAgentSessionStep implements Step<AgentSessionStartInput> {
  public readonly inputCodec = agentSessionStartInputCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.startAgentSession;
  }

  public getStepOptions(): StepOptions {
    return { ...syncDurability, executeLoadAttributeMaps: [this.transitions.persistence.agentMessages] };
  }

  public execute(context: Context, input: AgentSessionStartInput): StepDecision {
    const persistence = this.transitions.persistence;
    const state: AgentState = {
      status: "waiting_for_user_message",
      turnCount: 0,
      activeTurnId: null,
      nextMessageSequence: 1,
      lastTurnError: null,
      acceptsTurnControl: false,
    };
    const sessionSettings = input.sessionSettings ?? null;
    if (sessionSettings !== null) {
      try {
        checkStoredSessionSettings(sessionSettings);
      } catch (error: unknown) {
        // Every retry would fail the same way, so the session fails now.
        return forceFail(`${invalidStartSessionSettingsReason}: ${error instanceof Error ? error.message : String(error)}`);
      }
      if (Object.keys(sessionSettings).length > 0) {
        persistence.sessionSettings.set(context, sessionSettings);
      }
    }
    this.transitions.writeAgentState(context, state);
    persistence.agentUsage.set(context, emptyAgentUsage());
    persistence.pendingInterrupts.set(context, []);
    const initialMessage = input.initialMessage;
    if (initialMessage === null || isBlankText(initialMessage)) {
      return this.transitions.goToAwaitUserMessage();
    }
    return this.transitions.beginTurn(
      context,
      state,
      [{ text: initialMessage }],
      this.getStepType(),
      "chat",
      undefined,
      false,
      sessionSettings,
    );
  }
}

/** Waits for one queued user message, or an approved plan execution, and starts its turn. */
export class AwaitUserMessageStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.awaitUserMessage;
  }

  public getStepOptions(): StepOptions {
    // The lock keeps a turn from starting while cancel_turn decides about the last one.
    // Storing the turn's user messages may move the oldest window messages into a chunk.
    return {
      ...syncDurability,
      executeLoadAttributeMaps: [this.transitions.persistence.agentMessages],
      executeLockAttributes: [this.transitions.persistence.turnCancellationRequest.lock()],
    };
  }

  public waitFor(context: Context): Wait {
    const persistence = this.transitions.persistence;
    // Dex selects Channel conditions in argument order: a steer, a queued message, a plan.
    const conditions: Condition[] = [
      persistence.turnControlRequests.range(1, allPendingMessages),
      persistence.queuedUserMessages.forOne(),
    ];
    const plan = this.transitions.readOptionalAgentPlan(context);
    // Keyed by revision, so an approval of an earlier revision never starts a turn.
    if (plan !== null && isExecutablePlanStatus(plan.status)) {
      conditions.push(persistence.planExecutions.forOne(planExecutionKey(plan.revision)));
    }
    return Wait.anyOf(...conditions);
  }

  public execute(context: Context): StepDecision {
    const transitions = this.transitions;
    const persistence = transitions.persistence;
    if (context.channelResults(persistence.turnControlRequests).length > 0) {
      return transitions.goToApplyTurnControl(context);
    }
    const [message] = context.channelResults(persistence.queuedUserMessages);
    if (message !== undefined) {
      if (isBlankText(message.text)) {
        return transitions.goToAwaitUserMessage();
      }
      return this.beginUserMessageTurn(context, message);
    }
    const plan = transitions.readOptionalAgentPlan(context);
    const [execution] =
      plan === null ? [] : context.channelResults(persistence.planExecutions, planExecutionKey(plan.revision));
    if (execution === undefined) {
      return transitions.goToAwaitUserMessage();
    }
    if (persistence.pendingPlanExecution.get(context) !== undefined) {
      persistence.pendingPlanExecution.delete(context);
    }
    if (plan === null || execution.revision !== plan.revision || !isExecutablePlanStatus(plan.status)) {
      return transitions.goToAwaitUserMessage();
    }
    const wasPlanDraft = plan.status !== "active";
    const executedPlan: AgentPlan = wasPlanDraft
      ? { ...plan, status: "active", updatedAt: new Date().toISOString() }
      : plan;
    if (wasPlanDraft) {
      persistence.agentPlan.set(context, executedPlan);
    }
    return transitions.beginTurn(
      context,
      transitions.readAgentState(context),
      [{ text: buildPlanExecutionMessage(plan.revision) }],
      this.getStepType(),
      "executing",
      executedPlan,
      wasPlanDraft,
    );
  }

  private beginUserMessageTurn(context: Context, message: AgentUserMessage): StepDecision {
    const transitions = this.transitions;
    return transitions.beginTurn(
      context,
      transitions.readAgentState(context),
      [message],
      this.getStepType(),
      transitions.decideInteractionMode(context, [message]),
    );
  }
}

/** Plan statuses that `execute_plan` accepts and `AwaitUserMessage` waits for. */
export function isExecutablePlanStatus(status: PlanStatus): boolean {
  return status === "draft" || status === "active";
}

/** Makes the turn's next model call and fans the requested tool calls out to parallel Steps. */
export class RunModelStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.runModel;
  }

  public getStepOptions(): StepOptions {
    // Each movement into RunModel adds the archive chunks of the model input.
    return messageStepOptions(
      this.transitions.persistence,
      this.getStepType(),
      staticMessageChunkLoads(this.transitions),
      this.transitions.config.runModelLimit,
    );
  }

  public async execute(context: AsyncContext): Promise<StepDecision> {
    const transitions = this.transitions;
    const state = transitions.readAgentState(context);
    const turnId = state.activeTurnId;
    if (turnId === null) {
      throw new Error(`the agent session ${context.flowId} has no active turn`);
    }
    let toolCalls = transitions.readTurnToolCalls(context, turnId);
    if (toolCalls.some((toolCall) => toolCall.writeQueued === true)) {
      const writeDecision = this.continueOrderedWrites(context, state, turnId, toolCalls);
      if (writeDecision !== null) {
        return writeDecision;
      }
      toolCalls = transitions.readTurnToolCalls(context, turnId);
    }
    const openToolCall = toolCalls.find((toolCall) => !isFinalToolCallStatus(toolCall.status));
    if (openToolCall !== undefined) {
      throw new Error(`RunModel for ${turnId} started before tool call ${openToolCall.toolCallId} finished`);
    }
    const decision = this.decideBeforeModelCall(context, turnId);
    if (decision !== null) {
      return decision;
    }
    const events = transitions.createEventWriter(context, turnId);
    try {
      const turnContext = transitions.createTurnContext(context, state, turnId, toolCalls, events, () =>
        transitions.recordHeartbeat(context),
      );
      const throughSequence = await this.selectCompactionBeforeModelCall(turnContext, state);
      if (throughSequence !== null) {
        return this.startCompaction(context, state, turnContext.contextSummary ?? null, throughSequence);
      }
      const refusal = await transitions.checkSpendLimit(context, state, turnId, this.getStepType());
      if (refusal !== null) {
        return refusal;
      }
      return await transitions.tracer.traceModelCall(
        context,
        this.getStepType(),
        turnContext,
        async (modelCallSpan) => {
          const outcome = await transitions.runWithHeartbeats(context, async () => {
            const result = await transitions.runner.runModelStep(turnContext);
            if (
              result?.kind === "completed" ||
              result?.kind === "tool_calls_requested" ||
              result?.kind === "turn_continued"
            ) {
              await transitions.reportModelUsage(context, turnId, this.getStepType(), result.usage);
            }
            return result;
          });
          switch (outcome?.kind) {
            case "completed": {
              modelCallSpan.recordOutcome(outcome, appendedMessageKeys(state, outcome.newMessages));
              // The index keeps where the turn's replies begin, so the count includes messages summarized away.
              const storedTurnMessageCount = transitions.countStoredTurnReplies(context, state, turnId);
              return this.completeTurn(context, state, turnId, toolCalls, outcome, storedTurnMessageCount, events);
            }
            case "tool_calls_requested":
              if ((outcome.newMessages?.length ?? 0) > 0 && transitions.runner.buildUnfinishedTurnMessages === undefined) {
                // A runner defect: a retry would only repeat the paid model call, so the turn fails now.
                modelCallSpan.recordOutcome(outcome, []);
                transitions.recordUsage(context, outcome.usage, events);
                return recordFailedTurn(transitions, context, state, toolCalls, [], missingUnfinishedTurnMessageBuilder);
              }
              modelCallSpan.recordOutcome(outcome, appendedMessageKeys(state, outcome.newMessages ?? []));
              return this.requestToolCalls(context, state, turnId, toolCalls, outcome, events);
            case "turn_continued":
              validateTurnContinued(turnId, outcome);
              if (outcome.newMessages.length > 0 && transitions.runner.buildUnfinishedTurnMessages === undefined) {
                // The same runner defect as for ToolCallsRequested.newMessages.
                modelCallSpan.recordOutcome(outcome, []);
                transitions.recordUsage(context, outcome.usage, events);
                return recordFailedTurn(transitions, context, state, toolCalls, [], missingUnfinishedTurnMessageBuilder);
              }
              modelCallSpan.recordOutcome(outcome, appendedMessageKeys(state, outcome.newMessages));
              return this.continueTurn(context, state, turnId, outcome, events);
            default:
              throw new TypeError("runModelStep must return a TurnCompleted, ToolCallsRequested, or TurnContinued outcome");
          }
        },
      );
    } finally {
      events.close();
    }
  }

  /** Starts the next queued write or skips the rest; `null` lets a cancelled turn end here. */
  private continueOrderedWrites(
    context: Context,
    state: AgentState,
    turnId: string,
    toolCalls: readonly AgentToolCallRecord[],
  ): StepDecision | null {
    const transitions = this.transitions;
    const queued = toolCalls.filter((toolCall) => toolCall.writeQueued === true);
    const reason = transitions.readTurnControlReason(context, turnId);
    if (reason !== null) {
      transitions.writeEvents(context, turnId, (events) => {
        for (const toolCall of queued) {
          transitions.finishUnscheduledToolCall(context, { ...cancelledToolCall(toolCall, reason), writeQueued: false }, events);
        }
      });
      return null;
    }
    const openToolCall = toolCalls.find(
      (toolCall) => toolCall.writeQueued !== true && !isFinalToolCallStatus(toolCall.status),
    );
    if (openToolCall !== undefined) {
      throw new Error(`RunModel for ${turnId} started before tool call ${openToolCall.toolCallId} finished`);
    }
    const nextWrite = queued[0]!;
    const previousWrite = toolCalls
      .filter(
        (toolCall) =>
          toolCall.orderedWrite === true &&
          toolCall.writeQueued !== true &&
          toolCall.deferralOrder < nextWrite.deferralOrder,
      )
      .at(-1);
    if (previousWrite !== undefined && previousWrite.status !== "succeeded") {
      transitions.writeEvents(context, turnId, (events) => {
        for (const toolCall of queued) {
          transitions.finishUnscheduledToolCall(context, skippedToolCall(toolCall), events);
        }
      });
      // The skips commit before the model call, so readers see them while the model runs.
      return transitions.goToRunModel(context);
    }
    const createdAt = new Date();
    const traceContext = transitions.tracer.readActivationTraceparent(context);
    const { traceContext: _queuedTraceContext, ...queuedWrite } = nextWrite;
    const started: AgentToolCallRecord = {
      ...queuedWrite,
      writeQueued: false,
      status: nextWrite.requiresApproval ? "awaiting_approval" : "approved",
      ...(traceContext === null ? {} : { traceContext }),
    };
    transitions.writeToolCall(context, started);
    let movement: StepMovement<string>;
    if (started.requiresApproval) {
      const interrupt = toolApprovalInterrupt(
        started,
        createdAt.toISOString(),
        interruptDeadline(transitions, createdAt),
      );
      transitions.persistence.pendingInterrupts.set(context, [...transitions.readPendingInterrupts(context), interrupt]);
      transitions.writeEvents(context, turnId, (events) => transitions.writeInterruptRaised(events, interrupt));
      movement = toolStepMovement(transitions.persistence, agentSessionStepTypes.awaitToolApproval, started.toolCallId);
    } else {
      movement = toolCallMovement(transitions, started);
    }
    transitions.writeAgentState(context, { ...state, status: "running_tools" });
    return goToMany(movement, StepMovement.of(new StepRef(agentSessionStepTypes.resumeTurn), 1));
  }

  /** Returns RunModel's move instead of a model call, or `null` to call the model. */
  private decideBeforeModelCall(context: Context, turnId: string): StepDecision | null {
    // Execute state is fixed at the first attempt, so this and the compaction check are RunModel's decision points.
    const transitions = this.transitions;
    if (transitions.readTurnControlReason(context, turnId) === null) {
      return null;
    }
    // Ending the turn in this commit leaves FinishTurnCancellation no pending turn end to race.
    transitions.endControlledTurn(
      context,
      transitions.readAgentState(context),
      turnId,
      context.channelSize(transitions.persistence.steeredUserMessages),
    );
    return transitions.goToApplyTurnControl(context);
  }

  private completeTurn(
    context: Context,
    state: AgentState,
    turnId: string,
    toolCalls: readonly AgentToolCallRecord[],
    outcome: TurnCompleted,
    storedTurnMessageCount: number,
    events: StreamAgentEventWriter,
  ): StepDecision {
    const transitions = this.transitions;
    const persistence = transitions.persistence;
    const turnError = readTurnError(turnId, outcome);
    const stateWithMessages = transitions.appendMessages(context, state, turnId, outcome.newMessages);
    for (const toolCall of toolCalls) {
      persistence.agentToolCalls.delete(context, toolCall.toolCallId);
    }
    persistence.frameworkRunState.delete(context);
    transitions.recordUsage(context, outcome.usage, events);
    transitions.writeAgentState(context, {
      ...stateWithMessages,
      status: "waiting_for_user_message",
      turnCount: state.turnCount + 1,
      activeTurnId: null,
      lastTurnError: turnError,
      acceptsTurnControl: false,
    });
    if (turnError !== null) {
      events.writeEvent("turn_failed", { error: turnError });
      return transitions.goToAwaitUserMessage();
    }
    // The turn's user messages are stored but not counted; incremental runners stored more before now.
    const messageCount = storedTurnMessageCount + outcome.newMessages.length;
    events.writeEvent("turn_completed", { message_count: messageCount });
    return transitions.goToAwaitUserMessage();
  }

  /** Stores the answer and any continuation message, then moves to `RunModel` again in the same turn. */
  private continueTurn(
    context: Context,
    state: AgentState,
    turnId: string,
    outcome: TurnContinued,
    events: StreamAgentEventWriter,
  ): StepDecision {
    const transitions = this.transitions;
    const stateWithMessages = transitions.appendMessages(context, state, turnId, outcome.newMessages);
    transitions.persistence.frameworkRunState.set(context, outcome.frameworkRunState);
    transitions.recordUsage(context, outcome.usage, events);
    transitions.writeAgentState(context, { ...stateWithMessages, status: "running_model" });
    return transitions.goToRunModel(context);
  }

  /** Returns the sequence to summarize through before this model call, or `null` to make the call. */
  private async selectCompactionBeforeModelCall(turn: AgentTurnContext, state: AgentState): Promise<number | null> {
    const transitions = this.transitions;
    const policy = transitions.config.compaction;
    const runner = transitions.runner;
    if (policy === undefined || !isContextCompactionRunner(runner)) {
      return null;
    }
    if (!isCompactionTriggered(policy, await runner.estimateInputTokens(turn))) {
      return null;
    }
    const cutoff = await runner.selectCompactionCutoff(turn, readRetainedTokenBudget(policy));
    // A cutoff that does not advance means nothing more to summarize, so the pair cannot loop.
    if (cutoff === null || cutoff <= (turn.contextSummary?.lastSequence ?? 0)) {
      return null;
    }
    if (!Number.isSafeInteger(cutoff) || cutoff >= state.nextMessageSequence) {
      throw new TypeError(`selectCompactionCutoff returned ${String(cutoff)}, which is not a stored message`);
    }
    return cutoff;
  }

  private startCompaction(
    context: Context,
    state: AgentState,
    previousSummary: ContextSummary | null,
    throughSequence: number,
  ): StepDecision {
    const persistence = this.transitions.persistence;
    // CompactContext commits once, so this commit is where the status becomes visible.
    this.transitions.writeAgentState(context, { ...state, status: "compacting_context" });
    const firstSequence = this.transitions.readModelInputFirstSequence(context, previousSummary);
    const chunkLoads = this.transitions.messageChunkLoads(context, firstSequence, throughSequence + 1);
    const input: CompactContextInput = { throughSequence };
    return goToMany(
      StepMovement.of(new StepRef(agentSessionStepTypes.compactContext), input, {
        ...syncDurability,
        ...modelCallStepOptions,
        executeLoadAttributeMaps: [persistence.agentMessages],
        ...(chunkLoads.instances.length === 0 ? {} : { executeLoadAttributeMapInstances: chunkLoads.instances }),
      }),
    );
  }

  private requestToolCalls(
    context: Context,
    state: AgentState,
    turnId: string,
    toolCalls: readonly AgentToolCallRecord[],
    outcome: ToolCallsRequested,
    events: StreamAgentEventWriter,
  ): StepDecision {
    const transitions = this.transitions;
    validateToolCallsRequested(turnId, toolCalls, outcome);
    const newMessages = outcome.newMessages ?? [];
    const stateWithMessages =
      newMessages.length === 0 ? state : transitions.appendMessages(context, state, turnId, newMessages);
    transitions.persistence.frameworkRunState.set(context, outcome.frameworkRunState);
    const createdAt = new Date();
    const batch: ToolCallBatchCommit = {
      context,
      turnId,
      createdAt: createdAt.toISOString(),
      interruptDeadline: interruptDeadline(transitions, createdAt),
      interrupts: [],
      traceContext: transitions.tracer.readActivationTraceparent(context),
      movements: [],
      events,
      interactionMode: state.interactionMode ?? "chat",
      writeTodosCallCount: outcome.toolCalls.filter(
        (toolCall) => toolCall.toolName === "write_todos" && transitions.isEnabledBuiltInTool(toolCall.toolName),
      ).length,
      hasStartedOrderedWrite: false,
      queuedCount: 0,
    };
    for (const [index, requestedToolCall] of outcome.toolCalls.entries()) {
      const deferralOrder = toolCalls.length + index;
      const toolName = requestedToolCall.toolName;
      if (batch.interactionMode === "planning" && !(toolName === "write_todos" && transitions.isEnabledBuiltInTool(toolName))) {
        this.rejectPlanningTurnToolCall(batch, requestedToolCall, deferralOrder);
      } else if (transitions.isEnabledBuiltInTool(toolName)) {
        this.deferBuiltInToolCall(batch, requestedToolCall, toolName, deferralOrder);
      } else {
        this.deferApplicationToolCall(batch, requestedToolCall, deferralOrder);
      }
    }
    transitions.persistence.pendingInterrupts.set(context, batch.interrupts);
    transitions.recordUsage(context, outcome.usage, events);
    transitions.writeAgentState(context, { ...stateWithMessages, status: "running_tools" });
    batch.movements.push(
      StepMovement.of(new StepRef(agentSessionStepTypes.resumeTurn), outcome.toolCalls.length - batch.queuedCount),
    );
    return goToMany(...batch.movements);
  }

  /** A planning turn hides executable tools; a call to one never runs. */
  private rejectPlanningTurnToolCall(
    batch: ToolCallBatchCommit,
    requestedToolCall: DeferredToolCall,
    deferralOrder: number,
  ): void {
    const isBuiltInTool = this.transitions.isEnabledBuiltInTool(requestedToolCall.toolName);
    const record = createToolCallRecord(
      batch,
      requestedToolCall,
      isBuiltInTool ? "read" : requestedToolCall.policy.effect,
      isBuiltInTool ? false : requestedToolCall.policy.requiresApproval,
      deferralOrder,
    );
    writeToolCallDeferred(batch.events, record);
    this.transitions.finishToolCall(
      batch.context,
      { ...record, status: "rejected", rejectionMessage: planningTurnToolRejection },
      batch.events,
    );
  }

  private deferApplicationToolCall(
    batch: ToolCallBatchCommit,
    requestedToolCall: DeferredToolCall,
    deferralOrder: number,
  ): void {
    const transitions = this.transitions;
    const requiresApproval = requestedToolCall.policy.requiresApproval;
    const effect = requestedToolCall.policy.effect;
    const isOrderedWrite = requestedToolCall.ordered === true && effect !== "read";
    const record: AgentToolCallRecord = {
      ...createToolCallRecord(batch, requestedToolCall, effect, requiresApproval, deferralOrder),
      orderedWrite: isOrderedWrite,
    };
    writeToolCallDeferred(batch.events, record);
    if (isOrderedWrite && batch.hasStartedOrderedWrite) {
      // Started by RunModel once every earlier ordered write of the turn succeeded.
      transitions.writeToolCall(batch.context, { ...record, status: "approved", writeQueued: true });
      batch.queuedCount += 1;
      return;
    }
    batch.hasStartedOrderedWrite ||= isOrderedWrite;
    const deferred: AgentToolCallRecord = { ...record, status: requiresApproval ? "awaiting_approval" : "approved" };
    transitions.writeToolCall(batch.context, deferred);
    if (!requiresApproval) {
      batch.movements.push(toolCallMovement(transitions, deferred));
      return;
    }
    batch.movements.push(
      toolStepMovement(transitions.persistence, agentSessionStepTypes.awaitToolApproval, requestedToolCall.toolCallId),
    );
    const interrupt = toolApprovalInterrupt(record, batch.createdAt, batch.interruptDeadline);
    batch.interrupts.push(interrupt);
    transitions.writeInterruptRaised(batch.events, interrupt);
  }

  /** Records a built-in call with its fixed policy and routes it to its wait Step; invalid arguments fail at once. */
  private deferBuiltInToolCall(
    batch: ToolCallBatchCommit,
    requestedToolCall: DeferredToolCall,
    builtInTool: BuiltInTool,
    deferralOrder: number,
  ): void {
    const transitions = this.transitions;
    const persistence = transitions.persistence;
    const record = createToolCallRecord(batch, requestedToolCall, "read", false, deferralOrder);
    writeToolCallDeferred(batch.events, record);
    switch (builtInTool) {
      case "ask_user": {
        const validation = validateAskUserArguments(record.arguments);
        if (!validation.isValid) {
          this.failBuiltInToolCall(batch, record, validation.error);
          return;
        }
        transitions.writeToolCall(batch.context, { ...record, status: "awaiting_answer" });
        const interrupt: AgentInterrupt = {
          interruptId: record.toolCallId,
          kind: "question",
          toolCallId: record.toolCallId,
          toolName: record.toolName,
          arguments: record.arguments,
          createdAt: batch.createdAt,
          deadline: batch.interruptDeadline,
          question: validation.arguments.question,
          choices: validation.arguments.choices,
          allowsFreeText: validation.arguments.allowsFreeText,
        };
        batch.interrupts.push(interrupt);
        transitions.writeInterruptRaised(batch.events, interrupt);
        batch.movements.push(questionAnswerMovement(persistence, { ...record, status: "awaiting_answer" }));
        return;
      }
      case "durable_wait": {
        const maxDurableWaitSeconds = Math.floor(transitions.config.maxDurableWaitMs / 1_000);
        const validation = validateDurableWaitArguments(record.arguments, maxDurableWaitSeconds);
        if (!validation.isValid) {
          this.failBuiltInToolCall(batch, record, validation.error);
          return;
        }
        const waitUntil = new Date(
          Date.parse(batch.createdAt) + validation.arguments.durationSeconds * 1_000,
        ).toISOString();
        transitions.writeToolCall(batch.context, { ...record, status: "waiting_for_timer", waitUntil });
        batch.events.writeEvent("timer_started", {
          tool_call_id: record.toolCallId,
          wait_until: waitUntil,
          reason: validation.arguments.reason,
        });
        batch.movements.push(toolStepMovement(persistence, agentSessionStepTypes.awaitTimer, record.toolCallId));
        return;
      }
      case "write_todos":
        this.writeTodos(batch, record);
        return;
    }
  }

  /** Replaces the plan and records the call final, in this commit; no tool Step runs. */
  private writeTodos(batch: ToolCallBatchCommit, record: AgentToolCallRecord): void {
    const transitions = this.transitions;
    if (batch.writeTodosCallCount > 1) {
      this.failBuiltInToolCall(batch, record, writeTodosCalledMoreThanOnce);
      return;
    }
    const validation = validateWriteTodosArguments(record.arguments);
    if (!validation.isValid) {
      this.failBuiltInToolCall(batch, record, validation.error);
      return;
    }
    const tasks = validation.arguments;
    const previous = transitions.readOptionalAgentPlan(batch.context);
    const plan: AgentPlan = {
      revision: (previous?.revision ?? 0) + 1,
      status: planStatusAfterWrite(tasks, batch.interactionMode),
      tasks,
      updatedAt: batch.createdAt,
    };
    transitions.persistence.agentPlan.set(batch.context, plan);
    writePlanUpdated(batch.events, plan);
    const output =
      plan.status === "cleared" ? buildPlanClearedOutput(plan.revision) : buildPlanUpdatedOutput(plan.revision, tasks.length);
    transitions.finishToolCall(batch.context, { ...record, status: "succeeded", output }, batch.events);
  }

  private failBuiltInToolCall(batch: ToolCallBatchCommit, record: AgentToolCallRecord, error: string): void {
    this.transitions.finishToolCall(batch.context, { ...record, status: "failed", error }, batch.events);
  }
}

/** Summarizes the messages through a sequence for the model input, then moves back to `RunModel`. */
export class CompactContextStep implements Step<CompactContextInput | undefined> {
  public readonly inputCodec = turnFailureRouteInputCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.compactContext;
  }

  public getStepOptions(): StepOptions {
    return {
      ...syncDurability,
      ...modelCallStepOptions,
      executeLoadAttributeMaps: [this.transitions.persistence.agentMessages],
    };
  }

  public async execute(context: AsyncContext, input: CompactContextInput | undefined): Promise<StepDecision> {
    const transitions = this.transitions;
    if (input === undefined) {
      throw new TypeError("CompactContext needs a CompactContextInput");
    }
    const state = transitions.readAgentState(context);
    const turnId = state.activeTurnId;
    if (turnId === null) {
      throw new Error(`the agent session ${context.flowId} has no active turn`);
    }
    const previousSummary = transitions.readContextSummary(context);
    const firstSequence = previousSummary === null ? 1 : previousSummary.lastSequence + 1;
    const throughSequence = input.throughSequence;
    if (throughSequence < firstSequence || throughSequence >= state.nextMessageSequence) {
      throw new Error(`CompactContext cannot summarize through message ${throughSequence}`);
    }
    const runner = transitions.runner;
    if (!isContextCompactionRunner(runner)) {
      throw new TypeError("CompactContext needs a runner that implements the compaction methods");
    }
    const refusal = await transitions.checkSpendLimit(context, state, turnId, this.getStepType());
    if (refusal !== null) {
      return refusal;
    }
    const events = transitions.createEventWriter(context, turnId);
    try {
      events.writeEvent("compaction_started", { through_sequence: throughSequence });
      const result = await this.summarize(context, runner, turnId, previousSummary, throughSequence, events);
      const summary: ContextSummary = {
        generation: previousSummary === null ? 1 : previousSummary.generation + 1,
        firstSequence: 1,
        lastSequence: throughSequence,
        text: result.text,
        createdAt: new Date().toISOString(),
      };
      transitions.persistence.contextSummary.set(context, summary);
      const index = transitions.readMessageIndex(context);
      if (index !== null) {
        const retainedIndex = transitions.applyMessageRetention(context, index, state.nextMessageSequence, summary);
        if (retainedIndex !== index) {
          transitions.persistence.agentMessageIndex.set(context, retainedIndex);
        }
      }
      transitions.recordUsage(context, result.usage, events);
      transitions.writeAgentState(context, { ...state, status: "running_model" });
      events.writeEvent("compaction_finished", { generation: summary.generation, through_sequence: throughSequence });
      return transitions.goToRunModel(context, summary);
    } finally {
      events.close();
    }
  }

  private async summarize(
    context: AsyncContext,
    runner: ContextCompactionRunner,
    turnId: string,
    previousSummary: ContextSummary | null,
    throughSequence: number,
    events: StreamAgentEventWriter,
  ): Promise<ContextSummaryResult> {
    const transitions = this.transitions;
    const firstSequence = transitions.readModelInputFirstSequence(context, previousSummary);
    const messages = transitions.readMessages(context, firstSequence, throughSequence + 1);
    const policy = transitions.config.compaction;
    return transitions.tracer.traceContextSummary(
      context,
      this.getStepType(),
      turnId,
      throughSequence,
      messages,
      async (summarySpan) => {
        const result = await transitions.runWithHeartbeats(context, async () => {
          const summaryResult = await runner.summarizeContext({
            flowId: context.flowId,
            turnId,
            attempt: context.attempt,
            stepExecutionId: context.stepExecutionId,
            previousSummary,
            messages,
            throughSequence,
            events,
            cancellationSignal: context.cancellationSignal,
            deadline: context.deadline,
            ...(policy === undefined ? {} : { inputTokenBudget: policy.contextWindowTokens }),
            sessionSettings: transitions.readTurnSessionSettings(context),
            heartbeat: () => transitions.recordHeartbeat(context),
          });
          if (typeof summaryResult?.usage === "object") {
            await transitions.reportModelUsage(context, turnId, this.getStepType(), summaryResult.usage);
          }
          return summaryResult;
        });
        if (typeof result?.text !== "string" || result.text.trim() === "" || typeof result.usage !== "object") {
          throw new TypeError("summarizeContext must return summary text and usage");
        }
        summarySpan.recordResult(result);
        return result;
      },
    );
  }
}

/** Everything one `RunModel` commit collects while it records a batch of tool calls. */
interface ToolCallBatchCommit {
  readonly context: Context;
  readonly turnId: string;
  readonly createdAt: string;
  readonly interruptDeadline: string | null;
  readonly interrupts: AgentInterrupt[];
  /** W3C traceparent of the activation root that the batch's tool spans join, or `null`. */
  readonly traceContext: string | null;
  // goToMany takes movements of mixed input types, which the SDK types as StepMovement<any>.
  readonly movements: StepMovement<any>[];
  readonly events: StreamAgentEventWriter;
  /** The turn's mode; a `planning` turn rejects every call but `write_todos`. */
  readonly interactionMode: InteractionMode;
  /** How many calls of the response name the enabled `write_todos`; more than one fails each of them. */
  readonly writeTodosCallCount: number;
  /** Whether an earlier call of the response started as an ordered write; later ones queue. */
  hasStartedOrderedWrite: boolean;
  /** Ordered writes recorded as queued; `ResumeTurn` does not wait for them. */
  queuedCount: number;
}

/** Waits for one tool call's approval or its deadline, then runs or rejects the call. */
export class AwaitToolApprovalStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.awaitToolApproval;
  }

  public getStepOptions(): StepOptions {
    return toolStepOptions(this.transitions.persistence, agentSessionStepTypes.awaitToolApproval);
  }

  public waitFor(context: Context, toolCallId: string): Wait {
    const transitions = this.transitions;
    const conditions: Condition[] = [
      transitions.persistence.toolCallCancellations.forOne(toolCallId),
      transitions.persistence.interruptResolutions.forOne(toolCallId),
    ];
    // A resolved interrupt left PendingInterrupts, so its deadline no longer applies.
    const interrupt = transitions
      .readPendingInterrupts(context)
      .find((pendingInterrupt) => pendingInterrupt.interruptId === toolCallId);
    if (interrupt?.deadline != null) {
      // Dex Timers take whole seconds; rounding up can only fire late, never early.
      const remainingMs = Math.max(0, Date.parse(interrupt.deadline) - Date.now());
      conditions.push(Timer.byDuration(Math.ceil(remainingMs / 1_000) * 1_000));
    }
    return Wait.anyOf(...conditions);
  }

  public execute(context: Context, toolCallId: string): StepDecision {
    const transitions = this.transitions;
    const persistence = transitions.persistence;
    const toolCall = transitions.readToolCall(context, toolCallId);
    if (toolCall.status !== "awaiting_approval") {
      return deadEnd();
    }
    if (context.channelResults(persistence.toolCallCancellations, toolCallId).length > 0) {
      return cancelWaitingToolCall(
        this.transitions,
        context,
        toolCall,
        { interruptId: toolCallId, kind: "tool_approval" },
        toolStepMovement(persistence, agentSessionStepTypes.awaitToolApproval, toolCallId),
      );
    }
    const [resolution] = context.channelResults(persistence.interruptResolutions, toolCallId);
    if (resolution === undefined) {
      if (!context.hasTimerFired()) {
        throw new Error(`the approval wait for ${toolCallId} completed without an answer`);
      }
      const pendingInterrupts = transitions.readPendingInterrupts(context);
      const interrupt = pendingInterrupts.find((pending) => pending.interruptId === toolCallId);
      // resolve_interrupt answered first, or the Timer fired early; wait again.
      if (interrupt?.deadline == null || Date.parse(interrupt.deadline) > Date.now()) {
        return goToMany(
          toolStepMovement(persistence, agentSessionStepTypes.awaitToolApproval, toolCallId),
        );
      }
      persistence.pendingInterrupts.set(
        context,
        pendingInterrupts.filter((interrupt) => interrupt.interruptId !== toolCallId),
      );
      return this.applyResolution(context, toolCall, false, approvalDeadlinePassed, true);
    }
    return this.applyResolution(context, toolCall, resolution.approved, resolution.rejectionMessage, false);
  }

  private applyResolution(
    context: Context,
    toolCall: AgentToolCallRecord,
    isApproved: boolean,
    rejectionMessage: string | null,
    isDeadlinePassed: boolean,
  ): StepDecision {
    return this.transitions.tracer.traceInterruptActivation(
      context,
      this.getStepType(),
      toolCall.turnId,
      toolCall.toolCallId,
      isApproved,
      isDeadlinePassed,
      (activation) => {
        if (!isApproved) {
          return this.rejectToolCall(context, toolCall, rejectionMessage);
        }
        // The approval starts a new activation, so the tool's spans join its root, not the RunModel one.
        const { traceContext: _deferredTraceContext, ...deferredToolCall } = toolCall;
        this.transitions.writeToolCall(context, {
          ...deferredToolCall,
          status: "approved",
          cancellationReason: null,
          ...(activation.traceparent === null ? {} : { traceContext: activation.traceparent }),
        });
        this.transitions.writeInterruptResolved(context, toolCall.turnId, {
          interruptId: toolCall.toolCallId,
          kind: "tool_approval",
          isApproved: true,
          isAnswered: false,
          rejectionMessage: null,
        });
        return goToMany(toolCallMovement(this.transitions, { ...toolCall, status: "approved" }));
      },
    );
  }

  private rejectToolCall(
    context: Context,
    toolCall: AgentToolCallRecord,
    rejectionMessage: string | null,
  ): StepDecision {
    this.transitions.writeInterruptResolved(context, toolCall.turnId, {
      interruptId: toolCall.toolCallId,
      kind: "tool_approval",
      isApproved: false,
      isAnswered: false,
      rejectionMessage,
    });
    this.transitions.finishToolCall(context, {
      ...toolCall,
      status: "rejected",
      rejectionMessage,
      cancellationReason: null,
    });
    return deadEnd();
  }
}

/** Runs one tool call; one class serves `CallTool` and `CallSingleAttemptTool`. */
export class ToolCallStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(
    private readonly transitions: AgentSessionTransitions,
    private readonly stepType: string,
  ) {}

  public getStepType(): string {
    return this.stepType;
  }

  public getStepOptions(): StepOptions {
    return toolStepOptions(this.transitions.persistence, this.stepType);
  }

  public async execute(context: AsyncContext, toolCallId: string): Promise<StepDecision> {
    const transitions = this.transitions;
    const toolCall = transitions.readToolCall(context, toolCallId);
    if (isFinalToolCallStatus(toolCall.status)) {
      return deadEnd();
    }
    if (toolCall.status !== "approved" || toolStepTypeForEffect(toolCall.effect) !== this.stepType) {
      throw new Error(`tool call ${toolCallId} cannot run in ${this.stepType}`);
    }
    // Retries reuse the first attempt's state, so a call that started is never abandoned.
    const reason = transitions.readTurnControlReason(context, toolCall.turnId);
    if (reason !== null) {
      transitions.finishToolCall(context, cancelledToolCall(toolCall, reason));
      return deadEnd();
    }
    const events = transitions.createEventWriter(context, toolCall.turnId);
    try {
      return await transitions.tracer.traceToolCall(context, this.stepType, toolCall, async (toolCallSpan) => {
        events.writeToolCallStarted(toolCall.toolCallId, toolCall.toolName);
        const heartbeats = new ToolStepHeartbeats(context);
        // Output first: Dex returns an attempt as not run when its stream half-closes before any output.
        await heartbeats.record();
        const execution: DeferredToolExecution = {
          flowId: context.flowId,
          toolCall: toRunnerToolCall(toolCall),
          idempotencyKey: `${context.flowId}/${toolCall.turnId}/${toolCall.toolCallId}`,
          attempt: context.attempt,
          events,
          cancellationSignal: context.cancellationSignal,
          deadline: context.deadline,
          inputAnswers: toolCall.inputAnswers,
          supportsToolTasks: transitions.runner.pollToolTask !== undefined,
          heartbeat: () => heartbeats.record(),
          ...(heartbeats.previousCheckpoint === undefined ? {} : { heartbeatCheckpoint: heartbeats.previousCheckpoint }),
          saveHeartbeatCheckpoint: (checkpoint: string) => heartbeats.saveCheckpoint(checkpoint),
          context,
        };
        let outcome: DeferredToolOutcome;
        try {
          outcome = await transitions.runWithHeartbeats(
            context,
            () => transitions.runner.executeDeferredToolCall(execution),
            () => heartbeats.record(),
          );
        } catch (error: unknown) {
          if (error instanceof ToolOutcomeUnknownError) {
            const unknown: AgentToolCallRecord = { ...toolCall, status: "outcome_unknown", error: error.message };
            toolCallSpan.recordFinished(unknown);
            transitions.finishToolCall(context, unknown, events);
            return deadEnd();
          }
          if (!(error instanceof ToolExecutionError)) {
            throw error;
          }
          const failed: AgentToolCallRecord = { ...toolCall, status: "failed", error: error.message };
          toolCallSpan.recordFinished(failed);
          transitions.finishToolCall(context, failed, events);
          return deadEnd();
        }
        if (isToolInputRequested(outcome)) {
          return this.requestToolInput(context, toolCall, outcome, events, toolCallSpan);
        }
        if (isToolTaskStarted(outcome)) {
          return this.startToolTask(context, toolCall, outcome, events, toolCallSpan);
        }
        if (typeof outcome?.output !== "string") {
          throw new TypeError(`executeDeferredToolCall returned no output for ${toolCallId}`);
        }
        const succeeded: AgentToolCallRecord = {
          ...toolCall,
          status: "succeeded",
          output: outcome.output,
          truncatedOutputBytes: outcome.truncatedOutputBytes ?? null,
          runnerToolState: outcome.runnerToolState ?? null,
          ...toolResultImages(outcome.images),
        };
        toolCallSpan.recordFinished(succeeded);
        if (outcome.isFinishedByResumedTurn === true) {
          // The resumed turn runs the call and writes its tool_call_finished.
          transitions.writeToolCall(context, succeeded);
          transitions.publishToolCallResult(context, succeeded.toolCallId);
          return deadEnd();
        }
        transitions.finishToolCall(context, succeeded, events);
        return deadEnd();
      });
    } finally {
      events.close();
    }
  }

  /** Records `awaiting_answer` with the tool's question; `AwaitQuestionAnswer` raises it under the lock. */
  private requestToolInput(
    context: Context,
    toolCall: AgentToolCallRecord,
    request: ToolInputRequested,
    events: StreamAgentEventWriter,
    toolCallSpan: ToolCallSpan,
  ): StepDecision {
    const transitions = this.transitions;
    const waiting = recordToolInputRequest(toolCall, request);
    toolCallSpan.recordFinished(waiting);
    if (isFinalToolCallStatus(waiting.status)) {
      transitions.finishToolCall(context, waiting, events);
      return deadEnd();
    }
    // This Step runs unlocked, so it leaves PendingInterrupts to AwaitQuestionAnswer.
    transitions.writeToolCall(context, waiting);
    return goToMany(questionAnswerMovement(transitions.persistence, waiting));
  }

  /** Records `waiting_for_task` and moves to `PollToolTask`, or records a task it cannot poll by effect. */
  private startToolTask(
    context: Context,
    toolCall: AgentToolCallRecord,
    started: ToolTaskStarted,
    events: StreamAgentEventWriter,
    toolCallSpan: ToolCallSpan,
  ): StepDecision {
    const transitions = this.transitions;
    const pollIntervalSeconds = toolTaskPollIntervalSeconds(started.pollIntervalSeconds);
    const defect =
      transitions.runner.pollToolTask === undefined
        ? missingToolTaskPoller
        : typeof started.taskId !== "string" || isBlankText(started.taskId)
          ? blankToolTaskId
          : pollIntervalSeconds === null
            ? invalidToolTaskPollInterval
            : undefined;
    if (defect !== undefined || pollIntervalSeconds === null) {
      const finished = recordToolTaskDefect(toolCall, defect ?? invalidToolTaskPollInterval);
      toolCallSpan.recordFinished(finished);
      transitions.finishToolCall(context, finished, events);
      return deadEnd();
    }
    // The tool consumed any continuation of its own questions when it ran again.
    const waiting: AgentToolCallRecord = {
      ...toolCall,
      status: "waiting_for_task",
      taskId: started.taskId,
      taskPollIntervalSeconds: pollIntervalSeconds,
      inputContinuation: null,
      ...noPendingToolQuestion,
    };
    toolCallSpan.recordFinished(waiting);
    transitions.writeToolCall(context, waiting);
    events.writeEvent("tool_task_started", {
      tool_call_id: waiting.toolCallId,
      tool_name: waiting.toolName,
      task_id: started.taskId,
    });
    return goToMany(toolTaskPollMovement(transitions.persistence, waiting));
  }
}

/**
 * Failure route of a tool Step; it receives the failed Step's input, the tool call ID.
 * `RecordToolFailure` records `failed` for a `read` call and `outcome_unknown` for a write;
 * `RecordToolOutcomeUnknown` always records `outcome_unknown`. Either records `failed` with
 * {@link toolCallNotRunErrorPrefix} when the tool certainly never ran.
 */
export class ToolCallFailureRecordingStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(
    private readonly transitions: AgentSessionTransitions,
    private readonly stepType: string,
    private readonly isReadFailureRecorded: boolean,
  ) {}

  public getStepType(): string {
    return this.stepType;
  }

  public getStepOptions(): StepOptions {
    return toolStepOptions(this.transitions.persistence, this.stepType);
  }

  public execute(context: Context, toolCallId: string): StepDecision {
    const transitions = this.transitions;
    if (isStateLimitFailure(context.recoveryError)) {
      return recordFailedSession(transitions, context, readRecoveryDetail(context));
    }
    const toolCall = transitions.readToolCall(context, toolCallId);
    if (isFinalToolCallStatus(toolCall.status)) {
      return deadEnd();
    }
    const status =
      this.isReadFailureRecorded && toolCall.effect === "read" ? "failed" : "outcome_unknown";
    transitions.finishToolCall(context, recoveredToolCall(toolCall, context.recoveryError, status));
    return deadEnd();
  }
}

/** Returns the final record that a tool Step's failure route writes. */
export function recoveredToolCall(
  toolCall: AgentToolCallRecord,
  recoveryError: RecoveryErrorInfo | undefined,
  status: "failed" | "outcome_unknown",
): AgentToolCallRecord {
  // An abandoned task keeps the text CancelToolTask records, whatever made its attempts fail.
  const error =
    isAbandonedToolTask(toolCall) && (toolCall.cancellationReason ?? null) !== null
      ? toolTaskAbandonedError
      : recoveryErrorDetail(recoveryError);
  if (isToolCallNotRun(toolCall, recoveryError)) {
    return { ...toolCall, status: "failed", error: `${toolCallNotRunErrorPrefix}: ${error}` };
  }
  return { ...toolCall, status, error };
}

/** Returns whether the failed Step certainly never ran the call's tool. */
function isToolCallNotRun(toolCall: AgentToolCallRecord, recoveryError: RecoveryErrorInfo | undefined): boolean {
  // Polls and cancellations follow a tool that already ran, as does an answered question's call.
  return (
    recoveryError?.attemptProgress === StepAttemptProgress.NOT_STARTED &&
    toolCall.status === "approved" &&
    toolCall.inputAnswers.length === 0
  );
}

/**
 * Waits until every tool call of the batch has a final record, then makes the next model call.
 *
 * A steer wakes it early: it cancels the turn's pending interrupts and Timers, and waits again. After a steer or
 * `cancel_turn`, once every result is in, it moves to `ApplyTurnControl` instead of `RunModel`.
 */
export class ResumeTurnStep implements Step<number> {
  public readonly inputCodec = expectedResultCountCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.resumeTurn;
  }

  public getStepOptions(): StepOptions {
    const persistence = this.transitions.persistence;
    // A control wake rewrites PendingInterrupts, which the interrupt RPCs also write.
    return {
      ...syncDurability,
      executeLoadAttributeMaps: [persistence.agentToolCalls],
      executeLockAttributes: [persistence.pendingInterrupts.lock()],
    };
  }

  public waitFor(_context: Context, expectedResultCount: number): Wait {
    const persistence = this.transitions.persistence;
    return Wait.anyOf(
      persistence.toolCallResults.forN(expectedResultCount),
      persistence.turnControlRequests.range(1, allPendingMessages),
    );
  }

  public execute(context: Context, expectedResultCount: number): StepDecision {
    const transitions = this.transitions;
    const state = transitions.readAgentState(context);
    const turnId = state.activeTurnId;
    const toolCalls = transitions.readTurnToolCalls(context, turnId);
    const reason = transitions.readTurnControlReason(context, turnId);
    if (context.channelResults(transitions.persistence.turnControlRequests).length > 0) {
      // FinishTurnCancellation, not this wake, ends the waits of a cancelled turn.
      if (reason === "steered" && turnId !== null) {
        this.wakeWaitingToolCalls(context, turnId, toolCalls, reason);
      }
      // The results did not all arrive, so wait again for the same count.
      return goTo(new StepRef(agentSessionStepTypes.resumeTurn), expectedResultCount);
    }
    const unfinishedCount = toolCalls.filter(
      (toolCall) => !isFinalToolCallStatus(toolCall.status) && toolCall.writeQueued !== true,
    ).length;
    if (unfinishedCount > 0) {
      // A consumed result from an earlier turn must not resume this one early.
      return goTo(new StepRef(agentSessionStepTypes.resumeTurn), unfinishedCount);
    }
    if (reason !== null) {
      transitions.writeEvents(context, turnId, (events) => {
        for (const toolCall of toolCalls.filter((queued) => queued.writeQueued === true)) {
          transitions.finishUnscheduledToolCall(context, { ...cancelledToolCall(toolCall, reason), writeQueued: false }, events);
        }
      });
      return transitions.goToApplyTurnControl(context);
    }
    transitions.writeAgentState(context, { ...state, status: "running_model" });
    return transitions.goToRunModel(context);
  }

  /** Cancels the turn's pending interrupts and wakes each Step waiting for a person or a Timer. */
  private wakeWaitingToolCalls(
    context: Context,
    turnId: string,
    toolCalls: readonly AgentToolCallRecord[],
    reason: ToolCallCancellationReason,
  ): void {
    const transitions = this.transitions;
    const persistence = transitions.persistence;
    const interrupts = transitions.readPendingInterrupts(context);
    const cancelledInterruptIds = new Set<string>();
    for (const toolCall of toolCalls) {
      if (!isCancellableByTurnControl(toolCall)) {
        continue;
      }
      if (toolCall.status === "waiting_for_timer") {
        // A later control wake finds the first message still pending and adds none.
        if (context.channelSize(persistence.toolCallCancellations, toolCall.toolCallId) === 0) {
          persistence.toolCallCancellations.publish(context, toolCall.toolCallId, { reason });
        }
        continue;
      }
      const isApproval = toolCall.status === "awaiting_approval";
      const interruptId = isApproval ? toolCall.toolCallId : questionInterruptId(toolCall);
      const kind: AgentInterruptKind = isApproval ? "tool_approval" : "question";
      // An answered interrupt is gone, so its answer wins; an unraised question checks flags.
      if (
        interrupts.some(
          (interrupt) => interrupt.interruptId === interruptId && interrupt.toolCallId === toolCall.toolCallId,
        )
      ) {
        cancelledInterruptIds.add(interruptId);
        transitions.writeInterruptCancelled(context, turnId, interruptId, kind, reason);
        persistence.toolCallCancellations.publish(context, toolCall.toolCallId, { reason });
      }
    }
    if (cancelledInterruptIds.size > 0) {
      persistence.pendingInterrupts.set(
        context,
        interrupts.filter((interrupt) => !cancelledInterruptIds.has(interrupt.interruptId)),
      );
    }
  }
}

/** Ends a steered or cancelled turn, then starts a turn with the steered messages. */
export class ApplyTurnControlStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.applyTurnControl;
  }

  public getStepOptions(): StepOptions {
    // Each movement into ApplyTurnControl adds the archive chunks of the model input.
    // The lock keeps a turn from starting while cancel_turn decides about the last one.
    return {
      ...messageStepOptions(
        this.transitions.persistence,
        this.getStepType(),
        staticMessageChunkLoads(this.transitions),
        this.transitions.config.runModelLimit,
      ),
      executeLockAttributes: [this.transitions.persistence.turnCancellationRequest.lock()],
    };
  }

  public waitFor(): Wait {
    const persistence = this.transitions.persistence;
    // Both conditions are non-blocking: the wait consumes every steered message and stale wake.
    return Wait.allOf(
      persistence.steeredUserMessages.atMost(allPendingMessages),
      persistence.turnControlRequests.atMost(allPendingMessages),
    );
  }

  public execute(context: Context): StepDecision {
    const transitions = this.transitions;
    let state = transitions.readAgentState(context);
    const steeredMessages = context.channelResults(transitions.persistence.steeredUserMessages);
    if (state.activeTurnId !== null) {
      state = transitions.endControlledTurn(context, state, state.activeTurnId, steeredMessages.length);
    }
    if (steeredMessages.length === 0) {
      return transitions.goToAwaitUserMessage();
    }
    // Any steered message with planMode, or a draft plan, makes it a planning turn.
    return transitions.beginTurn(
      context,
      state,
      steeredMessages,
      this.getStepType(),
      transitions.decideInteractionMode(context, steeredMessages),
    );
  }
}

// server-zd reports a Step whose commit would pass limits.maxFlowStateBytes with this type and text:
// "run state of <X> bytes exceeds the maximum of <Y> bytes", or "active run state of ..." since the limit
// counts only active state.
const stateLimitErrorType = "ValueTooLarge";
const stateLimitErrorText = "run state of ";

/** Ends a failed turn; it is the failure route of `RunModel` and `CompactContext`, so it takes either's input. */
export class RecordTurnFailureStep implements Step<CompactContextInput | undefined> {
  public readonly inputCodec = turnFailureRouteInputCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.recordTurnFailure;
  }

  public getStepOptions(): StepOptions {
    const persistence = this.transitions.persistence;
    const hasUnfinishedTurnMessages = this.transitions.runner.buildUnfinishedTurnMessages !== undefined;
    return {
      ...syncDurability,
      executeRetry: recoveryRetry,
      // A failure route has only static options, so an incremental runner loads every retained chunk.
      executeLoadAttributeMaps: hasUnfinishedTurnMessages
        ? [persistence.agentToolCalls, persistence.agentMessages, persistence.agentMessageChunks]
        : [persistence.agentToolCalls, persistence.agentMessages],
    };
  }

  public execute(context: Context): StepDecision {
    const transitions = this.transitions;
    if (isStateLimitFailure(context.recoveryError)) {
      return recordFailedSession(transitions, context, readRecoveryDetail(context));
    }
    const state = transitions.readAgentState(context);
    const toolCalls = transitions.readTurnToolCalls(context, state.activeTurnId);
    const finalToolCalls = toolCalls.filter((toolCall) => isFinalToolCallStatus(toolCall.status));
    const unfinishedTurnMessages =
      state.activeTurnId === null
        ? null
        : transitions.buildUnfinishedTurnMessages(context, state, state.activeTurnId, finalToolCalls);
    // A refused model call moves here on purpose, with its reason already in the state.
    const error =
      context.recoveryError === undefined && state.lastTurnError ? state.lastTurnError : readRecoveryDetail(context);
    return recordFailedTurn(transitions, context, state, toolCalls, unfinishedTurnMessages ?? [], error);
  }
}

/** Reports whether a Step failed because its commit would exceed the Flow's state limit. */
export function isStateLimitFailure(recoveryError: RecoveryErrorInfo | undefined): boolean {
  // The Server gives this failure no code of its own; a value over its own limit shares the type.
  return recoveryError?.errorType === stateLimitErrorType && recoveryError.detail.includes(stateLimitErrorText);
}

/**
 * Ends a session whose Flow reached the state limit, in one commit with the Flow's close. This commit is checked
 * against the same limit, so it stores no turn messages and keeps the turn's tool call records. When it does not
 * fit, Dex fails the Flow with the same text and `AgentState` stays as it was; see `agent-session-failure.ts`.
 */
function recordFailedSession(
  transitions: AgentSessionTransitions,
  context: Context,
  error: string,
): StepDecision {
  const persistence = transitions.persistence;
  const state = transitions.readAgentState(context);
  persistence.frameworkRunState.delete(context);
  if (transitions.readPendingInterrupts(context).length > 0) {
    persistence.pendingInterrupts.set(context, []);
  }
  transitions.writeAgentState(context, {
    ...state,
    status: "failed",
    turnCount: state.turnCount + (state.activeTurnId === null ? 0 : 1),
    activeTurnId: null,
    lastTurnError: error,
    acceptsTurnControl: false,
  });
  transitions.writeEvents(context, state.activeTurnId, (events) => events.writeEvent("turn_failed", { error }));
  return forceFail(error);
}

/**
 * Ends the active turn as failed in the current commit: appends the unfinished-turn messages, then the
 * turn-failure note when the turn has final records, and clears the turn.
 */
function recordFailedTurn(
  transitions: AgentSessionTransitions,
  context: Context,
  initialState: AgentState,
  toolCalls: readonly AgentToolCallRecord[],
  unfinishedTurnMessages: readonly FrameworkMessage[],
  turnError: string,
): StepDecision {
  const persistence = transitions.persistence;
  const turnId = initialState.activeTurnId;
  let state = initialState;
  const finalToolCalls = toolCalls.filter((toolCall) => isFinalToolCallStatus(toolCall.status));
  if (turnId !== null) {
    state = transitions.appendMessages(context, state, turnId, unfinishedTurnMessages);
  }
  // The records are deleted below; the note keeps the next model from repeating writes.
  if (turnId !== null && finalToolCalls.length > 0) {
    const note = transitions.runner.buildUserMessage(buildTurnFailureNote(finalToolCalls));
    if (note.role !== "user") {
      throw new TypeError("buildUserMessage must return a message with role user");
    }
    state = transitions.appendMessages(context, state, turnId, [note]);
  }
  for (const toolCall of toolCalls) {
    persistence.agentToolCalls.delete(context, toolCall.toolCallId);
  }
  persistence.frameworkRunState.delete(context);
  persistence.pendingInterrupts.set(context, []);
  transitions.writeAgentState(context, {
    ...state,
    status: "waiting_for_user_message",
    turnCount: state.turnCount + 1,
    activeTurnId: null,
    lastTurnError: turnError,
    acceptsTurnControl: false,
  });
  transitions.writeEvents(context, turnId, (events) => events.writeEvent("turn_failed", { error: turnError }));
  return transitions.goToAwaitUserMessage();
}

/**
 * Chunk loads of a Step's static options, used by a retry or by a movement an earlier version scheduled: none
 * beyond the window with compaction, else every chunk.
 */
function staticMessageChunkLoads(transitions: AgentSessionTransitions): MessageChunkLoads {
  return { isWholeMap: transitions.config.compaction === undefined, instances: [] };
}

/** The `AgentMessages` keys that appending these messages after the state's last one will use. */
function appendedMessageKeys(state: AgentState, messages: readonly FrameworkMessage[]): string[] {
  return messages.map((_message, offset) => messageSequenceKey(state.nextMessageSequence + offset));
}

/** `last_turn_error` of a turn whose runner stored messages incrementally without the builder. */
export const missingUnfinishedTurnMessageBuilder =
  "a runner that sets ToolCallsRequested.newMessages must implement buildUnfinishedTurnMessages";

function validateTurnContinued(turnId: string, outcome: TurnContinued): void {
  if (typeof outcome.frameworkRunState !== "string" || outcome.frameworkRunState === "") {
    throw new TypeError(`TurnContinued for ${turnId} needs a non-empty frameworkRunState`);
  }
  if (!Array.isArray(outcome.newMessages)) {
    throw new TypeError(`TurnContinued.newMessages for ${turnId} must be an array`);
  }
}

/** Returns `TurnCompleted.turnError`, or `null` for a completed turn. */
function readTurnError(turnId: string, outcome: TurnCompleted): string | null {
  const turnError: unknown = outcome.turnError;
  if (turnError === undefined) {
    return null;
  }
  if (typeof turnError !== "string" || turnError.trim() === "") {
    throw new TypeError(`TurnCompleted.turnError for ${turnId} must be a non-blank string`);
  }
  return turnError;
}

function validateToolCallsRequested(
  turnId: string,
  existingToolCalls: readonly AgentToolCallRecord[],
  outcome: ToolCallsRequested,
): void {
  if (typeof outcome.frameworkRunState !== "string" || outcome.frameworkRunState === "") {
    throw new TypeError(`ToolCallsRequested for ${turnId} needs a non-empty frameworkRunState`);
  }
  if (outcome.newMessages !== undefined && !Array.isArray(outcome.newMessages)) {
    throw new TypeError(`ToolCallsRequested.newMessages for ${turnId} must be an array`);
  }
  if (outcome.toolCalls.length === 0) {
    throw new TypeError(`ToolCallsRequested for ${turnId} needs at least one tool call`);
  }
  const seenIds = new Set(existingToolCalls.map((toolCall) => toolCall.toolCallId));
  for (const requestedToolCall of outcome.toolCalls) {
    const toolCallId = requestedToolCall.toolCallId;
    if (toolCallId.trim() === "" || toolCallId.includes("/")) {
      throw new TypeError(`tool call ID ${JSON.stringify(toolCallId)} is empty or contains '/'`);
    }
    if (seenIds.has(toolCallId)) {
      throw new TypeError(`tool call ${toolCallId} was already requested in ${turnId}`);
    }
    seenIds.add(toolCallId);
  }
}

/**
 * Returns the full options of a tool Step type. With a tool call ID, the Step loads only that call's
 * record; a movement's options replace the Step's static options, so every option is repeated here.
 */
export function toolStepOptions(
  persistence: AgentSessionPersistence,
  stepType: string,
  toolCallId?: string,
): StepOptions {
  const loads =
    toolCallId === undefined
      ? { executeLoadAttributeMaps: [persistence.agentToolCalls] }
      : { executeLoadAttributeMapInstances: [persistence.agentToolCalls.load(toolCallId)] };
  const waitForLoads =
    toolCallId === undefined
      ? { waitForLoadAttributeMaps: [persistence.agentToolCalls] }
      : { waitForLoadAttributeMapInstances: [persistence.agentToolCalls.load(toolCallId)] };
  switch (stepType) {
    case agentSessionStepTypes.awaitToolApproval:
      // The deadline path rewrites PendingInterrupts, which resolve_interrupt and sibling Steps also write.
      return { ...syncDurability, ...loads, executeLockAttributes: [persistence.pendingInterrupts.lock()] };
    case agentSessionStepTypes.awaitQuestionAnswer:
      // Raising a tool's question and the deadline path rewrite PendingInterrupts, as in AwaitToolApproval.
      return {
        ...syncDurability,
        ...loads,
        ...waitForLoads,
        // A movement loads only its question's instance; see questionAnswerMovement.
        ...(toolCallId === undefined
          ? {
              waitForLoadChannelMaps: [persistence.interruptResolutions],
              executeLoadChannelMaps: [persistence.interruptResolutions],
            }
          : {}),
        executeLockAttributes: [persistence.pendingInterrupts.lock()],
      };
    case agentSessionStepTypes.awaitTimer:
      return { ...syncDurability, ...loads, ...waitForLoads };
    case agentSessionStepTypes.callTool:
      return {
        ...syncDurability,
        ...loads,
        executeRetry: { maximumAttempts: 3 },
        executeFailure: ExecuteFailure.proceedTo(
          new StepRef(agentSessionStepTypes.recordToolFailure),
          toolStepOptions(persistence, agentSessionStepTypes.recordToolFailure, toolCallId),
        ),
      };
    case agentSessionStepTypes.callSingleAttemptTool:
      return {
        ...syncDurability,
        ...loads,
        executeRetry: { maximumAttempts: 1 },
        executeFailure: ExecuteFailure.proceedTo(
          new StepRef(agentSessionStepTypes.recordToolOutcomeUnknown),
          toolStepOptions(persistence, agentSessionStepTypes.recordToolOutcomeUnknown, toolCallId),
        ),
      };
    case agentSessionStepTypes.pollToolTask:
      return toolTaskPollOptions(persistence, agentSessionStepTypes.recordToolFailure, toolCallId);
    case agentSessionStepTypes.recordToolFailure:
    case agentSessionStepTypes.recordToolOutcomeUnknown:
      return { ...syncDurability, ...loads, executeRetry: recoveryRetry };
    case agentSessionStepTypes.cancelToolTask:
      // Retries that run out still record the call, so the cancelled turn always ends.
      return {
        ...syncDurability,
        ...loads,
        executeMethodTimeoutMs: 30_000,
        executeRetry: { maximumAttempts: 3, totalDurationMs: 2 * minuteMs },
        executeFailure: ExecuteFailure.proceedTo(
          new StepRef(agentSessionStepTypes.recordToolOutcomeUnknown),
          toolStepOptions(persistence, agentSessionStepTypes.recordToolOutcomeUnknown, toolCallId),
        ),
      };
    default:
      throw new TypeError(`${stepType} is not a tool Step type`);
  }
}

export function toolStepMovement(
  persistence: AgentSessionPersistence,
  stepType: string,
  toolCallId: string,
): StepMovement<string> {
  return StepMovement.of(new StepRef(stepType), toolCallId, toolStepOptions(persistence, stepType, toolCallId));
}

/**
 * Returns the movement that starts an approved call's tool Step, with the runner's timeout and retry policy.
 * Only `CallTool` takes the retry policy; `CallSingleAttemptTool` keeps one attempt.
 */
export function toolCallMovement(transitions: AgentSessionTransitions, toolCall: AgentToolCallRecord): StepMovement<string> {
  const stepType = toolStepTypeForEffect(toolCall.effect);
  const options = toolStepOptions(transitions.persistence, stepType, toolCall.toolCallId);
  const overrides = readToolCallStepOptions(transitions, toolCall);
  return StepMovement.of(new StepRef(stepType), toolCall.toolCallId, {
    ...options,
    ...(overrides?.executeMethodTimeout === undefined ? {} : { executeMethodTimeoutMs: overrides.executeMethodTimeout }),
    ...(overrides?.executeRetry === undefined || stepType !== agentSessionStepTypes.callTool
      ? {}
      : { executeRetry: overrides.executeRetry }),
  });
}

function readToolCallStepOptions(
  transitions: AgentSessionTransitions,
  toolCall: AgentToolCallRecord,
): ToolCallStepOptions | undefined {
  const provide = transitions.runner.toolCallStepOptions;
  if (provide === undefined) {
    return undefined;
  }
  try {
    return provide.call(transitions.runner, toRunnerToolCall(toolCall));
  } catch (error: unknown) {
    // The call then runs with the defaults; a missing tool fails by its effect class.
    console.warn(`dex-agents: toolCallStepOptions failed for ${toolCall.toolName}:`, error);
    return undefined;
  }
}

function toolApprovalInterrupt(
  toolCall: AgentToolCallRecord,
  createdAt: string,
  deadline: string | null,
): AgentInterrupt {
  return {
    interruptId: toolCall.toolCallId,
    kind: "tool_approval",
    toolCallId: toolCall.toolCallId,
    toolName: toolCall.toolName,
    arguments: toolCall.arguments,
    createdAt,
    deadline,
    question: null,
    choices: [],
    allowsFreeText: true,
    ...(toolCall.approvalDisplay === undefined || toolCall.approvalDisplay === null
      ? {}
      : { display: toolCall.approvalDisplay }),
  };
}

function interruptDeadline(transitions: AgentSessionTransitions, createdAt: Date): string | null {
  const deadlineMs = transitions.config.interruptDeadlineMs;
  return deadlineMs === undefined ? null : new Date(createdAt.getTime() + deadlineMs).toISOString();
}

function skippedToolCall(toolCall: AgentToolCallRecord): AgentToolCallRecord {
  return {
    ...toolCall,
    status: "failed",
    writeQueued: false,
    skipped: true,
    error: `${toolCallNotRunErrorPrefix}: ${toolCallSkippedAfterWriteDetail}`,
  };
}

/**
 * Returns the options of `PollToolTask`. It takes no lock and never waits on a cancellation: it owns the
 * record, and a started task runs on.
 */
function toolTaskPollOptions(
  persistence: AgentSessionPersistence,
  recoveryStepType: string,
  toolCallId: string | undefined,
): StepOptions {
  const loads =
    toolCallId === undefined
      ? {
          waitForLoadAttributeMaps: [persistence.agentToolCalls],
          executeLoadAttributeMaps: [persistence.agentToolCalls],
        }
      : {
          waitForLoadAttributeMapInstances: [persistence.agentToolCalls.load(toolCallId)],
          executeLoadAttributeMapInstances: [persistence.agentToolCalls.load(toolCallId)],
        };
  return {
    ...syncDurability,
    ...loads,
    executeRetry: { maximumAttempts: 3 },
    executeFailure: ExecuteFailure.proceedTo(
      new StepRef(recoveryStepType),
      toolStepOptions(persistence, recoveryStepType, toolCallId),
    ),
  };
}

/** Moves to `PollToolTask`; a poll that keeps failing leaves the task running, so the call is recorded by effect. */
export function toolTaskPollMovement(
  persistence: AgentSessionPersistence,
  toolCall: AgentToolCallRecord,
): StepMovement<string> {
  const recoveryStepType =
    toolStepTypeForEffect(toolCall.effect) === agentSessionStepTypes.callTool
      ? agentSessionStepTypes.recordToolFailure
      : agentSessionStepTypes.recordToolOutcomeUnknown;
  return StepMovement.of(
    new StepRef(agentSessionStepTypes.pollToolTask),
    toolCall.toolCallId,
    toolTaskPollOptions(persistence, recoveryStepType, toolCall.toolCallId),
  );
}

/** Returns the record of a tool's question: `awaiting_answer`, or `failed` when it is blank. */
export function recordToolInputRequest(
  toolCall: AgentToolCallRecord,
  request: ToolInputRequested,
): AgentToolCallRecord {
  const choices = request.choices ?? [];
  if (
    typeof request.question !== "string" ||
    isBlankText(request.question) ||
    !choices.every((choice) => typeof choice === "string" && !isBlankText(choice))
  ) {
    return { ...toolCall, status: "failed", error: "the tool asked for input with a blank question or choice" };
  }
  return {
    ...toolCall,
    status: "awaiting_answer",
    inputContinuation: request.continuation,
    inputQuestion: request.question,
    inputChoices: [...choices],
    inputAllowsFreeText: (request.allowsFreeText ?? true) || choices.length === 0,
  };
}

/** Returns a final record for a task that cannot be polled: `failed` for a read, else `outcome_unknown`. */
export function recordToolTaskDefect(toolCall: AgentToolCallRecord, error: string): AgentToolCallRecord {
  return {
    ...toolCall,
    status: toolCall.effect === "read" ? "failed" : "outcome_unknown",
    error,
    ...noPendingToolQuestion,
  };
}

/** Returns a poll interval in whole seconds from 1 to a day, or `null` when it is not a finite number. */
export function toolTaskPollIntervalSeconds(seconds: unknown): number | null {
  if (typeof seconds !== "number" || !Number.isFinite(seconds)) {
    return null;
  }
  // Dex Timers take whole seconds, and a zero interval would poll without a pause.
  return Math.min(Math.max(1, Math.ceil(seconds)), maximumToolTaskPollIntervalSeconds);
}

function createToolCallRecord(
  batch: ToolCallBatchCommit,
  requestedToolCall: DeferredToolCall,
  effect: AgentToolCallRecord["effect"],
  requiresApproval: boolean,
  deferralOrder: number,
): AgentToolCallRecord {
  return {
    toolCallId: requestedToolCall.toolCallId,
    turnId: batch.turnId,
    toolName: requestedToolCall.toolName,
    arguments: requestedToolCall.arguments,
    effect,
    requiresApproval,
    status: "approved",
    output: null,
    error: null,
    rejectionMessage: null,
    deferralOrder,
    ...(requestedToolCall.toolSourceReference === undefined
      ? {}
      : { toolSourceReference: requestedToolCall.toolSourceReference }),
    ...(batch.traceContext === null ? {} : { traceContext: batch.traceContext }),
    waitUntil: null,
    inputContinuation: null,
    inputAnswers: [],
    ...noPendingToolQuestion,
    cancellationReason: null,
    taskId: null,
    taskPollIntervalSeconds: null,
    taskAnswerPending: false,
    orderedWrite: false,
    writeQueued: false,
    skipped: false,
    truncatedOutputBytes: null,
    runnerToolState: null,
    ...approvalDisplayOf(requestedToolCall, requiresApproval),
  };
}

function approvalDisplayOf(
  requestedToolCall: DeferredToolCall,
  requiresApproval: boolean,
): { readonly approvalDisplay?: AgentInterruptDisplay } {
  const approvalDisplay = requiresApproval ? boundApprovalDisplay(requestedToolCall.approvalDisplay) : null;
  return approvalDisplay === null ? {} : { approvalDisplay };
}

function writeToolCallDeferred(events: StreamAgentEventWriter, record: AgentToolCallRecord): void {
  events.writeEvent("tool_call_deferred", {
    tool_call_id: record.toolCallId,
    tool_name: record.toolName,
    effect: record.effect,
    requires_approval: record.requiresApproval,
  });
}

/** Record fields of a call without a pending tool question. */
export const noPendingToolQuestion = {
  inputQuestion: null,
  inputChoices: [],
  inputAllowsFreeText: true,
} as const satisfies Pick<AgentToolCallRecord, "inputQuestion" | "inputChoices" | "inputAllowsFreeText">;

/** Moves to `AwaitQuestionAnswer`, which reads the answer count to tell an unraised question from an answer. */
export function questionAnswerMovement(
  persistence: AgentSessionPersistence,
  toolCall: AgentToolCallRecord,
): StepMovement<string> {
  const options = toolStepOptions(persistence, agentSessionStepTypes.awaitQuestionAnswer, toolCall.toolCallId);
  const interruptId = questionInterruptId(toolCall);
  const answerLoad = persistence.interruptResolutions.loadMessages(interruptId);
  // A record keyed by the question's interrupt ID means another call already owns that ID.
  const recordLoads =
    interruptId === toolCall.toolCallId
      ? [persistence.agentToolCalls.load(toolCall.toolCallId)]
      : [persistence.agentToolCalls.load(toolCall.toolCallId), persistence.agentToolCalls.load(interruptId)];
  return StepMovement.of(new StepRef(agentSessionStepTypes.awaitQuestionAnswer), toolCall.toolCallId, {
    ...options,
    waitForLoadAttributeMapInstances: recordLoads,
    executeLoadAttributeMapInstances: recordLoads,
    waitForLoadChannelMapInstances: [answerLoad],
    executeLoadChannelMapInstances: [answerLoad],
  });
}

/** An `ask_user` question uses the tool call ID; a tool's `n`-th question appends `-input-<n>`. */
export function questionInterruptId(toolCall: AgentToolCallRecord): string {
  return toolCall.inputQuestion === null
    ? toolCall.toolCallId
    : `${toolCall.toolCallId}-input-${toolCall.inputAnswers.length + 1}`;
}

/** The interrupt a waiting tool call answers to: its ID and kind. */
export interface WaitingToolCallInterrupt {
  readonly interruptId: string;
  readonly kind: AgentInterruptKind;
}

/**
 * Records a call that `ToolCallCancellations` woke as cancelled, or waits again. Pass `interrupt` `null` for a
 * durable wait.
 */
export function cancelWaitingToolCall(
  transitions: AgentSessionTransitions,
  context: Context,
  toolCall: AgentToolCallRecord,
  interrupt: WaitingToolCallInterrupt | null,
  waitAgain: StepMovement<string>,
): StepDecision {
  const persistence = transitions.persistence;
  // The flags decide; a wake-up left from an earlier turn's call ID changes nothing.
  const reason = transitions.readTurnControlReason(context, toolCall.turnId);
  if (reason === null || !isCancellableByTurnControl(toolCall)) {
    return goToMany(waitAgain);
  }
  if (interrupt !== null) {
    const pendingInterrupts = transitions.readPendingInterrupts(context);
    if (pendingInterrupts.some((pending) => pending.interruptId === interrupt.interruptId)) {
      persistence.pendingInterrupts.set(
        context,
        pendingInterrupts.filter((pending) => pending.interruptId !== interrupt.interruptId),
      );
      transitions.writeInterruptCancelled(context, toolCall.turnId, interrupt.interruptId, interrupt.kind, reason);
    } else if (context.channelSize(persistence.interruptResolutions, interrupt.interruptId) > 0) {
      // The answer committed first; the next wait consumes it.
      return goToMany(waitAgain);
    }
  }
  transitions.finishToolCall(context, cancelledToolCall(toolCall, reason));
  return deadEnd();
}

export function isToolInputRequested(outcome: unknown): outcome is ToolInputRequested {
  return (outcome as { readonly kind?: unknown } | undefined)?.kind === "tool_input_requested";
}

function isToolTaskStarted(outcome: DeferredToolOutcome): outcome is ToolTaskStarted {
  return (outcome as { readonly kind?: unknown } | undefined)?.kind === "tool_task_started";
}

function requirePositiveResultCount(value: number): number {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new TypeError(`ResumeTurn input must be a positive integer: ${value}`);
  }
  return value;
}
