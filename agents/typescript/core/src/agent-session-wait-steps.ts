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
  type Condition,
  type Context,
  type Step,
  type StepDecision,
  type StepOptions,
} from "@superdurable/dex";

import {
  noPendingToolQuestion,
  questionAnswerMovement,
  questionInterruptId,
  toolStepMovement,
  toolStepOptions,
} from "./agent-session-steps.js";
import {
  agentSessionStepTypes,
  toolStepTypeForEffect,
  type AgentSessionTransitions,
} from "./agent-session-transitions.js";
import {
  defaultQuestionDeclinedToolResult,
  questionDeadlinePassed,
  type AgentInterrupt,
  type AgentInterruptResolution,
  type AgentToolCallRecord,
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
    const conditions: Condition[] = [transitions.persistence.interruptResolutions.forOne(interruptId)];
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
      transitions.finishToolCall(context, {
        ...toolCall,
        status: "failed",
        error: `the tool's question cannot use interrupt ID ${interruptId}, which another tool call of this turn uses`,
        ...noPendingToolQuestion,
      });
      return deadEnd();
    }
    const [resolution] = context.channelResults(persistence.interruptResolutions, interruptId);
    if (resolution !== undefined) {
      return this.resolveQuestion(context, toolCall, interruptId, resolution);
    }
    if (!context.hasTimerFired()) {
      throw new Error(`the question wait for ${interruptId} completed without an answer`);
    }
    if (this.isToolQuestionUnraised(context, toolCall, interruptId)) {
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
    return this.resolveQuestion(context, toolCall, interruptId, {
      approved: false,
      rejectionMessage: questionDeadlinePassed,
      answer: null,
    });
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
    if (!isAnswered || answer === null) {
      transitions.finishToolCall(context, {
        ...toolCall,
        status: "rejected",
        rejectionMessage: resolution.rejectionMessage ?? defaultQuestionDeclinedToolResult,
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
        { interruptId, question: toolCall.inputQuestion, answer },
      ],
      ...noPendingToolQuestion,
    });
    return goToMany(
      toolStepMovement(transitions.persistence, toolStepTypeForEffect(toolCall.effect), toolCall.toolCallId),
    );
  }
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
    return Wait.until(Timer.byDuration(Math.ceil(remainingMs / 1_000) * 1_000));
  }

  public execute(context: Context, toolCallId: string): StepDecision {
    const transitions = this.transitions;
    const toolCall = transitions.readToolCall(context, toolCallId);
    if (toolCall.status !== "waiting_for_timer") {
      return deadEnd();
    }
    if (!context.hasTimerFired()) {
      throw new Error(`the durable wait ${toolCallId} completed without its Timer`);
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
