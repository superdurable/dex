// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useState } from 'react';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import type { FilterRow } from './filters';
import { EMPTY_RUN_QUERY, toFilterRows, type RunQuery } from './runQuery';

/**
 * Holds the four controls and compiles them to filter rows on submit.
 *
 * Compiled on submit rather than on change so a half-typed keyword is not a query, and so the
 * rows the list was actually searched with stay stable while somebody edits the next one.
 */
export function useRunQuery(definition: FlowV2Definition | undefined, initial: RunQuery = EMPTY_RUN_QUERY) {
  const [query, setQuery] = useState<RunQuery>(initial);
  const [applied, setApplied] = useState<FilterRow[]>(
    () => (definition === undefined ? [] : toFilterRows(initial, definition, Date.now())),
  );

  const apply = useCallback((next: RunQuery) => {
    if (definition === undefined) return;
    setQuery(next);
    setApplied(toFilterRows(next, definition, Date.now()));
  }, [definition]);

  return {
    query,
    setQuery,
    appliedFilters: applied,
    submit: useCallback(() => apply(query), [apply, query]),
    clear: useCallback(() => apply(EMPTY_RUN_QUERY), [apply]),
  };
}
