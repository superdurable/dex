// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Definition, V2ValueType } from '@superdurable/flow-definition-renderer';
import {
  FLOW_STATUS,
  FLOW_STATUS_QUERY_VALUE,
  type FlowDefinitionEntry,
  type FlowStatus,
} from '@/lib/types';
import { ACTIVE_STEP_TYPES_FIELD, newFilterRow, type FilterRow } from './filters';
import { SEARCH_COPY } from './searchCopy';

/**
 * Two controls in front, three behind a disclosure. Not a query builder.
 *
 * Every one compiles to a filter Dex can actually index: there is no field here that the Flow did
 * not declare, and no operator offered that the index type will reject.
 */
export interface RunQuery {
  /** An ExecutionStatus query value such as `TimedOut`, or '' for any. */
  status: string;
  /** A relative window, or '' for any. Relative because nobody remembers a timestamp. */
  since: SinceWindow;
  /** An exact Flow ID. Its own control because looking one up is not the same as narrowing a list. */
  flowId: string;
  /** One declared Indexed Attribute, or '' for none. */
  attributeKey: string;
  attributeOperator: RunOperator;
  attributeValue: string;
  /** Step types a run must be at, any of them. Empty for no filter. */
  activeStepTypes: string[];
  /**
   * Every Flow type searched when the reader widened past the page's own, which it always includes.
   * Empty otherwise, so a search of one Flow type never names one the page does not show.
   */
  flowTypes: string[];
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
  activeStepTypes: [],
  flowTypes: [],
};

const OPERATOR_LABEL: Record<RunOperator, string> = {
  eq: 'equals',
  contains: 'contains',
  gte: 'at least',
  lte: 'at most',
};

type ListedFlowStatus = Exclude<FlowStatus, 'Unspecified'>;

/** Every status shown in the run list. */
export function statusOptions(): { value: string; label: ListedFlowStatus }[] {
  return Object.values(FLOW_STATUS)
    .filter((label): label is ListedFlowStatus => label !== 'Unspecified')
    .map((label) => ({ value: FLOW_STATUS_QUERY_VALUE[label], label }));
}

/** The ExecutionStatus query value for a status label; a query value is returned unchanged. */
export function flowStatusQueryValue(status: string): string {
  return Object.hasOwn(FLOW_STATUS_QUERY_VALUE, status)
    ? FLOW_STATUS_QUERY_VALUE[status as keyof typeof FLOW_STATUS_QUERY_VALUE]
    : status;
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
    rows.push(newFilterRow('executionStatus', 'eq', flowStatusQueryValue(query.status)));
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

  if (query.activeStepTypes.length > 0) {
    rows.push({
      ...newFilterRow(ACTIVE_STEP_TYPES_FIELD, 'in', query.activeStepTypes.join(', ')),
      values: query.activeStepTypes,
    });
  }

  // An Attribute means what one Flow type declares, so it cannot narrow several at once.
  const value = query.flowTypes.length > 1 ? '' : query.attributeValue.trim();
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
    || query.activeStepTypes.length > 0
    || query.flowTypes.length > 0
    || (query.attributeKey !== '' && query.attributeValue.trim() !== '');
}

function activeParts(query: RunQuery): number {
  return [
    query.status,
    query.since,
    query.flowId.trim(),
    query.attributeKey !== '' ? query.attributeValue.trim() : '',
    query.activeStepTypes.join(),
    query.flowTypes.join(),
  ].filter((part) => part !== '').length;
}

/**
 * The Flow types one search covers: the page's own, unless the reader widened past it.
 *
 * A widening that left the page out is stale, as after Back to a Flow type it never covered.
 */
export function searchedFlowTypes(flowTypes: readonly string[], pageFlowType: string): readonly string[] {
  if (flowTypes.length > 1 && flowTypes.includes(pageFlowType)) return flowTypes;
  return pageFlowType === '' ? [] : [pageFlowType];
}

/** The widened selection after one Flow type is ticked or unticked. The page's own always stays. */
export function toggleFlowType(
  flowTypes: readonly string[],
  pageFlowType: string,
  flowType: string,
): string[] {
  if (flowType === pageFlowType) return [...flowTypes];
  const searched = searchedFlowTypes(flowTypes, pageFlowType);
  const next = searched.includes(flowType)
    ? searched.filter((candidate) => candidate !== flowType)
    : [...searched, flowType];
  return next.length > 1 ? next.sort() : [];
}

export function toggleStepType(stepTypes: readonly string[], stepType: string): string[] {
  return stepTypes.includes(stepType)
    ? stepTypes.filter((candidate) => candidate !== stepType)
    : [...stepTypes, stepType].sort();
}

/**
 * The Step types a reader can ask for, read off the graphs of the Flow types being searched.
 *
 * Anything already chosen stays listed even when it no longer belongs to one of those Flow types,
 * so a filter that is still applied can always be seen and unticked.
 */
export function stepTypeOptions(
  definitions: readonly FlowDefinitionEntry[],
  flowTypes: readonly string[],
  chosen: readonly string[],
): string[] {
  const stepTypes = new Set(chosen);
  for (const entry of definitions) {
    if (entry.schemaVersion !== '2.0' || !entry.valid || !flowTypes.includes(entry.flowName)) continue;
    for (const node of entry.graph.nodes) {
      if (node.kind === 'step') stepTypes.add(node.name);
    }
  }
  return [...stepTypes].sort();
}

/**
 * What an empty list says, from the search that actually ran.
 *
 * A run is findable by Step only when its Flow opted in, so an empty answer to that filter must
 * not read as "no work": unindexed runs can be waiting at exactly those Steps.
 */
export function emptyRunsText(
  filters: readonly FilterRow[],
  flowTypes: readonly string[],
  otherwise: string,
): string {
  if (filters.some((filter) => filter.field === ACTIVE_STEP_TYPES_FIELD)) return SEARCH_COPY.noActiveStepMatch;
  return flowTypes.length > 1 ? SEARCH_COPY.noRunsAcrossFlowTypes : otherwise;
}
