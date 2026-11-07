// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowV2Action } from '@superdurable/flow-definition-renderer';
import type { V2Flow } from '@/lib/types';
import { AGENT_INBOX_COPY } from './agentCopy';
import { agentActionInput, agentActionNotice } from './agentActions';
import { askedText, groupInboxRows, waitText, type AgentInboxRow } from './agentInbox';
import { parseInterrupts, type AgentInterrupt, type AgentSessionRead } from './agentSession';

const NOW = Date.parse('2026-11-07T12:00:00.000Z');

function interrupt(overrides: Partial<AgentInterrupt> = {}): AgentInterrupt {
  return {
    interruptId: 'call-1', kind: 'tool_approval', toolCallId: 'call-1', toolName: 'publish_note',
    arguments: '{"message": "Refund order 5521"}', createdAt: '2026-11-07T11:00:00.000Z', deadline: null,
    question: null, choices: [], allowsFreeText: true, ...overrides,
  };
}

function read(interrupts: AgentInterrupt[], isActive = true): AgentSessionRead {
  return { interrupts, toolCalls: {}, lastMessage: null, isActive };
}

function row(flowId: string, flowType: string, interrupts: AgentInterrupt[] | null): AgentInboxRow {
  const flow = { flowId, flowType, flowStatus: 'Running', flowStatusCode: 1 } as V2Flow;
  return { flow, read: interrupts === null ? null : read(interrupts), readError: '' };
}

describe('agent inbox rows', () => {
  it('groups by Flow type and puts the earliest deadline first, then the longest wait', () => {
    const groups = groupInboxRows([
      row('ses-late', 'SupportAgent', [interrupt({ deadline: '2026-11-07T13:00:00.000Z' })]),
      row('ses-question', 'FinanceAgent', [interrupt({ kind: 'question', question: 'Which account?' })]),
      row('ses-unread', 'SupportAgent', null),
      row('ses-no-deadline', 'SupportAgent', [interrupt({ createdAt: '2026-11-07T08:00:00.000Z' })]),
      row('ses-soon', 'SupportAgent', [interrupt({ deadline: '2026-11-07T12:10:00.000Z' })]),
      row('ses-done', 'SupportAgent', []),
    ]);

    expect(groups.map((group) => [group.flowType, group.rows.map((entry) => entry.flow.flowId)])).toEqual([
      ['FinanceAgent', ['ses-question']],
      ['SupportAgent', ['ses-soon', 'ses-late', 'ses-no-deadline', 'ses-unread']],
    ]);
  });

  it('says what is asked and how long is left or how long it waited', () => {
    expect(askedText(interrupt())).toBe('publish_note · message: Refund order 5521');
    expect(askedText(interrupt({ kind: 'question', question: 'Which account?' }))).toBe('Which account?');
    expect(waitText(interrupt({ deadline: '2026-11-07T12:42:30.000Z' }), NOW)).toBe('42 min left');
    expect(waitText(interrupt({ deadline: '2026-11-07T11:59:00.000Z' }), NOW)).toBe('past its deadline');
    expect(waitText(interrupt({ createdAt: '2026-11-07T09:00:00.000Z' }), NOW)).toBe('waiting 3 h');
  });

  it('reads interrupts as dex-agents 0.1.0 and later wrote them', () => {
    const parsed = parseInterrupts([
      { interrupt_id: 'call-1', kind: 'tool_approval', tool_call_id: 'call-1', tool_name: 'send_email', arguments: '{}', created_at: '2026-11-02T09:30:00.000Z', deadline: null },
      { interrupt_id: 'call-7-input-3', kind: 'question', tool_call_id: 'call-7', tool_name: 'transfer_funds', arguments: '{}', created_at: '', deadline: '2026-11-02T09:45:30.125Z', question: 'Which currency?', choices: ['EUR', 'USD'], allows_free_text: false },
      { interrupt_id: 'x', kind: 'something_new' },
    ]);
    expect(parsed.map((entry) => [entry.interruptId, entry.kind, entry.allowsFreeText, entry.choices])).toEqual([
      ['call-1', 'tool_approval', true, []],
      ['call-7-input-3', 'question', false, ['EUR', 'USD']],
    ]);
  });
});

describe('agent answers', () => {
  const resolve: FlowV2Action = {
    rpcName: 'resolve_interrupt', label: 'Resolve interrupt', requiredPermission: 'agent.operate',
    input: { kind: 'object', fields: ['interrupt_id', 'approved', 'rejection_message'].map((fieldName) => ({
      fieldName, valueType: 'string', source: 'user', required: false, description: fieldName,
    })) },
  } as FlowV2Action;

  it('builds the input from the declared fields and refuses a graph that lacks one', () => {
    expect(agentActionInput(resolve, interrupt(), { rpc: 'resolve', approved: false, rejectionMessage: ' too much ' }))
      .toEqual({ interrupt_id: 'call-1', approved: false, rejection_message: 'too much' });
    expect(agentActionInput(resolve, interrupt(), { rpc: 'resolve', approved: true, rejectionMessage: 'ignored' }))
      .toEqual({ interrupt_id: 'call-1', approved: true, rejection_message: null });
    expect(agentActionInput(resolve, interrupt(), { rpc: 'answer', answer: 'red' })).toBeNull();
  });

  it('reads the outcome back from committed state', () => {
    const pending = interrupt({ deadline: '2026-11-07T12:30:00.000Z' });
    expect(agentActionNotice({ state: 'applied' }, pending, read([]), NOW)).toBeNull();
    expect(agentActionNotice({ state: 'not-applied' }, pending, read([]), NOW)).toEqual({
      title: AGENT_INBOX_COPY.alreadyHandled, text: AGENT_INBOX_COPY.alreadyHandledWhy,
    });
    expect(agentActionNotice({ state: 'not-applied' }, pending, read([]), Date.parse('2026-11-07T12:31:00.000Z'))?.text)
      .toBe(AGENT_INBOX_COPY.deadlinePassed);
    expect(agentActionNotice({ state: 'lock-conflict' }, pending, read([]), NOW)?.title).toBe(AGENT_INBOX_COPY.alreadyHandled);
    expect(agentActionNotice({ state: 'lock-conflict' }, pending, read([pending]), NOW)).toEqual({
      title: AGENT_INBOX_COPY.notApplied, text: AGENT_INBOX_COPY.stillWaitingAfterLock,
    });
    expect(agentActionNotice({ state: 'not-applied' }, pending, read([pending], false), NOW)?.text).toBe(AGENT_INBOX_COPY.ended);
  });
});
