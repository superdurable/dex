// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useRef, useState, type ChangeEventHandler, type CSSProperties } from 'react';
import { Navigate, useNavigate, useParams } from 'react-router-dom';
import type {
  FlowSupervisionAction,
  FlowSupervisionActionInputField,
  FlowSupervisionDefinition,
  SupervisionValueType,
} from '@superdurable/flow-definition-renderer';
import { displayValue, formatDate } from '@/lib/format';
import { readResponseJSON } from '@/lib/http';
import type { SupervisionDisplay, SupervisionFlow, SupervisionSearchResult } from '@/lib/types';
import { usePreferences } from '../providers';
import {
  parseTypedValue,
  v2ActionUserFields,
  v2ActionUserInput,
  v2FlowPath,
  v2ListColumns,
  visibleV2Actions,
} from './contract';
import './css/v2.css';
import { V2Canvas } from './V2Canvas';
import {
  CASE_HEIGHT_KEY,
  LIST_WIDTH_DEFAULT,
  LIST_WIDTH_KEY,
  V2SplitHandle,
  readStoredPixels,
  writeStoredPixels,
} from './V2SplitHandle';
import { useWebCatalog } from './WebCatalogProvider';

interface FilterRow {
  id: string;
  field: string;
  operator: string;
  value: string;
}

export function HomePage() {
  const { ready, canUseV2, error } = useWebCatalog();
  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  if (!ready) return <div className="page-loading">Loading Dex Web…</div>;
  return <Navigate to={canUseV2 ? '/v2' : '/v1/flows'} replace />;
}

export function V2Workspace() {
  const { flowType = '', flowId = '' } = useParams();
  const navigate = useNavigate();
  const { ready, canUseV2, catalog, error } = useWebCatalog();
  const { timezone } = usePreferences();
  const entry = catalog?.flows.find((candidate) => candidate.flowType === flowType);
  const [filters, setFilters] = useState<FilterRow[]>([]);
  const [flows, setFlows] = useState<SupervisionFlow[]>([]);
  const [loading, setLoading] = useState(false);
  const [searchError, setSearchError] = useState('');
  const [nextPageToken, setNextPageToken] = useState('');
  const [pageTokens, setPageTokens] = useState<string[]>(['']);
  const [page, setPage] = useState(0);
  const shellRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const listPaneRef = useRef<HTMLElement>(null);
  const [listWidth, setListWidth] = useState(() => {
    const stored = readStoredPixels(LIST_WIDTH_KEY);
    return Number.isFinite(stored) ? stored : LIST_WIDTH_DEFAULT;
  });
  const [caseHeight, setCaseHeight] = useState(() => readStoredPixels(CASE_HEIGHT_KEY));

  const commitListWidth = useCallback((width: number) => {
    const next = Math.round(width);
    setListWidth(next);
    writeStoredPixels(LIST_WIDTH_KEY, next);
  }, []);

  const commitCaseHeight = useCallback((height: number) => {
    const next = Math.round(height);
    setCaseHeight(next);
    writeStoredPixels(CASE_HEIGHT_KEY, next);
  }, []);

  const executeSearch = useCallback(async (token = '', nextPage = 0) => {
    if (!entry) return;
    setLoading(true);
    setSearchError('');
    try {
      const response = await fetch('/api/supervision/search', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowType: entry.flowType,
          filters: filters.map((filter) => ({
            field: filter.field,
            operator: filter.operator,
            values: parseFilterValues(filter.value, filterValueType(filter.field, entry.definition)),
          })),
          pageSize: 50,
          nextPageToken: token,
        }),
      });
      const result = await readResponseJSON<SupervisionSearchResult>(response);
      setFlows(result.flows);
      setNextPageToken(result.nextPageToken);
      setPage(nextPage);
    } catch (failedSearch) {
      setSearchError(failedSearch instanceof Error ? failedSearch.message : 'Search failed');
      setFlows([]);
    } finally {
      setLoading(false);
    }
  }, [entry, filters]);

  useEffect(() => {
    if (entry) void executeSearch();
  }, [entry]);

  if (!ready) return <div className="page-loading">Loading Dex Web…</div>;
  if (!canUseV2) return <Navigate to="/v1/flows" replace />;
  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  if (!catalog) return <div className="page-loading">Loading Dex Web…</div>;
  if (catalog.flows.length === 0) {
    return (
      <div className="v2-shell sv">
        <div className="v2-empty">Load a valid Flow Definition Graph 2.0 file to search runs in v2.</div>
      </div>
    );
  }
  if (!flowType) {
    return <Navigate to={v2FlowPath(catalog.flows[0].flowType)} replace />;
  }
  if (!entry) return <Navigate to="/v2" replace />;

  const fields = filterFields(entry.definition);
  const columns = v2ListColumns(entry.definition);
  const paneStyle = {
    '--v2-list-w': `${listWidth}px`,
    ...(Number.isFinite(caseHeight) && caseHeight > 0 ? { '--v2-case-h': `${caseHeight}px` } : {}),
  } as CSSProperties;
  return (
    <div className="v2-shell sv" ref={shellRef} style={paneStyle}>
      <div className="sv-body" ref={bodyRef}>
        <aside className="sq" ref={listPaneRef} data-has-case={flowId ? 'true' : undefined}>
          <div className="sq-head">
            <span className="sq-title">{entry.flowType}</span>
            <span className="sq-live">current runs</span>
          </div>
          {catalog.flows.length > 1 && (
            <div className="sv-choose">
              <span className="sv-chooselabel">Flow type</span>
              {catalog.flows.map((candidate) => (
                <button
                  aria-pressed={candidate.flowType === entry.flowType}
                  className="sv-flow"
                  key={candidate.flowType}
                  onClick={() => navigate(v2FlowPath(candidate.flowType))}
                  type="button"
                >
                  {candidate.flowType}
                </button>
              ))}
            </div>
          )}
          <div className="v2-filters">
            {filters.map((filter) => (
              <div className="v2-filter-row" key={filter.id}>
                <select value={filter.field} onChange={(event) => setFilters(filters.map((item) => (
                  item.id === filter.id ? { ...item, field: event.target.value, operator: 'eq', value: '' } : item
                )))}>
                  {fields.map((field) => <option key={field.key} value={field.key}>{field.label}</option>)}
                </select>
                <select value={filter.operator} onChange={(event) => setFilters(updateFilter(filters, filter.id, 'operator', event.target.value))}>
                  {filterOperators(filterIndexType(filter.field, entry.definition)).map((operator) => (
                    <option key={operator.value} value={operator.value}>{operator.label}</option>
                  ))}
                </select>
                <FilterInput
                  operator={filter.operator}
                  value={filter.value}
                  valueType={filterValueType(filter.field, entry.definition)}
                  onChange={(event) => setFilters(updateFilter(filters, filter.id, 'value', event.target.value))}
                />
                <button className="v2-ghost" onClick={() => setFilters(filters.filter((item) => item.id !== filter.id))} type="button">×</button>
              </div>
            ))}
          </div>
          <div className="v2-filter-actions">
            <button className="v2-ghost" onClick={() => setFilters((current) => [
              ...current,
              { id: `${Date.now()}-${Math.random()}`, field: 'executionStatus', operator: 'eq', value: 'Running' },
            ])} type="button">Add filter</button>
            <button className="v2-primary" disabled={loading} onClick={() => {
              setPageTokens(['']);
              void executeSearch();
            }} type="button">{loading ? 'Searching…' : 'Search'}</button>
          </div>
          {searchError && <p className="v2-error">{searchError}</p>}
          <ol className="v2-list sq-list">
            {flows.map((flow) => (
              <li
                className="sq-item"
                data-selected={flow.flowId === flowId ? 'true' : undefined}
                key={flow.flowId}
              >
                <button
                  className="sq-row"
                  onClick={() => navigate(v2FlowPath(entry.flowType, flow.flowId))}
                  type="button"
                >
                  <span className="sq-run t-mono" title={flow.flowId}>{flow.flowId}</span>
                  <span className="sq-attention">{formatDate(flow.startTime, timezone)}</span>
                  <span className="sq-step t-mono">{flow.flowStatus}</span>
                  <span className="v2-columns">
                    {columns.map((column) => (
                      <span className="v2-column" key={`${column.source}:${column.key}`}>
                        {column.source === 'summary' && flow.summaryError
                          ? 'Unavailable'
                          : displayValue(column.source === 'indexed' ? flow.indexedAttributes[column.key] : flow.summary?.[column.key])}
                      </span>
                    ))}
                  </span>
                </button>
              </li>
            ))}
            {!loading && flows.length === 0 && <li className="v2-empty">No matching Flows</li>}
          </ol>
          <div className="v2-filter-actions">
            <button className="v2-ghost" disabled={page === 0 || loading} onClick={() => {
              const previous = page - 1;
              const tokens = pageTokens.slice(0, -1);
              setPageTokens(tokens);
              void executeSearch(tokens[previous] ?? '', previous);
            }} type="button">Previous</button>
            <span className="sq-live">Page {page + 1}</span>
            <button className="v2-ghost" disabled={!nextPageToken || loading} onClick={() => {
              setPageTokens([...pageTokens, nextPageToken]);
              void executeSearch(nextPageToken, page + 1);
            }} type="button">Next</button>
          </div>
          {flowId ? (
            <>
              <V2SplitHandle
                axis="row"
                cssVariable="--v2-case-h"
                targetRef={shellRef}
                measureRef={listPaneRef}
                value={Number.isFinite(caseHeight) ? caseHeight : 0}
                ariaLabel="Resize the Display pane"
                onCommit={commitCaseHeight}
              />
              <SelectedRunPanel flowType={entry.flowType} flowId={flowId} definition={entry.definition} />
            </>
          ) : (
            <p className="sq-state">Select a run to edit fields and invoke Actions.</p>
          )}
          <V2SplitHandle
            axis="column"
            cssVariable="--v2-list-w"
            targetRef={shellRef}
            measureRef={bodyRef}
            value={listWidth}
            ariaLabel="Resize the listing pane"
            onCommit={commitListWidth}
          />
        </aside>
        <section className="v2-canvas" aria-label="Flow definition">
          <V2Canvas flowType={entry.flowType} flowId={flowId} />
        </section>
      </div>
    </div>
  );
}

function SelectedRunPanel({
  flowType,
  flowId,
  definition,
}: {
  flowType: string;
  flowId: string;
  definition: FlowSupervisionDefinition;
}) {
  const [result, setResult] = useState<SupervisionDisplay | null>(null);
  const [error, setError] = useState('');
  const [busyKey, setBusyKey] = useState('');
  const [editingKey, setEditingKey] = useState('');
  const [editValue, setEditValue] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [actionValues, setActionValues] = useState<Record<string, Record<string, string>>>({});

  const loadDisplay = useCallback(async () => {
    setError('');
    try {
      const query = new URLSearchParams({ flowType, flowId });
      const response = await fetch(`/api/supervision/display?${query}`);
      setResult(await readResponseJSON<SupervisionDisplay>(response));
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : 'Display failed to load');
    }
  }, [flowId, flowType]);

  useEffect(() => { void loadDisplay(); }, [loadDisplay]);

  async function saveField(attributeKey: string, valueType: SupervisionValueType) {
    setBusyKey(attributeKey);
    setError('');
    setFieldErrors((current) => ({ ...current, [attributeKey]: '' }));
    try {
      const response = await fetch('/api/supervision/display', {
        method: 'PATCH', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowType, flowId, attributeKey,
          value: parseTypedValue(editValue, valueType),
        }),
      });
      await readResponseJSON(response);
      setEditingKey('');
      await loadDisplay();
    } catch (saveError) {
      setFieldErrors((current) => ({
        ...current,
        [attributeKey]: saveError instanceof Error ? saveError.message : 'Edit failed',
      }));
    } finally {
      setBusyKey('');
    }
  }

  async function invokeAction(action: FlowSupervisionAction) {
    setBusyKey(action.rpcName);
    setError('');
    try {
      const input = v2ActionUserInput(action, actionValues[action.rpcName] ?? {});
      const response = await fetch('/api/supervision/actions', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowType, flowId, rpcName: action.rpcName,
          input, attributeSnapshot: result?.attributeSnapshot ?? {},
        }),
      });
      await readResponseJSON(response);
      await loadDisplay();
    } catch (actionError) {
      setError(actionError instanceof Error ? actionError.message : 'Action failed');
    } finally {
      setBusyKey('');
    }
  }

  return (
    <div className="v2-case sc">
      <div className="sc-head">
        <span className="sc-title">{flowId}</span>
        {result && <span className="sc-status">{result.flowStatus}</span>}
      </div>
      {error && <p className="v2-error">{error}</p>}
      {!result && !error && <p className="sc-state">Loading Display…</p>}
      {result && (
        <>
          <div className="sc-block">
            <div className="sc-blockhead">Display</div>
            <dl className="sc-facts">
              {definition.display.fields.map((field) => {
                const isEditing = editingKey === field.attributeKey;
                return (
                  <div className="sc-fact" key={field.attributeKey}>
                    <dt className="sc-fname">{field.description}</dt>
                    <dd className="sc-fvalue">
                      {isEditing ? (
                        <>
                          <TypedInput field={field} value={editValue} onChange={setEditValue} />
                          <button className="v2-primary" disabled={busyKey === field.attributeKey} onClick={() => void saveField(field.attributeKey, field.valueType)} type="button">Save</button>
                          <button className="v2-ghost" onClick={() => setEditingKey('')} type="button">Cancel</button>
                          {fieldErrors[field.attributeKey] && <small className="v2-error">{fieldErrors[field.attributeKey]}</small>}
                        </>
                      ) : (
                        <>
                          <span>{displayValue(result.display[field.attributeKey])}</span>
                          {field.editable && result.isActive && (
                            <button className="v2-ghost" onClick={() => {
                              setEditingKey(field.attributeKey);
                              setFieldErrors((current) => ({ ...current, [field.attributeKey]: '' }));
                              setEditValue(editableValue(result.display[field.attributeKey], field.valueType));
                            }} type="button">Edit</button>
                          )}
                        </>
                      )}
                    </dd>
                  </div>
                );
              })}
            </dl>
          </div>
          <div className="sc-block">
            <div className="sc-blockhead">Actions</div>
            {visibleV2Actions(definition.actions, result.eligibleActions).map((action) => {
              const userFields = v2ActionUserFields(action);
              return (
                <form className="sc-actions" key={action.rpcName} onSubmit={(event) => {
                  event.preventDefault();
                  void invokeAction(action);
                }}>
                  {userFields.map((field) => (
                    <label key={field.fieldName}>
                      <span className="sc-fname">{field.description}</span>
                      <ActionInput
                        field={field}
                        value={actionValues[action.rpcName]?.[field.fieldName] ?? ''}
                        onChange={(value) => setActionValues((current) => ({
                          ...current, [action.rpcName]: { ...current[action.rpcName], [field.fieldName]: value },
                        }))}
                      />
                    </label>
                  ))}
                  <button className="v2-primary" disabled={!result.isActive || busyKey === action.rpcName} type="submit">
                    {busyKey === action.rpcName ? 'Working…' : action.label}
                  </button>
                </form>
              );
            })}
            {definition.actions.length > 0 && result.eligibleActions.length === 0 && (
              <p className="sc-state">No Actions are available in the current state.</p>
            )}
          </div>
        </>
      )}
    </div>
  );
}

function filterFields(definition: FlowSupervisionDefinition) {
  return [
    { key: 'flowId', label: 'Flow ID' },
    { key: 'executionStatus', label: 'Execution status' },
    { key: 'startTime', label: 'Start time' },
    { key: 'closeTime', label: 'Close time' },
    ...definition.indexedAttributes.map((attribute) => ({ key: attribute.attributeKey, label: attribute.description })),
  ];
}

function filterValueType(field: string, definition: FlowSupervisionDefinition): SupervisionValueType {
  if (field === 'startTime' || field === 'closeTime') return 'datetime';
  if (field === 'flowId' || field === 'executionStatus') return 'string';
  return definition.indexedAttributes.find((attribute) => attribute.attributeKey === field)?.valueType ?? 'string';
}

function filterIndexType(field: string, definition: FlowSupervisionDefinition) {
  if (field === 'startTime' || field === 'closeTime') return 'datetime';
  if (field === 'flowId' || field === 'executionStatus') return 'keyword';
  return definition.indexedAttributes.find((attribute) => attribute.attributeKey === field)?.indexType ?? 'keyword';
}

function filterOperators(indexType: FlowSupervisionDefinition['indexedAttributes'][number]['indexType']) {
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

function parseFilterValues(value: string, valueType: SupervisionValueType): unknown[] {
  return value.split(',').map((part) => part.trim()).filter(Boolean).map((part) => parseTypedValue(part, valueType));
}

function FilterInput({ operator, value, valueType, onChange }: {
  operator: string;
  value: string;
  valueType: SupervisionValueType;
  onChange: ChangeEventHandler<HTMLInputElement | HTMLSelectElement>;
}) {
  if (valueType === 'bool') {
    return <select aria-label="Filter values" value={value} onChange={onChange}><option value="true">True</option><option value="false">False</option></select>;
  }
  const allowsAlternatives = operator === 'in';
  const type = allowsAlternatives
    ? 'text'
    : valueType === 'int64' || valueType === 'double'
      ? 'number'
      : valueType === 'datetime'
        ? 'datetime-local'
        : 'text';
  return <input aria-label="Filter values" placeholder={allowsAlternatives ? 'Separate alternatives with commas' : 'Value'} type={type} value={value} onChange={onChange} />;
}

function updateFilter(filters: FilterRow[], id: string, key: keyof FilterRow, value: string) {
  return filters.map((filter) => filter.id === id ? { ...filter, [key]: value } : filter);
}

function editableValue(value: unknown, valueType: SupervisionValueType): string {
  if (value === null || value === undefined) return '';
  const text = typeof value === 'string' ? value : String(value);
  if (valueType !== 'datetime') return text;
  const date = new Date(text);
  if (Number.isNaN(date.getTime())) return '';
  const localDate = new Date(date.getTime() - date.getTimezoneOffset() * 60_000);
  return localDate.toISOString().slice(0, 16);
}

function TypedInput({ field, value, onChange, required = false }: {
  field: { valueType: SupervisionValueType; description: string };
  value: string;
  onChange: (value: string) => void;
  required?: boolean;
}) {
  if (field.valueType === 'bool') {
    return <select aria-label={field.description} required={required} value={value} onChange={(event) => onChange(event.target.value)}>{!required && <option value="">Unset</option>}<option value="true">True</option><option value="false">False</option></select>;
  }
  return <input aria-label={field.description} required={required} type={field.valueType === 'datetime' ? 'datetime-local' : field.valueType === 'int64' || field.valueType === 'double' ? 'number' : 'text'} value={value} onChange={(event) => onChange(event.target.value)} />;
}

function ActionInput({ field, value, onChange }: {
  field: FlowSupervisionActionInputField;
  value: string;
  onChange: (value: string) => void;
}) {
  return <TypedInput field={field} required={field.required} value={value} onChange={onChange} />;
}
