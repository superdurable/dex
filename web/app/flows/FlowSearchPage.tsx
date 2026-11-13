// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link, useNavigate } from 'react-router-dom';
import { rememberFlowsSearch } from '../components/frame/lastFlowsSearch';
import { useCallback, useEffect, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { displayValue, formatDate, formatDuration } from '@/lib/format';
import {
  buildVisibilityQuery,
  parseVisibilityQuery,
  type BasicFilter,
  type QueryOperator,
} from '@/lib/query';
import { readResponseJSON } from '@/lib/http';
import type { FlowDefinitionTypeList, FlowExecution, SearchFlowsResult } from '@/lib/types';
import { DESIGNS_PATH } from '../routes';
import {
  ArrowDown,
  ArrowUp,
  Bookmark,
  ChevronLeft,
  ChevronRight,
  Columns3,
  History,
  Inbox,
  Layers,
  Plus,
  Search,
  X,
} from 'lucide-react';
import { StatusBadge } from '../components/StatusBadge';
import { middleTruncate } from './details/RunFactsMenu';
import { usePreferences } from '../providers';
import { scopedStorageKey, webURL } from '@/lib/basePath';

type QueryMode = 'basic' | 'advanced';
type ColumnId = 'status' | 'flowId' | 'runId' | 'flowType' | 'start' | 'close' | 'duration';

const allColumns: ColumnId[] = [
  'status',
  'flowId',
  'flowType',
  'runId',
  'start',
  'close',
  'duration',
];

// The Run ID stays one click away in Columns, so the default columns fit a laptop screen.
const defaultColumns: ColumnId[] = allColumns.filter((column) => column !== 'runId');

const columnLabels: Record<ColumnId, string> = {
  status: 'Status',
  flowId: 'Flow ID',
  runId: 'Run ID',
  flowType: 'Flow type',
  start: 'Started',
  close: 'Closed',
  duration: 'Duration',
};

const builtInFields = [
  'ExecutionStatus',
  'WorkflowId',
  'RunId',
  'FlowType',
  'StartTime',
  'CloseTime',
];

const hiddenIndexedAttributeKeys = new Set([
  'ActiveStepTypes',
  'BinaryChecksums',
  'BuildIds',
  'CadenceChangeVersion',
  'DexParentFlowID',
  'DexWorkQueuePermissions',
  'FlowType',
  'TemporalChangeVersion',
]);

// Recent and saved searches name one Dex Server's Flows, so scopedStorageKey scopes them by base path.
const recentQueriesStorageKey = 'dex-web-recent-queries';
const savedQueriesStorageKey = 'dex-web-saved-queries';

interface SavedQuery {
  name: string;
  query: string;
}

function newFilter(field = 'ExecutionStatus'): BasicFilter {
  return {
    id: `${Date.now()}-${Math.random()}`,
    field,
    operator: '=',
    value: field === 'ExecutionStatus' ? 'Running' : '',
  };
}

/** A SubFlow reads as a child in the list: its name without the "SubFlow:" prefix, after a child mark. */
function listFlowIdOf(flowId: string): string {
  return flowId.startsWith('SubFlow:') && flowId.length > 'SubFlow:'.length
    ? `↳ ${middleTruncate(flowId.slice('SubFlow:'.length), 26)}`
    : flowId;
}

const ALL_STATUSES = 'All';
const QUICK_STATUSES = [ALL_STATUSES, 'Running', 'Failed', 'Completed'] as const;
type QuickStatus = typeof QUICK_STATUSES[number];

/** The quick status a search is exactly, or null when it filters anything else. */
function quickStatusOf(filters: BasicFilter[] | null): QuickStatus | null {
  if (!filters) return null;
  if (filters.length === 0) return ALL_STATUSES;
  const [only] = filters;
  if (filters.length !== 1 || only.field !== 'ExecutionStatus' || only.operator !== '=') return null;
  return QUICK_STATUSES.find((status) => status === only.value) ?? null;
}

function readJSON<T>(key: string, fallback: T): T {
  try {
    const stored = window.localStorage.getItem(key);
    return stored ? JSON.parse(stored) as T : fallback;
  } catch {
    return fallback;
  }
}

function normalizeColumnOrder(columns: ColumnId[]): ColumnId[] {
  if (!columns.includes('flowType')) return columns;
  const reordered: ColumnId[] = columns.filter((column) => column !== 'flowType');
  const flowIdIndex = reordered.indexOf('flowId');
  reordered.splice(flowIdIndex < 0 ? 0 : flowIdIndex + 1, 0, 'flowType');
  return reordered;
}

export function selectVisibleIndexedAttributeKeys(flows: FlowExecution[]): string[] {
  const visibleIndexedAttributeKeys = new Set<string>();
  flows.forEach((flow) => flow.indexedAttributes.forEach((indexedAttribute) => {
    if (!hiddenIndexedAttributeKeys.has(indexedAttribute.key)) {
      visibleIndexedAttributeKeys.add(indexedAttribute.key);
    }
  }));
  return [...visibleIndexedAttributeKeys].sort();
}

// A new key, so a choice made while grouping was off by default does not carry over.
const groupByTypeStorageKey = 'dex-web.flows.group-by-type';

function storedIsGroupedByType(): boolean {
  try {
    // The landing page groups by Flow type until a viewer turns it off.
    return window.localStorage.getItem(groupByTypeStorageKey) !== 'false';
  } catch {
    return true;
  }
}

function persistIsGroupedByType(isGrouped: boolean) {
  try {
    window.localStorage.setItem(groupByTypeStorageKey, String(isGrouped));
  } catch (storageError) {
    console.warn('Unable to save the Flows grouping preference.', storageError);
  }
}

/** The page's runs grouped by Flow type, in the order each type first appears. */
export function flowsByType(flows: readonly FlowExecution[]): Array<{ flowType: string; flows: FlowExecution[] }> {
  const groups = new Map<string, FlowExecution[]>();
  for (const flow of flows) groups.set(flow.flowType, [...(groups.get(flow.flowType) ?? []), flow]);
  return [...groups].map(([flowType, groupFlows]) => ({ flowType, flows: groupFlows }));
}

/** Counts a group's runs by status, such as "1 failed · 2 completed", busiest first. */
export function flowGroupCountsLabel(flows: readonly FlowExecution[]): string {
  const counts = new Map<string, number>();
  for (const flow of flows) {
    const status = flow.flowStatus.toLowerCase();
    counts.set(status, (counts.get(status) ?? 0) + 1);
  }
  return [...counts].sort((left, right) => right[1] - left[1]).map(([status, count]) => `${count} ${status}`).join(' · ');
}

/** The Flow types with a loaded design, read only while the list is grouped. */
function useDesignedFlowTypes(isGrouped: boolean): Set<string> {
  const [flowTypes, setFlowTypes] = useState<Set<string>>(() => new Set());
  useEffect(() => {
    if (!isGrouped) return undefined;
    const controller = new AbortController();
    fetch(webURL('/api/flow-definition-types'), { signal: controller.signal })
      .then((response) => readResponseJSON<FlowDefinitionTypeList>(response))
      .then((list) => setFlowTypes(new Set(list.configured ? list.flowTypes.map((item) => item.flowType) : [])))
      // Without the list, groups simply offer no Design link.
      .catch(() => undefined);
    return () => controller.abort();
  }, [isGrouped]);
  return flowTypes;
}

export function FlowSearchPage() {
  const { timezone } = usePreferences();
  const navigate = useNavigate();
  const [mode, setMode] = useState<QueryMode>('basic');
  const modeTabs = useRef<Partial<Record<QueryMode, HTMLButtonElement | null>>>({});
  const [filters, setFilters] = useState<BasicFilter[]>([]);
  const [query, setQuery] = useState('');
  const [searchedQuery, setSearchedQuery] = useState('');
  const [flows, setFlows] = useState<FlowExecution[]>([]);
  const [nextPageToken, setNextPageToken] = useState('');
  const [pageTokens, setPageTokens] = useState<string[]>(['']);
  const [page, setPage] = useState(0);
  const [pageSize, setPageSize] = useState(50);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [recent, setRecent] = useState<string[]>([]);
  const [saved, setSaved] = useState<SavedQuery[]>([]);
  const [columns, setColumns] = useState<ColumnId[]>(defaultColumns);
  const [columnsOpen, setColumnsOpen] = useState(false);
  const [isGroupedByType, setIsGroupedByType] = useState(storedIsGroupedByType);
  const flowGroups = useMemo(() => (isGroupedByType ? flowsByType(flows) : null), [flows, isGroupedByType]);
  const designedFlowTypes = useDesignedFlowTypes(isGroupedByType);
  const [savedOpen, setSavedOpen] = useState(false);
  const [hydrated, setHydrated] = useState(false);

  const generatedQuery = useMemo(() => buildVisibilityQuery(filters), [filters]);
  const appliedQuery = mode === 'basic' ? generatedQuery : query;
  const quickStatus = quickStatusOf(parseVisibilityQuery(searchedQuery));
  // A search that is just one status chip keeps the card one row; Add filter opens the rows.
  const isQuickStatusSearch = mode === 'basic' && quickStatus !== null && generatedQuery === searchedQuery;
  const customAttributes = useMemo(() => selectVisibleIndexedAttributeKeys(flows), [flows]);

  const executeSearch = useCallback(async (
    requestedQuery: string,
    requestedToken = '',
    requestedPage = 0,
    requestedPageSize = 50,
  ) => {
    setLoading(true);
    setError('');
    try {
      const response = await fetch(webURL('/api/flows/search'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          query: requestedQuery,
          pageSize: requestedPageSize,
          nextPageToken: requestedToken,
        }),
      });
      const data = await readResponseJSON<SearchFlowsResult>(response);
      setFlows(data.flows);
      setSearchedQuery(requestedQuery);
      setNextPageToken(data.nextPageToken);
      setPage(requestedPage);
      const url = new URL(window.location.href);
      if (requestedQuery) url.searchParams.set('q', requestedQuery);
      else url.searchParams.delete('q');
      url.searchParams.set('size', String(requestedPageSize));
      if (requestedPage) url.searchParams.set('page', String(requestedPage + 1));
      else url.searchParams.delete('page');
      window.history.replaceState(null, '', url);
      rememberFlowsSearch(url.search);
      if (requestedQuery.trim()) {
        setRecent((current) => {
          const next = [requestedQuery, ...current.filter((item) => item !== requestedQuery)].slice(0, 10);
          window.localStorage.setItem(scopedStorageKey(recentQueriesStorageKey), JSON.stringify(next));
          return next;
        });
      }
    } catch (searchError) {
      setError(searchError instanceof Error ? searchError.message : 'Search failed');
      setFlows([]);
      setNextPageToken('');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const initialQuery = params.get('q') || '';
    const initialPageSize = Number(params.get('size') || 50);
    setPageSize(initialPageSize);
    setQuery(initialQuery);
    const parsed = parseVisibilityQuery(initialQuery);
    if (parsed) {
      setFilters(parsed);
      setMode('basic');
    } else {
      setMode('advanced');
    }
    setRecent(readJSON(scopedStorageKey(recentQueriesStorageKey), []));
    setSaved(readJSON(scopedStorageKey(savedQueriesStorageKey), []));
    const storedColumns = normalizeColumnOrder(readJSON('dex-web-columns', defaultColumns));
    setColumns(storedColumns);
    window.localStorage.setItem('dex-web-columns', JSON.stringify(storedColumns));
    setHydrated(true);
    void executeSearch(initialQuery, '', 0, initialPageSize);
  }, [executeSearch]);

  function applySearch() {
    setPageTokens(['']);
    void executeSearch(appliedQuery, '', 0, pageSize);
  }

  /** Switches the query mode; returns whether the mode now matches the request. */
  function switchMode(next: QueryMode): boolean {
    if (next === mode) return true;
    if (next === 'advanced') {
      setQuery(generatedQuery);
      setMode(next);
      return true;
    }
    const parsed = parseVisibilityQuery(query);
    if (!parsed) {
      setError('This search query uses syntax the Filters tab cannot represent.');
      return false;
    }
    setFilters(parsed);
    setMode(next);
    return true;
  }

  /** Moves between the query mode tabs with the arrow, Home, and End keys. */
  function handleModeTabKeyDown(event: KeyboardEvent<HTMLDivElement>) {
    const modes: QueryMode[] = ['basic', 'advanced'];
    const current = modes.indexOf(mode);
    let target: QueryMode;
    if (event.key === 'ArrowRight') target = modes[(current + 1) % modes.length];
    else if (event.key === 'ArrowLeft') target = modes[(current + modes.length - 1) % modes.length];
    else if (event.key === 'Home') target = modes[0];
    else if (event.key === 'End') target = modes[modes.length - 1];
    else return;
    event.preventDefault();
    if (switchMode(target)) modeTabs.current[target]?.focus();
  }

  // One click per status: the filters become just that status, and the search runs.
  function searchByStatus(status: QuickStatus) {
    const nextFilters = status === ALL_STATUSES ? [] : [{ ...newFilter(), value: status }];
    const nextQuery = buildVisibilityQuery(nextFilters);
    setMode('basic');
    setFilters(nextFilters);
    setQuery(nextQuery);
    setPageTokens(['']);
    void executeSearch(nextQuery, '', 0, pageSize);
  }

  function loadQuery(nextQuery: string) {
    setQuery(nextQuery);
    const parsed = parseVisibilityQuery(nextQuery);
    if (parsed) {
      setFilters(parsed);
      setMode('basic');
    } else {
      setMode('advanced');
    }
    setPageTokens(['']);
    void executeSearch(nextQuery, '', 0, pageSize);
  }

  function saveCurrentQuery() {
    const name = window.prompt('Name this query');
    if (!name?.trim() || !appliedQuery.trim()) return;
    const next = [
      { name: name.trim(), query: appliedQuery },
      ...saved.filter((item) => item.name !== name.trim()),
    ];
    setSaved(next);
    window.localStorage.setItem(scopedStorageKey(savedQueriesStorageKey), JSON.stringify(next));
  }

  function toggleColumn(column: ColumnId) {
    const next = columns.includes(column)
      ? columns.filter((item) => item !== column)
      : [...columns, column];
    if (!next.length) return;
    setColumns(next);
    window.localStorage.setItem('dex-web-columns', JSON.stringify(next));
  }

  function moveColumn(column: ColumnId, direction: -1 | 1) {
    const index = columns.indexOf(column);
    const target = index + direction;
    if (target < 0 || target >= columns.length) return;
    const next = [...columns];
    [next[index], next[target]] = [next[target], next[index]];
    setColumns(next);
    window.localStorage.setItem('dex-web-columns', JSON.stringify(next));
  }

  function nextPage() {
    if (!nextPageToken) return;
    const nextTokens = [...pageTokens, nextPageToken];
    setPageTokens(nextTokens);
    void executeSearch(appliedQuery, nextPageToken, page + 1, pageSize);
  }

  function previousPage() {
    if (page === 0) return;
    const nextPage = page - 1;
    const nextTokens = pageTokens.slice(0, -1);
    setPageTokens(nextTokens);
    void executeSearch(appliedQuery, nextTokens[nextPage] || '', nextPage, pageSize);
  }

  if (!hydrated) return <div className="page-loading">Loading flow search…</div>;

  return (
    <div className="page-shell flow-search-page v1-page">
      <section className={`card query-card${isQuickStatusSearch ? ' is-quick-status' : ''}`}>
        <div className="query-toolbar">
          <div className="segmented" role="tablist" aria-label="Query mode" onKeyDown={handleModeTabKeyDown}>
            <button
              aria-controls={mode === 'basic' ? 'flow-search-filters-panel' : undefined}
              aria-selected={mode === 'basic'}
              className={mode === 'basic' ? 'active' : ''}
              id="flow-search-filters-tab"
              onClick={() => switchMode('basic')}
              ref={(node) => { modeTabs.current.basic = node; }}
              role="tab"
              tabIndex={mode === 'basic' ? 0 : -1}
              type="button"
            >
              Filters
            </button>
            <button
              aria-controls={mode === 'advanced' ? 'flow-search-query-panel' : undefined}
              aria-selected={mode === 'advanced'}
              className={mode === 'advanced' ? 'active' : ''}
              id="flow-search-query-tab"
              onClick={() => switchMode('advanced')}
              ref={(node) => { modeTabs.current.advanced = node; }}
              role="tab"
              tabIndex={mode === 'advanced' ? 0 : -1}
              type="button"
            >
              Query
            </button>
          </div>
          <div aria-label="Status" className="status-quick-filters" role="group">
            {QUICK_STATUSES.map((status) => (
              <button
                aria-pressed={quickStatus === status}
                className={quickStatus === status ? 'active' : ''}
                key={status}
                onClick={() => searchByStatus(status)}
                type="button"
              >
                {status === ALL_STATUSES ? 'All' : status}
              </button>
            ))}
          </div>
          <div className="query-actions">
            <button aria-expanded={savedOpen} className="button quiet" onClick={() => setSavedOpen(!savedOpen)} type="button">
              <History aria-hidden="true" size={15} strokeWidth={2} />
              Queries
            </button>
            <button className="button quiet" onClick={saveCurrentQuery} type="button">
              <Bookmark aria-hidden="true" size={15} strokeWidth={2} />
              Save query
            </button>
          </div>
        </div>

        {savedOpen && (
          <div className="saved-query-panel">
            <div>
              <strong>Named queries</strong>
              {saved.length === 0 && <span className="muted">No saved queries</span>}
              {saved.map((item) => (
                <button key={item.name} onClick={() => loadQuery(item.query)}>
                  <b>{item.name}</b><span>{item.query}</span>
                </button>
              ))}
            </div>
            <div>
              <strong>Recent</strong>
              {recent.length === 0 && <span className="muted">No recent queries</span>}
              {recent.map((item) => <button key={item} onClick={() => loadQuery(item)}>{item}</button>)}
            </div>
          </div>
        )}

        {mode === 'basic' ? (
          <div aria-labelledby="flow-search-filters-tab" className="filter-list" id="flow-search-filters-panel" role="tabpanel">
            {filters.map((filter) => (
              <div className="filter-row" key={filter.id}>
                <input
                  list="visibility-fields"
                  aria-label="Filter field"
                  value={filter.field}
                  onChange={(event) => setFilters(filters.map((item) => (
                    item.id === filter.id ? { ...item, field: event.target.value } : item
                  )))}
                />
                <select
                  aria-label="Filter operator"
                  value={filter.operator}
                  onChange={(event) => setFilters(filters.map((item) => (
                    item.id === filter.id
                      ? { ...item, operator: event.target.value as QueryOperator }
                      : item
                  )))}
                >
                  {['=', '!=', '>', '>=', '<', '<='].map((operator) => (
                    <option key={operator}>{operator}</option>
                  ))}
                </select>
                {filter.field === 'ExecutionStatus' ? (
                  <select
                    aria-label="Filter value"
                    value={filter.value}
                    onChange={(event) => setFilters(filters.map((item) => (
                      item.id === filter.id ? { ...item, value: event.target.value } : item
                    )))}
                  >
                    {['Running', 'Completed', 'Failed', 'TimedOut', 'Terminated', 'Canceled']
                      .map((status) => <option key={status}>{status}</option>)}
                  </select>
                ) : (
                  <input
                    aria-label="Filter value"
                    value={filter.value}
                    placeholder="Value"
                    onChange={(event) => setFilters(filters.map((item) => (
                      item.id === filter.id ? { ...item, value: event.target.value } : item
                    )))}
                  />
                )}
                <button
                  className="icon-button"
                  aria-label="Remove filter"
                  onClick={() => setFilters(filters.filter((item) => item.id !== filter.id))}
                >
                  <X aria-hidden="true" size={16} strokeWidth={2} />
                </button>
              </div>
            ))}
            <datalist id="visibility-fields">
              {builtInFields.map((field) => <option value={field} key={field} />)}
            </datalist>
            <button className="text-button" onClick={() => setFilters([...filters, newFilter('FlowType')])} type="button">
              <Plus aria-hidden="true" size={15} strokeWidth={2} />
              Add filter
            </button>
            {generatedQuery && (
              <div className="query-preview">
                <span>Generated query</span>
                <code>{generatedQuery}</code>
              </div>
            )}
          </div>
        ) : (
          <div aria-labelledby="flow-search-query-tab" id="flow-search-query-panel" role="tabpanel">
            <label className="advanced-query">
              <span>Search query</span>
              <textarea
                value={query}
                rows={4}
                placeholder='ExecutionStatus = "Running" AND FlowType = "Checkout"'
                onChange={(event) => setQuery(event.target.value)}
              />
            </label>
          </div>
        )}

        <div className="query-submit">
          <button
            className="button ghost"
            onClick={() => {
              setFilters([]);
              setQuery('');
              setPageTokens(['']);
              void executeSearch('', '', 0, pageSize);
            }}
          >
            Clear
          </button>
          <button className="button primary" disabled={loading} onClick={applySearch} type="button">
            <Search aria-hidden="true" size={15} strokeWidth={2.25} />
            {loading ? 'Searching…' : 'Search'}
          </button>
        </div>
      </section>

      {error && <div className="error-banner">{error}</div>}

      <section className="card results-card">
        <div className="results-toolbar">
          <div>
            <strong>{flows.length}</strong> {flows.length === 1 ? 'flow' : 'flows'}
          </div>
          <div className="results-controls">
            <label>
              Page size
              <select
                value={pageSize}
                onChange={(event) => {
                  const size = Number(event.target.value);
                  setPageSize(size);
                  setPageTokens(['']);
                  void executeSearch(appliedQuery, '', 0, size);
                }}
              >
                {[20, 50, 100, 200].map((size) => <option key={size}>{size}</option>)}
              </select>
            </label>
            <button
              aria-pressed={isGroupedByType}
              className="button quiet flow-group-toggle"
              onClick={() => {
                persistIsGroupedByType(!isGroupedByType);
                setIsGroupedByType(!isGroupedByType);
              }}
              type="button"
            >
              <Layers aria-hidden="true" size={15} strokeWidth={2} />
              Group by type
            </button>
            <div className="popover-wrap">
              <button aria-expanded={columnsOpen} className="button quiet" onClick={() => setColumnsOpen(!columnsOpen)} type="button">
                <Columns3 aria-hidden="true" size={15} strokeWidth={2} />
                Columns
              </button>
              {columnsOpen && (
                <div className="column-popover">
                  {allColumns.map((column) => (
                    <div key={column}>
                      <label>
                        <input
                          type="checkbox"
                          checked={columns.includes(column)}
                          onChange={() => toggleColumn(column)}
                        />
                        {columnLabels[column]}
                      </label>
                      {columns.includes(column) && (
                        <span>
                          <button aria-label={`Move ${columnLabels[column]} up`} onClick={() => moveColumn(column, -1)} type="button">
                            <ArrowUp aria-hidden="true" size={14} strokeWidth={2} />
                          </button>
                          <button aria-label={`Move ${columnLabels[column]} down`} onClick={() => moveColumn(column, 1)} type="button">
                            <ArrowDown aria-hidden="true" size={14} strokeWidth={2} />
                          </button>
                        </span>
                      )}
                    </div>
                  ))}
                </div>
              )}
            </div>
          </div>
        </div>

        <div className="table-scroll">
          <table className="flow-table">
            <thead>
              <tr>
                {columns.map((column) => (
                  <th className={`column-${column}`} key={column}>{columnLabels[column]}</th>
                ))}
                {customAttributes.map((key) => <th key={key}>{key}</th>)}
              </tr>
            </thead>
            <tbody>
              {!loading && (flowGroups ?? [{ flowType: '', flows }]).flatMap((group) => [
                ...(flowGroups ? [(
                  <tr className="flow-group-row" key={`group-${group.flowType}`}>
                    <td colSpan={columns.length + customAttributes.length}>
                      <span className="flow-group-name">{group.flowType}</span>
                      <span className="flow-group-counts">{flowGroupCountsLabel(group.flows)}</span>
                      {designedFlowTypes.has(group.flowType) && (
                        <Link className="flow-group-design" to={`${DESIGNS_PATH}?${new URLSearchParams({ flowType: group.flowType })}`}>
                          Design ›
                        </Link>
                      )}
                    </td>
                  </tr>
                )] : []),
                ...group.flows.map((flow) => (
                <tr
                  className="flow-row"
                  key={`${flow.flowId}-${flow.runId}`}
                  onClick={(event) => {
                    // The Flow ID link handles its own click; the rest of the row opens the run too.
                    if ((event.target as HTMLElement).closest('a, button') || window.getSelection()?.toString()) return;
                    navigate(`/v1/flows/${encodeURIComponent(flow.flowId)}/${encodeURIComponent(flow.runId)}`);
                  }}
                >
                  {columns.map((column) => (
                    <td className={`column-${column}`} key={column}>
                      {renderCell(column, flow, timezone)}
                    </td>
                  ))}
                  {customAttributes.map((key) => (
                    <td key={key}>
                      {displayValue(flow.indexedAttributes.find((item) => item.key === key)?.value)}
                    </td>
                  ))}
                </tr>
                )),
              ])}
            </tbody>
          </table>
          {!loading && flows.length === 0 && (
            <div className="empty-state">
              <span><Inbox aria-hidden="true" size={28} strokeWidth={1.75} /></span>
              <h3>No flows found</h3>
              <p>Adjust the query or clear filters to search all visible executions.</p>
              <p>
                Getting started? Try the{' '}
                <a
                  href="https://github.com/superdurable/dex/tree/main/examples"
                  rel="noreferrer"
                  target="_blank"
                >
                  Dex examples
                </a>
                .
              </p>
            </div>
          )}
          {loading && <div className="table-loading">Loading flows…</div>}
        </div>
        <div className="pagination">
          <button className="button quiet" disabled={page === 0 || loading} onClick={previousPage} type="button">
            <ChevronLeft aria-hidden="true" size={15} strokeWidth={2} />
            Previous
          </button>
          <span>Page {page + 1}</span>
          <button className="button quiet" disabled={!nextPageToken || loading} onClick={nextPage} type="button">
            Next
            <ChevronRight aria-hidden="true" size={15} strokeWidth={2} />
          </button>
        </div>
      </section>
    </div>
  );
}

function renderCell(column: ColumnId, flow: FlowExecution, timezone: 'local' | 'UTC') {
  switch (column) {
    case 'status':
      return <StatusBadge status={flow.flowStatus} />;
    case 'flowId':
      return (
        <Link
          className="table-link table-id table-id-flow"
          to={`/v1/flows/${encodeURIComponent(flow.flowId)}/${encodeURIComponent(flow.runId)}`}
          title={flow.flowId}
        >
          {listFlowIdOf(flow.flowId)}
        </Link>
      );
    case 'runId':
      return <span className="mono table-id table-id-run" title={flow.runId}>{flow.runId}</span>;
    case 'flowType':
      return flow.flowType || '—';
    case 'start':
      return formatDate(flow.startTime, timezone);
    case 'close':
      return formatDate(flow.closeTime, timezone);
    case 'duration':
      return formatDuration(flow.startTime, flow.closeTime);
  }
}
