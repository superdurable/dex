// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowHistoryEvent } from '@/lib/types';
import {
  attemptCountFromRecord,
  mergeStepExecutions,
  overlayFromHistory,
  payloadFromRecord,
  previousRunIdFromEvents,
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
