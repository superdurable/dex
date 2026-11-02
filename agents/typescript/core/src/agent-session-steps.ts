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
  Timer,
  Wait,
  deadEnd,
  goTo,
  goToMany,
  stringCodec,
  voidCodec,
  type AsyncContext,
  type Codec,
  type Condition,
  type Context,
  type RetryPolicy,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import {
  isBlankText,
  validateAskUserArguments,
  validateDurableWaitArguments,
  type BuiltInTool,
} from "./agent-built-in-tools.js";
import type { StreamAgentEventWriter } from "./agent-event-writer.js";
import {
  ToolExecutionError,
  type DeferredToolCall,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type ToolCallsRequested,
  type ToolInputRequested,
  type TurnCompleted,
} from "./agent-framework-runner.js";
import { agentSessionStartInputCodec } from "./agent-session-codecs.js";
import {
  agentSessionStepTypes,
  readRecoveryDetail,
  toRunnerToolCall,
  toolStepTypeForEffect,
  ToolStepHeartbeats,
  type AgentSessionTransitions,
} from "./agent-session-transitions.js";
import { messageSequenceKey, type AgentSessionPersistence } from "./agent-session-persistence.js";
import type { ToolCallSpan } from "./agent-session-tracer.js";
import {
  approvalDeadlinePassed,
  buildTurnFailureNote,
  emptyAgentUsage,
  isFinalToolCallStatus,
  type AgentInterrupt,
  type AgentSessionStartInput,
  type AgentState,
  type AgentToolCallRecord,
  type FrameworkMessage,
} from "./agent-session-values.js";

const minuteMs = 60_000;
// Recovery Steps must eventually run; cap their backoff so a returning Worker picks them up quickly.
const recoveryRetry: RetryPolicy = { maximumIntervalMs: 5_000 };
// An ASYNC FlowConfig could replay a committed result after a Server failover, re-running a write.
const syncDurability = { waitForDurability: "sync", executeDurability: "sync" } as const;
// An int64 Value, as the Python core's Step[int] encodes it.
const expectedResultCountCodec: Codec<number> = {
  typeName: "ExpectedResultCount",
  wireKind: "int64",
  encode: (value) => int64Codec.encode(BigInt(requirePositiveResultCount(value))),
  decode: (value) => requirePositiveResultCount(Number(int64Codec.decode(value))),
};

export class StartAgentSessionStep implements Step<AgentSessionStartInput> {
  public readonly inputCodec = agentSessionStartInputCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.startAgentSession;
  }

  public getStepOptions(): StepOptions {
    return syncDurability;
  }

  public execute(context: Context, input: AgentSessionStartInput): StepDecision {
    const persistence = this.transitions.persistence;
    const state: AgentState = {
      status: "waiting_for_user_message",
      turnCount: 0,
      activeTurnId: null,
      nextMessageSequence: 1,
      lastTurnError: null,
    };
    this.transitions.writeAgentState(context, state);
    persistence.agentUsage.set(context, emptyAgentUsage());
    persistence.pendingInterrupts.set(context, []);
    const initialMessage = input.initialMessage;
    if (initialMessage === null || initialMessage.trim() === "") {
      return this.transitions.goToAwaitUserMessage();
    }
    return this.transitions.beginTurn(context, state, initialMessage, this.getStepType());
  }
}

export class AwaitUserMessageStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.awaitUserMessage;
  }

  public getStepOptions(): StepOptions {
    return syncDurability;
  }

  public waitFor(): Wait {
    return Wait.until(this.transitions.persistence.queuedUserMessages.forOne());
  }

  public execute(context: Context): StepDecision {
    const [message] = context.channelResults(this.transitions.persistence.queuedUserMessages);
    if (message === undefined || message.text.trim() === "") {
      return this.transitions.goToAwaitUserMessage();
    }
    return this.transitions.beginTurn(
      context,
      this.transitions.readAgentState(context),
      message.text,
      this.getStepType(),
    );
  }
}

/** Makes the turn's next model call and fans the requested tool calls out to parallel Steps. */
export class RunModelStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.runModel;
  }

  public getStepOptions(): StepOptions {
    const persistence = this.transitions.persistence;
    return {
      ...syncDurability,
      executeMethodTimeoutMs: 15 * minuteMs,
      heartbeatTimeoutMs: 2 * minuteMs,
      executeRetry: { maximumAttempts: 3, totalDurationMs: 30 * minuteMs },
      executeFailure: ExecuteFailure.proceedTo(new StepRef(agentSessionStepTypes.recordTurnFailure)),
      executeLoadAttributeMaps: [persistence.agentMessages, persistence.agentToolCalls],
    };
  }

  public async execute(context: AsyncContext): Promise<StepDecision> {
    const transitions = this.transitions;
    const state = transitions.readAgentState(context);
    const turnId = state.activeTurnId;
    if (turnId === null) {
      throw new Error(`the agent session ${context.flowId} has no active turn`);
    }
    const toolCalls = transitions.readTurnToolCalls(context, turnId);
    const openToolCall = toolCalls.find((toolCall) => !isFinalToolCallStatus(toolCall.status));
    if (openToolCall !== undefined) {
      throw new Error(`RunModel for ${turnId} started before tool call ${openToolCall.toolCallId} finished`);
    }
    const events = transitions.createEventWriter(context, turnId);
    try {
      const turnContext = transitions.createTurnContext(context, state, turnId, toolCalls, events, () =>
        transitions.recordHeartbeat(context),
      );
      return await transitions.tracer.traceModelCall(
        context,
        this.getStepType(),
        turnContext,
        async (modelCallSpan) => {
          const outcome = await transitions.runWithHeartbeats(context, () =>
            transitions.runner.runModelStep(turnContext),
          );
          switch (outcome?.kind) {
            case "completed": {
              modelCallSpan.recordOutcome(outcome, appendedMessageKeys(state, outcome.newMessages));
              const storedTurnMessageCount = turnContext.messages.filter((message) => message.turnId === turnId).length;
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
            default:
              throw new TypeError("runModelStep must return a TurnCompleted or ToolCallsRequested outcome");
          }
        },
      );
    } finally {
      events.close();
    }
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
      lastTurnError: null,
    });
    // The turn's user message is stored but not counted; incremental runners stored more before now.
    const messageCount = Math.max(0, storedTurnMessageCount - 1) + outcome.newMessages.length;
    events.writeEvent("turn_completed", { message_count: messageCount });
    return transitions.goToAwaitUserMessage();
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
      interruptDeadline:
        transitions.config.interruptDeadlineMs === undefined
          ? null
          : new Date(createdAt.getTime() + transitions.config.interruptDeadlineMs).toISOString(),
      interrupts: [],
      traceContext: transitions.tracer.readActivationTraceparent(context),
      movements: [],
      events,
    };
    for (const [index, requestedToolCall] of outcome.toolCalls.entries()) {
      const deferralOrder = toolCalls.length + index;
      if (transitions.isEnabledBuiltInTool(requestedToolCall.toolName)) {
        this.deferBuiltInToolCall(batch, requestedToolCall, requestedToolCall.toolName, deferralOrder);
      } else {
        this.deferApplicationToolCall(batch, requestedToolCall, deferralOrder);
      }
    }
    transitions.persistence.pendingInterrupts.set(context, batch.interrupts);
    transitions.recordUsage(context, outcome.usage, events);
    transitions.writeAgentState(context, { ...stateWithMessages, status: "running_tools" });
    batch.movements.push(
      StepMovement.of(new StepRef(agentSessionStepTypes.resumeTurn), outcome.toolCalls.length),
    );
    return goToMany(...batch.movements);
  }

  private deferApplicationToolCall(
    batch: ToolCallBatchCommit,
    requestedToolCall: DeferredToolCall,
    deferralOrder: number,
  ): void {
    const transitions = this.transitions;
    const requiresApproval = requestedToolCall.policy.requiresApproval;
    const effect = requestedToolCall.policy.effect;
    const record = createToolCallRecord(batch, requestedToolCall, effect, requiresApproval, deferralOrder);
    transitions.writeToolCall(batch.context, {
      ...record,
      status: requiresApproval ? "awaiting_approval" : "approved",
    });
    writeToolCallDeferred(batch.events, record);
    const nextStepType = requiresApproval
      ? agentSessionStepTypes.awaitToolApproval
      : toolStepTypeForEffect(effect);
    batch.movements.push(toolStepMovement(transitions.persistence, nextStepType, requestedToolCall.toolCallId));
    if (!requiresApproval) {
      return;
    }
    const interrupt: AgentInterrupt = {
      interruptId: requestedToolCall.toolCallId,
      kind: "tool_approval",
      toolCallId: requestedToolCall.toolCallId,
      toolName: requestedToolCall.toolName,
      arguments: requestedToolCall.arguments,
      createdAt: batch.createdAt,
      deadline: batch.interruptDeadline,
      question: null,
      choices: [],
      allowsFreeText: true,
    };
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
    }
  }

  private failBuiltInToolCall(batch: ToolCallBatchCommit, record: AgentToolCallRecord, error: string): void {
    this.transitions.finishToolCall(batch.context, { ...record, status: "failed", error }, batch.events);
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
    const conditions: Condition[] = [transitions.persistence.interruptResolutions.forOne(toolCallId)];
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
          ...(activation.traceparent === null ? {} : { traceContext: activation.traceparent }),
        });
        this.transitions.writeInterruptResolved(context, toolCall.turnId, {
          interruptId: toolCall.toolCallId,
          kind: "tool_approval",
          isApproved: true,
          isAnswered: false,
          rejectionMessage: null,
        });
        return goToMany(
          toolStepMovement(
            this.transitions.persistence,
            toolStepTypeForEffect(toolCall.effect),
            toolCall.toolCallId,
          ),
        );
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
    this.transitions.finishToolCall(context, { ...toolCall, status: "rejected", rejectionMessage });
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
    const events = transitions.createEventWriter(context, toolCall.turnId);
    try {
      return await transitions.tracer.traceToolCall(context, this.stepType, toolCall, async (toolCallSpan) => {
        events.writeToolCallStarted(toolCall.toolCallId, toolCall.toolName);
        const heartbeats = new ToolStepHeartbeats(context);
        // Dex re-delivers an attempt whose Worker vanished before any output, so emit output first.
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
          heartbeat: () => heartbeats.record(),
          ...(heartbeats.previousCheckpoint === undefined ? {} : { heartbeatCheckpoint: heartbeats.previousCheckpoint }),
          saveHeartbeatCheckpoint: (checkpoint: string) => heartbeats.saveCheckpoint(checkpoint),
        };
        let outcome: DeferredToolOutcome;
        try {
          outcome = await transitions.runWithHeartbeats(
            context,
            () => transitions.runner.executeDeferredToolCall(execution),
            () => heartbeats.record(),
          );
        } catch (error: unknown) {
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
        if (typeof outcome?.output !== "string") {
          throw new TypeError(`executeDeferredToolCall returned no output for ${toolCallId}`);
        }
        const succeeded: AgentToolCallRecord = { ...toolCall, status: "succeeded", output: outcome.output };
        toolCallSpan.recordFinished(succeeded);
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
    const choices = request.choices ?? [];
    if (
      typeof request.question !== "string" ||
      isBlankText(request.question) ||
      !choices.every((choice) => typeof choice === "string" && !isBlankText(choice))
    ) {
      const failed: AgentToolCallRecord = {
        ...toolCall,
        status: "failed",
        error: "the tool asked for input with a blank question or choice",
      };
      toolCallSpan.recordFinished(failed);
      transitions.finishToolCall(context, failed, events);
      return deadEnd();
    }
    // This Step runs unlocked, so it leaves PendingInterrupts to AwaitQuestionAnswer.
    const waiting: AgentToolCallRecord = {
      ...toolCall,
      status: "awaiting_answer",
      inputContinuation: request.continuation,
      inputQuestion: request.question,
      inputChoices: [...choices],
      inputAllowsFreeText: (request.allowsFreeText ?? true) || choices.length === 0,
    };
    toolCallSpan.recordFinished(waiting);
    transitions.writeToolCall(context, waiting);
    return goToMany(questionAnswerMovement(transitions.persistence, waiting));
  }
}

/**
 * Failure route of a tool Step; it receives the failed Step's input, the tool call ID.
 * `RecordToolFailure` records `failed` for a `read` call and `outcome_unknown` for a write;
 * `RecordToolOutcomeUnknown` always records `outcome_unknown`.
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
    const toolCall = transitions.readToolCall(context, toolCallId);
    if (isFinalToolCallStatus(toolCall.status)) {
      return deadEnd();
    }
    const status =
      this.isReadFailureRecorded && toolCall.effect === "read" ? "failed" : "outcome_unknown";
    transitions.finishToolCall(context, { ...toolCall, status, error: readRecoveryDetail(context) });
    return deadEnd();
  }
}

/** Waits until every tool call of the batch has a final record, then makes the next model call. */
export class ResumeTurnStep implements Step<number> {
  public readonly inputCodec = expectedResultCountCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.resumeTurn;
  }

  public getStepOptions(): StepOptions {
    return { ...syncDurability, executeLoadAttributeMaps: [this.transitions.persistence.agentToolCalls] };
  }

  public waitFor(_context: Context, expectedResultCount: number): Wait {
    return Wait.until(this.transitions.persistence.toolCallResults.forN(expectedResultCount));
  }

  public execute(context: Context): StepDecision {
    const transitions = this.transitions;
    const state = transitions.readAgentState(context);
    const unfinishedCount = transitions
      .readTurnToolCalls(context, state.activeTurnId)
      .filter((toolCall) => !isFinalToolCallStatus(toolCall.status)).length;
    if (unfinishedCount > 0) {
      // A consumed result from an earlier turn must not resume this one early.
      return goTo(new StepRef(agentSessionStepTypes.resumeTurn), unfinishedCount);
    }
    transitions.writeAgentState(context, { ...state, status: "running_model" });
    return goTo(new StepRef(agentSessionStepTypes.runModel), undefined);
  }
}

export class RecordTurnFailureStep implements Step<void> {
  public readonly inputCodec = voidCodec;

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
      executeLoadAttributeMaps: hasUnfinishedTurnMessages
        ? [persistence.agentMessages, persistence.agentToolCalls]
        : [persistence.agentToolCalls],
    };
  }

  public execute(context: Context): StepDecision {
    const transitions = this.transitions;
    const state = transitions.readAgentState(context);
    const toolCalls = transitions.readTurnToolCalls(context, state.activeTurnId);
    const finalToolCalls = toolCalls.filter((toolCall) => isFinalToolCallStatus(toolCall.status));
    const unfinishedTurnMessages = this.buildUnfinishedTurnMessages(context, state, finalToolCalls);
    return recordFailedTurn(transitions, context, state, toolCalls, unfinishedTurnMessages, readRecoveryDetail(context));
  }

  /** Returns the runner's unfinished-turn messages, or none when it has no such method or it throws. */
  private buildUnfinishedTurnMessages(
    context: Context,
    state: AgentState,
    finalToolCalls: readonly AgentToolCallRecord[],
  ): readonly FrameworkMessage[] {
    const transitions = this.transitions;
    const turnId = state.activeTurnId;
    if (transitions.runner.buildUnfinishedTurnMessages === undefined || turnId === null) {
      return [];
    }
    const events = transitions.createEventWriter(context, turnId);
    try {
      const turnContext = transitions.createTurnContext(context, state, turnId, finalToolCalls, events, () =>
        Promise.resolve(),
      );
      const messages: unknown = transitions.runner.buildUnfinishedTurnMessages(turnContext);
      if (!Array.isArray(messages) || !messages.every(isFrameworkMessage)) {
        throw new TypeError("buildUnfinishedTurnMessages must return an array of framework messages");
      }
      return messages;
    } catch (error: unknown) {
      // A runner defect must not block the failure route; the note below still tells the model.
      console.warn(`dex-agents: RecordTurnFailure stores no unfinished-turn messages for ${turnId}:`, error);
      return [];
    } finally {
      events.close();
    }
  }
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
  });
  transitions.writeEvents(context, turnId, (events) => events.writeEvent("turn_failed", { error: turnError }));
  return transitions.goToAwaitUserMessage();
}

/** The `AgentMessages` keys that appending these messages after the state's last one will use. */
function appendedMessageKeys(state: AgentState, messages: readonly FrameworkMessage[]): string[] {
  return messages.map((_message, offset) => messageSequenceKey(state.nextMessageSequence + offset));
}

/** `last_turn_error` of a turn whose runner stored messages incrementally without the builder. */
export const missingUnfinishedTurnMessageBuilder =
  "a runner that sets ToolCallsRequested.newMessages must implement buildUnfinishedTurnMessages";

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
    case agentSessionStepTypes.recordToolFailure:
    case agentSessionStepTypes.recordToolOutcomeUnknown:
      return { ...syncDurability, ...loads, executeRetry: recoveryRetry };
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
  };
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

function isToolInputRequested(outcome: DeferredToolOutcome): outcome is ToolInputRequested {
  return (outcome as { readonly kind?: unknown } | undefined)?.kind === "tool_input_requested";
}

function requirePositiveResultCount(value: number): number {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new TypeError(`ResumeTurn input must be a positive integer: ${value}`);
  }
  return value;
}
