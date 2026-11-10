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
  parseTypedValue,
  v2ActionUserFields,
  v2ActionUserInput,
  visibleV2Actions,
} from './definitionContract';

describe('Action contract helpers', () => {
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

  it('shows an eligible Action that has no condition', () => {
    const confirm: FlowV2Action = {
      rpcName: 'ConfirmCase',
      label: 'Confirm',
      requiredPermission: 'refund.manage',
      input: { kind: 'none' },
    };
    expect(visibleV2Actions([confirm], ['ConfirmCase'])).toEqual([confirm]);
    expect(visibleV2Actions([confirm], [])).toEqual([]);
  });

  it('preserves int64 Action values as decimal strings', () => {
    const countAction: FlowV2Action = {
      rpcName: 'RetryRefund',
      label: 'Retry',
      requiredPermission: 'refund.retry',
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

  it('parses typed values by value type', () => {
    expect(parseTypedValue('', 'string', true)).toBeNull();
    expect(parseTypedValue('', 'string')).toBe('');
    expect(parseTypedValue('9223372036854775807', 'int64')).toBe('9223372036854775807');
    expect(parseTypedValue('1.5', 'double')).toBe(1.5);
    expect(parseTypedValue('true', 'bool')).toBe(true);
    expect(parseTypedValue('false', 'bool')).toBe(false);
    expect(parseTypedValue('2026-10-24T17:00:00Z', 'datetime')).toBe('2026-10-24T17:00:00.000Z');
  });
});

const approveAction: FlowV2Action = {
  rpcName: 'ApproveRefund',
  label: 'Approve',
  requiredPermission: 'refund.manage',
  condition: { attributeKey: 'case-status', operator: 'in', values: ['awaiting-manager'] },
  input: { kind: 'none' },
};

const rejectAction: FlowV2Action = {
  rpcName: 'RejectRefund',
  label: 'Reject',
  requiredPermission: 'refund.manage',
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
