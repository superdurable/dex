// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  deadEnd,
  goToMany,
  stringCodec,
  type AsyncContext,
  type Context,
  type Step,
  type StepDecision,
  type StepMovement,
  type StepOptions,
} from "@superdurable/dex";

import type { ToolTaskCancellation } from "./agent-framework-runner.js";
import {
  noPendingToolQuestion,
  questionAnswerMovement,
  questionInterruptId,
  toolStepMovement,
  toolStepOptions,
  type WaitingToolCallInterrupt,
} from "./agent-session-steps.js";
import {
  agentSessionStepTypes,
  cancelledToolCall,
  isAbandonedToolTask,
  isCancellableByTurnControl,
  type AgentSessionTransitions,
} from "./agent-session-transitions.js";
import { isFinalToolCallStatus, toolTaskAbandonedError, type AgentToolCallRecord } from "./agent-session-values.js";

// Recovery Steps must eventually run; cap their backoff so a returning Worker picks them up quickly.
const recoveryRetry = { maximumIntervalMs: 5_000 } as const;

/**
 * Finishes a turn that `cancel_turn` stopped, from the state that committed.
 *
 * `cancel_turn` cancels the Steps that call a model or wait, then moves here with the turn ID. When the model
 * call or compaction was cancelled, it moves to `ApplyTurnControl`. Otherwise `ResumeTurn` still waits: this
 * Step records each call whose wait was cancelled, hands each abandoned task to `CancelToolTask`, and ends, so
 * `ResumeTurn` ends the turn once every started tool Step finishes.
 */
export class FinishTurnCancellationStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.finishTurnCancellation;
  }

  public getStepOptions(): StepOptions {
    const persistence = this.transitions.persistence;
    // The cancel_turn RPC holds both locks, so this Step reads the state that RPC committed.
    return {
      waitForDurability: "sync",
      executeDurability: "sync",
      executeRetry: recoveryRetry,
      executeLoadAttributeMaps: [persistence.agentToolCalls],
      executeLockAttributes: [persistence.turnCancellationRequest.lock(), persistence.pendingInterrupts.lock()],
    };
  }

  public execute(context: Context, turnId: string): StepDecision {
    const transitions = this.transitions;
    const state = transitions.readOptionalAgentState(context);
    if (state === null || state.activeTurnId !== turnId || !transitions.isTurnCancelled(context, turnId)) {
      // The turn ended before the cancellation applied.
      return deadEnd();
    }
    if (state.status !== "running_tools") {
      // cancel_turn cancelled the model call or compaction, so no other Step ends the turn.
      return transitions.goToApplyTurnControl(context);
    }
    // ResumeTurn waits for every result and ends the turn; started tool Steps still finish.
    return this.finishWaitingToolCalls(context, turnId);
  }

  private finishWaitingToolCalls(context: Context, turnId: string): StepDecision {
    const transitions = this.transitions;
    const persistence = transitions.persistence;
    const interrupts = transitions.readPendingInterrupts(context);
    const cancelledInterruptIds = new Set<string>();
    const movements: StepMovement<string>[] = [];
    for (const toolCall of transitions.readTurnToolCalls(context, turnId)) {
      const interrupt = waitingInterrupt(toolCall);
      if (interrupt !== null) {
        if (context.channelSize(persistence.interruptResolutions, interrupt.interruptId) > 0) {
          // The answer committed first; a new wait applies it, and its next Step decides.
          if ((toolCall.cancellationReason ?? null) === null) {
            // The mark keeps a later FinishTurnCancellation from starting a second wait.
            const handedBack: AgentToolCallRecord = { ...toolCall, cancellationReason: "turn_cancelled" };
            transitions.writeToolCall(context, handedBack);
            movements.push(
              handedBack.status === "awaiting_approval"
                ? toolStepMovement(persistence, agentSessionStepTypes.awaitToolApproval, handedBack.toolCallId)
                : questionAnswerMovement(persistence, handedBack),
            );
          }
          continue;
        }
        if (interrupts.some((pending) => pending.interruptId === interrupt.interruptId)) {
          cancelledInterruptIds.add(interrupt.interruptId);
          transitions.writeInterruptCancelled(context, turnId, interrupt.interruptId, interrupt.kind, "turn_cancelled");
        }
      }
      if (isAbandonedToolTask(toolCall)) {
        if ((toolCall.cancellationReason ?? null) === null) {
          const abandoned: AgentToolCallRecord = {
            ...toolCall,
            status: "waiting_for_task",
            cancellationReason: "turn_cancelled",
            taskAnswerPending: false,
            ...noPendingToolQuestion,
          };
          transitions.writeToolCall(context, abandoned);
          movements.push(toolStepMovement(persistence, agentSessionStepTypes.cancelToolTask, toolCall.toolCallId));
        }
        continue;
      }
      if (isCancellableByTurnControl(toolCall)) {
        transitions.finishToolCall(context, cancelledToolCall(toolCall, "turn_cancelled"));
      }
    }
    if (cancelledInterruptIds.size > 0) {
      persistence.pendingInterrupts.set(
        context,
        interrupts.filter((pending) => !cancelledInterruptIds.has(pending.interruptId)),
      );
    }
    return movements.length > 0 ? goToMany(...movements) : deadEnd();
  }
}

/** Asks the runner to cancel one task that `cancel_turn` abandoned, then records it `outcome_unknown`. */
export class CancelToolTaskStep implements Step<string> {
  public readonly inputCodec = stringCodec;

  public constructor(private readonly transitions: AgentSessionTransitions) {}

  public getStepType(): string {
    return agentSessionStepTypes.cancelToolTask;
  }

  public getStepOptions(): StepOptions {
    return toolStepOptions(this.transitions.persistence, agentSessionStepTypes.cancelToolTask);
  }

  public async execute(context: AsyncContext, toolCallId: string): Promise<StepDecision> {
    const transitions = this.transitions;
    const toolCall = transitions.readToolCall(context, toolCallId);
    if (isFinalToolCallStatus(toolCall.status)) {
      return deadEnd();
    }
    if ((toolCall.taskId ?? null) === null || (toolCall.cancellationReason ?? null) === null) {
      throw new Error(`tool call ${toolCallId} has no abandoned task`);
    }
    const cancelToolTask = transitions.runner.cancelToolTask;
    if (cancelToolTask !== undefined) {
      const cancellation: ToolTaskCancellation = {
        flowId: context.flowId,
        toolCall,
        idempotencyKey: `${context.flowId}/${toolCall.turnId}/${toolCall.toolCallId}`,
        attempt: context.attempt,
        cancellationSignal: context.cancellationSignal,
        heartbeat: () => transitions.recordHeartbeat(context),
      };
      await transitions.runWithHeartbeats(context, () => cancelToolTask.call(transitions.runner, cancellation));
    }
    transitions.finishToolCall(context, { ...toolCall, status: "outcome_unknown", error: toolTaskAbandonedError });
    return deadEnd();
  }
}

/** Returns the interrupt a waiting record's Step waits on, or `null`. */
function waitingInterrupt(toolCall: AgentToolCallRecord): WaitingToolCallInterrupt | null {
  if (toolCall.status === "awaiting_approval") {
    return { interruptId: toolCall.toolCallId, kind: "tool_approval" };
  }
  if (toolCall.status === "awaiting_answer") {
    return { interruptId: questionInterruptId(toolCall), kind: "question" };
  }
  return null;
}
