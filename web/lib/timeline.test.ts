// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { describe, expect, it } from 'vitest';
import {
  buildSelectedTimelineLinks,
  buildTimelineLinks,
  buildTimelineStepLinks,
  displayEventNumber,
  formatElapsedDuration,
  newestTimelineEvents,
} from './timeline';
import type { FlowHistoryEvent } from './types';

function event(
  eventId: number,
  type: FlowHistoryEvent['type'],
  stepExecutionId = '',
  eventTime: string | null = null,
  startedTime: string | null = null,
): FlowHistoryEvent {
  return {
    eventId,
    eventTime,
    type,
    payload: stepExecutionId ? { context: { stepExecutionId, startedTime } } : {},
  };
}

describe('timeline', () => {
  it('shows newest events first without mutating history order', () => {
    const history = [event(1, 'FlowStartedOrContinued'), event(8, 'FlowClosed'), event(4, 'ChannelExternalPublish')];
    expect(newestTimelineEvents(history).map((entry) => entry.eventId)).toEqual([8, 4, 1]);
    expect(history.map((entry) => entry.eventId)).toEqual([1, 8, 4]);
  });

  it('numbers Dex events contiguously instead of displaying backend IDs', () => {
    const history = [
      event(1, 'FlowStartedOrContinued'),
      event(8, 'StepWaitForCompleted'),
      event(14, 'StepExecuteCompleted'),
      event(16, 'FlowClosed'),
    ];
    expect(history.map((entry) => displayEventNumber(history, entry))).toEqual([1, 2, 3, 4]);
  });

  it('pairs each WaitFor start with the following Execute result for the same execution', () => {
    const links = buildTimelineStepLinks([
      event(40, 'StepExecuteCompleted', 'B-1'),
      event(10, 'StepWaitForCompleted', 'A-1'),
      event(30, 'StepWaitForCompleted', 'B-1'),
      event(20, 'StepExecuteFailed', 'A-1'),
    ]);
    expect(links).toEqual([
      { stepExecutionId: 'A-1', waitForEventId: 10, executeEventId: 20, conditionWaitDurationMs: null, lane: 0 },
      { stepExecutionId: 'B-1', waitForEventId: 30, executeEventId: 40, conditionWaitDurationMs: null, lane: 0 },
    ]);
  });

  it('separates overlapping step executions and reuses lanes afterward', () => {
    const links = buildTimelineStepLinks([
      event(9, 'StepWaitForCompleted', 'A-1'),
      event(10, 'StepWaitForCompleted', 'B-1'),
      event(11, 'StepWaitForCompleted', 'C-1'),
      event(22, 'StepExecuteCompleted', 'A-1'),
      event(28, 'StepExecuteCompleted', 'B-1'),
      event(30, 'StepExecuteCompleted', 'C-1'),
      event(40, 'StepWaitForCompleted', 'D-1'),
      event(50, 'StepExecuteCompleted', 'D-1'),
    ]);
    expect(links.map(({ stepExecutionId, lane }) => ({ stepExecutionId, lane }))).toEqual([
      { stepExecutionId: 'A-1', lane: 0 },
      { stepExecutionId: 'B-1', lane: 1 },
      { stepExecutionId: 'C-1', lane: 2 },
      { stepExecutionId: 'D-1', lane: 0 },
    ]);
  });

  it('measures condition wait until Execute starts', () => {
    const links = buildTimelineStepLinks([
      event(10, 'StepWaitForCompleted', 'A-1', '2026-08-03T20:00:00.000Z'),
      event(20, 'StepExecuteCompleted', 'A-1', '2026-08-03T20:00:09.000Z', '2026-08-03T20:00:07.250Z'),
    ]);
    expect(links[0].conditionWaitDurationMs).toBe(7250);
  });

  it('pairs WaitFor with an Execute left pending by flow closure', () => {
    const links = buildTimelineStepLinks([
      event(10, 'StepWaitForCompleted', 'A-1', '2026-08-03T20:00:00.000Z'),
      event(14, 'StepExecutePending', 'A-1', '2026-08-03T20:00:07.250Z', '2026-08-03T20:00:07.250Z'),
      event(15, 'FlowClosed', '', '2026-08-03T20:00:08.000Z'),
    ]);
    expect(links).toEqual([{
      stepExecutionId: 'A-1',
      waitForEventId: 10,
      executeEventId: 14,
      conditionWaitDurationMs: 7250,
      lane: 0,
    }]);
  });

  it('ignores WaitFor failures and unpaired Execute events', () => {
    expect(buildTimelineStepLinks([
      event(1, 'StepWaitForFailed', 'A-1'),
      event(2, 'StepExecuteCompleted', 'A-1'),
      event(3, 'StepExecuteCompleted', 'B-1'),
    ])).toEqual([]);
  });

  it('links the first Step event to Flow start and then to Execute', () => {
    const start = event(1, 'FlowStartedOrContinued');
    const waitFor = event(10, 'StepWaitForCompleted', 'A-1');
    waitFor.payload.context = {
      ...waitFor.payload.context as Record<string, unknown>,
      fromStepExecutionId: '__start__',
      stepType: 'A',
    };
    const links = buildTimelineLinks([
      start,
      waitFor,
      event(20, 'StepExecuteCompleted', 'A-1'),
    ]);

    expect(links).toEqual([
      {
        kind: 'lineage',
        stepExecutionId: 'A-1',
        fromEventId: 1,
        toEventId: 10,
        label: 'Flow start to A-1 first event',
        elapsedDurationMs: null,
        lane: 0,
      },
      {
        kind: 'condition-wait',
        stepExecutionId: 'A-1',
        fromEventId: 10,
        toEventId: 20,
        label: 'A-1: WaitForCondition started to Execute',
        elapsedDurationMs: null,
        lane: 0,
      },
    ]);
  });

  it('shows only links connected to the selected event', () => {
    const start = event(1, 'FlowStartedOrContinued');
    const source = event(7, 'StepExecuteCompleted', 'source-1');
    source.payload.context = {
      ...source.payload.context as Record<string, unknown>,
      fromStepExecutionId: '__start__',
      stepType: 'source',
    };
    source.payload.output = {
      stepDecision: { nextSteps: [{ stepType: 'terminal-failure' }] },
    };
    const waitFor = event(8, 'StepWaitForCompleted', 'terminal-failure-1');
    waitFor.payload.context = {
      ...waitFor.payload.context as Record<string, unknown>,
      fromStepExecutionId: 'source-1',
      stepType: 'terminal-failure',
    };
    const execute = event(9, 'StepExecuteFailed', 'terminal-failure-1');

    expect(buildSelectedTimelineLinks([start, source, waitFor, execute], undefined)).toEqual([]);
    expect(buildSelectedTimelineLinks([start, source, waitFor, execute], 1)).toEqual([]);
    expect(buildSelectedTimelineLinks([start, source, waitFor, execute], 8)).toEqual([
      {
        kind: 'lineage',
        stepExecutionId: 'terminal-failure-1',
        fromEventId: 7,
        toEventId: 8,
        label: 'source-1 decision to terminal-failure-1 first event',
        elapsedDurationMs: null,
        lane: 0,
      },
      {
        kind: 'condition-wait',
        stepExecutionId: 'terminal-failure-1',
        fromEventId: 8,
        toEventId: 9,
        label: 'terminal-failure-1: WaitForCondition started to Execute',
        elapsedDurationMs: null,
        lane: 0,
      },
    ]);
    expect(buildSelectedTimelineLinks([start, source, waitFor, execute], 9)).toEqual([{
      kind: 'condition-wait',
      stepExecutionId: 'terminal-failure-1',
      fromEventId: 8,
      toEventId: 9,
      label: 'terminal-failure-1: WaitForCondition started to Execute',
      elapsedDurationMs: null,
      lane: 0,
    }]);
  });

  it('links a Step decision to the first event of the scheduled execution', () => {
    const source = event(10, 'StepExecuteCompleted', 'authorize-1');
    source.payload.context = {
      ...source.payload.context as Record<string, unknown>,
      stepType: 'authorize',
    };
    source.payload.output = {
      stepDecision: { nextSteps: [{ stepType: 'charge' }] },
    };
    const target = event(20, 'StepExecuteFailed', 'charge-1');
    target.payload.context = {
      ...target.payload.context as Record<string, unknown>,
      fromStepExecutionId: 'authorize-1',
      stepType: 'charge',
    };

    expect(buildTimelineLinks([source, target])).toEqual([{
      kind: 'lineage',
      stepExecutionId: 'charge-1',
      fromEventId: 10,
      toEventId: 20,
      label: 'authorize-1 decision to charge-1 first event',
      elapsedDurationMs: null,
      lane: 0,
    }]);
  });

  it('links RPC and continued-run sources', () => {
    const rpc: FlowHistoryEvent = {
      eventId: 5,
      eventTime: null,
      type: 'RpcExecutionCompleted',
      payload: { rpcName: 'approve' },
    };
    const rpcStep = event(8, 'StepExecuteCompleted', 'approval-1');
    rpcStep.payload.context = {
      ...rpcStep.payload.context as Record<string, unknown>,
      fromStepExecutionId: '__rpc/approve',
      stepType: 'approval',
    };
    const continued: FlowHistoryEvent = {
      eventId: 20,
      eventTime: null,
      type: 'FlowStartedOrContinued',
      payload: { continuedStart: { previousRunId: 'previous-run' } },
    };
    const resumed = event(25, 'StepWaitForFailed', 'resumed-1');
    resumed.payload.context = {
      ...resumed.payload.context as Record<string, unknown>,
      fromStepExecutionId: 'previous-run-step-1',
      stepType: 'resumed',
    };

    expect(buildTimelineLinks([rpc, rpcStep, continued, resumed])
      .filter((link) => link.kind === 'lineage'))
      .toEqual([
        {
          kind: 'lineage',
          stepExecutionId: 'approval-1',
          fromEventId: 5,
          toEventId: 8,
          label: 'RPC approve to approval-1 first event',
          elapsedDurationMs: null,
          lane: 0,
        },
        {
          kind: 'lineage',
          stepExecutionId: 'resumed-1',
          fromEventId: 20,
          toEventId: 25,
          label: 'Flow continued to resumed-1 first event',
          elapsedDurationMs: null,
          lane: 0,
        },
      ]);
  });

  it('formats elapsed condition waits compactly', () => {
    expect(formatElapsedDuration(450)).toBe('450ms');
    expect(formatElapsedDuration(7250)).toBe('7.3s');
    expect(formatElapsedDuration(65_000)).toBe('1m 5s');
    expect(formatElapsedDuration(3_720_000)).toBe('1h 2m');
  });
});
