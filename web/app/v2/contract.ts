// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type {
  FlowV2Action,
  FlowV2Definition,
} from '@superdurable/flow-definition-renderer';

export function v2HomePath(canUseV2: boolean) {
  return canUseV2 ? '/v2' : '/v1/flows';
}

export function v2FlowPath(flowType: string, flowID?: string) {
  const typePath = `/v2/${encodeURIComponent(flowType)}`;
  if (flowID === undefined) return typePath;
  return `${typePath}/${encodeURIComponent(flowID)}`;
}

export function v2ListColumns(definition: FlowV2Definition) {
  return [
    ...definition.indexedAttributes.map((attribute) => ({
      source: 'indexed' as const,
      key: attribute.attributeKey,
      description: attribute.description,
    })),
    ...definition.summary.fields.map((field) => ({
      source: 'summary' as const,
      key: field.attributeKey,
      description: field.description,
    })),
  ];
}

export function visibleV2Actions(
  actions: FlowV2Action[],
  eligibleRPCNames: string[],
) {
  const eligible = new Set(eligibleRPCNames);
  return actions.filter((action) => eligible.has(action.rpcName));
}

export function v2ActionUserFields(action: FlowV2Action) {
  return (action.input.fields ?? []).filter((field) => field.source === 'user');
}

export function v2ActionUserInput(
  action: FlowV2Action,
  values: Record<string, string>,
) {
  return Object.fromEntries(v2ActionUserFields(action).map((field) => [
    field.fieldName,
    parseTypedValue(values[field.fieldName] ?? '', field.valueType, !field.required),
  ]));
}

export function parseTypedValue(value: string, valueType: string, optional = false): unknown {
  if (optional && value === '') return null;
  if (valueType === 'int64') return value;
  if (valueType === 'double') return Number(value);
  if (valueType === 'bool') return value === 'true';
  if (valueType === 'datetime') return new Date(value).toISOString();
  return value;
}
