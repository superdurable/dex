// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import {
  decidingEventIdOf,
  inferPrunedEventIdRange,
  prunedEventIdRangeOf,
  reportedRetainedHistoryOf,
  waitForCompletedEventIdOf,
  waitForCompletedTimeOf,
} from './historyRelations';
import { stepEvent } from './historyTestServer';
import type { FlowHistoryEvent } from './types';

const flowStart: FlowHistoryEvent = {
  eventId: 1,
  eventTime: '2026-10-24T17:00:00Z',
  type: 'FlowStartedOrContinued',
  payload: {},
  runAttempt: 1,
};

describe('retained history fields', () => {
  it('reads a reported pruned range and treats absent fields as unreported', () => {
    expect(reportedRetainedHistoryOf({ events: [] })).toBeUndefined();
    expect(reportedRetainedHistoryOf({
      firstRetainedEventId: '1',
      prunedEventIdRange: { startEventId: '2', endEventId: '4000' },
    })).toEqual({ prunedEventIdRange: { firstEventId: 2, lastEventId: 4000 } });
    expect(reportedRetainedHistoryOf({ firstRetainedEventId: 4001 }))
      .toEqual({ prunedEventIdRange: { firstEventId: 2, lastEventId: 4000 } });
    expect(reportedRetainedHistoryOf({ firstRetainedEventId: 1, prunedEventIdRange: {} })).toEqual({});
  });

  it('keeps the inference when a Server sends empty or zero boundary fields', () => {
    expect(reportedRetainedHistoryOf({ prunedEventIdRange: {} })).toBeUndefined();
    expect(reportedRetainedHistoryOf({
      firstRetainedEventId: '0',
      prunedEventIdRange: { startEventId: 0, endEventId: 0 },
    })).toBeUndefined();
    expect(reportedRetainedHistoryOf({ firstRetainedEventId: null, prunedEventIdRange: null })).toBeUndefined();
  });

  it('prefers a reported range and keeps an inferred gap over a report of no gap', () => {
    const oldest = [flowStart, stepEvent(4001, 'StepWaitForCompleted', { stepExecutionId: 'Step-2000' })];
    const inferred = { firstEventId: 2, lastEventId: 4000 };

    expect(prunedEventIdRangeOf(undefined, oldest)).toEqual(inferred);
    expect(prunedEventIdRangeOf(reportedRetainedHistoryOf({ firstRetainedEventId: 1 }), oldest)).toEqual(inferred);
    expect(prunedEventIdRangeOf(
      reportedRetainedHistoryOf({ firstRetainedEventId: 5001, prunedEventIdRange: { startEventId: 2, endEventId: 5000 } }),
      oldest,
    )).toEqual({ firstEventId: 2, lastEventId: 5000 });
    expect(prunedEventIdRangeOf(reportedRetainedHistoryOf({ firstRetainedEventId: 2 }), [flowStart])).toBeUndefined();
  });

  it('reads related event IDs from the event context', () => {
    const execute = stepEvent(30, 'StepExecuteCompleted', {
      stepExecutionId: 'Review-3',
      waitForCompletedEventId: '12',
      waitForCompletedTime: '2026-10-24T17:00:12Z',
      fromEventId: 9,
    });
    expect(waitForCompletedEventIdOf(execute)).toBe(12);
    expect(waitForCompletedTimeOf(execute)).toBe('2026-10-24T17:00:12Z');
    expect(decidingEventIdOf(execute)).toBe(9);
    const legacy = stepEvent(31, 'StepExecuteCompleted', { stepExecutionId: 'Review-4' });
    expect(waitForCompletedEventIdOf(legacy)).toBeUndefined();
    expect(waitForCompletedTimeOf(legacy)).toBeUndefined();
    expect(decidingEventIdOf(legacy)).toBeUndefined();
  });

  it('infers pruning only from a gap after FlowStart within one run attempt', () => {
    const retained = stepEvent(4001, 'StepWaitForCompleted', { stepExecutionId: 'Step-2000' });
    expect(inferPrunedEventIdRange([flowStart, retained])).toEqual({ firstEventId: 2, lastEventId: 4000 });
    expect(inferPrunedEventIdRange([flowStart, { ...retained, eventId: 2 }])).toBeUndefined();
    expect(inferPrunedEventIdRange([flowStart])).toBeUndefined();
  });

  it('does not infer pruning from the gap a Time Travel fork leaves', () => {
    const fork: FlowHistoryEvent = {
      eventId: 4,
      eventTime: '2026-10-24T17:00:05Z',
      type: 'TimeTravelFork',
      payload: { previousRunAttempt: 1, targetRunAttempt: 1, targetEventId: '1' },
      runAttempt: 2,
    };
    expect(inferPrunedEventIdRange([flowStart, fork])).toBeUndefined();
    const laterAttempt = { ...stepEvent(9, 'StepWaitForCompleted', { stepExecutionId: 'Step-1' }), runAttempt: 2 };
    expect(inferPrunedEventIdRange([flowStart, laterAttempt])).toBeUndefined();
  });

  it('does not infer pruning on Servers without run attempts', () => {
    const legacyStart = { ...flowStart, runAttempt: 0 };
    const legacyNext = { ...stepEvent(7, 'StepWaitForCompleted', { stepExecutionId: 'Step-1' }), runAttempt: 0 };
    expect(inferPrunedEventIdRange([legacyStart, legacyNext])).toBeUndefined();
  });
});
