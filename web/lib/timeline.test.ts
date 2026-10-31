// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { sequentialStepHistory, stepEvent } from './historyTestServer';
import {
  buildSelectedTimelineLinks,
  buildSelectedTimelineRelations,
  buildTimelineLinks,
  buildTimelineStepLinks,
  formatElapsedDuration,
  newestTimelineEvents,
  timelineRelatedEventLabel,
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

describe('selected event relations across pages', () => {
  const newestPage = { hasOlder: true, hasNewer: false };
  const review = { stepExecutionId: 'Review-3', fromStepExecutionId: 'Intake-1', stepType: 'Review' };

  function executeAfterWaitFor(context: Record<string, unknown>): FlowHistoryEvent {
    return {
      ...stepEvent(1234, 'StepExecuteCompleted', { ...review, startedTime: '2026-10-24T19:05:00Z', ...context }),
      payload: {
        context: { ...review, startedTime: '2026-10-24T19:05:00Z', ...context },
        input: { conditionResults: {} },
      },
    };
  }

  it('draws a loaded condition-wait link and lists it as loaded', () => {
    const waitFor = stepEvent(10, 'StepWaitForCompleted', review);
    const execute = { ...executeAfterWaitFor({}), eventId: 11 };
    const relations = buildSelectedTimelineRelations([waitFor, execute], execute, newestPage);

    expect(relations.links.map((link) => [link.kind, link.fromEventId, link.toEventId])).toEqual([['condition-wait', 10, 11]]);
    expect(relations.stubs).toEqual([]);
    expect(relations.related.map((entry) => [entry.relation, entry.location, entry.eventId])).toEqual([['wait-for', 'loaded', 10]]);
  });

  it('labels a WaitFor on another page by its reported event ID and wait time', () => {
    const execute = executeAfterWaitFor({ waitForCompletedEventId: '1001', waitForCompletedTime: '2026-10-24T17:00:00Z' });
    const relations = buildSelectedTimelineRelations([execute], execute, newestPage);

    expect(relations.stubs).toHaveLength(1);
    expect(timelineRelatedEventLabel(relations.stubs[0]))
      .toBe('from Review-3 WaitFor completed, event #1001 (older page), waited 2h 5m');
  });

  it('falls back to an unknown far end without the reported event ID', () => {
    const execute = executeAfterWaitFor({});
    const relations = buildSelectedTimelineRelations([execute], execute, newestPage);

    expect(relations.stubs.map((stub) => [stub.relation, stub.location])).toEqual([['wait-for', 'unknown']]);
    expect(timelineRelatedEventLabel(relations.stubs[0])).toBe('from Review-3 WaitFor completed (event not on this page)');
  });

  it('does not invent a WaitFor for an Execute that skipped it', () => {
    const execute = stepEvent(20, 'StepExecuteCompleted', review);
    const relations = buildSelectedTimelineRelations([execute], execute, newestPage);

    expect(relations.related.map((entry) => entry.relation)).toEqual(['source']);
  });

  it('marks a far end inside the pruned range as pruned', () => {
    const execute = executeAfterWaitFor({ waitForCompletedEventId: 900 });
    const relations = buildSelectedTimelineRelations([execute], execute, {
      ...newestPage,
      prunedEventIdRange: { firstEventId: 2, lastEventId: 1000 },
    });

    expect(relations.stubs[0].location).toBe('pruned');
    expect(timelineRelatedEventLabel(relations.stubs[0]))
      .toBe('from Review-3 WaitFor completed (event #900, removed by the history window)');
  });

  it('marks a missing older event on the oldest retained page as pruned', () => {
    const flowStart: FlowHistoryEvent = { eventId: 1, eventTime: null, type: 'FlowStartedOrContinued', payload: {}, runAttempt: 1 };
    const execute = executeAfterWaitFor({});
    const relations = buildSelectedTimelineRelations([flowStart, execute], execute, {
      hasOlder: false,
      hasNewer: true,
      prunedEventIdRange: { firstEventId: 2, lastEventId: 1233 },
    });

    expect(relations.stubs.map((stub) => [stub.relation, stub.location])).toEqual([['wait-for', 'pruned']]);
  });

  it('points a Step started by Flow start at event 1 on another page', () => {
    const waitFor = stepEvent(2001, 'StepWaitForCompleted', { stepExecutionId: 'Step-1', fromStepExecutionId: '__start__', stepType: 'Step' });
    const relations = buildSelectedTimelineRelations([waitFor], waitFor, { hasOlder: true, hasNewer: true });

    expect(relations.related.map((entry) => [entry.relation, entry.location, entry.eventId])).toEqual([
      ['source', 'other-page', 1],
      ['execute', 'unknown', undefined],
    ]);
  });

  it('uses the reported deciding event ID for a source on another page', () => {
    const waitFor = stepEvent(2001, 'StepWaitForCompleted', { ...review, fromEventId: '77' });
    const relations = buildSelectedTimelineRelations([waitFor], waitFor, newestPage);

    expect(timelineRelatedEventLabel(relations.stubs[0])).toBe('from Intake-1 decision, event #77 (older page)');
  });

  it('lists started executions on the page and on a newer page', () => {
    const decision: FlowHistoryEvent = {
      ...stepEvent(50, 'StepExecuteCompleted', { stepExecutionId: 'Intake-1', stepType: 'Intake' }),
      payload: {
        context: { stepExecutionId: 'Intake-1', stepType: 'Intake' },
        output: { stepDecision: { nextSteps: [{ stepType: 'Review' }, { stepType: 'Notify' }] } },
      },
    };
    const started = stepEvent(51, 'StepWaitForCompleted', review);
    const relations = buildSelectedTimelineRelations([decision, started], decision, { hasOlder: true, hasNewer: true });

    expect(relations.related.filter((entry) => entry.relation === 'started').map((entry) => [entry.description, entry.location]))
      .toEqual([['Review-3 first event', 'loaded'], ['Notify execution', 'unknown']]);
  });

  it('builds every link of a 20,000-event page in O(n log n)', () => {
    const events = sequentialStepHistory(20_000);
    const startedAt = performance.now();
    const links = buildTimelineLinks(events);
    const relations = buildSelectedTimelineRelations(events, events[10_000], { hasOlder: false, hasNewer: false });
    const elapsedMs = performance.now() - startedAt;

    expect(links).toHaveLength(19_999);
    expect(relations.related.map((entry) => entry.relation)).toEqual(['wait-for', 'started']);
    expect(elapsedMs).toBeLessThan(1000);
  });
});
