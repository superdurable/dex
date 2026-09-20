// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { FLOW_STATUS } from '@/lib/types';
import { newFilterRow, type FilterRow } from './filters';

/**
 * Four controls, not a query builder.
 *
 * Every one compiles to a filter Dex can actually index: there is no field here that the Flow
 * did not declare, and no operator the index type does not support.
 */
export interface RunQuery {
  /** Free text, only meaningful when the Flow declares a fulltext Attribute. */
  keyword: string;
  /** An execution status label, or '' for any. */
  status: string;
  /** A relative window, or '' for any. Relative because nobody remembers a timestamp. */
  since: SinceWindow;
  /** One declared Indexed Attribute, or '' for none. */
  attributeKey: string;
  attributeValue: string;
}

export type SinceWindow = '' | '1h' | '24h' | '7d' | '30d';

export const SINCE_WINDOWS: { value: SinceWindow; label: string; hours: number }[] = [
  { value: '', label: 'Any time', hours: 0 },
  { value: '1h', label: 'Last hour', hours: 1 },
  { value: '24h', label: 'Last 24 hours', hours: 24 },
  { value: '7d', label: 'Last 7 days', hours: 24 * 7 },
  { value: '30d', label: 'Last 30 days', hours: 24 * 30 },
];

export const EMPTY_RUN_QUERY: RunQuery = {
  keyword: '',
  status: '',
  since: '',
  attributeKey: '',
  attributeValue: '',
};

/** Every status Dex can report, so the picker is a real enum rather than a typed string. */
export function statusOptions(): string[] {
  return Object.values(FLOW_STATUS).filter((label) => label !== 'Unspecified');
}

/** The one Attribute a keyword box can legitimately search, or null when the Flow declares none. */
export function keywordAttribute(definition: FlowV2Definition) {
  return definition.indexedAttributes.find((attribute) => attribute.indexType === 'fulltext') ?? null;
}

/** Attributes worth offering as an exact filter: the keyword box already owns fulltext. */
export function filterableAttributes(definition: FlowV2Definition) {
  return definition.indexedAttributes.filter((attribute) => attribute.indexType !== 'fulltext');
}

/**
 * The operator each index type actually supports. A value filter therefore never asks the
 * server for something it will reject.
 */
function operatorFor(indexType: string): string {
  if (indexType === 'fulltext') return 'contains';
  return 'eq';
}

/** Compile the controls into the filter rows the search endpoint already understands. */
export function toFilterRows(
  query: RunQuery,
  definition: FlowV2Definition,
  nowMs: number,
): FilterRow[] {
  const rows: FilterRow[] = [];

  const keyword = query.keyword.trim();
  const fulltext = keywordAttribute(definition);
  if (keyword !== '' && fulltext !== null) {
    rows.push(newFilterRow(fulltext.attributeKey, 'contains', keyword));
  }

  if (query.status !== '') {
    rows.push(newFilterRow('executionStatus', 'eq', query.status));
  }

  const window = SINCE_WINDOWS.find((candidate) => candidate.value === query.since);
  if (window !== undefined && window.hours > 0) {
    const from = new Date(nowMs - window.hours * 3_600_000);
    rows.push(newFilterRow('startTime', 'gte', from.toISOString()));
  }

  const value = query.attributeValue.trim();
  if (query.attributeKey !== '' && value !== '') {
    const attribute = definition.indexedAttributes
      .find((candidate) => candidate.attributeKey === query.attributeKey);
    if (attribute !== undefined) {
      rows.push(newFilterRow(attribute.attributeKey, operatorFor(attribute.indexType), value));
    }
  }

  return rows;
}

/** True when nothing is narrowing the list, so a view can say so rather than imply a scope. */
export function isEmptyQuery(query: RunQuery): boolean {
  return toFilterRowsCount(query) === 0;
}

function toFilterRowsCount(query: RunQuery): number {
  return [
    query.keyword.trim(),
    query.status,
    query.since,
    query.attributeKey !== '' ? query.attributeValue.trim() : '',
  ].filter((part) => part !== '').length;
}
