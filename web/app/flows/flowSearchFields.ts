// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState } from 'react';
import { readResponseJSON } from '@/lib/http';
import { webURL } from '@/lib/basePath';
import type { FlowExecution, V2Catalog } from '@/lib/types';

/** The documentation of the search query syntax; absolute, so it also opens from embedded Dex Web. */
export const SEARCH_QUERY_SYNTAX_URL = 'https://docs.superdurable.io/reference/server/search#query-syntax';

/** A search index type, named as the query syntax reference names it. */
export type SearchFieldType = 'Keyword' | 'Text' | 'KeywordArray' | 'Int' | 'Double' | 'Bool' | 'Datetime';

/** One field a search query can name. */
export interface SearchField {
  /** The name a query uses, such as ExecutionStatus or an indexed Attribute's index key. */
  name: string;
  type: SearchFieldType;
  /** What the field holds, in a few words. */
  description: string;
}

/** An indexed Attribute that a Flow type declares in its definition. */
export interface DeclaredSearchField extends SearchField {
  flowType: string;
  attributeKey: string;
}

/** An indexed Attribute seen only in the loaded results; its type is guessed from the values. */
export interface ObservedSearchField extends SearchField {
  isTypeInferred: true;
}

/** The fields every Dex Server indexes for each Flow run. */
export const BUILT_IN_SEARCH_FIELDS: readonly SearchField[] = [
  { name: 'ExecutionStatus', type: 'Keyword', description: 'Running, Completed, Failed, Canceled, Terminated, or TimedOut' },
  { name: 'FlowType', type: 'Keyword', description: 'The Flow type' },
  { name: 'WorkflowId', type: 'Keyword', description: 'The Flow ID' },
  { name: 'RunId', type: 'Keyword', description: 'The run ID' },
  { name: 'StartTime', type: 'Datetime', description: 'When the run started, as RFC 3339' },
  { name: 'CloseTime', type: 'Datetime', description: 'When the run closed, as RFC 3339' },
  { name: 'ActiveStepTypes', type: 'KeywordArray', description: 'Step types active now, when the Flow turns this on' },
  { name: 'DexParentFlowID', type: 'Keyword', description: 'The parent Flow ID of a SubFlow' },
  { name: 'DexWorkQueuePermissions', type: 'KeywordArray', description: 'Internal: the Work Queue permissions the run has offered' },
  { name: 'WorkflowType', type: 'Keyword', description: 'The server engine name, not the Flow type' },
];

const builtInFieldNames = new Set(BUILT_IN_SEARCH_FIELDS.map((field) => field.name));

// Older servers report these engine keys with results; a query cannot use them.
const unsearchableResultKeys = new Set([
  'BinaryChecksums',
  'BuildIds',
  'CadenceChangeVersion',
  'TemporalChangeVersion',
]);

const catalogIndexTypes: Record<string, SearchFieldType> = {
  keyword: 'Keyword',
  fulltext: 'Text',
  'keyword-array': 'KeywordArray',
  int: 'Int',
  double: 'Double',
  bool: 'Bool',
  datetime: 'Datetime',
};

/** Lists the indexed Attributes every Flow type in the catalog declares, by Flow type and then index key. */
export function selectDeclaredSearchFields(catalog: V2Catalog | null): DeclaredSearchField[] {
  if (!catalog) return [];
  return catalog.flows.flatMap((entry) => (entry.definition.indexedAttributes ?? []).map((attribute) => ({
    flowType: entry.flowType,
    attributeKey: attribute.attributeKey,
    name: attribute.indexKey,
    type: catalogIndexTypes[attribute.indexType] ?? 'Keyword',
    description: attribute.description,
  }))).sort((left, right) => left.flowType.localeCompare(right.flowType) || left.name.localeCompare(right.name));
}

/**
 * Lists the indexed Attribute keys in the loaded results that neither the built-in fields nor a declaration name.
 *
 * Returns each key once, sorted, with a type guessed from its first value that is not null.
 */
export function selectObservedSearchFields(
  flows: readonly FlowExecution[],
  declaredNames: ReadonlySet<string>,
): ObservedSearchField[] {
  const values = new Map<string, unknown>();
  for (const flow of flows) {
    for (const { key, value } of flow.indexedAttributes) {
      if (builtInFieldNames.has(key) || unsearchableResultKeys.has(key) || declaredNames.has(key)) continue;
      if (!values.has(key) || values.get(key) === null) values.set(key, value);
    }
  }
  return [...values].sort(([left], [right]) => left.localeCompare(right)).map(([name, value]) => ({
    name,
    type: inferSearchFieldType(value),
    description: 'Seen in these results',
    isTypeInferred: true,
  }));
}

const rfc3339Pattern = /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(\.\d+)?(Z|[+-]\d{2}:\d{2})$/;

/** Guesses the index type of one search result value. */
export function inferSearchFieldType(value: unknown): SearchFieldType {
  if (Array.isArray(value)) return 'KeywordArray';
  if (typeof value === 'boolean') return 'Bool';
  if (typeof value === 'number') return Number.isInteger(value) ? 'Int' : 'Double';
  if (typeof value === 'string' && rfc3339Pattern.test(value)) return 'Datetime';
  return 'Keyword';
}

/** The catalog as the Fields panel reads it. */
export interface SearchFieldCatalogState {
  status: 'idle' | 'loading' | 'ready' | 'failed';
  catalog: V2Catalog | null;
  error: string;
}

/** Reads GET /api/v2/catalog once the panel first opens; a failure leaves the built-in and seen fields. */
export function useSearchFieldCatalog(isRequested: boolean): SearchFieldCatalogState {
  const [state, setState] = useState<SearchFieldCatalogState>({ status: 'idle', catalog: null, error: '' });
  const controllerRef = useRef<AbortController | null>(null);
  useEffect(() => () => {
    controllerRef.current?.abort();
    controllerRef.current = null;
  }, []);
  useEffect(() => {
    // One read per page: closing the panel keeps it going, and reopening reuses its answer.
    if (!isRequested || controllerRef.current) return;
    const controller = new AbortController();
    controllerRef.current = controller;
    setState({ status: 'loading', catalog: null, error: '' });
    fetch(webURL('/api/v2/catalog'), { cache: 'no-store', signal: controller.signal })
      .then((response) => readResponseJSON<V2Catalog>(response))
      .then((catalog) => setState({ status: 'ready', catalog, error: '' }))
      .catch((loadError: unknown) => {
        if (controller.signal.aborted) return;
        const message = loadError instanceof Error ? loadError.message : 'Flow definitions failed to load';
        setState({ status: 'failed', catalog: null, error: message });
      });
  }, [isRequested]);
  return state;
}
