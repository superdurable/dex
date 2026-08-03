import { describe, expect, it } from 'vitest';
import { buildTimelineStepLinks, formatElapsedDuration, newestTimelineEvents } from './timeline';
import type { FlowHistoryEvent } from './types';

function event(
  eventId: number,
  type: FlowHistoryEvent['type'],
  stepExecutionId = '',
  eventTime: string | null = null,
  firstStartedTime: string | null = null,
): FlowHistoryEvent {
  return {
    eventId,
    eventTime,
    type,
    payload: stepExecutionId ? { execution: { stepExecutionId, firstStartedTime } } : {},
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
      { stepExecutionId: 'A-1', waitForEventId: 10, executeEventId: 20, conditionWaitDurationMs: null },
      { stepExecutionId: 'B-1', waitForEventId: 30, executeEventId: 40, conditionWaitDurationMs: null },
    ]);
  });

  it('measures condition wait until Execute starts', () => {
    const links = buildTimelineStepLinks([
      event(10, 'StepWaitForCompleted', 'A-1', '2026-08-03T20:00:00.000Z'),
      event(20, 'StepExecuteCompleted', 'A-1', '2026-08-03T20:00:09.000Z', '2026-08-03T20:00:07.250Z'),
    ]);
    expect(links[0].conditionWaitDurationMs).toBe(7250);
  });

  it('ignores WaitFor failures and unpaired Execute events', () => {
    expect(buildTimelineStepLinks([
      event(1, 'StepWaitForFailed', 'A-1'),
      event(2, 'StepExecuteCompleted', 'A-1'),
      event(3, 'StepExecuteCompleted', 'B-1'),
    ])).toEqual([]);
  });

  it('formats elapsed condition waits compactly', () => {
    expect(formatElapsedDuration(450)).toBe('450ms');
    expect(formatElapsedDuration(7250)).toBe('7.3s');
    expect(formatElapsedDuration(65_000)).toBe('1m 5s');
    expect(formatElapsedDuration(3_720_000)).toBe('1h 2m');
  });
});
