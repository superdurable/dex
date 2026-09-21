// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Definition, V2ValueType } from '@superdurable/flow-definition-renderer';
import { FLOW_STATUS } from '@/lib/types';
import { newFilterRow, type FilterRow } from './filters';

/**
 * Two controls in front, three behind a disclosure. Not a query builder.
 *
 * Every one compiles to a filter Dex can actually index: there is no field here that the Flow did
 * not declare, and no operator offered that the index type will reject.
 */
export interface RunQuery {
  /** An execution status label, or '' for any. */
  status: string;
  /** A relative window, or '' for any. Relative because nobody remembers a timestamp. */
  since: SinceWindow;
  /** An exact Flow ID. Its own control because looking one up is not the same as narrowing a list. */
  flowId: string;
  /** One declared Indexed Attribute, or '' for none. */
  attributeKey: string;
  attributeOperator: RunOperator;
  attributeValue: string;
}

/**
 * The four comparisons worth offering.
 *
 * `in` is deliberately absent: it needs a comma convention explained in placeholder text, and a
 * second value control the moment anybody wants two ranges. Add it when a real query needs it.
 */
export type RunOperator = 'eq' | 'contains' | 'gte' | 'lte';

export type SinceWindow = '' | '1h' | '24h' | '7d' | '30d';

export const SINCE_WINDOWS: { value: SinceWindow; label: string; hours: number }[] = [
  { value: '', label: 'Any time', hours: 0 },
  { value: '1h', label: 'Last hour', hours: 1 },
  { value: '24h', label: 'Last 24 hours', hours: 24 },
  { value: '7d', label: 'Last 7 days', hours: 24 * 7 },
  { value: '30d', label: 'Last 30 days', hours: 24 * 30 },
];

export const EMPTY_RUN_QUERY: RunQuery = {
  status: '',
  since: '',
  flowId: '',
  attributeKey: '',
  attributeOperator: 'eq',
  attributeValue: '',
};

const OPERATOR_LABEL: Record<RunOperator, string> = {
  eq: 'equals',
  contains: 'contains',
  gte: 'at least',
  lte: 'at most',
};

/** Every status Dex can report, so the picker is a real enum rather than a typed string. */
export function statusOptions(): string[] {
  return Object.values(FLOW_STATUS).filter((label) => label !== 'Unspecified');
}

/** Every declared Indexed Attribute. The operator picker keeps each one to comparisons it supports. */
export function filterableAttributes(definition: FlowV2Definition) {
  return definition.indexedAttributes;
}

/**
 * What each index type can actually be asked.
 *
 * `contains` is rejected server-side unless the index is fulltext, and ordering makes no sense on a
 * keyword, so offering the whole set everywhere would hand the reader queries that only fail.
 */
export function operatorsFor(indexType: string): { value: RunOperator; label: string }[] {
  const ordered: RunOperator[] = indexType === 'fulltext'
    ? ['contains', 'eq']
    : indexType === 'int' || indexType === 'double' || indexType === 'datetime'
      ? ['eq', 'gte', 'lte']
      : ['eq'];
  return ordered.map((value) => ({ value, label: OPERATOR_LABEL[value] }));
}

/** The comparison a reader most likely wants for this index type, used when they pick an attribute. */
export function defaultOperator(indexType: string): RunOperator {
  return indexType === 'fulltext' ? 'contains' : 'eq';
}

export function attributeByKey(definition: FlowV2Definition, key: string) {
  return definition.indexedAttributes.find((attribute) => attribute.attributeKey === key) ?? null;
}

/** Which input the value deserves, so a bool or a date cannot be typed as free text. */
export function valueInputKind(valueType: V2ValueType | undefined): 'text' | 'number' | 'datetime' | 'bool' {
  if (valueType === 'int64' || valueType === 'double') return 'number';
  if (valueType === 'datetime') return 'datetime';
  if (valueType === 'bool') return 'bool';
  return 'text';
}

/** Compile the controls into the filter rows the search endpoint already understands. */
export function toFilterRows(
  query: RunQuery,
  definition: FlowV2Definition,
  nowMs: number,
): FilterRow[] {
  const rows: FilterRow[] = [];

  if (query.status !== '') {
    rows.push(newFilterRow('executionStatus', 'eq', query.status));
  }

  const window = SINCE_WINDOWS.find((candidate) => candidate.value === query.since);
  if (window !== undefined && window.hours > 0) {
    const from = new Date(nowMs - window.hours * 3_600_000);
    rows.push(newFilterRow('startTime', 'gte', from.toISOString()));
  }

  const flowId = query.flowId.trim();
  if (flowId !== '') {
    rows.push(newFilterRow('flowId', 'eq', flowId));
  }

  const value = query.attributeValue.trim();
  const attribute = attributeByKey(definition, query.attributeKey);
  if (attribute !== null && value !== '') {
    const legal = operatorsFor(attribute.indexType).map((operator) => operator.value);
    const operator = legal.includes(query.attributeOperator)
      ? query.attributeOperator
      : defaultOperator(attribute.indexType);
    rows.push(newFilterRow(attribute.attributeKey, operator, value));
  }

  return rows;
}

/** True when nothing is narrowing the list, so a view can say so rather than imply a scope. */
export function isEmptyQuery(query: RunQuery): boolean {
  return activeParts(query) === 0;
}

/** Whether anything behind the disclosure is set, so a closed panel cannot hide an active filter. */
export function hasAdvancedQuery(query: RunQuery): boolean {
  return query.flowId.trim() !== ''
    || (query.attributeKey !== '' && query.attributeValue.trim() !== '');
}

function activeParts(query: RunQuery): number {
  return [
    query.status,
    query.since,
    query.flowId.trim(),
    query.attributeKey !== '' ? query.attributeValue.trim() : '',
  ].filter((part) => part !== '').length;
}
