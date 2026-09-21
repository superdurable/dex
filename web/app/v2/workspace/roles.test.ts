// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { roleActionLabels, roleFilter, rolesOf } from './roles';

const action = (
  rpcName: string,
  role: string | undefined,
  attributeKey: string,
  values: unknown[],
  operator = 'in',
) => ({
  rpcName,
  label: rpcName,
  role,
  condition: { attributeKey, operator, values },
  input: { kind: 'none', fields: [] },
}) as unknown as FlowV2Definition['actions'][number];

const definition = (actions: FlowV2Definition['actions']) => ({
  indexedAttributes: [],
  summary: { rpcName: 'GetDexSummary', fields: [] },
  display: { rpcName: 'GetDexDisplay', fields: [] },
  actions,
}) as FlowV2Definition;

/** The refund Flow: two parties, two Actions each, both testing case-status. */
const refund = definition([
  action('ApproveRefund', 'manager', 'case-status', ['awaiting-manager-rule', 'awaiting-manager-agent']),
  action('RejectRefund', 'manager', 'case-status', ['awaiting-manager-rule', 'awaiting-manager-agent']),
  action('ConfirmCustomerMessage', 'support-agent', 'case-status', ['awaiting-message-approval']),
  action('EditCustomerMessage', 'support-agent', 'case-status', ['awaiting-message-approval']),
]);

describe('rolesOf', () => {
  it('lists each party once, in declaration order', () => {
    expect(rolesOf(refund)).toEqual(['manager', 'support-agent']);
  });

  it('is empty for a Flow that names no parties', () => {
    expect(rolesOf(definition([action('A', undefined, 'case-status', ['open'])]))).toEqual([]);
    expect(rolesOf(undefined)).toEqual([]);
  });
});

describe('roleFilter', () => {
  it('unions the conditions of every Action the role answers, without repeats', () => {
    expect(roleFilter(refund, 'manager')).toMatchObject({
      field: 'case-status',
      operator: 'in',
      value: 'awaiting-manager-rule,awaiting-manager-agent',
    });
  });

  it('narrows a role with one state to that state', () => {
    expect(roleFilter(refund, 'support-agent')).toMatchObject({
      field: 'case-status',
      operator: 'in',
      value: 'awaiting-message-approval',
    });
  });

  it('asks for nothing when no role is chosen', () => {
    expect(roleFilter(refund, '')).toBeNull();
  });

  it('asks for nothing for a role the Flow does not name', () => {
    expect(roleFilter(refund, 'auditor')).toBeNull();
  });

  /**
   * Filters are ANDed, so two Attributes would need an OR the search API does not have. An
   * unnarrowed list is the honest answer; a filter on one of the two would hide real work.
   */
  it('refuses when the role answers Actions testing different Attributes', () => {
    const split = definition([
      action('A', 'manager', 'case-status', ['awaiting-manager']),
      action('B', 'manager', 'billing-outcome', ['declined']),
    ]);
    expect(roleFilter(split, 'manager')).toBeNull();
  });

  it('refuses a value holding a comma, which the filter layer would split', () => {
    const comma = definition([action('A', 'manager', 'case-status', ['awaiting, then approve'])]);
    expect(roleFilter(comma, 'manager')).toBeNull();
  });

  it('renders non-string condition values as text', () => {
    const numeric = definition([action('A', 'manager', 'refund-amount', [450, 900])]);
    expect(roleFilter(numeric, 'manager')?.value).toBe('450,900');
  });
});

describe('roleActionLabels', () => {
  it('names what choosing a role means', () => {
    expect(roleActionLabels(refund, 'manager')).toEqual(['ApproveRefund', 'RejectRefund']);
    expect(roleActionLabels(refund, 'auditor')).toEqual([]);
  });
});
