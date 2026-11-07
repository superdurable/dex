// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useState } from 'react';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import type { FilterRow } from './filters';
import { EMPTY_RUN_QUERY, toFilterRows, type RunQuery } from './runQuery';

/**
 * Holds the four controls and compiles them to filter rows on submit.
 *
 * Compiled on submit rather than on change so a half-typed keyword is not a query, and so the
 * rows the list was actually searched with stay stable while somebody edits the next one. The
 * Flow types widen on submit for the same reason.
 */
export function useRunQuery(
  definition: FlowV2Definition | undefined,
  pageFlowType: string,
  initial: RunQuery = EMPTY_RUN_QUERY,
) {
  const [query, setQuery] = useState<RunQuery>(initial);
  const [applied, setApplied] = useState<FilterRow[]>(
    () => (definition === undefined ? [] : toFilterRows(initial, definition, Date.now())),
  );

  const [appliedFlowTypes, setAppliedFlowTypes] = useState<string[]>(initial.flowTypes);

  // Opening a row stays inside the widening. Any other route change can leave it, and a widening
  // that outlives its page would keep dropping Attribute filters the page now offers.
  useEffect(() => {
    const coversPage = (flowTypes: readonly string[]) => flowTypes.length === 0 || flowTypes.includes(pageFlowType);
    setQuery((current) => (coversPage(current.flowTypes) ? current : { ...current, flowTypes: [] }));
    setAppliedFlowTypes((current) => (coversPage(current) ? current : []));
  }, [pageFlowType]);

  const apply = useCallback((next: RunQuery) => {
    if (definition === undefined) return;
    setQuery(next);
    setApplied(toFilterRows(next, definition, Date.now()));
    setAppliedFlowTypes(next.flowTypes);
  }, [definition]);

  return {
    query,
    setQuery,
    appliedFilters: applied,
    appliedFlowTypes,
    submit: useCallback(() => apply(query), [apply, query]),
    clear: useCallback(() => apply(EMPTY_RUN_QUERY), [apply]),
    /** Back to the page's own Flow type, leaving the other filters as they were. */
    narrowToPageFlowType: useCallback(() => {
      setQuery((current) => ({ ...current, flowTypes: [] }));
      setAppliedFlowTypes([]);
    }, []),
  };
}
