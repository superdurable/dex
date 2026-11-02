// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Timer,
  Wait,
  deadEnd,
  goToMany,
  stringCodec,
  type AsyncContext,
  type Condition,
  type Context,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import {
  ToolExecutionError,
  type ToolTaskPending,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "./agent-framework-runner.js";
import {
  cancelWaitingToolCall,
  isToolInputRequested,
  noPendingToolQuestion,
  questionAnswerMovement,
  questionInterruptId,
  recordToolInputRequest,
  recordToolTaskDefect,
  toolStepMovement,
  toolStepOptions,
  toolTaskPollIntervalSeconds,
  toolTaskPollMovement,
} from "./agent-session-steps.js";
import {
  agentSessionStepTypes,
  cancelledToolCall,
  isCancellableByTurnControl,
  toolStepTypeForEffect,
  type AgentSessionTransitions,
} from "./agent-session-transitions.js";
import {
  defaultQuestionDeclinedToolResult,
  invalidToolTaskPollInterval,
  isFinalToolCallStatus,
  missingToolTaskPoller,
  questionDeadlinePassed,
  type AgentInterrupt,
  type AgentInterruptResolution,
  type AgentToolCallRecord,
  type QuestionAnswer,
} from "./agent-session-values.js";

/**
 * Waits for the answer to one question: an `ask_user` call, or a tool's {@link ToolInputRequested}.
 *
 * `RunModel` raises an `ask_user` question in its own commit. A tool Step runs unlocked, so it only
 * records its question, and this Step's first Execute raises it under the `PendingInterrupts` lock.
 */
export class AwaitQuestionAnswerStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.awaitQuestionAnswer;
  }

  public getStepOptions(): StepOptions {
    return toolStepOptions(this.transitions.persistence, agentSessionStepTypes.awaitQuestionAnswer);
  }

  public waitFor(context: Context, toolCallId: string): Wait {
    const transitions = this.transitions;
    const toolCall = transitions.readToolCall(context, toolCallId);
    const interruptId = questionInterruptId(toolCall);
    if (this.isInterruptIdOwnedByAnotherCall(context, toolCall, interruptId)) {
      // Never wait on the other call's answers; Execute fails this call at once.
      return Wait.until(Timer.byDuration(0));
    }
    const conditions: Condition[] = [
      transitions.persistence.toolCallCancellations.forOne(toolCallId),
      transitions.persistence.interruptResolutions.forOne(interruptId),
    ];
    // After answer_question removed the interrupt, its deadline no longer applies.
    const interrupt = transitions
      .readPendingInterrupts(context)
      .find((pendingInterrupt) => pendingInterrupt.interruptId === interruptId);
    if (interrupt?.deadline != null) {
      // Dex Timers take whole seconds; rounding up can only fire late, never early.
      const remainingMs = Math.max(0, Date.parse(interrupt.deadline) - Date.now());
      conditions.push(Timer.byDuration(Math.ceil(remainingMs / 1_000) * 1_000));
    } else if (this.isToolQuestionUnraised(context, toolCall, interruptId)) {
      // Execute holds the PendingInterrupts lock, so it raises the tool's question at once.
      conditions.push(Timer.byDuration(0));
    }
    return Wait.anyOf(...conditions);
  }

  public execute(context: Context, toolCallId: string): StepDecision {
    const transitions = this.transitions;
    const persistence = transitions.persistence;
    const toolCall = transitions.readToolCall(context, toolCallId);
    if (toolCall.status !== "awaiting_answer") {
      return deadEnd();
    }
    const interruptId = questionInterruptId(toolCall);
    if (this.isInterruptIdOwnedByAnotherCall(context, toolCall, interruptId)) {
      const error = `the tool's question cannot use interrupt ID ${interruptId}, which another tool call of this turn uses`;
      transitions.finishToolCall(
        context,
        // A started task may have taken effect, so its call is recorded by effect.
        (toolCall.taskId ?? null) !== null
          ? recordToolTaskDefect(toolCall, error)
          : { ...toolCall, status: "failed", error, ...noPendingToolQuestion },
      );
      return deadEnd();
    }
    if (context.channelResults(persistence.toolCallCancellations, toolCallId).length > 0) {
      return cancelWaitingToolCall(
        transitions,
        context,
        toolCall,
        { interruptId, kind: "question" },
        questionAnswerMovement(persistence, toolCall),
      );
    }
    const [resolution] = context.channelResults(persistence.interruptResolutions, interruptId);
    if (resolution !== undefined) {
      return this.resolveQuestion(context, toolCall, interruptId, resolution);
    }
    if (!context.hasTimerFired()) {
      throw new Error(`the question wait for ${interruptId} completed without an answer`);
    }
    if (this.isToolQuestionUnraised(context, toolCall, interruptId)) {
      const reason = transitions.readTurnControlReason(context, toolCall.turnId);
      if (reason !== null && isCancellableByTurnControl(toolCall)) {
        // The turn is steered or cancelled, so the tool's question is never asked.
        transitions.finishToolCall(context, cancelledToolCall(toolCall, reason));
        return deadEnd();
      }
      this.raiseToolQuestion(context, toolCall, interruptId);
      return goToMany(questionAnswerMovement(persistence, toolCall));
    }
    const pendingInterrupts = transitions.readPendingInterrupts(context);
    const interrupt = pendingInterrupts.find((pending) => pending.interruptId === interruptId);
    // An early Timer, or an answer that the next wait consumes.
    if (interrupt?.deadline == null || Date.parse(interrupt.deadline) > Date.now()) {
      return goToMany(questionAnswerMovement(persistence, toolCall));
    }
    persistence.pendingInterrupts.set(
      context,
      pendingInterrupts.filter((pending) => pending.interruptId !== interruptId),
    );
    return this.resolveQuestion(
      context,
      toolCall,
      interruptId,
      { approved: false, rejectionMessage: questionDeadlinePassed, answer: null },
      true,
    );
  }

  /**
   * A tool question's `<id>-input-<n>` is taken when another call's interrupt is pending under it or a
   * record has that key, as in the Python core.
   */
  private isInterruptIdOwnedByAnotherCall(
    context: Context,
    toolCall: AgentToolCallRecord,
    interruptId: string,
  ): boolean {
    if (toolCall.status !== "awaiting_answer" || toolCall.inputQuestion === null) {
      return false;
    }
    const transitions = this.transitions;
    const pending = transitions.readPendingInterrupts(context).find((interrupt) => interrupt.interruptId === interruptId);
    if (pending !== undefined && pending.toolCallId !== toolCall.toolCallId) {
      return true;
    }
    return transitions.persistence.agentToolCalls.get(context, interruptId) !== undefined;
  }

  /** A tool's question waits to be raised when it is neither pending nor answered. */
  private isToolQuestionUnraised(context: Context, toolCall: AgentToolCallRecord, interruptId: string): boolean {
    const transitions = this.transitions;
    return (
      toolCall.status === "awaiting_answer" &&
      toolCall.inputQuestion !== null &&
      !transitions.readPendingInterrupts(context).some((pending) => pending.interruptId === interruptId) &&
      context.channelSize(transitions.persistence.interruptResolutions, interruptId) === 0
    );
  }

  private raiseToolQuestion(context: Context, toolCall: AgentToolCallRecord, interruptId: string): void {
    const transitions = this.transitions;
    const createdAt = new Date();
    const deadlineMs = transitions.config.interruptDeadlineMs;
    const interrupt: AgentInterrupt = {
      interruptId,
      kind: "question",
      toolCallId: toolCall.toolCallId,
      toolName: toolCall.toolName,
      arguments: toolCall.arguments,
      createdAt: createdAt.toISOString(),
      deadline: deadlineMs === undefined ? null : new Date(createdAt.getTime() + deadlineMs).toISOString(),
      question: toolCall.inputQuestion,
      choices: [...toolCall.inputChoices],
      allowsFreeText: toolCall.inputAllowsFreeText,
    };
    transitions.persistence.pendingInterrupts.set(context, [...transitions.readPendingInterrupts(context), interrupt]);
    transitions.writeEvents(context, toolCall.turnId, (events) => transitions.writeInterruptRaised(events, interrupt));
  }

  private resolveQuestion(
    context: Context,
    toolCall: AgentToolCallRecord,
    interruptId: string,
    resolution: AgentInterruptResolution,
    isDeadlinePassed = false,
  ): StepDecision {
    const transitions = this.transitions;
    const answer = resolution.answer ?? null;
    const isAnswered = resolution.approved && answer !== null;
    transitions.writeInterruptResolved(context, toolCall.turnId, {
      interruptId,
      kind: "question",
      isApproved: resolution.approved,
      isAnswered,
      rejectionMessage: resolution.rejectionMessage,
    });
    if ((toolCall.taskId ?? null) !== null && toolCall.inputQuestion !== null) {
      return this.returnQuestionToTask(context, toolCall, interruptId, resolution, isDeadlinePassed);
    }
    if (!isAnswered || answer === null) {
      transitions.finishToolCall(context, {
        ...toolCall,
        status: "rejected",
        rejectionMessage: declineText(resolution.rejectionMessage),
        ...noPendingToolQuestion,
      });
      return deadEnd();
    }
    if (toolCall.inputQuestion === null) {
      transitions.finishToolCall(context, {
        ...toolCall,
        status: "succeeded",
        output: JSON.stringify({ status: "answered", answer }),
      });
      return deadEnd();
    }
    transitions.writeToolCall(context, {
      ...toolCall,
      status: "approved",
      inputAnswers: [
        ...toolCall.inputAnswers,
        { interruptId, question: toolCall.inputQuestion, answer, resolution: "answered" },
      ],
      ...noPendingToolQuestion,
    });
    return goToMany(
      toolStepMovement(transitions.persistence, toolStepTypeForEffect(toolCall.effect), toolCall.toolCallId),
    );
  }

  /** Hands a started task's question back to it; its result, not `rejected`, ends the call. */
  private returnQuestionToTask(
    context: Context,
    toolCall: AgentToolCallRecord,
    interruptId: string,
    resolution: AgentInterruptResolution,
    isDeadlinePassed: boolean,
  ): StepDecision {
    const answer = resolution.answer ?? null;
    const resolved: QuestionAnswer =
      resolution.approved && answer !== null
        ? { interruptId, question: toolCall.inputQuestion ?? "", answer, resolution: "answered" }
        : isDeadlinePassed
          ? { interruptId, question: toolCall.inputQuestion ?? "", answer: questionDeadlinePassed, resolution: "deadline_passed" }
          : {
              interruptId,
              question: toolCall.inputQuestion ?? "",
              answer: declineText(resolution.rejectionMessage),
              resolution: "declined",
            };
    const waiting: AgentToolCallRecord = {
      ...toolCall,
      status: "waiting_for_task",
      inputAnswers: [...toolCall.inputAnswers, resolved],
      taskAnswerPending: true,
      ...noPendingToolQuestion,
    };
    this.transitions.writeToolCall(context, waiting);
    return goToMany(toolTaskPollMovement(this.transitions.persistence, waiting));
  }
}

/**
 * Polls one tool's remote task after its poll interval, until the task finishes.
 *
 * A tool Step that received {@link ToolTaskStarted} moves here. Each execution waits on a Timer, calls
 * the runner's `pollToolTask` once, and records the result, asks the task's question, or moves to itself.
 */
export class PollToolTaskStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.pollToolTask;
  }

  public getStepOptions(): StepOptions {
    return toolStepOptions(this.transitions.persistence, agentSessionStepTypes.pollToolTask);
  }

  public waitFor(context: Context, toolCallId: string): Wait {
    const toolCall = this.transitions.readToolCall(context, toolCallId);
    // A resolved question reaches the task at once, not after a backed-off interval.
    const intervalSeconds = toolCall.taskAnswerPending ? 0 : (toolCall.taskPollIntervalSeconds ?? 1);
    return Wait.until(Timer.byDuration(intervalSeconds * 1_000));
  }

  public async execute(context: AsyncContext, toolCallId: string): Promise<StepDecision> {
    const transitions = this.transitions;
    const toolCall = transitions.readToolCall(context, toolCallId);
    if (isFinalToolCallStatus(toolCall.status)) {
      return deadEnd();
    }
    if (toolCall.status !== "waiting_for_task" || (toolCall.taskId ?? null) === null) {
      throw new Error(`tool call ${toolCallId} is not waiting for a task`);
    }
    const pollToolTask = transitions.runner.pollToolTask;
    const events = transitions.createEventWriter(context, toolCall.turnId);
    try {
      if (pollToolTask === undefined) {
        // A Worker whose runner lost the method; the task may still run.
        transitions.finishToolCall(context, recordToolTaskDefect(toolCall, missingToolTaskPoller), events);
        return deadEnd();
      }
      const poll: ToolTaskPoll = {
        flowId: context.flowId,
        toolCall,
        idempotencyKey: `${context.flowId}/${toolCall.turnId}/${toolCall.toolCallId}`,
        attempt: context.attempt,
        inputAnswers: toolCall.inputAnswers,
        pendingAnswer: toolCall.taskAnswerPending ? (toolCall.inputAnswers.at(-1) ?? null) : null,
        events,
        cancellationSignal: context.cancellationSignal,
        deadline: context.deadline,
        heartbeat: () => transitions.recordHeartbeat(context),
      };
      const polled = await transitions.tracer.traceToolCall(context, this.getStepType(), toolCall, async (pollSpan) => {
        let outcome: ToolTaskPollOutcome;
        try {
          outcome = await transitions.runWithHeartbeats(context, () => pollToolTask.call(transitions.runner, poll));
        } catch (error: unknown) {
          if (!(error instanceof ToolExecutionError)) {
            throw error;
          }
          const failed: AgentToolCallRecord = { ...toolCall, status: "failed", error: error.message };
          pollSpan.recordFinished(failed);
          return failed;
        }
        const record = recordToolTaskPoll(toolCall, outcome);
        pollSpan.recordFinished(record);
        return record;
      });
      if (isFinalToolCallStatus(polled.status)) {
        transitions.finishToolCall(context, polled, events);
        return deadEnd();
      }
      if (polled.status === "awaiting_answer") {
        // This Step runs unlocked, so it leaves PendingInterrupts to AwaitQuestionAnswer.
        transitions.writeToolCall(context, polled);
        return goToMany(questionAnswerMovement(transitions.persistence, polled));
      }
      if (
        polled.taskPollIntervalSeconds !== toolCall.taskPollIntervalSeconds ||
        polled.taskAnswerPending !== (toolCall.taskAnswerPending ?? false) ||
        polled.inputContinuation !== toolCall.inputContinuation
      ) {
        transitions.writeToolCall(context, polled);
      }
      return goToMany(toolTaskPollMovement(transitions.persistence, polled));
    } finally {
      events.close();
    }
  }
}

/** Returns the record after one poll: final, awaiting an answer, or still waiting. */
function recordToolTaskPoll(toolCall: AgentToolCallRecord, outcome: ToolTaskPollOutcome): AgentToolCallRecord {
  const polled: AgentToolCallRecord = { ...toolCall, taskAnswerPending: false };
  if (isToolTaskPending(outcome)) {
    const pollIntervalSeconds = toolTaskPollIntervalSeconds(outcome.pollIntervalSeconds);
    // The runner decides what the next poll needs to know, such as an answer not taken yet.
    return pollIntervalSeconds === null
      ? recordToolTaskDefect(toolCall, invalidToolTaskPollInterval)
      : { ...polled, taskPollIntervalSeconds: pollIntervalSeconds, inputContinuation: outcome.continuation ?? null };
  }
  if (isToolInputRequested(outcome)) {
    const asked = recordToolInputRequest(polled, outcome);
    return isFinalToolCallStatus(asked.status) ? recordToolTaskDefect(toolCall, asked.error ?? "") : asked;
  }
  if (typeof outcome?.output !== "string") {
    throw new TypeError("pollToolTask must return a ToolExecutionResult, a ToolInputRequested, or a ToolTaskPending");
  }
  return { ...polled, status: "succeeded", output: outcome.output };
}

/** Returns the text the model sees for a declined question; an empty message reads as none, as in Python. */
function declineText(rejectionMessage: string | null): string {
  return rejectionMessage === null || rejectionMessage === "" ? defaultQuestionDeclinedToolResult : rejectionMessage;
}

function isToolTaskPending(outcome: ToolTaskPollOutcome): outcome is ToolTaskPending {
  return (outcome as { readonly kind?: unknown } | undefined)?.kind === "tool_task_pending";
}

/** Waits on a Dex Timer for one `durable_wait` call, then records its result. */
export class AwaitTimerStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.awaitTimer;
  }

  public getStepOptions(): StepOptions {
    return toolStepOptions(this.transitions.persistence, agentSessionStepTypes.awaitTimer);
  }

  public waitFor(context: Context, toolCallId: string): Wait {
    const toolCall = this.transitions.readToolCall(context, toolCallId);
    if (toolCall.waitUntil === null) {
      throw new Error(`durable_wait call ${toolCallId} has no wait_until`);
    }
    // Dex Timers take whole seconds; rounding up can only fire late, never early.
    const remainingMs = Math.max(0, Date.parse(toolCall.waitUntil) - Date.now());
    return Wait.anyOf(
      Timer.byDuration(Math.ceil(remainingMs / 1_000) * 1_000),
      this.transitions.persistence.toolCallCancellations.forOne(toolCallId),
    );
  }

  public execute(context: Context, toolCallId: string): StepDecision {
    const transitions = this.transitions;
    const toolCall = transitions.readToolCall(context, toolCallId);
    if (toolCall.status !== "waiting_for_timer") {
      return deadEnd();
    }
    if (!context.hasTimerFired()) {
      if (context.channelResults(transitions.persistence.toolCallCancellations, toolCallId).length === 0) {
        throw new Error(`the durable wait ${toolCallId} completed without its Timer`);
      }
      return cancelWaitingToolCall(
        transitions,
        context,
        toolCall,
        null,
        toolStepMovement(transitions.persistence, agentSessionStepTypes.awaitTimer, toolCallId),
      );
    }
    // The Timer is the authority: it never fires early, and skipping it ends the wait.
    // RunModel validated these arguments, so a later maxDurableWaitMs cannot fail a started wait.
    const { duration_seconds: waitedSeconds, reason } = JSON.parse(toolCall.arguments) as {
      readonly duration_seconds: number;
      readonly reason: string;
    };
    transitions.finishToolCall(context, {
      ...toolCall,
      status: "succeeded",
      output: JSON.stringify({ status: "completed", waited_seconds: Math.trunc(waitedSeconds), reason }),
    });
    return deadEnd();
  }
}
