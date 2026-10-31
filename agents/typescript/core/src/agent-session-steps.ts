// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  ExecuteFailure,
  StepRef,
  Timer,
  Wait,
  voidCodec,
  type AsyncContext,
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
  type TurnCompleted,
  type TurnSuspended,
} from "./agent-framework-runner.js";
import { agentSessionStartInputCodec } from "./agent-session-codecs.js";
import {
  agentSessionStepTypes,
  readRecoveryDetail,
  toolStepTypeForEffect,
  type AgentSessionTransitions,
} from "./agent-session-transitions.js";
import {
  approvalDeadlinePassed,
  buildTurnFailureNote,
  emptyAgentUsage,
  isFinalToolCallStatus,
  type AgentInterrupt,
  type AgentInterruptResolution,
  type AgentSessionStartInput,
  type AgentState,
  type AgentToolCall,
} from "./agent-session-values.js";

const minuteMs = 60_000;
// Recovery Steps must eventually run; cap their backoff so a returning Worker picks them up quickly.
const recoveryRetry: RetryPolicy = { maximumIntervalMs: 5_000 };
// An ASYNC FlowConfig could replay a committed result after a Server failover, re-running a write.
const syncDurability = { waitForDurability: "sync", executeDurability: "sync" } as const;

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
      executingToolCallId: null,
      lastTurnError: null,
    };
    this.transitions.writeAgentState(context, state);
    persistence.agentUsage.set(context, emptyAgentUsage());
    persistence.pendingInterrupts.set(context, []);
    const initialMessage = input.initialMessage;
    if (initialMessage === null || initialMessage.trim() === "") {
      return this.transitions.goToAwaitUserMessage();
    }
    return this.transitions.beginTurn(context, state, initialMessage);
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
    return this.transitions.beginTurn(context, this.transitions.readAgentState(context), message.text);
  }
}

export class RunAgentTurnStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.runAgentTurn;
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
      throw new Error(`turn ${turnId} resumed before tool call ${openToolCall.toolCallId} finished`);
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
      const outcome = await transitions.runWithHeartbeats(context, () =>
        transitions.runner.runTurn(turnContext),
      );
      switch (outcome?.kind) {
        case "completed":
          return this.completeTurn(context, state, turnId, toolCalls, outcome, events);
        case "suspended":
          return this.suspendTurn(context, state, turnId, toolCalls, outcome, events);
        default:
          throw new TypeError("runTurn must return a TurnCompleted or TurnSuspended outcome");
      }
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
      executingToolCallId: null,
      lastTurnError: null,
    });
    events.writeEvent("turn_completed", { message_count: outcome.newMessages.length });
    return transitions.goToAwaitUserMessage();
  }

  private suspendTurn(
    context: Context,
    state: AgentState,
    turnId: string,
    toolCalls: readonly AgentToolCall[],
    outcome: TurnSuspended,
    events: StreamAgentEventWriter,
  ): StepDecision {
    validateTurnSuspension(turnId, toolCalls, outcome);
    const transitions = this.transitions;
    transitions.persistence.frameworkRunState.set(context, outcome.frameworkRunState);
    const createdAt = new Date();
    const deadline =
      transitions.config.interruptDeadlineMs === undefined
        ? null
        : new Date(createdAt.getTime() + transitions.config.interruptDeadlineMs).toISOString();
    const interrupts: AgentInterrupt[] = [];
    for (const [index, deferredToolCall] of outcome.deferredToolCalls.entries()) {
      const requiresApproval = deferredToolCall.policy.requiresApproval;
      transitions.writeToolCall(context, {
        toolCallId: deferredToolCall.toolCallId,
        turnId,
        toolName: deferredToolCall.toolName,
        arguments: deferredToolCall.arguments,
        effect: deferredToolCall.policy.effect,
        requiresApproval,
        status: requiresApproval ? "awaiting_approval" : "approved",
        output: null,
        error: null,
        rejectionMessage: null,
        deferralOrder: toolCalls.length + index,
      });
      events.writeEvent("tool_call_deferred", {
        tool_call_id: deferredToolCall.toolCallId,
        tool_name: deferredToolCall.toolName,
        effect: deferredToolCall.policy.effect,
        requires_approval: requiresApproval,
      });
      if (!requiresApproval) {
        continue;
      }
      const interrupt: AgentInterrupt = {
        interruptId: deferredToolCall.toolCallId,
        kind: "tool_approval",
        toolCallId: deferredToolCall.toolCallId,
        toolName: deferredToolCall.toolName,
        arguments: deferredToolCall.arguments,
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
    return transitions.routeToolCalls(context, state);
  }
}

export class AwaitInterruptResolutionStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.awaitInterruptResolution;
  }

  public getStepOptions(): StepOptions {
    const persistence = this.transitions.persistence;
    // The deadline path rewrites PendingInterrupts, which resolve_interrupt also writes.
    return {
      ...syncDurability,
      waitForLoadAttributeMaps: [persistence.agentToolCalls],
      executeLoadAttributeMaps: [persistence.agentToolCalls],
      executeLockAttributes: [persistence.pendingInterrupts.lock()],
    };
  }

  public waitFor(context: Context): Wait {
    const transitions = this.transitions;
    const state = transitions.readAgentState(context);
    const awaitingToolCalls = transitions
      .readTurnToolCalls(context, state.activeTurnId)
      .filter((toolCall) => toolCall.status === "awaiting_approval");
    const conditions: Condition[] = awaitingToolCalls.map((toolCall) =>
      transitions.persistence.interruptResolutions.forOne(toolCall.toolCallId),
    );
    // Resolved interrupts left PendingInterrupts, so their deadlines no longer apply.
    const awaitingIds = new Set(awaitingToolCalls.map((toolCall) => toolCall.toolCallId));
    const deadlines = transitions
      .readPendingInterrupts(context)
      .filter((interrupt) => interrupt.deadline !== null && awaitingIds.has(interrupt.interruptId))
      .map((interrupt) => Date.parse(interrupt.deadline ?? ""));
    if (deadlines.length > 0) {
      conditions.push(Timer.byDuration(Math.max(0, Math.min(...deadlines) - Date.now())));
    }
    return conditions.length === 0 ? Wait.skipImmediately() : Wait.anyOf(...conditions);
  }

  public execute(context: Context): StepDecision {
    const transitions = this.transitions;
    const state = transitions.readAgentState(context);
    const awaitingToolCalls = transitions
      .readTurnToolCalls(context, state.activeTurnId)
      .filter((toolCall) => toolCall.status === "awaiting_approval");
    transitions.writeEvents(context, state.activeTurnId, (events) => {
      if (context.hasTimerFired()) {
        this.rejectExpiredInterrupts(context, awaitingToolCalls, events);
        return;
      }
      for (const toolCall of awaitingToolCalls) {
        const [resolution] = context.channelResults(
          transitions.persistence.interruptResolutions,
          toolCall.toolCallId,
        );
        if (resolution !== undefined) {
          this.resolveToolCall(context, toolCall, resolution, events);
        }
      }
    });
    return transitions.routeToolCalls(context, state);
  }

  private rejectExpiredInterrupts(
    context: Context,
    awaitingToolCalls: readonly AgentToolCall[],
    events: StreamAgentEventWriter,
  ): void {
    const now = Date.now();
    const pendingInterrupts = this.transitions.readPendingInterrupts(context);
    const expiredIds = new Set(
      pendingInterrupts
        .filter((interrupt) => interrupt.deadline !== null && Date.parse(interrupt.deadline) <= now)
        .map((interrupt) => interrupt.interruptId),
    );
    if (expiredIds.size === 0) {
      return;
    }
    this.transitions.persistence.pendingInterrupts.set(
      context,
      pendingInterrupts.filter((interrupt) => !expiredIds.has(interrupt.interruptId)),
    );
    for (const toolCall of awaitingToolCalls) {
      if (expiredIds.has(toolCall.toolCallId)) {
        this.resolveToolCall(
          context,
          toolCall,
          { approved: false, rejectionMessage: approvalDeadlinePassed },
          events,
        );
      }
    }
  }

  private resolveToolCall(
    context: Context,
    toolCall: AgentToolCall,
    resolution: AgentInterruptResolution,
    events: StreamAgentEventWriter,
  ): void {
    this.transitions.writeToolCall(
      context,
      resolution.approved
        ? { ...toolCall, status: "approved" }
        : { ...toolCall, status: "rejected", rejectionMessage: resolution.rejectionMessage },
    );
    events.writeEvent("interrupt_resolved", {
      interrupt_id: toolCall.toolCallId,
      approved: resolution.approved,
      rejection_message: resolution.rejectionMessage,
    });
  }
}

/** Runs one approved deferred call; one class serves both tool Step types. */
export class ToolCallExecutionStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(
    private readonly transitions: AgentSessionTransitions,
    private readonly stepType: string,
    private readonly maximumAttempts: number,
    private readonly failureStepType: string,
  ) {}

  public getStepType(): string {
    return this.stepType;
  }

  public getStepOptions(): StepOptions {
    return {
      ...syncDurability,
      executeRetry: { maximumAttempts: this.maximumAttempts },
      executeFailure: ExecuteFailure.proceedTo(new StepRef(this.failureStepType)),
      executeLoadAttributeMaps: [this.transitions.persistence.agentToolCalls],
    };
  }

  public async execute(context: AsyncContext): Promise<StepDecision> {
    const transitions = this.transitions;
    const state = transitions.readAgentState(context);
    const toolCall = transitions.readExecutingToolCall(context, state);
    if (isFinalToolCallStatus(toolCall.status)) {
      return transitions.routeToolCalls(context, state);
    }
    if (toolCall.status !== "approved" || toolStepTypeForEffect(toolCall.effect) !== this.stepType) {
      throw new Error(`tool call ${toolCall.toolCallId} cannot run in ${this.stepType}`);
    }
    const events = transitions.createEventWriter(context, state.activeTurnId);
    try {
      events.writeToolCallStarted(toolCall.toolCallId, toolCall.toolName);
      // Dex re-delivers an attempt whose Worker vanished before any output, so emit output first.
      await transitions.recordHeartbeat(context);
      const execution: DeferredToolExecution = {
        flowId: context.flowId,
        toolCall,
        idempotencyKey: `${context.flowId}/${toolCall.turnId}/${toolCall.toolCallId}`,
        attempt: context.attempt,
        events,
        cancellationSignal: context.cancellationSignal,
        deadline: context.deadline,
        heartbeat: () => transitions.recordHeartbeat(context),
      };
      let finishedToolCall: AgentToolCall;
      try {
        const result = await transitions.runWithHeartbeats(context, () =>
          transitions.runner.executeDeferredToolCall(execution),
        );
        if (typeof result?.output !== "string") {
          throw new TypeError(`executeDeferredToolCall returned no output for ${toolCall.toolCallId}`);
        }
        finishedToolCall = { ...toolCall, status: "succeeded", output: result.output };
      } catch (error: unknown) {
        if (!(error instanceof ToolExecutionError)) {
          throw error;
        }
        finishedToolCall = { ...toolCall, status: "failed", error: error.message };
      }
      transitions.writeToolCall(context, finishedToolCall);
      events.writeToolCallFinished(toolCall.toolCallId, toolCall.toolName, finishedToolCall.status);
      return transitions.routeToolCalls(context, state);
    } finally {
      events.close();
    }
  }
}

/**
 * Failure route of a tool Step; one class serves `RecordToolFailure` and `RecordToolOutcomeUnknown`.
 * Both record `outcome_unknown`: a crash, timeout, or unexpected error leaves the effect unknown.
 */
export class ToolCallOutcomeUnknownRecordingStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(
    private readonly transitions: AgentSessionTransitions,
    private readonly stepType: string,
  ) {}

  public getStepType(): string {
    return this.stepType;
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
    const state = transitions.readAgentState(context);
    const toolCall = transitions.readExecutingToolCall(context, state);
    if (isFinalToolCallStatus(toolCall.status)) {
      return transitions.routeToolCalls(context, state);
    }
    transitions.writeToolCall(context, {
      ...toolCall,
      status: "outcome_unknown",
      error: readRecoveryDetail(context),
    });
    transitions.writeEvents(context, state.activeTurnId, (events) =>
      events.writeToolCallFinished(toolCall.toolCallId, toolCall.toolName, "outcome_unknown"),
    );
    return transitions.routeToolCalls(context, state);
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
      executingToolCallId: null,
      lastTurnError: turnError,
    });
    transitions.writeEvents(context, state.activeTurnId, (events) =>
      events.writeEvent("turn_failed", { error: turnError }),
    );
    return transitions.goToAwaitUserMessage();
  }
}

function validateTurnSuspension(
  turnId: string,
  existingToolCalls: readonly AgentToolCall[],
  outcome: TurnSuspended,
): void {
  if (typeof outcome.frameworkRunState !== "string" || outcome.frameworkRunState === "") {
    throw new TypeError(`TurnSuspended for ${turnId} needs a non-empty frameworkRunState`);
  }
  if (outcome.deferredToolCalls.length === 0) {
    throw new TypeError(`TurnSuspended for ${turnId} needs at least one deferred tool call`);
  }
  const seenIds = new Set(existingToolCalls.map((toolCall) => toolCall.toolCallId));
  for (const deferredToolCall of outcome.deferredToolCalls) {
    const toolCallId = deferredToolCall.toolCallId;
    if (toolCallId.trim() === "" || toolCallId.includes("/")) {
      throw new TypeError(`tool call ID ${JSON.stringify(toolCallId)} is empty or contains '/'`);
    }
    if (seenIds.has(toolCallId)) {
      throw new TypeError(`tool call ${toolCallId} was already deferred in ${turnId}`);
    }
    seenIds.add(toolCallId);
  }
}
