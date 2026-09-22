// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { permissionActionLabels, permissionsOf } from './permissions';

const action = (rpcName: string, requiredPermission: string) => ({
  rpcName,
  label: rpcName,
  requiredPermission,
  condition: { attributeKey: 'case-status', operator: 'in', values: ['open'] },
  input: { kind: 'none', fields: [] },
}) as FlowV2Definition['actions'][number];

const definition = (actions: FlowV2Definition['actions']) => ({
  indexedAttributes: [],
  summary: { rpcName: 'GetDexSummary', fields: [] },
  display: { rpcName: 'GetDexDisplay', fields: [] },
  actions,
}) as FlowV2Definition;

const refund = definition([
  action('ApproveRefund', 'refund.manage'),
  action('RejectRefund', 'refund.manage'),
  action('ConfirmCustomerMessage', 'refund.message'),
  action('EditCustomerMessage', 'refund.message'),
]);

describe('permissionsOf', () => {
  it('deduplicates and sorts permissions', () => {
    expect(permissionsOf(refund)).toEqual(['refund.manage', 'refund.message']);
  });

  it('is empty without a definition', () => {
    expect(permissionsOf(undefined)).toEqual([]);
  });
});

describe('permissionActionLabels', () => {
  it('names the Actions discovered by a permission', () => {
    expect(permissionActionLabels(refund, 'refund.manage'))
      .toEqual(['ApproveRefund', 'RejectRefund']);
    expect(permissionActionLabels(refund, 'refund.audit')).toEqual([]);
  });
});
