// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useState } from 'react';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { readResponseJSON } from '@/lib/http';
import type { V2Flow, V2SearchResult } from '@/lib/types';
import { absorb, nothingHeld, readFailureReason, type Liveness } from '../queue/liveness';
import { filterValueType, parseFilterValues, type FilterRow } from './filters';

export interface FlowSearch {
  flows: V2Flow[];
  loading: boolean;
  searchError: string;
  /** What the reader currently knows, which is not what the last request returned. */
  liveness: Liveness;
  page: number;
  hasNextPage: boolean;
  runSearch: () => void;
  goToNextPage: () => void;
  goToPreviousPage: () => void;
}

export function useFlowSearch(
  flowType: string | undefined,
  definition: FlowV2Definition | undefined,
  /** Owned by whatever renders the search controls, so there is one source of scope. */
  filters: readonly FilterRow[],
): FlowSearch {
  const [held, setHeld] = useState(() => nothingHeld<V2Flow[]>());
  const [loading, setLoading] = useState(false);
  const [nextPageToken, setNextPageToken] = useState('');
  const [pageTokens, setPageTokens] = useState<string[]>(['']);
  const [page, setPage] = useState(0);

  const executeSearch = useCallback(async (token = '', nextPage = 0) => {
    if (!flowType || !definition) return;
    setLoading(true);
    try {
      const response = await fetch('/api/v2/search', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowType,
          filters: filters.map((filter) => ({
            field: filter.field,
            operator: filter.operator,
            values: parseFilterValues(filter.value, filterValueType(filter.field, definition)),
          })),
          pageSize: 50,
          nextPageToken: token,
        }),
      });
      const result = await readResponseJSON<V2SearchResult>(response);
      setHeld((prior) => absorb(prior, { state: 'ok', value: result.flows }));
      setNextPageToken(result.nextPageToken);
      setPage(nextPage);
    } catch (failedSearch) {
      // Keep the rows that were true a moment ago; absorb marks them stale.
      setHeld((prior) => absorb(prior, {
        state: 'unreachable',
        reason: readFailureReason(failedSearch),
      }));
    } finally {
      setLoading(false);
    }
  }, [definition, filters, flowType]);

  // `filters` only changes when the reader submits, so this cannot fire per keystroke.
  useEffect(() => {
    if (flowType && definition) void executeSearch();
  }, [definition, filters, flowType]);

  const runSearch = useCallback(() => {
    setPageTokens(['']);
    void executeSearch();
  }, [executeSearch]);

  const goToNextPage = useCallback(() => {
    setPageTokens([...pageTokens, nextPageToken]);
    void executeSearch(nextPageToken, page + 1);
  }, [executeSearch, nextPageToken, page, pageTokens]);

  const goToPreviousPage = useCallback(() => {
    const previous = page - 1;
    const tokens = pageTokens.slice(0, -1);
    setPageTokens(tokens);
    void executeSearch(tokens[previous] ?? '', previous);
  }, [executeSearch, page, pageTokens]);

  return {
    flows: held.value ?? [],
    loading,
    searchError: held.liveness === 'stale' || held.liveness === 'unreachable' ? held.reason ?? '' : '',
    liveness: held.liveness,
    page,
    hasNextPage: nextPageToken !== '',
    runSearch,
    goToNextPage,
    goToPreviousPage,
  };
}
