// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { afterEach, describe, expect, it, vi } from 'vitest';
import {
  historyTestServer,
  historyTestServerResponse,
  sequentialStepHistory,
  stepEvent,
} from '@/lib/historyTestServer';
import type { FlowHistoryEvent } from '@/lib/types';
import {
  attemptCountFromRecord,
  isWindowSlideContiguous,
  loadRecentRunHistory,
  mergeStepExecutions,
  olderRecordsAfterWindowSlide,
  prependStepRecords,
  overlayFromHistory,
  payloadFromRecord,
  previousRunIdFromEvents,
  recentRunEvents,
  recordForExecution,
} from './overlayFromHistory';

function event(
  type: FlowHistoryEvent['type'],
  payload: Record<string, unknown>,
  eventTime = '2026-09-18T00:00:00.000Z',
): FlowHistoryEvent {
  return { eventId: 1, eventTime, type, payload };
}

describe('overlayFromHistory', () => {
  it('rolls wait and execute events onto one StepExecution', () => {
    const bundle = overlayFromHistory({
      flowId: 'flow-1',
      runId: 'run-2',
      status: 'Running',
      now: Date.parse('2026-09-18T00:01:00.000Z'),
      events: [
        event('FlowStartedOrContinued', { continuedStart: { previousRunId: 'run-1' } }),
        event('StepWaitForCompleted', {
          input: { stepInput: { ticket: 'A' } },
          output: { waitForCondition: { channelConditions: [{ channelName: 'refunds', satisfied: true }] } },
          context: {
            stepExecutionId: 'exec-2',
            stepType: 'RefundStep',
            startedTime: '2026-09-18T00:00:10.000Z',
          },
        }),
        event('StepExecuteCompleted', {
          input: { stepInput: { ticket: 'A' } },
          output: { stepDecision: { nextSteps: [{ stepType: 'NotifyStep' }] } },
          context: {
            stepExecutionId: 'exec-2',
            stepType: 'RefundStep',
            startedTime: '2026-09-18T00:00:10.000Z',
          },
        }),
      ],
    });

    expect(bundle.previousRunId).toBe('run-1');
    expect(bundle.overlay.simulated).toBe(false);
    expect(bundle.overlay.executions).toHaveLength(1);
    const execution = bundle.overlay.executions[0];
    expect(execution.stepType).toBe('RefundStep');
    expect(execution.waitFor?.status).toBe('completed');
    expect(execution.waitFor?.conditions).toEqual([
      { kind: 'channel', label: 'refunds', satisfied: true },
    ]);
    expect(execution.execute.status).toBe('completed');
    expect(execution.decisionType).toBe('goTo');
    expect(execution.nextStepTypes).toEqual(['NotifyStep']);
    expect(payloadFromRecord(bundle.records[0])).toEqual({
      input: { stepInput: { ticket: 'A' } },
      output: { stepDecision: { nextSteps: [{ stepType: 'NotifyStep' }] } },
      context: {
        stepExecutionId: 'exec-2',
        stepType: 'RefundStep',
        startedTime: '2026-09-18T00:00:10.000Z',
      },
    });
  });

  it('names close decisions from closeDecisionType', () => {
    const bundle = overlayFromHistory({
      flowId: 'flow-1',
      runId: 'run-2',
      status: 'Completed',
      events: [
        event('StepExecuteCompleted', {
          output: { stepDecision: { closeDecision: { closeDecisionType: 3 } } },
          context: { stepExecutionId: 'exec-close', stepType: 'CloseCaseStep' },
        }),
      ],
    });
    expect(bundle.overlay.executions[0].decisionType).toBe('forceComplete');
  });

  it('returns null payload when a record has no wait or execute event', () => {
    expect(payloadFromRecord(undefined)).toBeNull();
    expect(payloadFromRecord({
      runId: 'run-2',
      execution: {
        stepExecutionId: 'exec-empty',
        stepType: 'ReceiveRequestStep',
        ordinal: 1,
        waitFor: null,
        execute: { status: 'notStarted' },
        attempts: 1,
      },
    })).toBeNull();
  });

  it('counts attempts from finalAttempt and lastFailureInfo like v1', () => {
    const bundle = overlayFromHistory({
      flowId: 'flow-1',
      runId: 'run-2',
      status: 'Running',
      events: [
        event('StepExecuteFailed', {
          output: { failure: { attempt: 3, backendError: 'Unavailable' } },
          context: {
            stepExecutionId: 'exec-3',
            stepType: 'ReceiveRequestStep',
            finalAttempt: 3,
            lastFailureInfo: { attempt: 2 },
          },
        }),
      ],
    });
    expect(bundle.overlay.executions[0].attempts).toBe(3);
    expect(attemptCountFromRecord(bundle.records[0])).toBe(3);
    expect(payloadFromRecord(bundle.records[0])).not.toBeNull();
  });

  it('keeps current-run executions first and prepends only the requested step', () => {
    const current = overlayFromHistory({
      flowId: 'flow-1',
      runId: 'run-2',
      status: 'Running',
      events: [
        event('FlowStartedOrContinued', { continuedStart: { previousRunId: 'run-1' } }),
        event('StepExecuteCompleted', {
          input: { stepInput: 'now' },
          output: { stepDecision: { nextSteps: [{ stepType: 'NotifyStep' }] } },
          context: { stepExecutionId: 'exec-now', stepType: 'RefundStep' },
        }),
        event('StepExecuteCompleted', {
          output: { stepDecision: { nextSteps: [{ stepType: 'CloseCaseStep' }] } },
          context: { stepExecutionId: 'exec-other', stepType: 'NotifyStep' },
        }),
      ],
    });
    const previous = overlayFromHistory({
      flowId: 'flow-1',
      runId: 'run-1',
      status: 'Continued as new',
      events: [
        event('FlowStartedOrContinued', { continuedStart: { previousRunId: 'run-0' } }),
        event('StepExecuteCompleted', {
          input: { stepInput: 'then' },
          output: { stepDecision: { nextSteps: [{ stepType: 'NotifyStep' }] } },
          context: { stepExecutionId: 'exec-then', stepType: 'RefundStep' },
        }),
        event('StepExecuteCompleted', {
          output: { stepDecision: { nextSteps: [{ stepType: 'CloseCaseStep' }] } },
          context: { stepExecutionId: 'exec-old-notify', stepType: 'NotifyStep' },
        }),
      ],
    });

    expect(previousRunIdFromEvents([
      event('FlowStartedOrContinued', { continuedStart: { previousRunId: 'run-1' } }),
    ])).toBe('run-1');
    const merged = mergeStepExecutions(current, previous, 'RefundStep');
    expect(merged.previousRunId).toBe('run-0');
    expect(merged.overlay.executions.filter((item) => item.stepType === 'RefundStep').map((item) => item.stepExecutionId))
      .toEqual(['exec-then', 'exec-now']);
    expect(merged.overlay.executions.filter((item) => item.stepType === 'NotifyStep')).toHaveLength(1);
    expect(merged.overlay.executions.find((item) => item.stepExecutionId === 'exec-old-notify')).toBeUndefined();
    expect(recordForExecution(merged, 'RefundStep', null)?.execution.stepExecutionId).toBe('exec-now');
  });
});

describe('loadRecentRunHistory', () => {
  afterEach(() => vi.unstubAllGlobals());

  it('reads FlowStart and the newest window, then appends only new events on the next poll', async () => {
    const server = historyTestServer(sequentialStepHistory(20_000));
    vi.stubGlobal('fetch', vi.fn(async (input: string) => {
      const url = new URL(input, 'http://dex.test');
      if (url.pathname === '/api/flows/history') {
        return new Response(JSON.stringify(historyTestServerResponse(server, url)));
      }
      if (url.pathname === '/api/flows/wait') {
        return new Response(JSON.stringify({ eventAvailable: true, availableInternalEventId: String(server.latestEventId()) }));
      }
      throw new Error(`unexpected request ${input}`);
    }));

    const first = await loadRecentRunHistory('flow-1', 'run-1', 1);
    expect(recentRunEvents(first).map((entry) => entry.eventId).slice(0, 2)).toEqual([1, 19_001]);
    expect(recentRunEvents(first)).toHaveLength(1001);
    expect(server.requests.map((request) => request.startInternalEventId)).toEqual([1, 19_001]);

    server.requests.length = 0;
    server.events.push(stepEvent(20_001, 'StepExecuteCompleted', { stepExecutionId: 'Step-10000' }));
    const next = await loadRecentRunHistory('flow-1', 'run-1', 1, first);
    expect(next.historyWindow.events.at(-1)?.eventId).toBe(20_001);
    expect(server.requests.map((request) => request.startInternalEventId)).toEqual([20_001]);
  });
});

describe('overlay of a partial history window', () => {
  const executeAfterEarlierWaitFor: FlowHistoryEvent = {
    eventId: 5001,
    eventTime: '2026-09-18T00:00:09.000Z',
    type: 'StepExecuteCompleted',
    payload: {
      context: { stepExecutionId: 'Review-1', stepType: 'Review', startedTime: '2026-09-18T00:00:08.000Z' },
      input: { conditionResults: {} },
    },
  };

  it('marks the overlay partial and shows an edge WaitFor as earlier, not loaded', () => {
    const bundle = overlayFromHistory({
      flowId: 'flow-1',
      runId: 'run-1',
      status: 'Running',
      events: [executeAfterEarlierWaitFor],
      loadedFromEventId: 5001,
    });

    expect(bundle.overlay.hasEarlierExecutions).toBe(true);
    expect(bundle.loadedFromEventId).toBe(5001);
    expect(bundle.overlay.executions[0]).toMatchObject({
      waitFor: { status: 'completed', conditions: [] },
      isWaitForNotLoaded: true,
    });
    const complete = overlayFromHistory({ flowId: 'flow-1', runId: 'run-1', status: 'Running', events: [executeAfterEarlierWaitFor] });
    expect(complete.overlay.hasEarlierExecutions).toBe(false);
    expect(complete.overlay.executions[0].waitFor).toBeNull();
  });

  it('merges an execution that straddles the window edge when older events load', () => {
    const current = overlayFromHistory({
      flowId: 'flow-1',
      runId: 'run-1',
      status: 'Running',
      events: [executeAfterEarlierWaitFor],
      loadedFromEventId: 5001,
    });
    const earlier = overlayFromHistory({
      flowId: 'flow-1',
      runId: 'run-1',
      status: 'Running',
      events: [{
        eventId: 4000,
        eventTime: '2026-09-18T00:00:01.000Z',
        type: 'StepWaitForCompleted',
        payload: { context: { stepExecutionId: 'Review-1', stepType: 'Review' } },
      }],
      loadedFromEventId: 4000,
    });

    const merged = prependStepRecords(current, earlier.records, 'Review');

    expect(merged.records).toHaveLength(1);
    expect(merged.records[0].waitEvent?.eventId).toBe(4000);
    expect(merged.records[0].executeEvent?.eventId).toBe(5001);
    expect(merged.records[0].execution.isWaitForNotLoaded).toBe(false);
    expect(current.records[0].waitEvent).toBeUndefined();
  });
});

describe('older records while Live slides the window', () => {
  function stepMethodEvent(
    eventId: number,
    type: FlowHistoryEvent['type'],
    stepExecutionId: string,
  ): FlowHistoryEvent {
    return {
      eventId,
      eventTime: '2026-09-18T00:00:00.000Z',
      type,
      payload: {
        context: { stepExecutionId, stepType: 'Review' },
        ...(type.startsWith('StepExecute') ? { input: { conditionResults: {} } } : {}),
      },
    };
  }

  function windowBundle(loadedFromEventId: number, events: FlowHistoryEvent[]) {
    return overlayFromHistory({ flowId: 'flow-1', runId: 'run-1', status: 'Running', events, loadedFromEventId });
  }

  it('keeps executions that slide out between the older records and the window', () => {
    const olderWindow = windowBundle(900, [
      stepMethodEvent(900, 'StepWaitForCompleted', 'Review-0'),
      stepMethodEvent(901, 'StepExecuteCompleted', 'Review-0'),
    ]);
    const previous = windowBundle(1001, [
      stepMethodEvent(1001, 'StepWaitForCompleted', 'Review-1'),
      stepMethodEvent(1002, 'StepExecuteCompleted', 'Review-1'),
      stepMethodEvent(1200, 'StepWaitForCompleted', 'Review-2'),
      stepMethodEvent(1400, 'StepExecuteCompleted', 'Review-2'),
    ]);
    const next = windowBundle(1300, [
      stepMethodEvent(1400, 'StepExecuteCompleted', 'Review-2'),
      stepMethodEvent(1500, 'StepWaitForCompleted', 'Review-3'),
    ]);
    expect(isWindowSlideContiguous(previous, next)).toBe(true);

    const older = olderRecordsAfterWindowSlide({ Review: olderWindow.records }, previous, next);
    const shown = prependStepRecords(next, older.Review, 'Review');

    expect(shown.records.map((record) => record.execution.stepExecutionId))
      .toEqual(['Review-0', 'Review-1', 'Review-2', 'Review-3']);
    expect(shown.records[2].waitEvent?.eventId).toBe(1200);
    expect(shown.records[2].executeEvent?.eventId).toBe(1400);
  });

  it('reports a gap when the window reloads past the previous one', () => {
    const previous = windowBundle(1001, [stepMethodEvent(1001, 'StepWaitForCompleted', 'Review-1')]);
    const reloaded = windowBundle(5001, [stepMethodEvent(5001, 'StepWaitForCompleted', 'Review-9')]);
    const otherRun = { ...reloaded, loadedFromEventId: 1001, overlay: { ...reloaded.overlay, runId: 'run-2' } };

    expect(isWindowSlideContiguous(previous, reloaded)).toBe(false);
    expect(isWindowSlideContiguous(previous, otherRun)).toBe(false);
  });

  it('shows an active Step whose WaitFor completed before the window as earlier, not loaded', () => {
    const active = {
      stepExecutionId: 'Review-1',
      fromStepExecutionId: '__start__',
      stepType: 'Review',
      phase: 'Active' as const,
      completedConditions: { completedTimerConditions: { 0: 2 } },
      stepExecutionLocals: [],
      timers: [],
    };
    const partial = overlayFromHistory({
      flowId: 'flow-1',
      runId: 'run-1',
      status: 'Running',
      events: [],
      activeSteps: [active],
      loadedFromEventId: 5001,
    });
    expect(partial.overlay.executions[0]).toMatchObject({
      waitFor: { status: 'completed', conditions: [] },
      isWaitForNotLoaded: true,
      execute: { status: 'running' },
    });

    const withoutWaitFor = overlayFromHistory({
      flowId: 'flow-1',
      runId: 'run-1',
      status: 'Running',
      events: [],
      activeSteps: [{ ...active, completedConditions: undefined }],
      loadedFromEventId: 5001,
    });
    expect(withoutWaitFor.overlay.executions[0].waitFor).toBeNull();
    const complete = overlayFromHistory({ flowId: 'flow-1', runId: 'run-1', status: 'Running', events: [], activeSteps: [active] });
    expect(complete.overlay.executions[0].waitFor).toBeNull();
  });
});

