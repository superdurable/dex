// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { cloneForkSourceOf, suggestedCloneForkFlowId } from './cloneFork';

describe('cloneForkSourceOf', () => {
  it('reads the source point, including an int64 event ID written as a string', () => {
    expect(cloneForkSourceOf({
      eventId: 1,
      eventTime: null,
      type: 'FlowStartedOrContinued',
      payload: { cloneForkSource: { flowId: 'order-1', runId: 'run-1', runAttempt: 2, eventId: '12' } },
    })).toEqual({ flowId: 'order-1', runId: 'run-1', runAttempt: 2, eventId: '12' });
    expect(cloneForkSourceOf({
      eventId: 1,
      eventTime: null,
      type: 'FlowStartedOrContinued',
      payload: { cloneForkSource: { flowId: 'order-1', eventId: '9007199254740993' } },
    })?.eventId).toBe('9007199254740993');
  });

  it('returns null for a run that did not start from a clone fork', () => {
    expect(cloneForkSourceOf(null)).toBeNull();
    expect(cloneForkSourceOf({ eventId: 1, eventTime: null, type: 'FlowStartedOrContinued', payload: {} })).toBeNull();
    expect(cloneForkSourceOf({
      eventId: 2, eventTime: null, type: 'StepExecuteCompleted', payload: { cloneForkSource: { flowId: 'order-1' } },
    })).toBeNull();
  });
});

describe('suggestedCloneForkFlowId', () => {
  it('appends a fork suffix with the UTC time to the second', () => {
    expect(suggestedCloneForkFlowId('order-1', new Date('2026-11-15T08:09:10.123Z'))).toBe('order-1-fork-20261115-080910');
  });
});
