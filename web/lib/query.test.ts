// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { appendQueryCondition, buildVisibilityQuery, encodeQueryFieldName, parseVisibilityQuery } from './query';

describe('visibility query', () => {
  it('builds the basic filters used by the search page', () => {
    expect(buildVisibilityQuery([
      { id: '1', field: 'ExecutionStatus', operator: '=', value: 'Running' },
      { id: '2', field: 'FlowType', operator: '=', value: 'Checkout' },
    ])).toBe('ExecutionStatus = "Running" AND FlowType = "Checkout"');
  });

  it('round-trips simple advanced queries into basic mode', () => {
    const query = 'WorkflowId = "order-42" AND StartTime >= "2026-07-01T00:00:00Z"';
    expect(buildVisibilityQuery(parseVisibilityQuery(query) ?? [])).toBe(query);
  });

  it('keeps unsupported advanced syntax in advanced mode', () => {
    expect(parseVisibilityQuery('ExecutionStatus IN ("Running", "Failed")')).toBeNull();
  });

  it('quotes field names that are not plain identifiers or that are keywords', () => {
    expect(encodeQueryFieldName('keyword2')).toBe('keyword2');
    expect(encodeQueryFieldName('order-status')).toBe('`order-status`');
    expect(encodeQueryFieldName('order')).toBe('`order`');
    expect(encodeQueryFieldName('a`b\\c')).toBe('`a\\`b\\\\c`');
    const filters = [{ id: '1', field: 'order-status', operator: '=' as const, value: 'open' }];
    const query = buildVisibilityQuery(filters);
    expect(query).toBe('`order-status` = "open"');
    expect(parseVisibilityQuery(query)).toEqual([expect.objectContaining({ field: 'order-status', value: 'open' })]);
  });

  it('adds a condition before ORDER BY and keeps a top-level OR together', () => {
    expect(appendQueryCondition('', 'RunId = ')).toEqual({ query: 'RunId = ', cursor: 8 });
    expect(appendQueryCondition('FlowType = "A" ORDER BY StartTime', 'RunId = ').query)
      .toBe('FlowType = "A" AND RunId =  ORDER BY StartTime');
    expect(appendQueryCondition('ORDER BY CloseTime DESC', 'RunId = ').query).toBe('RunId =  ORDER BY CloseTime DESC');
    expect(appendQueryCondition('a = "x OR y order by z" OR (b = 1 OR c = 2)', 'RunId = ').query)
      .toBe('(a = "x OR y order by z" OR (b = 1 OR c = 2)) AND RunId = ');
    expect(appendQueryCondition('(a = 1 OR b = 2) AND c = 3', 'RunId = ').query)
      .toBe('(a = 1 OR b = 2) AND c = 3 AND RunId = ');
  });
});
