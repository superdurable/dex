// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { decodedValue } from '@/app/components/StructuredValue';
import { isBlobReferenceValue, isStoredValueUnavailable } from '@/lib/blobs';
import { stepDecision } from '@/lib/graph';
import { closeDecisionTypeLabel } from '@/lib/semantic';
import type { FlowHistoryEvent } from '@/lib/types';

/** The kind of Step decision a Graph node shows, chosen by `executeDecisionSummary`. */
export type ExecuteDecisionKind =
  | 'go-to'
  | 'dead-end'
  | 'graceful-complete'
  | 'force-complete'
  | 'force-fail'
  | 'force-complete-if-channels-empty'
  | 'cancel-steps';

/** The Step decision a completed Execute returned, as a Graph node shows it. */
export interface ExecuteDecisionSummary {
  kind: ExecuteDecisionKind;
  /** The SDK decision name, such as `GoTo` or `ForceFail`. */
  label: string;
  /** Next Steps the decision starts, counting each movement. */
  nextStepCount: number;
  /** Step types the decision cancels, across the entire Flow and the same parent execution. */
  canceledStepTypeCount: number;
  /** Conditional Channel names of a `force-complete-if-channels-empty` decision. */
  channelNames: string[];
  /** Every part of the decision, one per line, each starting with its SDK name. */
  fullText: string;
}

/** SDK decision names, so the node names a decision as application code does. */
export const executeDecisionSDKNames: Record<ExecuteDecisionKind, string> = {
  'go-to': 'GoTo',
  'dead-end': 'DeadEnd',
  'graceful-complete': 'GracefulComplete',
  'force-complete': 'ForceComplete',
  'force-fail': 'ForceFail',
  'force-complete-if-channels-empty': 'ForceCompleteIfChannelsEmpty',
  'cancel-steps': 'CancelSteps',
};

/** Keyed by the selected-event panel's close decision labels, so both views read the type the same way. */
const closeDecisionKindByLabel: Record<string, ExecuteDecisionKind> = {
  'Dead end': 'dead-end',
  'Graceful complete': 'graceful-complete',
  'Force complete': 'force-complete',
  'Force fail': 'force-fail',
  'Complete when channels are empty': 'force-complete-if-channels-empty',
};

/**
 * Summarizes the Step decision of a completed Execute event.
 *
 * Returns undefined for any other event, and for a decision with no next Step,
 * close decision, or cancellation. A close decision decides the kind first,
 * then next Steps, then cancellations. The full text lists every part.
 */
export function executeDecisionSummary(event: FlowHistoryEvent | undefined): ExecuteDecisionSummary | undefined {
  if (event?.type !== 'StepExecuteCompleted') return undefined;
  const decision = stepDecision(event);
  if (!decision) return undefined;
  const nextStepTypes = namesOf(decision.nextSteps, 'stepType');
  const flowCancels = stringsOf(decision.cancelStepTypes);
  const siblingCancels = stringsOf(decision.cancelSiblingStepTypes);
  const cancelCount = flowCancels.length + siblingCancels.length;
  const close = record(decision.closeDecision);
  const closeKind = close ? closeDecisionKindByLabel[closeDecisionTypeLabel(close.closeDecisionType)] : undefined;
  const channelNames = closeKind === 'force-complete-if-channels-empty' ? stringsOf(close?.conditionalChannelNames) : [];

  const lines: string[] = [];
  if (closeKind) lines.push(closeDecisionLine(closeKind, channelNames, close?.closeInput));
  for (const stepType of nextStepTypes) lines.push(`GoTo → ${stepType}`);
  if (flowCancels.length > 0) lines.push(`CancelSteps → ${flowCancels.join(', ')} (entire Flow)`);
  if (siblingCancels.length > 0) lines.push(`CancelSiblingSteps → ${siblingCancels.join(', ')} (same parent execution)`);
  const fullText = lines.join('\n');

  const kind = closeKind ?? (nextStepTypes.length > 0 ? 'go-to' : cancelCount > 0 ? 'cancel-steps' : undefined);
  if (!kind) return undefined;
  return {
    kind,
    label: executeDecisionSDKNames[kind],
    nextStepCount: nextStepTypes.length,
    canceledStepTypeCount: cancelCount,
    channelNames,
    fullText,
  };
}

function closeDecisionLine(kind: ExecuteDecisionKind, channelNames: string[], closeInput: unknown): string {
  const name = executeDecisionSDKNames[kind];
  if (kind === 'force-complete-if-channels-empty' && channelNames.length > 0) {
    return `${name} → channels ${channelNames.join(', ')}`;
  }
  if (kind === 'force-fail') {
    const reason = forceFailReason(closeInput);
    if (reason) return `${name}: ${reason}`;
  }
  return name;
}

/** The ForceFail reason when the history holds it as readable text, otherwise undefined. */
function forceFailReason(closeInput: unknown): string | undefined {
  if (!closeInput || typeof closeInput !== 'object') return undefined;
  if (isBlobReferenceValue(closeInput) || isStoredValueUnavailable(closeInput)) return undefined;
  const reason = decodedValue(closeInput);
  // Undecodable payloads come back as an object or as the original Value, never as a string.
  return typeof reason === 'string' ? reason.trim() || undefined : undefined;
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
