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
import type {
  AgentFrameworkRunner,
  ResolvedToolPolicy,
  ToolPolicy,
} from "./agent-framework-runner.js";
import { agentMessageKey, type AgentSessionPersistence } from "./agent-session-persistence.js";
import type {
  AgentInterrupt,
  AgentMessage,
  AgentState,
  AgentToolCall,
  FrameworkMessage,
  ToolEffect,
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

const heartbeatIntervalMs = 30_000;

export interface AgentSessionConfig {
  readonly flowType: string;
  readonly toolPolicies: ReadonlyMap<string, ToolPolicy>;
  readonly defaultToolEffect: ToolEffect;
  readonly agentEventsCapacityBytes: number;
  readonly interruptDeadlineMs: number | undefined;
}

/** Shared state reads, writes, and routing used by every session Step. */
export class AgentSessionTransitions {
  public constructor(
    public readonly persistence: AgentSessionPersistence,
    public readonly runner: AgentFrameworkRunner,
    public readonly config: AgentSessionConfig,
  ) {}

  public readAgentState(context: Context): AgentState {
    const state = this.persistence.agentState.get(context) as AgentState | undefined;
    if (state === undefined) {
      throw new Error(`AgentState is not set for session ${context.flowId}`);
    }
    return state;
  }

  public writeAgentState(context: Context, state: AgentState): void {
    this.persistence.agentState.set(context, state);
  }

  public readPendingInterrupts(context: Context): readonly AgentInterrupt[] {
    return (this.persistence.pendingInterrupts.get(context) as readonly AgentInterrupt[] | undefined) ?? [];
  }

  public readFrameworkRunState(context: Context): string | null {
    const runState = this.persistence.frameworkRunState.get(context) as string | undefined;
    return runState === undefined || runState === "" ? null : runState;
  }

  /** Returns every stored message in ascending sequence order; requires the AgentMessages load. */
  public readAllMessages(context: Context): AgentMessage[] {
    const messages = this.persistence.agentMessages;
    return messages
      .getAllInstanceKeys(context)
      .map((key) => messages.get(context, key))
      .sort((left, right) => left.sequence - right.sequence);
  }

  /** Returns the turn's tool call records ordered by tool call ID; requires the AgentToolCalls load. */
  public readTurnToolCalls(context: Context, turnId: string): AgentToolCall[] {
    const toolCalls = this.persistence.agentToolCalls;
    return toolCalls
      .getAllInstanceKeys(context)
      .map((key) => toolCalls.get(context, key))
      .filter((toolCall) => toolCall.turnId === turnId)
      .sort(compareToolCallIds);
  }

  public writeToolCall(context: Context, toolCall: AgentToolCall): void {
    this.persistence.agentToolCalls.set(context, toolCall.toolCallId, toolCall);
  }

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
      this.persistence.agentMessages.set(context, agentMessageKey(nextMessageSequence), message);
      nextMessageSequence += 1;
    }
    return { ...state, nextMessageSequence };
  }

  /** Appends the user message, opens the next turn, and moves to RunAgentTurn. */
  public startTurn(context: Context, state: AgentState, text: string): StepDecision {
    const turnId = `turn-${state.turnCount + 1}`;
    const userMessage = this.runner.buildUserMessage(text);
    const stateWithMessage = this.appendMessages(context, state, turnId, [userMessage]);
    this.writeAgentState(context, {
      ...stateWithMessage,
      status: "running_turn",
      activeTurnId: turnId,
      executingToolCallId: null,
    });
    this.writeEvents(context, turnId, (events) => events.writeAgentEvent("turn_started", {}));
    return goTo(new StepRef(agentSessionStepTypes.runAgentTurn), undefined);
  }

  public goToAwaitUserMessage(): StepDecision {
    return goTo(new StepRef(agentSessionStepTypes.awaitUserMessage), undefined);
  }

  /**
   * Picks the Step after a tool call record changed.
   * Order: approvals first, then approved calls by tool call ID, then resume the turn.
   */
  public routeToolCalls(
    context: Context,
    state: AgentState,
    toolCalls: readonly AgentToolCall[],
  ): StepDecision {
    if (toolCalls.some((toolCall) => toolCall.status === "awaiting_approval")) {
      this.writeAgentState(context, {
        ...state,
        status: "waiting_for_interrupt",
        executingToolCallId: null,
      });
      return goTo(new StepRef(agentSessionStepTypes.awaitInterruptResolution), undefined);
    }
    const approvedToolCall = toolCalls.find((toolCall) => toolCall.status === "approved");
    if (approvedToolCall !== undefined) {
      this.writeAgentState(context, {
        ...state,
        status: "executing_tool",
        executingToolCallId: approvedToolCall.toolCallId,
      });
      return goTo(new StepRef(toolStepTypeForEffect(approvedToolCall.effect)), undefined);
    }
    this.writeAgentState(context, { ...state, status: "running_turn", executingToolCallId: null });
    return goTo(new StepRef(agentSessionStepTypes.runAgentTurn), undefined);
  }

  public resolveToolPolicy(toolName: string, frameworkEffect?: ToolEffect): ResolvedToolPolicy {
    const policy = this.config.toolPolicies.get(toolName);
    const effect = policy?.effect ?? frameworkEffect ?? this.config.defaultToolEffect;
    const requiresApproval = policy?.requiresApproval ?? requiresApprovalByDefault(effect);
    return { effect, requiresApproval, isDeferred: effect !== "read" || requiresApproval };
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

  /** Runs an operation while recording a heartbeat every 30 seconds. */
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

function requiresApprovalByDefault(effect: ToolEffect): boolean {
  return effect === "non_idempotent_write" || effect === "unclassified";
}

export function compareToolCallIds(left: AgentToolCall, right: AgentToolCall): number {
  if (left.toolCallId < right.toolCallId) {
    return -1;
  }
  return left.toolCallId > right.toolCallId ? 1 : 0;
}
