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

function jsonValue(value: unknown): unknown {
  return { objValue: { encoding: 'json', payload: btoa(JSON.stringify(value)) } };
}

describe('executeDecisionSummary', () => {
  it('names one next Step as GoTo', () => {
    expect(executeDecisionSummary(executeCompleted({ nextSteps: [{ stepType: 'SyncBatch' }] }))).toEqual({
      kind: 'go-to',
      label: 'GoTo',
      nextStepCount: 1,
      canceledStepTypeCount: 0,
      channelNames: [],
      fullText: 'GoTo → SyncBatch',
    });
  });

  it('counts every next Step and lists each one in the full text', () => {
    const summary = executeDecisionSummary(executeCompleted({
      nextSteps: [{ stepType: 'ResumeTurn' }, { stepType: 'CallTool' }, { stepType: 'CallTool' }, { stepType: 'Audit' }],
    }));
    expect(summary).toMatchObject({ kind: 'go-to', label: 'GoTo', nextStepCount: 4 });
    expect(summary?.fullText).toBe('GoTo → ResumeTurn\nGoTo → CallTool\nGoTo → CallTool\nGoTo → Audit');
  });

  it.each([
    [5, 'dead-end', 'DeadEnd'],
    ['CLOSE_DECISION_TYPE_DEAD_END', 'dead-end', 'DeadEnd'],
    [2, 'graceful-complete', 'GracefulComplete'],
    ['CLOSE_DECISION_TYPE_GRACEFUL_COMPLETE', 'graceful-complete', 'GracefulComplete'],
    [3, 'force-complete', 'ForceComplete'],
    ['CLOSE_DECISION_TYPE_FORCE_COMPLETE', 'force-complete', 'ForceComplete'],
    [4, 'force-fail', 'ForceFail'],
    ['CLOSE_DECISION_TYPE_FORCE_FAIL', 'force-fail', 'ForceFail'],
    [1, 'force-complete-if-channels-empty', 'ForceCompleteIfChannelsEmpty'],
    ['CLOSE_DECISION_TYPE_FORCE_COMPLETE_ON_CHANNELS_EMPTY', 'force-complete-if-channels-empty', 'ForceCompleteIfChannelsEmpty'],
  ])('names close decision type %s by its SDK name', (closeDecisionType, kind, label) => {
    const summary = executeDecisionSummary(executeCompleted({ closeDecision: { closeDecisionType } }));
    expect(summary).toEqual({ kind, label, nextStepCount: 0, canceledStepTypeCount: 0, channelNames: [], fullText: label });
  });

  it('keeps every conditional Channel of ForceCompleteIfChannelsEmpty', () => {
    const summary = executeDecisionSummary(executeCompleted({
      closeDecision: { closeDecisionType: 1, conditionalChannelNames: ['orders', 'refunds', 'audits'] },
    }));
    expect(summary?.channelNames).toEqual(['orders', 'refunds', 'audits']);
    expect(summary?.fullText).toBe('ForceCompleteIfChannelsEmpty → channels orders, refunds, audits');
  });

  it('adds the ForceFail reason to the full text', () => {
    expect(executeDecisionSummary(executeCompleted({
      closeDecision: { closeDecisionType: 4, closeInput: jsonValue('quota exceeded') },
    }))?.fullText).toBe('ForceFail: quota exceeded');
    expect(executeDecisionSummary(executeCompleted({
      closeDecision: { closeDecisionType: 4, closeInput: { stringValue: 'card declined' } },
    }))?.fullText).toBe('ForceFail: card declined');
  });

  it('shows the close decision first and adds cancellations of either scope', () => {
    const summary = executeDecisionSummary(executeCompleted({
      nextSteps: [{ stepType: 'Notify' }],
      cancelStepTypes: ['Poll'],
      cancelSiblingStepTypes: ['Retry'],
      closeDecision: { closeDecisionType: 2 },
    }));
    expect(summary).toMatchObject({ kind: 'graceful-complete', label: 'GracefulComplete', nextStepCount: 1, canceledStepTypeCount: 2 });
    expect(summary?.fullText).toBe([
      'GracefulComplete',
      'GoTo → Notify',
      'CancelSteps → Poll (entire Flow)',
      'CancelSiblingSteps → Retry (same parent execution)',
    ].join('\n'));
  });

  it('adds cancellations to GoTo when there is no close decision', () => {
    const summary = executeDecisionSummary(executeCompleted({
      nextSteps: [{ stepType: 'RequestHumanApprovalStep' }],
      cancelStepTypes: ['GetUsageStep'],
      closeDecision: { closeDecisionType: 'CLOSE_DECISION_TYPE_UNSPECIFIED' },
    }));
    expect(summary).toEqual({
      kind: 'go-to',
      label: 'GoTo',
      nextStepCount: 1,
      canceledStepTypeCount: 1,
      channelNames: [],
      fullText: 'GoTo → RequestHumanApprovalStep\nCancelSteps → GetUsageStep (entire Flow)',
    });
  });

  it('names a decision that only cancels CancelSteps and counts its Step types', () => {
    const summary = executeDecisionSummary(executeCompleted({ cancelStepTypes: ['Poll'], cancelSiblingStepTypes: ['Retry'] }));
    expect(summary).toMatchObject({ kind: 'cancel-steps', label: 'CancelSteps', nextStepCount: 0, canceledStepTypeCount: 2 });
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
