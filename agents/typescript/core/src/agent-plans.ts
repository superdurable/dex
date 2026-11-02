// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentPlan, AgentPlanTask, InteractionMode, PlanStatus } from "./agent-session-values.js";

/**
 * Rejection message of a call to any tool other than `write_todos` in a `planning` turn; the same text as
 * the Python core.
 */
export const planningTurnToolRejection = "The tool call was not run: a planning turn can only call write_todos.";

/** First line of the plan note while no plan exists; the same text as the Python core. */
export const noPlanNote = "[Dex] There is no plan yet.";

/** Last line of the plan note in a `planning` turn; the same text as the Python core. */
export const planningTurnInstruction =
  "This turn only plans: write or revise the plan with write_todos, and do not do or claim any of its " +
  "tasks. The user reviews the plan before it runs.";

/**
 * Returns the user message text of a turn that `execute_plan` started.
 *
 * @param revision - The approved {@link AgentPlan.revision}.
 * @returns `[Dex] The user approved plan revision <n>. Execute it and keep the task statuses current with
 * write_todos.`, the same text as the Python core.
 */
export function buildPlanExecutionMessage(revision: number): string {
  return (
    `[Dex] The user approved plan revision ${revision}. ` +
    "Execute it and keep the task statuses current with write_todos."
  );
}

/**
 * Returns the text that shows the model the current plan, or `undefined` for none.
 *
 * A runner that honors plan mode adds it to the model input of every model call, after the context summary
 * and before the turn's messages, as user text. It is not stored as a message, so it always shows the
 * latest plan. The text is the same as the Python core's `build_plan_note`:
 *
 * ```text
 * [Dex] The current plan, revision 2, is draft:
 * - [completed] Look up the order
 * - [pending] Refund the order
 * This turn only plans: write or revise the plan with write_todos, ...
 * ```
 *
 * @param plan - The {@link AgentPlan}, or `null` or `undefined` when there is none.
 * @param interactionMode - The turn's mode; `undefined` means `chat`.
 * @returns One line for the plan and one per task, then {@link planningTurnInstruction} in a `planning`
 * turn, joined with newlines. {@link noPlanNote} stands for a missing or cleared plan in a `planning` turn.
 * `undefined` when there is no plan to show outside a `planning` turn.
 */
export function buildPlanNote(
  plan: AgentPlan | null | undefined,
  interactionMode: InteractionMode | undefined,
): string | undefined {
  const isPlanning = interactionMode === "planning";
  let lines: string[];
  if (plan == null || plan.status === "cleared") {
    if (!isPlanning) {
      return undefined;
    }
    lines = [noPlanNote];
  } else {
    lines = [
      `[Dex] The current plan, revision ${plan.revision}, is ${plan.status}:`,
      ...plan.tasks.map((task) => `- [${task.status}] ${task.content}`),
    ];
  }
  if (isPlanning) {
    lines.push(planningTurnInstruction);
  }
  return lines.join("\n");
}

/**
 * Returns the status of the plan that `write_todos` writes.
 *
 * @param tasks - The tasks written.
 * @param interactionMode - The mode of the turn that wrote them.
 * @returns `cleared` for no tasks, `draft` in a `planning` turn, `completed` when every task is completed,
 * and `active` otherwise.
 */
export function planStatusAfterWrite(
  tasks: readonly AgentPlanTask[],
  interactionMode: InteractionMode | undefined,
): PlanStatus {
  if (tasks.length === 0) {
    return "cleared";
  }
  if (interactionMode === "planning") {
    return "draft";
  }
  return tasks.every((task) => task.status === "completed") ? "completed" : "active";
}
