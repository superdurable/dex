// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { stepDecision } from '@/lib/graph';
import { closeDecisionTypeLabel } from '@/lib/semantic';
import type { FlowHistoryEvent } from '@/lib/types';

/** The part of a Step decision a Graph node shows, chosen by `executeDecisionSummary`. */
export type ExecuteDecisionKind =
  | 'next-steps'
  | 'dead-end'
  | 'graceful-complete'
  | 'force-complete'
  | 'force-fail'
  | 'complete-when-channels-empty'
  | 'cancel';

/** One line that names the Step decision a completed Execute returned. */
export interface ExecuteDecisionSummary {
  kind: ExecuteDecisionKind;
  /** The visible text: a close decision name, the first next Step types, or the canceled Step types. */
  label: string;
  /** Next Step types the label leaves out, shown as "+N". */
  hiddenCount: number;
  /** Every part of the decision, one per line, for the tooltip. */
  fullText: string;
}

/** A node shows this many next Step types before it shows "+N". */
export const maxVisibleNextStepTypes = 2;

/** Keyed by the selected-event panel's close decision labels, so both views use the same words. */
const closeDecisionKindByLabel: Record<string, ExecuteDecisionKind> = {
  'Dead end': 'dead-end',
  'Graceful complete': 'graceful-complete',
  'Force complete': 'force-complete',
  'Force fail': 'force-fail',
  'Complete when channels are empty': 'complete-when-channels-empty',
};

/**
 * Summarizes the Step decision of a completed Execute event.
 *
 * Returns undefined for any other event, and for a decision with no next Step,
 * close decision, or cancellation. A close decision decides the kind first,
 * then next Steps, then cancellations; the tooltip lists every part.
 */
export function executeDecisionSummary(event: FlowHistoryEvent | undefined): ExecuteDecisionSummary | undefined {
  if (event?.type !== 'StepExecuteCompleted') return undefined;
  const decision = stepDecision(event);
  if (!decision) return undefined;
  const nextStepTypes = namesOf(decision.nextSteps, 'stepType');
  const flowCancels = stringsOf(decision.cancelStepTypes);
  const siblingCancels = stringsOf(decision.cancelSiblingStepTypes);
  const close = record(decision.closeDecision);
  const closeLabel = close ? closeDecisionTypeLabel(close.closeDecisionType) : undefined;
  const closeKind = closeLabel ? closeDecisionKindByLabel[closeLabel] : undefined;
  const closeChannels = stringsOf(close?.conditionalChannelNames);

  const lines = nextStepTypes.map((stepType) => `→ ${stepType}`);
  if (flowCancels.length > 0) lines.push(`Cancel ${flowCancels.join(', ')} (entire Flow)`);
  if (siblingCancels.length > 0) lines.push(`Cancel ${siblingCancels.join(', ')} (same parent execution)`);
  if (closeKind && closeLabel) {
    lines.push(closeChannels.length > 0 ? `${closeLabel}: channels ${closeChannels.join(', ')}` : closeLabel);
  }
  const fullText = lines.join('\n');

  if (closeKind && closeLabel) return { kind: closeKind, label: closeLabel, hiddenCount: 0, fullText };
  if (nextStepTypes.length > 0) {
    return {
      kind: 'next-steps',
      label: nextStepTypes.slice(0, maxVisibleNextStepTypes).join(', '),
      hiddenCount: Math.max(0, nextStepTypes.length - maxVisibleNextStepTypes),
      fullText,
    };
  }
  const canceled = [...new Set([...flowCancels, ...siblingCancels])];
  if (canceled.length > 0) return { kind: 'cancel', label: `Cancel ${canceled.join(', ')}`, hiddenCount: 0, fullText };
  return undefined;
}

function record(value: unknown): Record<string, unknown> | undefined {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function stringsOf(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((entry): entry is string => typeof entry === 'string' && entry !== '') : [];
}

function namesOf(values: unknown, field: string): string[] {
  if (!Array.isArray(values)) return [];
  return values.flatMap((value) => {
    const name = record(value)?.[field];
    return typeof name === 'string' && name !== '' ? [name] : [];
  });
}
