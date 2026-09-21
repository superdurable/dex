// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import type { V2Flow } from '@/lib/types';
import { leadFields, runRow, slotsOf } from './slots';

const field = (attributeKey: string, slot?: string) => ({
  attributeKey, valueType: 'string', editable: false, description: attributeKey, slot,
}) as FlowV2Definition['display']['fields'][number];

const definition = (fields: FlowV2Definition['display']['fields']) => ({
  indexedAttributes: [],
  summary: { rpcName: 'GetDexSummary', fields: [] },
  display: { rpcName: 'GetDexDisplay', fields },
  actions: [],
}) as FlowV2Definition;

/** The refund Flow's shape: title and status indexed, recommendation in Summary, subtitle neither. */
const refund = definition([
  field('customer-email', 'title'),
  field('case-status', 'status'),
  field('in-email', 'subtitle'),
  field('recommended-action', 'recommendation'),
  field('recommendation-rationale', 'reason'),
  field('guardrail-rule', 'reason'),
  field('operator-note'),
]);

const flow = (patch: Partial<V2Flow> = {}) => ({
  flowId: 'adv-1',
  flowType: 'AgenticCustomerRefundFlow',
  flowStatus: 'Running',
  flowStatusCode: 1,
  startTime: '2026-09-20T12:47:00Z',
  closeTime: null,
  indexedAttributes: { 'customer-email': 'eve@acme.co', 'case-status': 'awaiting-message-approval' },
  summary: { 'recommended-action': 'IssueRefund', 'guardrail-rule': 'manager-approval-required' },
  ...patch,
} as V2Flow);

describe('slotsOf', () => {
  it('groups declared fields by slot, in declaration order', () => {
    const slots = slotsOf(refund);
    expect(slots.title?.map((f) => f.attributeKey)).toEqual(['customer-email']);
    expect(slots.reason?.map((f) => f.attributeKey))
      .toEqual(['recommendation-rationale', 'guardrail-rule']);
  });

  it('ignores a field with no slot, so the detail list keeps it', () => {
    expect(Object.values(slotsOf(refund)).flat().map((f) => f?.attributeKey))
      .not.toContain('operator-note');
  });

  it('ignores a slot name the UI does not render rather than trusting the file', () => {
    expect(slotsOf(definition([field('x', 'headline')]))).toEqual({});
  });

  it('is empty for a Flow that declares nothing', () => {
    expect(slotsOf(undefined)).toEqual({});
    expect(slotsOf(definition([]))).toEqual({});
  });
});

describe('runRow', () => {
  it('names the run with what the Flow declared, not the Flow ID', () => {
    const row = runRow(flow(), slotsOf(refund));
    expect(row.title).toBe('eve@acme.co');
    expect(row.titleIsFlowID).toBe(false);
    expect(row.status).toBe('awaiting-message-approval');
  });

  it('falls back to the Flow ID and execution status when no slot is declared', () => {
    const row = runRow(flow(), slotsOf(definition([])));
    expect(row.title).toBe('adv-1');
    expect(row.titleIsFlowID).toBe(true);
    expect(row.status).toBe('Running');
  });

  it('falls back when a slot is declared but this run carries no value for it', () => {
    const row = runRow(flow({ indexedAttributes: {}, summary: {} }), slotsOf(refund));
    expect(row.title).toBe('adv-1');
    expect(row.status).toBe('Running');
  });

  it('treats an empty string as no value, not as a title', () => {
    const row = runRow(flow({ indexedAttributes: { 'customer-email': '' } }), slotsOf(refund));
    expect(row.title).toBe('adv-1');
  });

  it('reads a slot out of Summary when it is not indexed', () => {
    const summaryTitle = definition([field('recommended-action', 'title')]);
    expect(runRow(flow(), slotsOf(summaryTitle)).title).toBe('IssueRefund');
  });

  it('skips a Display-only slot rather than showing a blank', () => {
    // `subtitle` is in-email, which a row never carries. The drawer draws it instead.
    const row = runRow(flow(), slotsOf(refund));
    expect(row.title).not.toBe('');
    expect(Object.keys(row)).not.toContain('subtitle');
  });

  it('renders a number-valued slot as text', () => {
    const amount = definition([field('refund-amount', 'status')]);
    expect(runRow(flow({ indexedAttributes: { 'refund-amount': 17500 } }), slotsOf(amount)).status)
      .toBe('17500');
  });
});

describe('leadFields', () => {
  it('orders by slot, not by how the author happened to type them', () => {
    const shuffled = definition([
      field('guardrail-rule', 'reason'),
      field('case-status', 'status'),
      field('customer-email', 'title'),
      field('recommended-action', 'recommendation'),
      field('in-email', 'subtitle'),
    ]);
    expect(leadFields(shuffled).map((f) => f.attributeKey)).toEqual([
      'customer-email', 'in-email', 'case-status', 'recommended-action', 'guardrail-rule',
    ]);
  });

  it('keeps several reasons together, in declaration order', () => {
    expect(leadFields(refund).map((f) => f.attributeKey)).toEqual([
      'customer-email', 'in-email', 'case-status',
      'recommended-action', 'recommendation-rationale', 'guardrail-rule',
    ]);
  });

  it('leaves unslotted fields out, so the detail list still has them', () => {
    expect(leadFields(refund).map((f) => f.attributeKey)).not.toContain('operator-note');
  });

  it('is empty for a Flow that declares no slots', () => {
    expect(leadFields(definition([field('a'), field('b')]))).toEqual([]);
  });
});
