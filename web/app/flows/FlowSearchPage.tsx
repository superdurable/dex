'use client';

import Link from 'next/link';
import { useCallback, useEffect, useMemo, useState } from 'react';
import { displayValue, formatDate, formatDuration } from '@/lib/format';
import {
  buildVisibilityQuery,
  parseVisibilityQuery,
  type BasicFilter,
  type QueryOperator,
} from '@/lib/query';
import type { FlowExecution, SearchFlowsResult } from '@/lib/types';
import { StatusBadge } from '../components/StatusBadge';
import { usePreferences } from '../providers';

type QueryMode = 'basic' | 'advanced';
type ColumnId = 'status' | 'flowId' | 'runId' | 'flowType' | 'start' | 'close' | 'duration';

const defaultColumns: ColumnId[] = [
  'status',
  'flowId',
  'flowType',
  'runId',
  'start',
  'close',
  'duration',
];

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

const hiddenSearchAttributes = new Set(['TemporalChangeVersion']);

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

export function FlowSearchPage() {
  const { timezone } = usePreferences();
  const [mode, setMode] = useState<QueryMode>('basic');
  const [filters, setFilters] = useState<BasicFilter[]>([]);
  const [query, setQuery] = useState('');
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
  const [savedOpen, setSavedOpen] = useState(false);
  const [hydrated, setHydrated] = useState(false);

  const generatedQuery = useMemo(() => buildVisibilityQuery(filters), [filters]);
  const appliedQuery = mode === 'basic' ? generatedQuery : query;
  const customAttributes = useMemo(() => {
    const keys = new Set<string>();
    flows.forEach((flow) => flow.searchAttributes.forEach((item) => {
      if (!hiddenSearchAttributes.has(item.key)) keys.add(item.key);
    }));
    return [...keys].sort();
  }, [flows]);

  const executeSearch = useCallback(async (
    requestedQuery: string,
    requestedToken = '',
    requestedPage = 0,
    requestedPageSize = 50,
  ) => {
    setLoading(true);
    setError('');
    try {
      const response = await fetch('/api/flows/search', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          query: requestedQuery,
          pageSize: requestedPageSize,
          nextPageToken: requestedToken,
        }),
      });
      const data = await response.json() as SearchFlowsResult & { error?: string };
      if (!response.ok) throw new Error(data.error || 'Search failed');
      setFlows(data.flows);
      setNextPageToken(data.nextPageToken);
      setPage(requestedPage);
      const url = new URL(window.location.href);
      if (requestedQuery) url.searchParams.set('q', requestedQuery);
      else url.searchParams.delete('q');
      url.searchParams.set('size', String(requestedPageSize));
      if (requestedPage) url.searchParams.set('page', String(requestedPage + 1));
      else url.searchParams.delete('page');
      window.history.replaceState(null, '', url);
      if (requestedQuery.trim()) {
        setRecent((current) => {
          const next = [requestedQuery, ...current.filter((item) => item !== requestedQuery)].slice(0, 10);
          window.localStorage.setItem('dex-web-recent-queries', JSON.stringify(next));
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
    setRecent(readJSON('dex-web-recent-queries', []));
    setSaved(readJSON('dex-web-saved-queries', []));
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

  function switchMode(next: QueryMode) {
    if (next === mode) return;
    if (next === 'advanced') {
      setQuery(generatedQuery);
      setMode(next);
      return;
    }
    const parsed = parseVisibilityQuery(query);
    if (!parsed) {
      setError('This advanced query uses syntax the Basic editor cannot represent.');
      return;
    }
    setFilters(parsed);
    setMode(next);
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
    window.localStorage.setItem('dex-web-saved-queries', JSON.stringify(next));
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
    <div className="page-shell">
      <section className="page-heading">
        <div>
          <h1>Dex</h1>
          <p>Redefine Durable Execution. Dead Simple. More Power.</p>
        </div>
      </section>

      <section className="card query-card">
        <div className="query-toolbar">
          <div className="segmented" role="tablist" aria-label="Query mode">
            <button className={mode === 'basic' ? 'active' : ''} onClick={() => switchMode('basic')}>
              Basic
            </button>
            <button className={mode === 'advanced' ? 'active' : ''} onClick={() => switchMode('advanced')}>
              Advanced
            </button>
          </div>
          <div className="query-actions">
            <button className="button ghost" onClick={() => setSavedOpen(!savedOpen)}>
              Queries
            </button>
            <button className="button ghost" onClick={saveCurrentQuery}>Save query</button>
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
          <div className="filter-list">
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
                    {['Running', 'Completed', 'Failed', 'TimedOut', 'Terminated', 'Canceled', 'ContinuedAsNew']
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
                  ×
                </button>
              </div>
            ))}
            <datalist id="visibility-fields">
              {builtInFields.map((field) => <option value={field} key={field} />)}
            </datalist>
            <button className="text-button" onClick={() => setFilters([...filters, newFilter('FlowType')])}>
              + Add filter
            </button>
            <div className="query-preview">
              <span>Generated query</span>
              <code>{generatedQuery || 'All flows'}</code>
            </div>
          </div>
        ) : (
          <label className="advanced-query">
            <span>Visibility query</span>
            <textarea
              value={query}
              rows={4}
              placeholder='ExecutionStatus = "Running" AND FlowType = "Checkout"'
              onChange={(event) => setQuery(event.target.value)}
            />
          </label>
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
          <button className="button primary" disabled={loading} onClick={applySearch}>
            {loading ? 'Searching…' : 'Search'}
          </button>
        </div>
      </section>

      {error && <div className="error-banner">{error}</div>}

      <section className="card results-card">
        <div className="results-toolbar">
          <div>
            <strong>{flows.length}</strong> flows on page {page + 1}
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
            <div className="popover-wrap">
              <button className="button ghost" onClick={() => setColumnsOpen(!columnsOpen)}>
                Columns
              </button>
              {columnsOpen && (
                <div className="column-popover">
                  {defaultColumns.map((column) => (
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
                          <button onClick={() => moveColumn(column, -1)}>↑</button>
                          <button onClick={() => moveColumn(column, 1)}>↓</button>
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
              {!loading && flows.map((flow) => (
                <tr key={`${flow.flowId}-${flow.runId}`}>
                  {columns.map((column) => (
                    <td className={`column-${column}`} key={column}>
                      {renderCell(column, flow, timezone)}
                    </td>
                  ))}
                  {customAttributes.map((key) => (
                    <td key={key}>
                      {displayValue(flow.searchAttributes.find((item) => item.key === key)?.value)}
                    </td>
                  ))}
                </tr>
              ))}
            </tbody>
          </table>
          {!loading && flows.length === 0 && (
            <div className="empty-state">
              <span>⌁</span>
              <h3>No flows found</h3>
              <p>Adjust the query or clear filters to search all visible executions.</p>
            </div>
          )}
          {loading && <div className="table-loading">Loading flows…</div>}
        </div>
        <div className="pagination">
          <button className="button ghost" disabled={page === 0 || loading} onClick={previousPage}>
            Previous
          </button>
          <span>Page {page + 1}</span>
          <button className="button ghost" disabled={!nextPageToken || loading} onClick={nextPage}>
            Next
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
          href={`/flows/${encodeURIComponent(flow.flowId)}/${encodeURIComponent(flow.runId)}`}
          title={flow.flowId}
        >
          {flow.flowId}
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
