// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { PocFlow } from '../canvas/model/pocFlow';
import { activeStepTypes, type RunOverlay } from '../canvas/model/run';
import { answeredBy } from '../canvas/views/stepBox';

export type ActionableState = 'done' | 'current' | 'upcoming';

export interface ActionableStep {
  stepId: string;
  stepType: string;
  /** The RPC names that answer this Step's wait, from the graph. */
  answeredBy: string[];
  state: ActionableState;
}

/**
 * The Steps of a Flow that wait for a person, in the order the graph reaches them.
 *
 * Derived, not declared: a Step is human-actionable when something outside the Flow publishes to
 * the Channel it waits on, which `answeredBy` reads straight off the graph. So this needs no new
 * contract — and a Flow that grows a third gate grows a third entry with no UI change.
 */
export function actionableSteps(flow: PocFlow, overlay: RunOverlay | null): ActionableStep[] {
  const open = overlay === null ? [] : activeStepTypes(overlay);
  const ordered = orderedStepIds(flow);
  const waiting = flow.steps
    .filter((step) => answeredBy(flow, step).length > 0)
    .sort((left, right) => ordered.indexOf(left.id) - ordered.indexOf(right.id));

  const currentIndex = waiting.findIndex((step) => open.includes(step.stepType));
  return waiting.map((step, index) => ({
    stepId: step.id,
    stepType: step.stepType,
    answeredBy: answeredBy(flow, step),
    state: stateFor(index, currentIndex, overlay, step.stepType),
  }));
}

function stateFor(
  index: number,
  currentIndex: number,
  overlay: RunOverlay | null,
  stepType: string,
): ActionableState {
  if (currentIndex === index) return 'current';
  // No run yet: the list is the Flow's shape, so nothing has happened to any of it.
  if (overlay === null) return 'upcoming';
  const ran = overlay.executions.some((execution) => execution.stepType === stepType);
  if (ran) return 'done';
  // Before the open gate in graph order but never executed: a branch skipped it.
  return currentIndex >= 0 && index < currentIndex ? 'done' : 'upcoming';
}

/**
 * Step ids in breadth-first order from the start, which is the order a reader meets them.
 *
 * Graph order rather than execution order: an upcoming gate has no execution to sort by, and a
 * stepper has to place it before the reader gets there.
 */
function orderedStepIds(flow: PocFlow): string[] {
  const start = flow.steps.find((step) => step.isStart);
  const order: string[] = [];
  const seen = new Set<string>();
  const queue = start === undefined ? [] : [start.id];
  while (queue.length > 0) {
    const id = queue.shift() as string;
    if (seen.has(id)) continue;
    seen.add(id);
    order.push(id);
    for (const transition of flow.transitions) {
      if (transition.fromStepId === id && transition.kind === 'transition' && !transition.isSelfLoop) {
        queue.push(transition.toStepId);
      }
    }
  }
  // Anything unreachable still gets a stable position rather than -1.
  for (const step of flow.steps) if (!seen.has(step.id)) order.push(step.id);
  return order;
}

/** Whether a stepper is worth drawing at all. One gate needs no progress strip. */
export function hasMultipleActions(steps: readonly ActionableStep[]): boolean {
  return steps.length > 1;
}
