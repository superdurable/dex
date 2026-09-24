// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Actor, PocFlow, StepModel } from '../canvas/model/pocFlow';
import type { StepGroup } from '../canvas/views/groups';

export interface StepFact {
  label: string;
  value: string;
}

export interface StepContextView {
  stepType: string;
  /** The Step's declared purpose, or null when the Flow never said. Never invented. */
  explanation: string | null;
  facts: StepFact[];
  connector?: StepModel['connector'];
}

/** Who moves this Step along. The graph knows this; a reader should not have to infer it. */
const ACTOR_PHRASE: Record<Actor, string> = {
  external: 'a person or system outside the flow',
  child: 'a SubFlow',
  clock: 'a timer',
  machine: 'the flow itself',
  unknown: 'not stated',
};

/**
 * Where a Step sits in its flow, in the flow's own terms.
 *
 * Purpose first, then placement: the phase it belongs to, who acts, what reaches it, what it
 * waits for, and where it can go. Every line is read from the graph — nothing is inferred, and a
 * fact the Flow did not declare is omitted rather than guessed at.
 */
export function stepContext(
  flow: PocFlow,
  step: StepModel,
  groups: readonly StepGroup[],
): StepContextView {
  const facts: StepFact[] = [];

  const phase = groups.find((group) => group.stepTypes.includes(step.stepType));
  if (phase !== undefined) facts.push({ label: 'In phase', value: phase.label });

  facts.push({ label: 'Who acts', value: ACTOR_PHRASE[step.actor] });

  if (step.isStart) {
    facts.push({ label: 'Reached from', value: 'the flow starts here' });
  } else {
    const inbound = flow.transitions
      .filter((t) => t.toStepId === step.id && t.kind === 'transition' && !t.isSelfLoop)
      .map((t) => stepTypeOf(flow, t.fromStepId))
      .filter((name): name is string => name !== null);
    if (inbound.length > 0) {
      facts.push({ label: 'Reached from', value: unique(inbound).join(', ') });
    }
  }

  // The wait phase already carries one prose line, paraphrased with identifiers verbatim.
  if (step.waitFor !== null && step.waitFor.sentence !== '') {
    facts.push({ label: 'Waits for', value: step.waitFor.sentence });
  }

  const outbound = flow.transitions
    .filter((t) => t.fromStepId === step.id && t.kind === 'transition' && !t.isSelfLoop)
    .map((t) => stepTypeOf(flow, t.toStepId))
    .filter((name): name is string => name !== null);
  if (outbound.length > 0) {
    facts.push({ label: 'Then goes to', value: unique(outbound).join(', ') });
  }

  const recovery = flow.transitions
    .filter((t) => t.fromStepId === step.id && t.kind === 'failure_transition')
    .map((t) => stepTypeOf(flow, t.toStepId))
    .filter((name): name is string => name !== null);
  if (recovery.length > 0) {
    facts.push({ label: 'If it fails', value: unique(recovery).join(', ') });
  }

  return {
    stepType: step.stepType,
    explanation: step.explanation ?? null,
    facts,
    connector: step.connector,
  };
}

function stepTypeOf(flow: PocFlow, stepId: string): string | null {
  return flow.steps.find((step) => step.id === stepId)?.stepType ?? null;
}

function unique(values: readonly string[]): string[] {
  return [...new Set(values)];
}
