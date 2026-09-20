// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { QUEUE_COPY } from '../queue/copy';
import { describeFilters, type FilterRow } from './filters';

const definition: FlowV2Definition = {
  indexedAttributes: [{
    attributeKey: 'case-status', indexKey: 'case-status', indexType: 'keyword',
    valueType: 'string', description: 'Current case status',
  }],
  summary: { rpcName: 'GetDexSummary', fields: [] },
  display: { rpcName: 'GetDexDisplay', fields: [] },
  actions: [],
};

const row = (field: string, operator: string, value: string): FilterRow => ({
  id: `${field}-${operator}`, field, operator, value,
});

describe('describeFilters', () => {
  it('names the field by the description the contract supplies', () => {
    expect(describeFilters([row('case-status', 'eq', 'awaiting-manager-rule')], definition))
      .toEqual(['current case status is awaiting-manager-rule']);
  });

  it('describes the built-in fields the contract does not declare', () => {
    expect(describeFilters([row('executionStatus', 'eq', 'Running')], definition))
      .toEqual(['execution status is Running']);
  });

  it('ignores a filter with no value, because the server ignores it too', () => {
    expect(describeFilters([row('executionStatus', 'eq', '   ')], definition)).toEqual([]);
  });

  it('joins several filters the way the server ANDs them', () => {
    const clauses = describeFilters([
      row('executionStatus', 'eq', 'Running'),
      row('case-status', 'in', 'a,b'),
    ], definition);
    expect(QUEUE_COPY.scope(clauses))
      .toBe('Showing runs where execution status is Running and current case status is one of a,b.');
  });

  it('stops claiming a scope once the reader deletes every filter', () => {
    expect(QUEUE_COPY.scope(describeFilters([], definition)))
      .toBe('Showing every run of this Flow type, open or closed.');
  });

  it('falls back to the raw operator rather than inventing a phrase', () => {
    expect(describeFilters([row('case-status', 'weird', 'x')], definition))
      .toEqual(['current case status weird x']);
  });
});
