// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  StepRef,
  goTo,
  stringCodec,
  type AsyncContext,
  type Context,
  type StepDecision,
} from "@superdurable/dex";

import { StreamAgentEventWriter } from "./agent-event-writer.js";
import {
  resolveToolPolicy,
  type AgentFrameworkRunner,
  type ResolvedToolPolicy,
  type ToolPolicy,
} from "./agent-framework-runner.js";
import { messageSequenceKey, type AgentSessionPersistence } from "./agent-session-persistence.js";
import {
  addAgentUsage,
  emptyAgentUsage,
  type AgentInterrupt,
  type AgentMessage,
  type AgentState,
  type AgentToolCall,
  type AgentUsage,
  type FrameworkMessage,
  type ToolEffect,
} from "./agent-session-values.js";

export const agentSessionStepTypes = {
  startAgentSession: "StartAgentSession",
  awaitUserMessage: "AwaitUserMessage",
  runAgentTurn: "RunAgentTurn",
  awaitInterruptResolution: "AwaitInterruptResolution",
  executeToolCall: "ExecuteToolCall",
  executeSingleAttemptToolCall: "ExecuteSingleAttemptToolCall",
  recordToolFailure: "RecordToolFailure",
  recordToolOutcomeUnknown: "RecordToolOutcomeUnknown",
  recordTurnFailure: "RecordTurnFailure",
} as const;

// Keeps liveness well inside the shortest heartbeat timeout, one minute.
const heartbeatIntervalMs = 20_000;

export interface AgentSessionConfig {
  readonly flowType: string;
  readonly toolPolicies: Readonly<Record<string, ToolPolicy>>;
  readonly defaultToolEffect: ToolEffect;
  readonly eventStreamCapacityBytes: number;
  readonly interruptDeadlineMs: number | undefined;
}

/** Shared state reads, writes, and routing used by every session Step. */
export class AgentSessionTransitions {
  public constructor(
    public readonly persistence: AgentSessionPersistence,
    public readonly runner: AgentFrameworkRunner,
    public readonly config: AgentSessionConfig,
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

  public readPendingInterrupts(context: Context): readonly AgentInterrupt[] {
    return (this.persistence.pendingInterrupts.get(context) as readonly AgentInterrupt[] | undefined) ?? [];
  }

  public readAgentUsage(context: Context): AgentUsage {
    return (this.persistence.agentUsage.get(context) as AgentUsage | undefined) ?? emptyAgentUsage();
  }

  public readFrameworkRunState(context: Context): string | null {
    const runState = this.persistence.frameworkRunState.get(context) as string | undefined;
    return runState === undefined || runState === "" ? null : runState;
  }

  /** Returns messages with sequences in [start, end); requires the AgentMessages load. */
  public readMessages(context: Context, start: number, end: number): AgentMessage[] {
    const messages: AgentMessage[] = [];
    for (let sequence = Math.max(1, start); sequence < end; sequence += 1) {
      messages.push(this.persistence.agentMessages.get(context, messageSequenceKey(sequence)));
    }
    return messages;
  }

  /** Returns the turn's tool call records in deferral order; requires the AgentToolCalls load. */
  public readTurnToolCalls(context: Context, turnId: string | null): AgentToolCall[] {
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

  public readExecutingToolCall(context: Context, state: AgentState): AgentToolCall {
    const toolCallId = state.executingToolCallId;
    if (toolCallId === null) {
      throw new Error(`the agent session ${context.flowId} has no executing tool call`);
    }
    const toolCall = this.persistence.agentToolCalls.get(context, toolCallId) as AgentToolCall | undefined;
    if (toolCall === undefined) {
      throw new Error(`tool call ${toolCallId} has no record in session ${context.flowId}`);
    }
    return toolCall;
  }

  public writeToolCall(context: Context, toolCall: AgentToolCall): void {
    this.persistence.agentToolCalls.set(context, toolCall.toolCallId, toolCall);
  }

  /** Appends messages, writes the advanced AgentState, and returns it. */
  public appendMessages(
    context: Context,
    state: AgentState,
    turnId: string,
    frameworkMessages: readonly FrameworkMessage[],
  ): AgentState {
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
      };
      this.persistence.agentMessages.set(context, messageSequenceKey(nextMessageSequence), message);
      nextMessageSequence += 1;
    }
    const advancedState = { ...state, nextMessageSequence };
    this.writeAgentState(context, advancedState);
    return advancedState;
  }

  /** Appends the user message, opens the next turn, and moves to RunAgentTurn. */
  public beginTurn(context: Context, state: AgentState, text: string): StepDecision {
    const turnId = `turn-${state.turnCount + 1}`;
    const userMessage = this.runner.buildUserMessage(text);
    if (userMessage.role !== "user") {
      throw new TypeError("buildUserMessage must return a message with role user");
    }
    const sequence = state.nextMessageSequence;
    const stateWithMessage = this.appendMessages(context, state, turnId, [userMessage]);
    this.writeAgentState(context, {
      ...stateWithMessage,
      status: "running_turn",
      activeTurnId: turnId,
      executingToolCallId: null,
    });
    this.writeEvents(context, turnId, (events) => events.writeEvent("turn_started", { sequence }));
    return goTo(new StepRef(agentSessionStepTypes.runAgentTurn), undefined);
  }

  public goToAwaitUserMessage(): StepDecision {
    return goTo(new StepRef(agentSessionStepTypes.awaitUserMessage), undefined);
  }

  /**
   * Picks the Step after a tool call record changed.
   * Order: approvals first, then approved calls in deferral order, then resume the turn.
   */
  public routeToolCalls(context: Context, state: AgentState): StepDecision {
    const toolCalls = this.readTurnToolCalls(context, state.activeTurnId);
    if (toolCalls.some((toolCall) => toolCall.status === "awaiting_approval")) {
      this.writeAgentState(context, {
        ...state,
        status: "waiting_for_interrupt",
        executingToolCallId: null,
      });
      return goTo(new StepRef(agentSessionStepTypes.awaitInterruptResolution), undefined);
    }
    const approvedToolCall = toolCalls.find((toolCall) => toolCall.status === "approved");
    if (approvedToolCall === undefined) {
      this.writeAgentState(context, { ...state, status: "running_turn", executingToolCallId: null });
      return goTo(new StepRef(agentSessionStepTypes.runAgentTurn), undefined);
    }
    this.writeAgentState(context, {
      ...state,
      status: "executing_tool",
      executingToolCallId: approvedToolCall.toolCallId,
    });
    return goTo(new StepRef(toolStepTypeForEffect(approvedToolCall.effect)), undefined);
  }

  public resolveToolPolicy(toolName: string, frameworkEffect?: ToolEffect): ResolvedToolPolicy {
    return resolveToolPolicy(
      toolName,
      this.config.toolPolicies,
      this.config.defaultToolEffect,
      frameworkEffect,
    );
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
  ): Promise<Result> {
    const heartbeatTimer = setInterval(() => {
      // A rejected heartbeat means the attempt already ended; the operation result decides the outcome.
      this.recordHeartbeat(context).catch(() => undefined);
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

export function toolStepTypeForEffect(effect: ToolEffect): string {
  switch (effect) {
    case "read":
    case "idempotent_write":
      return agentSessionStepTypes.executeToolCall;
    case "non_idempotent_write":
    case "unclassified":
      return agentSessionStepTypes.executeSingleAttemptToolCall;
  }
}

/** Returns the text of the failure that selected a recovery Step. */
export function readRecoveryDetail(context: Context): string {
  const recoveryError = context.recoveryError;
  if (recoveryError === undefined) {
    return "unknown failure";
  }
  return recoveryError.detail !== "" ? recoveryError.detail : recoveryError.errorType;
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
