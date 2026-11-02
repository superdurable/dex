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

import type { StreamAgentEventWriter } from "./agent-event-writer.js";
import {
  ToolExecutionError,
  type AgentTurnContext,
  type DeferredToolExecution,
  type ToolCallsRequested,
  type TurnCompleted,
} from "./agent-framework-runner.js";
import { agentSessionStartInputCodec } from "./agent-session-codecs.js";
import {
  agentSessionStepTypes,
  readRecoveryDetail,
  toolStepTypeForEffect,
  ToolStepHeartbeats,
  type AgentSessionTransitions,
} from "./agent-session-transitions.js";
import { messageSequenceKey, type AgentSessionPersistence } from "./agent-session-persistence.js";
import {
  approvalDeadlinePassed,
  buildTurnFailureNote,
  emptyAgentUsage,
  isFinalToolCallStatus,
  type AgentInterrupt,
  type AgentSessionStartInput,
  type AgentState,
  type AgentToolCall,
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
    const toolCallsById = new Map(toolCalls.map((toolCall) => [toolCall.toolCallId, toolCall]));
    const frameworkRunState = transitions.readFrameworkRunState(context);
    const events = transitions.createEventWriter(context, turnId);
    try {
      const turnContext: AgentTurnContext = {
        flowId: context.flowId,
        turnId,
        attempt: context.attempt,
        messages: transitions.readMessages(context, 1, state.nextMessageSequence),
        frameworkRunState,
        isResuming: frameworkRunState !== null,
        toolCalls,
        events,
        cancellationSignal: context.cancellationSignal,
        deadline: context.deadline,
        resolveToolPolicy: (toolName, frameworkEffect) =>
          transitions.resolveToolPolicy(toolName, frameworkEffect),
        recordedToolCall: (toolCallId) => toolCallsById.get(toolCallId),
        heartbeat: () => transitions.recordHeartbeat(context),
      };
      return await transitions.tracer.traceModelCall(
        context,
        this.getStepType(),
        turnContext,
        async (modelCallSpan) => {
          const outcome = await transitions.runWithHeartbeats(context, () =>
            transitions.runner.runModelStep(turnContext),
          );
          switch (outcome?.kind) {
            case "completed":
              modelCallSpan.recordOutcome(
                outcome,
                outcome.newMessages.map((_message, offset) =>
                  messageSequenceKey(state.nextMessageSequence + offset),
                ),
              );
              return this.completeTurn(context, state, turnId, toolCalls, outcome, events);
            case "tool_calls_requested":
              modelCallSpan.recordOutcome(outcome, []);
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
    toolCalls: readonly AgentToolCall[],
    outcome: TurnCompleted,
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
    events.writeEvent("turn_completed", { message_count: outcome.newMessages.length });
    return transitions.goToAwaitUserMessage();
  }

  private requestToolCalls(
    context: Context,
    state: AgentState,
    turnId: string,
    toolCalls: readonly AgentToolCall[],
    outcome: ToolCallsRequested,
    events: StreamAgentEventWriter,
  ): StepDecision {
    validateToolCallsRequested(turnId, toolCalls, outcome);
    const transitions = this.transitions;
    transitions.persistence.frameworkRunState.set(context, outcome.frameworkRunState);
    const createdAt = new Date();
    const deadline =
      transitions.config.interruptDeadlineMs === undefined
        ? null
        : new Date(createdAt.getTime() + transitions.config.interruptDeadlineMs).toISOString();
    const interrupts: AgentInterrupt[] = [];
    const traceContext = transitions.tracer.readActivationTraceparent(context);
    // goToMany takes movements of mixed input types, which the SDK types as StepMovement<any>.
    const movements: StepMovement<any>[] = [];
    for (const [index, requestedToolCall] of outcome.toolCalls.entries()) {
      const requiresApproval = requestedToolCall.policy.requiresApproval;
      const effect = requestedToolCall.policy.effect;
      transitions.writeToolCall(context, {
        toolCallId: requestedToolCall.toolCallId,
        turnId,
        toolName: requestedToolCall.toolName,
        arguments: requestedToolCall.arguments,
        effect,
        requiresApproval,
        status: requiresApproval ? "awaiting_approval" : "approved",
        output: null,
        error: null,
        rejectionMessage: null,
        deferralOrder: toolCalls.length + index,
        ...(requestedToolCall.toolSourceReference === undefined
          ? {}
          : { toolSourceReference: requestedToolCall.toolSourceReference }),
        ...(traceContext === null ? {} : { traceContext }),
      });
      events.writeEvent("tool_call_deferred", {
        tool_call_id: requestedToolCall.toolCallId,
        tool_name: requestedToolCall.toolName,
        effect,
        requires_approval: requiresApproval,
      });
      const nextStepType = requiresApproval
        ? agentSessionStepTypes.awaitToolApproval
        : toolStepTypeForEffect(effect);
      movements.push(toolStepMovement(transitions.persistence, nextStepType, requestedToolCall.toolCallId));
      if (!requiresApproval) {
        continue;
      }
      const interrupt: AgentInterrupt = {
        interruptId: requestedToolCall.toolCallId,
        kind: "tool_approval",
        toolCallId: requestedToolCall.toolCallId,
        toolName: requestedToolCall.toolName,
        arguments: requestedToolCall.arguments,
        createdAt: createdAt.toISOString(),
        deadline,
      };
      interrupts.push(interrupt);
      events.writeEvent("interrupt_raised", {
        interrupt_id: interrupt.interruptId,
        tool_call_id: interrupt.toolCallId,
        tool_name: interrupt.toolName,
        deadline: interrupt.deadline,
      });
    }
    transitions.persistence.pendingInterrupts.set(context, interrupts);
    transitions.recordUsage(context, outcome.usage, events);
    transitions.writeAgentState(context, { ...state, status: "running_tools" });
    movements.push(
      StepMovement.of(new StepRef(agentSessionStepTypes.resumeTurn), outcome.toolCalls.length),
    );
    return goToMany(...movements);
  }
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
    toolCall: AgentToolCall,
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
        this.writeInterruptResolved(context, toolCall, true, null);
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
    toolCall: AgentToolCall,
    rejectionMessage: string | null,
  ): StepDecision {
    this.transitions.writeToolCall(context, { ...toolCall, status: "rejected", rejectionMessage });
    this.writeInterruptResolved(context, toolCall, false, rejectionMessage);
    this.transitions.writeEvents(context, toolCall.turnId, (events) =>
      events.writeToolCallFinished(toolCall.toolCallId, toolCall.toolName, "rejected"),
    );
    this.transitions.publishToolCallResult(context, toolCall.toolCallId);
    return deadEnd();
  }

  private writeInterruptResolved(
    context: Context,
    toolCall: AgentToolCall,
    isApproved: boolean,
    rejectionMessage: string | null,
  ): void {
    this.transitions.writeEvents(context, toolCall.turnId, (events) =>
      events.writeEvent("interrupt_resolved", {
        interrupt_id: toolCall.toolCallId,
        approved: isApproved,
        rejection_message: rejectionMessage,
      }),
    );
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
          toolCall,
          idempotencyKey: `${context.flowId}/${toolCall.turnId}/${toolCall.toolCallId}`,
          attempt: context.attempt,
          events,
          cancellationSignal: context.cancellationSignal,
          deadline: context.deadline,
          heartbeat: () => heartbeats.record(),
          ...(heartbeats.previousCheckpoint === undefined ? {} : { heartbeatCheckpoint: heartbeats.previousCheckpoint }),
          saveHeartbeatCheckpoint: (checkpoint: string) => heartbeats.saveCheckpoint(checkpoint),
        };
        let finishedToolCall: AgentToolCall;
        try {
          const result = await transitions.runWithHeartbeats(
            context,
            () => transitions.runner.executeDeferredToolCall(execution),
            () => heartbeats.record(),
          );
          if (typeof result?.output !== "string") {
            throw new TypeError(`executeDeferredToolCall returned no output for ${toolCallId}`);
          }
          finishedToolCall = { ...toolCall, status: "succeeded", output: result.output };
        } catch (error: unknown) {
          if (!(error instanceof ToolExecutionError)) {
            throw error;
          }
          finishedToolCall = { ...toolCall, status: "failed", error: error.message };
        }
        toolCallSpan.recordFinished(finishedToolCall);
        transitions.writeToolCall(context, finishedToolCall);
        events.writeToolCallFinished(toolCall.toolCallId, toolCall.toolName, finishedToolCall.status);
        transitions.publishToolCallResult(context, toolCallId);
        return deadEnd();
      });
    } finally {
      events.close();
    }
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
    transitions.writeToolCall(context, { ...toolCall, status, error: readRecoveryDetail(context) });
    transitions.writeEvents(context, toolCall.turnId, (events) =>
      events.writeToolCallFinished(toolCall.toolCallId, toolCall.toolName, status),
    );
    transitions.publishToolCallResult(context, toolCallId);
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
    return {
      ...syncDurability,
      executeRetry: recoveryRetry,
      executeLoadAttributeMaps: [this.transitions.persistence.agentToolCalls],
    };
  }

  public execute(context: Context): StepDecision {
    const transitions = this.transitions;
    const persistence = transitions.persistence;
    let state = transitions.readAgentState(context);
    const turnError = readRecoveryDetail(context);
    const toolCalls = transitions.readTurnToolCalls(context, state.activeTurnId);
    const finalToolCalls = toolCalls.filter((toolCall) => isFinalToolCallStatus(toolCall.status));
    // The records are deleted below; the note keeps the next model from repeating writes.
    if (state.activeTurnId !== null && finalToolCalls.length > 0) {
      const note = transitions.runner.buildUserMessage(buildTurnFailureNote(finalToolCalls));
      if (note.role !== "user") {
        throw new TypeError("buildUserMessage must return a message with role user");
      }
      state = transitions.appendMessages(context, state, state.activeTurnId, [note]);
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
    transitions.writeEvents(context, state.activeTurnId, (events) =>
      events.writeEvent("turn_failed", { error: turnError }),
    );
    return transitions.goToAwaitUserMessage();
  }
}

function validateToolCallsRequested(
  turnId: string,
  existingToolCalls: readonly AgentToolCall[],
  outcome: ToolCallsRequested,
): void {
  if (typeof outcome.frameworkRunState !== "string" || outcome.frameworkRunState === "") {
    throw new TypeError(`ToolCallsRequested for ${turnId} needs a non-empty frameworkRunState`);
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
function toolStepOptions(
  persistence: AgentSessionPersistence,
  stepType: string,
  toolCallId?: string,
): StepOptions {
  const loads =
    toolCallId === undefined
      ? { executeLoadAttributeMaps: [persistence.agentToolCalls] }
      : { executeLoadAttributeMapInstances: [persistence.agentToolCalls.load(toolCallId)] };
  switch (stepType) {
    case agentSessionStepTypes.awaitToolApproval:
      // The deadline path rewrites PendingInterrupts, which resolve_interrupt and sibling Steps also write.
      return { ...syncDurability, ...loads, executeLockAttributes: [persistence.pendingInterrupts.lock()] };
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

function toolStepMovement(
  persistence: AgentSessionPersistence,
  stepType: string,
  toolCallId: string,
): StepMovement<string> {
  return StepMovement.of(new StepRef(stepType), toolCallId, toolStepOptions(persistence, stepType, toolCallId));
}

function requirePositiveResultCount(value: number): number {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new TypeError(`ResumeTurn input must be a positive integer: ${value}`);
  }
  return value;
}
