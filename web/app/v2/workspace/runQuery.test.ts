// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import {
  EMPTY_RUN_QUERY,
  filterableAttributes,
  isEmptyQuery,
  keywordAttribute,
  statusOptions,
  toFilterRows,
} from './runQuery';

const indexed = (attributeKey: string, indexType: string, valueType: string) => ({
  attributeKey, indexKey: attributeKey, indexType, valueType, description: attributeKey,
}) as FlowV2Definition['indexedAttributes'][number];

const searchable = {
  indexedAttributes: [
    indexed('customer-email', 'fulltext', 'string'),
    indexed('refund-amount', 'double', 'double'),
    indexed('case-status', 'keyword', 'string'),
  ],
  summary: { rpcName: 'GetDexSummary', fields: [] },
  display: { rpcName: 'GetDexDisplay', fields: [] },
  actions: [],
} as FlowV2Definition;

/** The deterministic refund Flow: one keyword Attribute, nothing full-text. */
const keywordOnly = {
  ...searchable,
  indexedAttributes: [indexed('case-status', 'keyword', 'string')],
} as FlowV2Definition;

const NOW = Date.parse('2026-09-20T12:00:00.000Z');
const rows = (q: Partial<typeof EMPTY_RUN_QUERY>, definition = searchable) =>
  toFilterRows({ ...EMPTY_RUN_QUERY, ...q }, definition, NOW)
    .map((row) => `${row.field} ${row.operator} ${row.value}`);

describe('keywordAttribute', () => {
  it('finds the one Attribute a keyword box may search', () => {
    expect(keywordAttribute(searchable)?.attributeKey).toBe('customer-email');
  });

  it('returns null when the Flow declares no fulltext Attribute', () => {
    expect(keywordAttribute(keywordOnly)).toBeNull();
  });
});

describe('toFilterRows', () => {
  it('sends a keyword to the fulltext Attribute as contains', () => {
    expect(rows({ keyword: 'northwind.com' })).toEqual(['customer-email contains northwind.com']);
  });

  it('drops a keyword the Flow cannot search rather than sending a query the server rejects', () => {
    expect(rows({ keyword: 'northwind.com' }, keywordOnly)).toEqual([]);
  });

  it('turns a relative window into an absolute lower bound', () => {
    expect(rows({ since: '24h' })).toEqual(['startTime gte 2026-09-19T12:00:00.000Z']);
  });

  it('treats "any time" as no filter at all', () => {
    expect(rows({ since: '' })).toEqual([]);
  });

  it('uses eq for a keyword or numeric Attribute, never contains', () => {
    expect(rows({ attributeKey: 'refund-amount', attributeValue: '1450' }))
      .toEqual(['refund-amount eq 1450']);
    expect(rows({ attributeKey: 'case-status', attributeValue: 'resolved' }))
      .toEqual(['case-status eq resolved']);
  });

  it('ignores an Attribute chosen with no value, and a value with no Attribute', () => {
    expect(rows({ attributeKey: 'case-status', attributeValue: '   ' })).toEqual([]);
    expect(rows({ attributeValue: 'resolved' })).toEqual([]);
  });

  it('ignores an Attribute the Flow does not declare', () => {
    expect(rows({ attributeKey: 'not-a-thing', attributeValue: 'x' })).toEqual([]);
  });

  it('ANDs every control that is set', () => {
    expect(rows({
      keyword: 'northwind.com', status: 'Running', since: '1h',
      attributeKey: 'case-status', attributeValue: 'awaiting-manager-rule',
    })).toEqual([
      'customer-email contains northwind.com',
      'executionStatus eq Running',
      'startTime gte 2026-09-20T11:00:00.000Z',
      'case-status eq awaiting-manager-rule',
    ]);
  });
});

describe('scope reporting', () => {
  it('knows when nothing is narrowing the list', () => {
    expect(isEmptyQuery(EMPTY_RUN_QUERY)).toBe(true);
    expect(isEmptyQuery({ ...EMPTY_RUN_QUERY, status: 'Running' })).toBe(false);
    expect(isEmptyQuery({ ...EMPTY_RUN_QUERY, keyword: '  ' })).toBe(true);
  });
});

describe('option lists', () => {
  it('offers real statuses and never Unspecified', () => {
    const options = statusOptions();
    expect(options).toContain('Running');
    expect(options).toContain('Completed');
    expect(options).not.toContain('Unspecified');
  });

  it('leaves fulltext out of the exact-filter picker, because the keyword box owns it', () => {
    expect(filterableAttributes(searchable).map((a) => a.attributeKey))
      .toEqual(['refund-amount', 'case-status']);
  });
});
