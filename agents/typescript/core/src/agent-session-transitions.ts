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
  type AgentPlan,
  type AgentState,
  type AgentToolCall,
  type AgentToolCallRecord,
  type AgentToolCallStatus,
  type AgentUsage,
  type AgentUserMessage,
  type FrameworkMessage,
  type InteractionMode,
  type ToolCallCancellationReason,
  type ToolEffect,
  toolCallCancelledBySteeringResult,
  toolCallCancelledByTurnCancellationResult,
  type TurnCancellationRequest,
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
  pollToolTask: "PollToolTask",
  recordToolFailure: "RecordToolFailure",
  recordToolOutcomeUnknown: "RecordToolOutcomeUnknown",
  resumeTurn: "ResumeTurn",
  applyTurnControl: "ApplyTurnControl",
  recordTurnFailure: "RecordTurnFailure",
} as const;

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

  public readOptionalAgentPlan(context: Context): AgentPlan | null {
    return (this.persistence.agentPlan.get(context) as AgentPlan | undefined) ?? null;
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
      interactionMode: state.interactionMode ?? "chat",
      plan: this.readOptionalAgentPlan(context),
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

  /** Appends the turn's user messages, in order, opens the next turn, and moves to RunModel. */
  public beginTurn(
    context: Context,
    state: AgentState,
    messages: readonly AgentUserMessage[],
    stepType: string,
    interactionMode: InteractionMode = "chat",
    executedPlan?: AgentPlan,
    wasPlanDraft = false,
  ): StepDecision {
    const turnId = `turn-${state.turnCount + 1}`;
    const sequence = state.nextMessageSequence;
    return this.tracer.traceUserMessageActivation(
      context,
      stepType,
      turnId,
      messageSequenceKey(sequence),
      messages.map((message) => message.text).join("\n"),
      () => {
        const userMessages = messages.map((message) => this.runner.buildUserMessage(message.text));
        if (userMessages.some((userMessage) => userMessage.role !== "user")) {
          throw new TypeError("buildUserMessage must return a message with role user");
        }
        const stateWithMessage = this.appendMessages(context, state, turnId, userMessages);
        this.writeAgentState(context, {
          ...stateWithMessage,
          status: "running_model",
          activeTurnId: turnId,
          interactionMode,
          acceptsTurnControl: true,
        });
        this.writeEvents(context, turnId, (events) => {
          events.writeEvent("turn_started", { sequence });
          if (executedPlan !== undefined) {
            events.writeEvent("plan_execution_started", { revision: executedPlan.revision });
            if (wasPlanDraft) {
              writePlanUpdated(events, executedPlan);
            }
          }
        });
        return goTo(new StepRef(agentSessionStepTypes.runModel), undefined);
      },
    );
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

  public goToAwaitUserMessage(): StepDecision {
    return goTo(new StepRef(agentSessionStepTypes.awaitUserMessage), undefined);
  }

  public goToApplyTurnControl(): StepDecision {
    return goTo(new StepRef(agentSessionStepTypes.applyTurnControl), undefined);
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
  const recoveryError = context.recoveryError;
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
