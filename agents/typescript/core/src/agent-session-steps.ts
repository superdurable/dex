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
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import {
  ToolExecutionError,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolCall,
  type DeferredToolExecution,
  type TurnCompleted,
  type TurnSuspended,
} from "./agent-framework-runner.js";
import { agentSessionStartInputCodec } from "./agent-session-codecs.js";
import {
  agentSessionStepTypes,
  compareToolCallIds,
  toolStepTypeForEffect,
  type AgentSessionTransitions,
} from "./agent-session-transitions.js";
import {
  addAgentUsage,
  emptyAgentUsage,
  isFinalToolCallStatus,
  type AgentInterrupt,
  type AgentSessionStartInput,
  type AgentState,
  type AgentToolCall,
  type AgentToolCallStatus,
  type AgentUsage,
} from "./agent-session-values.js";
import type { StreamAgentEventWriter } from "./agent-event-writer.js";

const minuteMs = 60_000;
const approvalDeadlinePassedMessage = "approval deadline passed";

export class StartAgentSessionStep implements Step<AgentSessionStartInput> {
  public readonly inputCodec = agentSessionStartInputCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.startAgentSession;
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
    persistence.agentUsage.set(context, emptyAgentUsage());
    persistence.pendingInterrupts.set(context, []);
    const initialMessage = input.initialMessage;
    if (initialMessage === null || initialMessage.trim() === "") {
      this.transitions.writeAgentState(context, state);
      return this.transitions.goToAwaitUserMessage();
    }
    return this.transitions.startTurn(context, state, initialMessage);
  }
}

export class AwaitUserMessageStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.awaitUserMessage;
  }

  public waitFor(): Wait {
    return Wait.until(this.transitions.persistence.queuedUserMessages.forOne());
  }

  public execute(context: Context): StepDecision {
    const [message] = context.channelResults(this.transitions.persistence.queuedUserMessages);
    const state = this.transitions.readAgentState(context);
    if (message === undefined || message.text.trim() === "") {
      return this.transitions.goToAwaitUserMessage();
    }
    return this.transitions.startTurn(context, state, message.text);
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
      transitions.writeAgentState(context, { ...state, status: "waiting_for_user_message" });
      return transitions.goToAwaitUserMessage();
    }
    const toolCalls = transitions.readTurnToolCalls(context, turnId);
    const events = transitions.createEventWriter(context, turnId);
    try {
      const turnContext: AgentTurnContext = {
        flowId: context.flowId,
        turnId,
        attempt: context.attempt,
        messages: transitions.readAllMessages(context),
        frameworkRunState: transitions.readFrameworkRunState(context),
        toolCalls,
        events,
        cancellationSignal: context.cancellationSignal,
        deadline: context.deadline,
        resolveToolPolicy: (toolName, frameworkEffect) =>
          transitions.resolveToolPolicy(toolName, frameworkEffect),
        recordHeartbeat: () => transitions.recordHeartbeat(context),
      };
      const outcome = await transitions.runWithHeartbeats(context, () =>
        transitions.runner.runTurn(turnContext),
      );
      return this.applyTurnOutcome(context, state, turnId, toolCalls, outcome, events);
    } finally {
      events.close();
    }
  }

  private applyTurnOutcome(
    context: Context,
    state: AgentState,
    turnId: string,
    toolCalls: readonly AgentToolCall[],
    outcome: AgentTurnOutcome,
    events: StreamAgentEventWriter,
  ): StepDecision {
    switch (outcome.kind) {
      case "completed":
        return this.completeTurn(context, state, turnId, toolCalls, outcome, events);
      case "suspended":
        return this.suspendTurn(context, state, turnId, toolCalls, outcome, events);
      default:
        throw new TypeError(`runner returned an unknown turn outcome for ${turnId}`);
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
    const persistence = this.transitions.persistence;
    const stateWithMessages = this.transitions.appendMessages(
      context,
      state,
      turnId,
      outcome.newMessages,
    );
    for (const toolCall of toolCalls) {
      persistence.agentToolCalls.delete(context, toolCall.toolCallId);
    }
    persistence.frameworkRunState.delete(context);
    persistence.pendingInterrupts.set(context, []);
    this.recordUsage(context, outcome.usage, events);
    this.transitions.writeAgentState(context, {
      ...stateWithMessages,
      status: "waiting_for_user_message",
      turnCount: state.turnCount + 1,
      activeTurnId: null,
      executingToolCallId: null,
      lastTurnError: null,
    });
    events.writeAgentEvent("turn_completed", { new_message_count: outcome.newMessages.length });
    return this.transitions.goToAwaitUserMessage();
  }

  private suspendTurn(
    context: Context,
    state: AgentState,
    turnId: string,
    toolCalls: readonly AgentToolCall[],
    outcome: TurnSuspended,
    events: StreamAgentEventWriter,
  ): StepDecision {
    validateDeferredToolCalls(turnId, toolCalls, outcome.deferredToolCalls);
    const persistence = this.transitions.persistence;
    persistence.frameworkRunState.set(context, outcome.frameworkRunState);
    this.recordUsage(context, outcome.usage, events);
    const createdAt = new Date();
    const deadline =
      this.transitions.config.interruptDeadlineMs === undefined
        ? null
        : new Date(createdAt.getTime() + this.transitions.config.interruptDeadlineMs).toISOString();
    const newInterrupts: AgentInterrupt[] = [];
    const deferredRecords = outcome.deferredToolCalls.map((deferredToolCall): AgentToolCall => {
      const record: AgentToolCall = {
        toolCallId: deferredToolCall.toolCallId,
        turnId,
        toolName: deferredToolCall.toolName,
        arguments: deferredToolCall.arguments,
        effect: deferredToolCall.effect,
        requiresApproval: deferredToolCall.requiresApproval,
        status: deferredToolCall.requiresApproval ? "awaiting_approval" : "approved",
        output: null,
        error: null,
        rejectionMessage: null,
      };
      this.transitions.writeToolCall(context, record);
      events.writeAgentEvent("tool_call_deferred", {
        tool_call_id: record.toolCallId,
        tool_name: record.toolName,
        effect: record.effect,
        requires_approval: record.requiresApproval,
      });
      if (record.requiresApproval) {
        newInterrupts.push({
          interruptId: record.toolCallId,
          kind: "tool_approval",
          toolCallId: record.toolCallId,
          toolName: record.toolName,
          arguments: record.arguments,
          createdAt: createdAt.toISOString(),
          deadline,
        });
      }
      return record;
    });
    persistence.pendingInterrupts.set(context, [
      ...this.transitions.readPendingInterrupts(context),
      ...newInterrupts,
    ]);
    for (const interrupt of newInterrupts) {
      events.writeAgentEvent("interrupt_raised", {
        interrupt_id: interrupt.interruptId,
        tool_call_id: interrupt.toolCallId,
        tool_name: interrupt.toolName,
        deadline: interrupt.deadline,
      });
    }
    const allToolCalls = [...toolCalls, ...deferredRecords].sort(compareToolCallIds);
    return this.transitions.routeToolCalls(context, state, allToolCalls);
  }

  private recordUsage(context: Context, usage: AgentUsage, events: StreamAgentEventWriter): void {
    const persistence = this.transitions.persistence;
    const totalUsage = (persistence.agentUsage.get(context) as AgentUsage | undefined) ?? emptyAgentUsage();
    persistence.agentUsage.set(context, addAgentUsage(totalUsage, usage));
    events.writeAgentEvent("usage_recorded", {
      model_calls: usage.modelCalls,
      input_tokens: usage.inputTokens,
      output_tokens: usage.outputTokens,
      cached_input_tokens: usage.cachedInputTokens,
      reasoning_tokens: usage.reasoningTokens,
    });
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
    return {
      waitForLoadAttributeMaps: [persistence.agentToolCalls],
      executeLoadAttributeMaps: [persistence.agentToolCalls],
      executeLockAttributes: [persistence.pendingInterrupts.lock()],
    };
  }

  public waitFor(context: Context): Wait {
    const transitions = this.transitions;
    const awaitingToolCalls = this.readAwaitingToolCalls(context);
    if (awaitingToolCalls.length === 0) {
      return Wait.skipImmediately();
    }
    const conditions: Condition[] = awaitingToolCalls.map((toolCall) =>
      transitions.persistence.interruptResolutions.forOne(toolCall.toolCallId),
    );
    const awaitingIds = new Set(awaitingToolCalls.map((toolCall) => toolCall.toolCallId));
    const deadlines = transitions
      .readPendingInterrupts(context)
      .filter((interrupt) => awaitingIds.has(interrupt.toolCallId) && interrupt.deadline !== null)
      .map((interrupt) => Date.parse(interrupt.deadline ?? ""));
    if (deadlines.length > 0) {
      const earliestDeadline = Math.min(...deadlines);
      conditions.push(Timer.byDuration(Math.max(0, earliestDeadline - Date.now())));
    }
    return Wait.anyOf(...conditions);
  }

  public execute(context: Context): StepDecision {
    const transitions = this.transitions;
    const state = transitions.readAgentState(context);
    const turnId = state.activeTurnId;
    if (turnId === null) {
      return transitions.goToAwaitUserMessage();
    }
    const toolCalls = transitions.readTurnToolCalls(context, turnId);
    let pendingInterrupts = transitions.readPendingInterrupts(context);
    const now = Date.now();
    transitions.writeEvents(context, turnId, (events) => {
      for (const [index, toolCall] of toolCalls.entries()) {
        if (toolCall.status !== "awaiting_approval") {
          continue;
        }
        const [resolution] = context.channelResults(
          transitions.persistence.interruptResolutions,
          toolCall.toolCallId,
        );
        const pendingInterrupt = pendingInterrupts.find(
          (interrupt) => interrupt.interruptId === toolCall.toolCallId,
        );
        let resolvedToolCall: AgentToolCall | undefined;
        if (resolution !== undefined) {
          resolvedToolCall = resolution.approved
            ? { ...toolCall, status: "approved" }
            : { ...toolCall, status: "rejected", rejectionMessage: resolution.rejectionMessage };
        } else if (pendingInterrupt?.deadline != null && Date.parse(pendingInterrupt.deadline) <= now) {
          resolvedToolCall = {
            ...toolCall,
            status: "rejected",
            rejectionMessage: approvalDeadlinePassedMessage,
          };
        }
        if (resolvedToolCall === undefined) {
          continue;
        }
        toolCalls[index] = resolvedToolCall;
        transitions.writeToolCall(context, resolvedToolCall);
        pendingInterrupts = pendingInterrupts.filter(
          (interrupt) => interrupt.interruptId !== toolCall.toolCallId,
        );
        events.writeAgentEvent("interrupt_resolved", {
          interrupt_id: toolCall.toolCallId,
          approved: resolvedToolCall.status === "approved",
          rejection_message: resolvedToolCall.rejectionMessage,
        });
        if (resolvedToolCall.status === "rejected") {
          events.writeToolCallFinished(toolCall.toolCallId, toolCall.toolName, "rejected");
        }
      }
    });
    transitions.persistence.pendingInterrupts.set(context, pendingInterrupts);
    return transitions.routeToolCalls(context, state, toolCalls);
  }

  private readAwaitingToolCalls(context: Context): AgentToolCall[] {
    const state = this.transitions.readAgentState(context);
    if (state.activeTurnId === null) {
      return [];
    }
    return this.transitions
      .readTurnToolCalls(context, state.activeTurnId)
      .filter((toolCall) => toolCall.status === "awaiting_approval");
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
      executeRetry: { maximumAttempts: this.maximumAttempts },
      executeFailure: ExecuteFailure.proceedTo(new StepRef(this.failureStepType)),
      executeLoadAttributeMaps: [this.transitions.persistence.agentToolCalls],
    };
  }

  public async execute(context: AsyncContext): Promise<StepDecision> {
    const transitions = this.transitions;
    const state = transitions.readAgentState(context);
    const turnId = state.activeTurnId;
    const toolCallId = state.executingToolCallId;
    if (turnId === null || toolCallId === null) {
      throw new Error(`${this.stepType} has no executing tool call in session ${context.flowId}`);
    }
    const toolCalls = transitions.readTurnToolCalls(context, turnId);
    const index = toolCalls.findIndex((toolCall) => toolCall.toolCallId === toolCallId);
    const toolCall = toolCalls[index];
    if (toolCall === undefined) {
      throw new Error(`tool call ${toolCallId} has no record in session ${context.flowId}`);
    }
    if (isFinalToolCallStatus(toolCall.status)) {
      return transitions.routeToolCalls(context, state, toolCalls);
    }
    if (toolStepTypeForEffect(toolCall.effect) !== this.stepType) {
      throw new Error(`tool call ${toolCallId} with effect ${toolCall.effect} reached ${this.stepType}`);
    }
    const events = transitions.createEventWriter(context, turnId);
    try {
      events.writeToolCallStarted(toolCall.toolCallId, toolCall.toolName);
      // Dex re-delivers an attempt whose Worker vanished before any output, so emit output first.
      await transitions.recordHeartbeat(context);
      const execution: DeferredToolExecution = {
        flowId: context.flowId,
        turnId,
        toolCallId: toolCall.toolCallId,
        toolName: toolCall.toolName,
        arguments: toolCall.arguments,
        effect: toolCall.effect,
        idempotencyKey: `${context.flowId}/${toolCall.toolCallId}`,
        attempt: context.attempt,
        events,
        cancellationSignal: context.cancellationSignal,
        deadline: context.deadline,
        recordHeartbeat: () => transitions.recordHeartbeat(context),
      };
      let finishedToolCall: AgentToolCall;
      try {
        const result = await transitions.runWithHeartbeats(context, () =>
          transitions.runner.executeDeferredToolCall(execution),
        );
        if (typeof result?.output !== "string") {
          throw new TypeError(`runner returned no output text for tool call ${toolCallId}`);
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
      toolCalls[index] = finishedToolCall;
      return transitions.routeToolCalls(context, state, toolCalls);
    } finally {
      events.close();
    }
  }
}

/** Failure route of a tool Step; one class serves `RecordToolFailure` and `RecordToolOutcomeUnknown`. */
export class ToolCallFailureRecordingStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(
    private readonly transitions: AgentSessionTransitions,
    private readonly stepType: string,
    private readonly recordedStatus: Extract<AgentToolCallStatus, "failed" | "outcome_unknown">,
  ) {}

  public getStepType(): string {
    return this.stepType;
  }

  public getStepOptions(): StepOptions {
    return { executeLoadAttributeMaps: [this.transitions.persistence.agentToolCalls] };
  }

  public execute(context: Context): StepDecision {
    const transitions = this.transitions;
    const state = transitions.readAgentState(context);
    const turnId = state.activeTurnId;
    const toolCallId = state.executingToolCallId;
    if (turnId === null || toolCallId === null) {
      throw new Error(`${this.stepType} has no executing tool call in session ${context.flowId}`);
    }
    const toolCalls = transitions.readTurnToolCalls(context, turnId);
    const index = toolCalls.findIndex((toolCall) => toolCall.toolCallId === toolCallId);
    const toolCall = toolCalls[index];
    if (toolCall === undefined) {
      throw new Error(`tool call ${toolCallId} has no record in session ${context.flowId}`);
    }
    if (isFinalToolCallStatus(toolCall.status)) {
      return transitions.routeToolCalls(context, state, toolCalls);
    }
    const recoveryDetail = context.recoveryError?.detail ?? "the tool Step failed";
    const recordedToolCall: AgentToolCall = {
      ...toolCall,
      status: this.recordedStatus,
      error: recoveryDetail,
    };
    transitions.writeToolCall(context, recordedToolCall);
    transitions.writeEvents(context, turnId, (events) =>
      events.writeToolCallFinished(toolCall.toolCallId, toolCall.toolName, this.recordedStatus),
    );
    toolCalls[index] = recordedToolCall;
    return transitions.routeToolCalls(context, state, toolCalls);
  }
}

export class RecordTurnFailureStep implements Step<void> {
  public readonly inputCodec = voidCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.recordTurnFailure;
  }

  public getStepOptions(): StepOptions {
    return { executeLoadAttributeMaps: [this.transitions.persistence.agentToolCalls] };
  }

  public execute(context: Context): StepDecision {
    const transitions = this.transitions;
    const persistence = transitions.persistence;
    const state = transitions.readAgentState(context);
    const turnError = context.recoveryError?.detail ?? "the turn failed";
    for (const toolCallId of persistence.agentToolCalls.getAllInstanceKeys(context)) {
      persistence.agentToolCalls.delete(context, toolCallId);
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
      events.writeAgentEvent("turn_failed", { error: turnError }),
    );
    return transitions.goToAwaitUserMessage();
  }
}

function validateDeferredToolCalls(
  turnId: string,
  existingToolCalls: readonly AgentToolCall[],
  deferredToolCalls: readonly DeferredToolCall[],
): void {
  if (deferredToolCalls.length === 0) {
    throw new TypeError(`runner suspended ${turnId} without deferred tool calls`);
  }
  const seenIds = new Set(existingToolCalls.map((toolCall) => toolCall.toolCallId));
  for (const deferredToolCall of deferredToolCalls) {
    const toolCallId = deferredToolCall.toolCallId;
    if (toolCallId.trim() === "" || toolCallId.includes("/")) {
      throw new TypeError(`runner deferred tool call with invalid ID ${JSON.stringify(toolCallId)}`);
    }
    if (seenIds.has(toolCallId)) {
      throw new TypeError(`runner deferred tool call ${toolCallId} twice in ${turnId}`);
    }
    seenIds.add(toolCallId);
  }
}
