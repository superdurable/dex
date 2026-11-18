// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowHistoryEvent } from '@/lib/types';
import { executeDecisionSummary } from './executeDecisionSummary';

function executeCompleted(stepDecision: unknown): FlowHistoryEvent {
  return { eventId: 3, eventTime: null, type: 'StepExecuteCompleted', payload: { output: { stepDecision } } };
}

describe('executeDecisionSummary', () => {
  it('names one next Step type', () => {
    expect(executeDecisionSummary(executeCompleted({ nextSteps: [{ stepType: 'SyncBatch' }] }))).toEqual({
      kind: 'next-steps',
      label: 'SyncBatch',
      hiddenCount: 0,
      fullText: '→ SyncBatch',
    });
  });

  it('shows two next Step types and counts the rest', () => {
    const summary = executeDecisionSummary(executeCompleted({
      nextSteps: [{ stepType: 'ResumeTurn' }, { stepType: 'CallTool' }, { stepType: 'CallTool' }, { stepType: 'Audit' }],
    }));
    expect(summary?.label).toBe('ResumeTurn, CallTool');
    expect(summary?.hiddenCount).toBe(2);
    expect(summary?.fullText).toBe('→ ResumeTurn\n→ CallTool\n→ CallTool\n→ Audit');
  });

  it.each([
    [5, 'dead-end', 'Dead end'],
    ['CLOSE_DECISION_TYPE_DEAD_END', 'dead-end', 'Dead end'],
    [2, 'graceful-complete', 'Graceful complete'],
    ['CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE', 'graceful-complete', 'Graceful complete'],
    [3, 'force-complete', 'Force complete'],
    ['CLOSE_DECISION_TYPE_FORCE_COMPLETE', 'force-complete', 'Force complete'],
    [4, 'force-fail', 'Force fail'],
    ['CLOSE_DECISION_TYPE_FORCE_FAIL', 'force-fail', 'Force fail'],
    [1, 'complete-when-channels-empty', 'Complete when channels are empty'],
    ['CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY', 'complete-when-channels-empty', 'Complete when channels are empty'],
  ])('names close decision type %s', (closeDecisionType, kind, label) => {
    const summary = executeDecisionSummary(executeCompleted({ closeDecision: { closeDecisionType } }));
    expect(summary).toEqual({ kind, label, hiddenCount: 0, fullText: label });
  });

  it('lists the conditional Channels of a channels-empty completion in the tooltip', () => {
    const summary = executeDecisionSummary(executeCompleted({
      closeDecision: { closeDecisionType: 1, conditionalChannelNames: ['orders', 'refunds'] },
    }));
    expect(summary?.fullText).toBe('Complete when channels are empty: channels orders, refunds');
  });

  it('shows the close decision before next Steps and lists every part in the tooltip', () => {
    const summary = executeDecisionSummary(executeCompleted({
      nextSteps: [{ stepType: 'Notify' }],
      cancelStepTypes: ['Poll'],
      cancelSiblingStepTypes: ['Retry'],
      closeDecision: { closeDecisionType: 2 },
    }));
    expect(summary?.kind).toBe('graceful-complete');
    expect(summary?.fullText).toBe([
      '→ Notify',
      'Cancel Poll (entire Flow)',
      'Cancel Retry (same parent execution)',
      'Graceful complete',
    ].join('\n'));
  });

  it('shows next Steps before cancellations when there is no close decision', () => {
    const summary = executeDecisionSummary(executeCompleted({
      nextSteps: [{ stepType: 'Notify' }],
      cancelStepTypes: ['Poll'],
      closeDecision: { closeDecisionType: 'CLOSE_DECISION_TYPE_UNSPECIFIED' },
    }));
    expect(summary).toEqual({
      kind: 'next-steps',
      label: 'Notify',
      hiddenCount: 0,
      fullText: '→ Notify\nCancel Poll (entire Flow)',
    });
  });

  it('names cancellations when the decision only cancels', () => {
    const summary = executeDecisionSummary(executeCompleted({ cancelStepTypes: ['Poll'], cancelSiblingStepTypes: ['Retry'] }));
    expect(summary).toMatchObject({ kind: 'cancel', label: 'Cancel Poll, Retry' });
  });

  it('has no summary for an empty decision, an unspecified close type, or a failed Execute', () => {
    expect(executeDecisionSummary(executeCompleted({}))).toBeUndefined();
    expect(executeDecisionSummary(executeCompleted({ closeDecision: { closeDecisionType: 0 } }))).toBeUndefined();
    expect(executeDecisionSummary(executeCompleted({
      closeDecision: { closeDecisionType: 'CLOSE_DECISION_TYPE_UNSPECIFIED' },
    }))).toBeUndefined();
    expect(executeDecisionSummary({ eventId: 3, eventTime: null, type: 'StepExecuteCompleted', payload: {} })).toBeUndefined();
    expect(executeDecisionSummary({
      eventId: 3,
      eventTime: null,
      type: 'StepExecuteFailed',
      payload: { output: { stepDecision: { nextSteps: [{ stepType: 'Retry' }] } } },
    })).toBeUndefined();
    expect(executeDecisionSummary(undefined)).toBeUndefined();
  });
});
