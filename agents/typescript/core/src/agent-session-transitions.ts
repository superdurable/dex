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

import type { BuiltInTool } from "./agent-built-in-tools.js";
import { StreamAgentEventWriter } from "./agent-event-writer.js";
import {
  resolveToolPolicy,
  type AgentFrameworkRunner,
  type AgentTurnContext,
  type DeferredToolOutcome,
  type ResolvedToolPolicy,
  type ToolPolicy,
} from "./agent-framework-runner.js";
import { messageSequenceKey, type AgentSessionPersistence } from "./agent-session-persistence.js";
import type { AgentSessionTracer } from "./agent-session-tracer.js";
import {
  addAgentUsage,
  emptyAgentUsage,
  isFinalToolCallStatus,
  type AgentInterrupt,
  type AgentInterruptKind,
  type AgentMessage,
  type AgentState,
  type AgentToolCall,
  type AgentToolCallRecord,
  type AgentToolCallStatus,
  type AgentUsage,
  type FrameworkMessage,
  type ToolEffect,
} from "./agent-session-values.js";
import type { ResolvedAgentTracingOptions } from "./agent-tracing-options.js";

export const agentSessionStepTypes = {
  startAgentSession: "StartAgentSession",
  awaitUserMessage: "AwaitUserMessage",
  runModel: "RunModel",
  awaitToolApproval: "AwaitToolApproval",
  awaitQuestionAnswer: "AwaitQuestionAnswer",
  awaitTimer: "AwaitTimer",
  callTool: "CallTool",
  callSingleAttemptTool: "CallSingleAttemptTool",
  recordToolFailure: "RecordToolFailure",
  recordToolOutcomeUnknown: "RecordToolOutcomeUnknown",
  resumeTurn: "ResumeTurn",
  recordTurnFailure: "RecordTurnFailure",
} as const;

/** Fields of one `interrupt_resolved` event. */
export interface InterruptResolvedEvent {
  readonly interruptId: string;
  readonly kind: AgentInterruptKind;
  readonly isApproved: boolean;
  readonly isAnswered: boolean;
  readonly rejectionMessage: string | null;
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
        cancellation_reason: null,
      }),
    );
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
    return {
      flowId: context.flowId,
      turnId,
      attempt: context.attempt,
      stepExecutionId: context.stepExecutionId,
      messages: this.readMessages(context, 1, state.nextMessageSequence),
      frameworkRunState,
      isResuming: frameworkRunState !== null,
      toolCalls: runnerToolCalls,
      events,
      cancellationSignal: context.cancellationSignal,
      deadline: context.deadline,
      enabledBuiltInTools: this.config.builtInTools,
      toolPolicies: this.config.toolPolicies,
      defaultToolEffect: this.config.defaultToolEffect,
      resolveToolPolicy: (toolName, frameworkEffect) => this.resolveToolPolicy(toolName, frameworkEffect),
      recordedToolCall: (toolCallId) => toolCallsById.get(toolCallId),
      heartbeat,
    };
  }

  public isEnabledBuiltInTool(toolName: string): toolName is BuiltInTool {
    return this.config.builtInTools.has(toolName as BuiltInTool);
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

  /** Appends the user message, opens the next turn, and moves to RunModel. */
  public beginTurn(context: Context, state: AgentState, text: string, stepType: string): StepDecision {
    const turnId = `turn-${state.turnCount + 1}`;
    const sequence = state.nextMessageSequence;
    return this.tracer.traceUserMessageActivation(
      context,
      stepType,
      turnId,
      messageSequenceKey(sequence),
      text,
      () => {
        const userMessage = this.runner.buildUserMessage(text);
        if (userMessage.role !== "user") {
          throw new TypeError("buildUserMessage must return a message with role user");
        }
        const stateWithMessage = this.appendMessages(context, state, turnId, [userMessage]);
        this.writeAgentState(context, { ...stateWithMessage, status: "running_model", activeTurnId: turnId });
        this.writeEvents(context, turnId, (events) => events.writeEvent("turn_started", { sequence }));
        return goTo(new StepRef(agentSessionStepTypes.runModel), undefined);
      },
    );
  }

  public goToAwaitUserMessage(): StepDecision {
    return goTo(new StepRef(agentSessionStepTypes.awaitUserMessage), undefined);
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
  if (record.status === "awaiting_answer" || record.status === "waiting_for_timer") {
    throw new TypeError(`tool call ${record.toolCallId} is still ${record.status}`);
  }
  return record as AgentToolCall;
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
