// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type {
  FlowSupervisionAction,
  FlowSupervisionDefinition,
} from '@superdurable/flow-definition-renderer';
import {
  supervisionActionUserFields,
  supervisionActionUserInput,
  supervisionDetailPath,
  supervisionHomePath,
  supervisionListColumns,
  visibleSupervisionActions,
} from './SupervisionPages';

describe('Supervision pages', () => {
  it('defaults to Supervision only when a valid v2 catalog is enabled', () => {
    expect(supervisionHomePath(true, 'supervision')).toBe('/supervision');
    expect(supervisionHomePath(true, 'operations')).toBe('/flows');
    expect(supervisionHomePath(false, 'supervision')).toBe('/flows');
  });

  it('keeps indexed and Summary columns in protocol order', () => {
    expect(supervisionListColumns(definition).map((column) => `${column.source}:${column.key}`)).toEqual([
      'indexed:case-status',
      'indexed:risk-score',
      'summary:charge-reference',
      'summary:recommended-action',
    ]);
  });

  it('builds Flow-ID-only detail routes', () => {
    const path = supervisionDetailPath('Refund Flow', 'refund/42');
    expect(path).toBe('/supervision/Refund%20Flow/refund%2F42');
    expect(path).not.toContain('run');
  });

  it('shows only eligible Actions and hides Attribute-sourced inputs', () => {
    const approve = definition.actions[0];
    const reject = definition.actions[1];
    expect(visibleSupervisionActions(definition.actions, ['RejectRefund'])).toEqual([reject]);
    expect(supervisionActionUserFields(approve)).toEqual([]);
    expect(supervisionActionUserInput(approve, {})).toEqual({});
    expect(supervisionActionUserFields(reject).map((field) => field.fieldName)).toEqual(['reason']);
    expect(supervisionActionUserInput(reject, { reason: 'duplicate', gateRequestKey: 'forged' })).toEqual({
      reason: 'duplicate',
    });
  });

  it('preserves int64 Action values as decimal strings', () => {
    const countAction: FlowSupervisionAction = {
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
    expect(supervisionActionUserInput(countAction, { count: '9223372036854775807' })).toEqual({
      count: '9223372036854775807',
    });
  });
});

const approveAction: FlowSupervisionAction = {
  rpcName: 'ApproveRefund',
  label: 'Approve',
  condition: { attributeKey: 'case-status', operator: 'in', values: ['awaiting-manager'] },
  input: { kind: 'none' },
};

const rejectAction: FlowSupervisionAction = {
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

const definition: FlowSupervisionDefinition = {
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
