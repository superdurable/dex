// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { V2Flow } from '@/lib/types';
import type { AgentInterruptRecord } from '../agents/sessionSchema';
import { askedText, groupInboxRows, waitText, type AgentInboxRow, type AgentSessionRead } from './agentInbox';

type AgentInterrupt = AgentInterruptRecord;

const NOW = Date.parse('2026-11-07T12:00:00.000Z');

function interrupt(overrides: Partial<AgentInterrupt> = {}): AgentInterrupt {
  return {
    interruptId: 'call-1', kind: 'tool_approval', toolCallId: 'call-1', toolName: 'publish_note',
    arguments: '{"message": "Refund order 5521"}', createdAt: '2026-11-07T11:00:00.000Z', deadline: null,
    question: null, choices: [], allowsFreeText: true, ...overrides,
  };
}

function read(interrupts: AgentInterrupt[], isActive = true): AgentSessionRead {
  return { interrupts, isActive };
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
});
