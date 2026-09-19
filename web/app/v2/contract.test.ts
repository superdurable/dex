// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type {
  FlowV2Action,
  FlowV2Definition,
} from '@superdurable/flow-definition-renderer';
import {
  v1RunPath,
  v2ActionUserFields,
  v2ActionUserInput,
  v2HomePath,
  v2ListColumns,
  v2QueuePath,
  v2RunPath,
  visibleV2Actions,
} from './contract';

describe('Dex Web v2 contract helpers', () => {
  it('defaults to v2 Run only when a JSON directory is configured', () => {
    expect(v2HomePath(true)).toBe('/v2/run');
    expect(v2HomePath(false)).toBe('/v1/flows');
  });

  it('keeps indexed and Summary columns in protocol order', () => {
    expect(v2ListColumns(definition).map((column) => `${column.source}:${column.key}`)).toEqual([
      'indexed:case-status',
      'indexed:risk-score',
      'summary:charge-reference',
      'summary:recommended-action',
    ]);
  });

  it('builds Flow-ID-only routes per mode', () => {
    expect(v2RunPath('Refund Flow', 'refund/42')).toBe('/v2/run/Refund%20Flow/refund%2F42');
    expect(v2QueuePath('Refund Flow', 'refund/42')).toBe('/v2/queue/Refund%20Flow/refund%2F42');
    expect(v2RunPath()).toBe('/v2/run');
    expect(v2QueuePath('Refund Flow')).toBe('/v2/queue/Refund%20Flow');
  });

  it('links a run to the v1 page that owns Timeline and controls', () => {
    expect(v1RunPath('refund/42')).toBe('/v1/flows/refund%2F42');
  });

  it('shows only eligible Actions and hides Attribute-sourced inputs', () => {
    const approve = definition.actions[0];
    const reject = definition.actions[1];
    expect(visibleV2Actions(definition.actions, ['RejectRefund'])).toEqual([reject]);
    expect(v2ActionUserFields(approve)).toEqual([]);
    expect(v2ActionUserInput(approve, {})).toEqual({});
    expect(v2ActionUserFields(reject).map((field) => field.fieldName)).toEqual(['reason']);
    expect(v2ActionUserInput(reject, { reason: 'duplicate', gateRequestKey: 'forged' })).toEqual({
      reason: 'duplicate',
    });
  });

  it('preserves int64 Action values as decimal strings', () => {
    const countAction: FlowV2Action = {
      rpcName: 'RetryRefund',
      label: 'Retry',
      condition: { attributeKey: 'case-status', operator: 'in', values: ['failed'] },
      input: {
        kind: 'object',
        fields: [{
          fieldName: 'count', valueType: 'int64', source: 'user', required: true,
          description: 'Retry count',
        }],
      },
    };
    expect(v2ActionUserInput(countAction, { count: '9223372036854775807' })).toEqual({
      count: '9223372036854775807',
    });
  });
});

const approveAction: FlowV2Action = {
  rpcName: 'ApproveRefund',
  label: 'Approve',
  condition: { attributeKey: 'case-status', operator: 'in', values: ['awaiting-manager'] },
  input: { kind: 'none' },
};

const rejectAction: FlowV2Action = {
  rpcName: 'RejectRefund',
  label: 'Reject',
  condition: { attributeKey: 'case-status', operator: 'in', values: ['awaiting-manager'] },
  input: {
    kind: 'object',
    fields: [
      {
        fieldName: 'reason', valueType: 'string', source: 'user', required: true,
        description: 'Rejection reason',
      },
      {
        fieldName: 'gateRequestKey', valueType: 'string', source: 'attribute',
        attributeKey: 'gate-request-key', required: true, description: 'Approval gate',
      },
    ],
  },
};

const definition: FlowV2Definition = {
  indexedAttributes: [
    {
      attributeKey: 'case-status', indexKey: 'case-status', indexType: 'keyword',
      valueType: 'string', description: 'Case status',
    },
    {
      attributeKey: 'risk-score', indexKey: 'risk-score', indexType: 'int',
      valueType: 'int64', description: 'Risk score',
    },
  ],
  summary: {
    rpcName: 'GetDexSummary',
    fields: [
      { attributeKey: 'charge-reference', valueType: 'string', editable: false, description: 'Charge' },
      { attributeKey: 'recommended-action', valueType: 'string', editable: false, description: 'Action' },
    ],
  },
  display: { rpcName: 'GetDexDisplay', fields: [] },
  actions: [approveAction, rejectAction],
};
