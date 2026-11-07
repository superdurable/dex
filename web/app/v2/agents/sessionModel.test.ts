// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { ActiveStepExecution, FlowHistoryEvent, KeyValue } from '@/lib/types';
import { readSession, type ToolCallView } from './sessionModel';

const message = (sequence: number, role: string, extra: Record<string, unknown> = {}) => ({
  key: `AgentMessages/${String(sequence).padStart(12, '0')}`,
  value: {
    sequence, turn_id: 'turn-1', role, text: null, tool_call_id: null, tool_name: null,
    tool_arguments: null, tool_output: null, framework_item: '{}', created_at: '2026-11-07T22:46:48Z', ...extra,
  },
});

const toolCall = (id: string, status: string, extra: Record<string, unknown> = {}) => ({
  key: `AgentToolCalls/${id}`,
  value: {
    tool_call_id: id, turn_id: 'turn-1', tool_name: `tool_${id}`, arguments: '{"a":1}', effect: 'non_idempotent_write',
    requires_approval: true, status, output: null, error: null, rejection_message: null, deferral_order: 0, ...extra,
  },
});

const requestOf = (sequence: number, id: string): KeyValue => message(sequence, 'assistant', {
  tool_call_id: id, tool_name: `tool_${id}`, tool_arguments: '{"a":1}',
});

const state = (extra: Record<string, unknown> = {}): KeyValue => ({
  key: 'AgentState',
  value: { status: 'running_tools', turn_count: 0, active_turn_id: 'turn-1', accepts_turn_control: true, ...extra },
});

function activeToolStep(id: string, invocation: Record<string, unknown>, stepType = 'CallSingleAttemptTool'): ActiveStepExecution {
  return {
    stepExecutionId: `${stepType}-1`,
    fromStepExecutionId: 'RunModel-2',
    stepType,
    phase: 'Active',
    movement: { stepInput: { stringValue: id }, stepOptions: {} },
    stepExecutionLocals: [],
    timers: [],
    executeInvocation: invocation,
  };
}

function callsOf(attributes: KeyValue[], activeSteps: ActiveStepExecution[] = [], events: FlowHistoryEvent[] = []): ToolCallView[] {
  return readSession({ attributes, activeSteps, events }).items.flatMap((item) => item.kind === 'tool' ? [item.call] : []);
}

describe('readSession', () => {
  it('draws messages in order with each tool call where the agent made it', () => {
    const view = readSession({
      attributes: [
        state(),
        message(1, 'user', { text: 'refund order 5521' }),
        message(2, 'assistant', { text: 'Let me look.' }),
        requestOf(3, 'c1'),
        message(4, 'tool', { tool_call_id: 'c1', tool_name: 'tool_c1', tool_output: '{"ok":true}' }),
        message(5, 'assistant', { text: 'I will refund it.' }),
        requestOf(6, 'c2'),
        toolCall('c1', 'succeeded', { output: '{"ok":true}', requires_approval: false, effect: 'read' }),
        toolCall('c2', 'awaiting_approval'),
        {
          key: 'PendingInterrupts',
          value: [{ interrupt_id: 'c2', kind: 'tool_approval', tool_call_id: 'c2', tool_name: 'tool_c2', arguments: '{}', created_at: 'x' }],
        },
        { key: 'AgentUsage', value: { input_tokens: 20, output_tokens: 10, model_calls: 2 } },
      ],
      activeSteps: [],
      events: [],
    });
    expect(view.items.map((item) => item.kind === 'tool' ? `tool:${item.call.toolCallId}:${item.call.state}` : item.kind === 'message' ? item.message.text : item.label))
      .toEqual(['refund order 5521', 'Let me look.', 'tool:c1:done', 'I will refund it.', 'tool:c2:awaiting_approval']);
    expect(view.waitingCalls.map((call) => call.interrupt?.interruptId)).toEqual(['c2']);
    expect(view.usage?.inputTokens).toBe(20);
    expect(view.missing).toEqual([]);
  });

  it('says a call queued for a Worker has not run', () => {
    const [call] = callsOf([state(), requestOf(1, 'c1'), toolCall('c1', 'approved')], [
      activeToolStep('c1', { phase: 1, pendingReason: 1, attempt: 1, pendingSinceTime: '2026-11-07T22:00:00Z' }),
    ]);
    expect(call).toMatchObject({ state: 'queued', pendingReason: 'worker', attempt: 1, stepType: 'CallSingleAttemptTool' });
  });

  it('reads running and backoff with attempt n of m from the Step options or history', () => {
    const running = activeToolStep('c1', { phase: 'STEP_METHOD_INVOCATION_PHASE_RUNNING', attempt: 2 }, 'CallTool');
    const history: FlowHistoryEvent = {
      eventId: 9, eventTime: null, type: 'StepExecuteCompleted',
      payload: { context: { stepType: 'CallTool', methodOptions: { retryPolicy: { maximumAttempts: 3 } } } },
    };
    expect(callsOf([state(), requestOf(1, 'c1'), toolCall('c1', 'approved')], [running], [history])[0])
      .toMatchObject({ state: 'running', attempt: 2, maxAttempts: 3 });
    const backoff = activeToolStep('c1', { phase: 3, attempt: 3, backoffFiresAtTime: '2026-11-07T22:01:00Z' }, 'CallTool');
    backoff.movement = { stepInput: { stringValue: 'c1' }, stepOptions: { executeRetryPolicy: { maximumAttempts: 5 } } };
    expect(callsOf([state(), requestOf(1, 'c1'), toolCall('c1', 'approved')], [backoff])[0])
      .toMatchObject({ state: 'retrying', attempt: 3, maxAttempts: 5 });
  });

  it('does not call a crashed single-attempt write unrun while its outcome is recorded', () => {
    const recovery = activeToolStep('c1', { phase: 1, pendingReason: 1, attempt: 1 }, 'RecordToolOutcomeUnknown');
    const [call] = callsOf([state(), requestOf(1, 'c1'), toolCall('c1', 'approved')], [recovery]);
    expect(call).toMatchObject({ state: 'recording', stepType: 'RecordToolOutcomeUnknown' });
  });

  it('tells final states apart', () => {
    const calls = callsOf([
      state(),
      requestOf(1, 'ok'), toolCall('ok', 'succeeded'),
      requestOf(2, 'bad'), toolCall('bad', 'failed', { error: 'boom' }),
      requestOf(3, 'unk'), toolCall('unk', 'outcome_unknown'),
      requestOf(4, 'skip'), toolCall('skip', 'failed', { error: 'the tool did not run, so it is safe to call again: earlier write failed' }),
      requestOf(5, 'rej'), toolCall('rej', 'rejected', { rejection_message: 'no' }),
      requestOf(6, 'stop'), toolCall('stop', 'rejected', { cancellation_reason: 'turn_cancelled' }),
    ]);
    expect(calls.map((call) => call.state)).toEqual(['done', 'failed', 'outcome_unknown', 'skipped', 'rejected', 'cancelled']);
  });

  it('marks an approval whose interrupt is gone as a recorded decision, not a pending one', () => {
    const [call] = callsOf([state(), requestOf(1, 'c1'), toolCall('c1', 'awaiting_approval'), { key: 'PendingInterrupts', value: [] }]);
    expect(call.state).toBe('deciding');
    expect(call.interrupt).toBeUndefined();
  });

  it('reads a past call from Flow history inside its window', () => {
    const record = {
      tool_call_id: 'old', turn_id: 'turn-1', tool_name: 'tool_old', arguments: '{}', effect: 'idempotent_write',
      requires_approval: true, status: 'rejected', rejection_message: 'wait',
    };
    const payload = btoa(JSON.stringify(record));
    const event: FlowHistoryEvent = {
      eventId: 20, eventTime: null, type: 'StepExecuteCompleted',
      payload: {
        context: { stepExecutionId: 'AwaitToolApproval-1' },
        output: { upsertAttributes: [{ key: 'AgentToolCalls/old', value: { objValue: { encoding: 'json', payload } } }] },
      },
    };
    const [call] = callsOf([state({ active_turn_id: null }), requestOf(1, 'old')], [], [event]);
    expect(call).toMatchObject({ source: 'history', state: 'rejected', effect: 'idempotent_write', rejectionMessage: 'wait' });
  });

  it('falls back to message text for a call outside the history window and says so', () => {
    const view = readSession({
      attributes: [
        state({ active_turn_id: null }),
        requestOf(1, 'old'),
        message(2, 'tool', { tool_call_id: 'old', tool_name: 'tool_old', tool_output: 'done' }),
      ],
      activeSteps: [],
      events: [],
    });
    const [item] = view.items;
    expect(item.kind === 'tool' && item.call).toMatchObject({ source: 'messages', state: 'returned', effect: null, output: 'done' });
    expect(view.hasCallsFromMessagesOnly).toBe(true);
  });

  it('shows what could not be read instead of an empty conversation', () => {
    const view = readSession({
      attributes: [
        message(1, 'user', { text: 'hi' }),
        { key: 'AgentMessages/000000000002', value: { __dexStoredValueUnavailable: true } },
        { key: 'AgentToolCalls/c9', value: { nonsense: true } },
      ],
      activeSteps: [],
      events: [],
    });
    expect(view.missing).toEqual([
      'AgentState is not set',
      'AgentMessages/000000000002 could not be loaded',
      'AgentToolCalls/c9 does not match the session schema',
    ]);
    expect(view.items.map((item) => item.kind)).toEqual(['message', 'missing']);
  });
});
