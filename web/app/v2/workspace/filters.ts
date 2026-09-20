// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Definition, V2ValueType } from '@superdurable/flow-definition-renderer';
import { parseTypedValue } from '../contract';

export interface FilterRow {
  id: string;
  field: string;
  operator: string;
  value: string;
}

export function filterFields(definition: FlowV2Definition) {
  return [
    { key: 'flowId', label: 'Flow ID' },
    { key: 'executionStatus', label: 'Execution status' },
    { key: 'startTime', label: 'Start time' },
    { key: 'closeTime', label: 'Close time' },
    ...definition.indexedAttributes.map((attribute) => ({
      key: attribute.attributeKey,
      label: attribute.description,
    })),
  ];
}

export function filterValueType(field: string, definition: FlowV2Definition): V2ValueType {
  if (field === 'startTime' || field === 'closeTime') return 'datetime';
  if (field === 'flowId' || field === 'executionStatus') return 'string';
  return definition.indexedAttributes
    .find((attribute) => attribute.attributeKey === field)?.valueType ?? 'string';
}

export function filterIndexType(field: string, definition: FlowV2Definition) {
  if (field === 'startTime' || field === 'closeTime') return 'datetime';
  if (field === 'flowId' || field === 'executionStatus') return 'keyword';
  return definition.indexedAttributes
    .find((attribute) => attribute.attributeKey === field)?.indexType ?? 'keyword';
}

export function filterOperators(
  indexType: FlowV2Definition['indexedAttributes'][number]['indexType'],
) {
  const equality = [{ value: 'eq', label: 'equals' }, { value: 'in', label: 'is one of' }];
  if (indexType === 'fulltext') return [...equality, { value: 'contains', label: 'contains' }];
  if (indexType === 'datetime' || indexType === 'int' || indexType === 'double') {
    return [
      ...equality,
      { value: 'gt', label: 'greater than' },
      { value: 'gte', label: 'at least' },
      { value: 'lt', label: 'less than' },
      { value: 'lte', label: 'at most' },
    ];
  }
  return equality;
}

export function parseFilterValues(value: string, valueType: V2ValueType): unknown[] {
  return value
    .split(',')
    .map((part) => part.trim())
    .filter(Boolean)
    .map((part) => parseTypedValue(part, valueType));
}

export function updateFilter(
  filters: FilterRow[],
  id: string,
  key: keyof FilterRow,
  value: string,
) {
  return filters.map((filter) => (filter.id === id ? { ...filter, [key]: value } : filter));
}

export function newFilterRow(field: string, operator: string, value: string): FilterRow {
  return { id: `${Date.now()}-${Math.random()}`, field, operator, value };
}

const OPERATOR_PHRASE: Record<string, string> = {
  eq: 'is',
  in: 'is one of',
  contains: 'contains',
  gt: 'is after',
  gte: 'is at least',
  lt: 'is before',
  lte: 'is at most',
};

/** One clause per filter the reader can actually see, so the scope sentence cannot overstate. */
export function describeFilters(
  filters: readonly FilterRow[],
  definition: FlowV2Definition,
): string[] {
  const labels = new Map(filterFields(definition).map((field) => [field.key, field.label]));
  return filters
    .filter((filter) => filter.value.trim() !== '')
    .map((filter) => {
      const field = labels.get(filter.field) ?? filter.field;
      const operator = OPERATOR_PHRASE[filter.operator] ?? filter.operator;
      return `${field.toLowerCase()} ${operator} ${filter.value.trim()}`;
    });
}
