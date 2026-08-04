// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { describe, expect, it } from 'vitest';
import { buildVisibilityQuery, parseVisibilityQuery } from './query';

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
});
