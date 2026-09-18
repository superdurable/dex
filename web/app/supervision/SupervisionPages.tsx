// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useMemo, useState } from 'react';
import { Link, Navigate, useParams } from 'react-router-dom';
import type {
  FlowSupervisionAction,
  FlowSupervisionActionInputField,
  FlowSupervisionDefinition,
  SupervisionValueType,
} from '@superdurable/flow-definition-renderer';
import { StatusBadge } from '../components/StatusBadge';
import { displayValue, formatDate } from '@/lib/format';
import { readResponseJSON } from '@/lib/http';
import type {
  SupervisionDisplay,
  SupervisionFlow,
  SupervisionSearchResult,
} from '@/lib/types';
import { usePreferences } from '../providers';
import { useSupervision } from './SupervisionProvider';

interface FilterRow {
  id: string;
  field: string;
  operator: string;
  value: string;
}

export function HomePage() {
  const { catalog, mode } = useSupervision();
  if (!catalog) return <div className="page-loading">Loading Dex Web…</div>;
  return <Navigate to={supervisionHomePath(catalog.enabled, mode)} replace />;
}

export function SupervisionHomePage() {
  const { catalog, error } = useSupervision();
  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  if (!catalog) return <div className="page-loading">Loading supervision catalog…</div>;
  if (!catalog.enabled) {
    return (
      <div className="page-shell"><div className="card empty-state">
        <h3>Supervision is not configured</h3>
        <p>Load at least one valid Flow Definition Graph 2.0 file to enable this workspace.</p>
      </div></div>
    );
  }
  if (catalog.flows.length === 1) {
    return <Navigate to={`/supervision/${encodeURIComponent(catalog.flows[0].flowType)}`} replace />;
  }
  return (
    <div className="page-shell supervision-home">
      <section className="supervision-hero">
        <p className="eyebrow">Operator workspace</p>
        <h1>Supervision</h1>
        <p>Choose a Flow type to search current logical Flow executions.</p>
      </section>
      <div className="supervision-flow-types">
        {catalog.flows.map((entry) => (
          <Link className="card supervision-flow-type" key={entry.flowType} to={`/supervision/${encodeURIComponent(entry.flowType)}`}>
            <span>Flow type</span>
            <strong>{entry.flowType}</strong>
            <small>{entry.definition.indexedAttributes.length} indexed attributes · {entry.definition.actions.length} actions</small>
          </Link>
        ))}
      </div>
    </div>
  );
}

export function SupervisionListPage() {
  const { flowType = '' } = useParams();
  const decodedFlowType = flowType;
  const { catalog } = useSupervision();
  const { timezone } = usePreferences();
  const entry = catalog?.flows.find((candidate) => candidate.flowType === decodedFlowType);
  const [filters, setFilters] = useState<FilterRow[]>([]);
  const [flows, setFlows] = useState<SupervisionFlow[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [nextPageToken, setNextPageToken] = useState('');
  const [pageTokens, setPageTokens] = useState<string[]>(['']);
  const [page, setPage] = useState(0);

  const executeSearch = useCallback(async (token = '', nextPage = 0) => {
    if (!entry) return;
    setLoading(true);
    setError('');
    try {
      const response = await fetch('/api/supervision/search', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowType: decodedFlowType,
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
    } catch (searchError) {
      setError(searchError instanceof Error ? searchError.message : 'Supervision search failed');
      setFlows([]);
    } finally {
      setLoading(false);
    }
  }, [decodedFlowType, entry, filters]);

  useEffect(() => {
    if (entry) void executeSearch();
  }, [entry]);

  if (!catalog) return <div className="page-loading">Loading supervision…</div>;
  if (!entry) return <Navigate to="/supervision" replace />;

  const fields = supervisionFilterFields(entry.definition);
  const columns = supervisionListColumns(entry.definition);
  return (
    <div className="page-shell supervision-list-page">
      <section className="supervision-title-row">
        <div>
          <p className="eyebrow">Supervision</p>
          <h1>{entry.flowType}</h1>
          <p>Current runs only. Historical runs remain available in Operations.</p>
        </div>
        <button className="button primary" disabled={loading} onClick={() => {
          setPageTokens(['']);
          void executeSearch();
        }} type="button">{loading ? 'Searching…' : 'Search'}</button>
      </section>

      <section className="card supervision-filters">
        <div className="supervision-filter-heading">
          <strong>Filters</strong>
          <button className="button ghost" onClick={() => setFilters((current) => [
            ...current,
            { id: `${Date.now()}-${Math.random()}`, field: 'executionStatus', operator: 'eq', value: 'Running' },
          ])} type="button">Add filter</button>
        </div>
        {filters.length === 0 && <span className="muted">All {entry.flowType} executions</span>}
        {filters.map((filter) => (
          <div className="supervision-filter-row" key={filter.id}>
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
            <button className="icon-button" aria-label="Remove filter" onClick={() => setFilters(filters.filter((item) => item.id !== filter.id))} type="button">×</button>
          </div>
        ))}
      </section>

      {error && <div className="error-banner">{error}</div>}
      <section className="card supervision-table-card">
        <div className="table-scroll">
          <table className="results-table supervision-table">
            <thead><tr>
              <th>Status</th><th>Flow ID</th><th>Started</th><th>Closed</th>
              {columns.map((column) => (
                <th key={`${column.source}:${column.key}`} title={column.description}>{column.description}</th>
              ))}
            </tr></thead>
            <tbody>
              {flows.map((flow) => (
                <tr key={flow.flowId}>
                  <td><StatusBadge status={flow.flowStatus} /></td>
                  <td><Link className="flow-id-link" to={supervisionDetailPath(entry.flowType, flow.flowId)}>{flow.flowId}</Link></td>
                  <td>{formatDate(flow.startTime, timezone)}</td>
                  <td>{formatDate(flow.closeTime, timezone)}</td>
                  {columns.map((column) => (
                    <td key={`${column.source}:${column.key}`} title={column.source === 'summary' ? flow.summaryError : undefined}>
                      {column.source === 'summary' && flow.summaryError
                        ? 'Unavailable'
                        : displayValue(column.source === 'indexed' ? flow.indexedAttributes[column.key] : flow.summary?.[column.key])}
                    </td>
                  ))}
                </tr>
              ))}
              {!loading && flows.length === 0 && <tr><td colSpan={4 + columns.length}>No matching Flows</td></tr>}
            </tbody>
          </table>
        </div>
        <div className="pagination">
          <button className="button ghost" disabled={page === 0 || loading} onClick={() => {
            const previous = page - 1;
            const tokens = pageTokens.slice(0, -1);
            setPageTokens(tokens);
            void executeSearch(tokens[previous] ?? '', previous);
          }} type="button">Previous</button>
          <span>Page {page + 1}</span>
          <button className="button ghost" disabled={!nextPageToken || loading} onClick={() => {
            setPageTokens([...pageTokens, nextPageToken]);
            void executeSearch(nextPageToken, page + 1);
          }} type="button">Next</button>
        </div>
      </section>
    </div>
  );
}

export function SupervisionDetailPage() {
  const { flowType = '', flowId = '' } = useParams();
  const decodedFlowType = flowType;
  const decodedFlowID = flowId;
  const { catalog } = useSupervision();
  const entry = catalog?.flows.find((candidate) => candidate.flowType === decodedFlowType);
  const [result, setResult] = useState<SupervisionDisplay | null>(null);
  const [error, setError] = useState('');
  const [busyKey, setBusyKey] = useState('');
  const [editingKey, setEditingKey] = useState('');
  const [editValue, setEditValue] = useState('');
  const [fieldErrors, setFieldErrors] = useState<Record<string, string>>({});
  const [actionValues, setActionValues] = useState<Record<string, Record<string, string>>>({});

  const loadDisplay = useCallback(async () => {
    if (!entry) return;
    setError('');
    try {
      const query = new URLSearchParams({ flowType: decodedFlowType, flowId: decodedFlowID });
      const response = await fetch(`/api/supervision/display?${query}`);
      setResult(await readResponseJSON<SupervisionDisplay>(response));
    } catch (loadError) {
      setError(loadError instanceof Error ? loadError.message : 'Display failed to load');
    }
  }, [decodedFlowID, decodedFlowType, entry]);

  useEffect(() => { void loadDisplay(); }, [loadDisplay]);
  if (!catalog) return <div className="page-loading">Loading supervision…</div>;
  if (!entry) return <Navigate to="/supervision" replace />;

  async function saveField(attributeKey: string, valueType: SupervisionValueType) {
    setBusyKey(attributeKey);
    setError('');
    setFieldErrors((current) => ({ ...current, [attributeKey]: '' }));
    try {
      const response = await fetch('/api/supervision/display', {
        method: 'PATCH', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowType: decodedFlowType, flowId: decodedFlowID, attributeKey,
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
      const input = supervisionActionUserInput(action, actionValues[action.rpcName] ?? {});
      const response = await fetch('/api/supervision/actions', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          flowType: decodedFlowType, flowId: decodedFlowID, rpcName: action.rpcName,
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
    <div className="page-shell supervision-detail-page">
      <section className="supervision-title-row">
        <div>
          <p className="eyebrow">{entry.flowType}</p>
          <h1>{decodedFlowID}</h1>
          <p>Current run · Run ID is intentionally hidden.</p>
        </div>
        {result && <StatusBadge status={result.flowStatus} />}
      </section>
      {error && <div className="error-banner">{error}</div>}
      {!result && !error && <div className="page-loading">Loading Display…</div>}
      {result && (
        <div className="supervision-detail-grid">
          <section className="card supervision-display-card">
            <div className="supervision-section-heading"><div><p className="eyebrow">Display</p><h2>Flow details</h2></div><code>{entry.definition.display.rpcName}</code></div>
            <dl>
              {entry.definition.display.fields.map((field) => {
                const isEditing = editingKey === field.attributeKey;
                return (
                  <div key={field.attributeKey}>
                    <dt><span>{field.description}</span><code>{field.attributeKey}</code></dt>
                    <dd>
                      {isEditing ? (
                        <div className="supervision-inline-edit-shell">
                          <div className="supervision-inline-edit">
                            <TypedInput field={field} value={editValue} onChange={setEditValue} />
                            <button className="button primary" disabled={busyKey === field.attributeKey} onClick={() => void saveField(field.attributeKey, field.valueType)} type="button">Save</button>
                            <button className="button ghost" onClick={() => setEditingKey('')} type="button">Cancel</button>
                          </div>
                          {fieldErrors[field.attributeKey] && <small className="supervision-field-error">{fieldErrors[field.attributeKey]}</small>}
                        </div>
                      ) : (
                        <><span>{displayValue(result.display[field.attributeKey])}</span>{field.editable && result.isActive && <button className="button ghost" onClick={() => {
                          setEditingKey(field.attributeKey);
                          setFieldErrors((current) => ({ ...current, [field.attributeKey]: '' }));
                          setEditValue(editableValue(result.display[field.attributeKey], field.valueType));
                        }} type="button">Edit</button>}</>
                      )}
                    </dd>
                  </div>
                );
              })}
            </dl>
          </section>
          <section className="card supervision-actions-card">
            <div className="supervision-section-heading"><div><p className="eyebrow">Actions</p><h2>Operator controls</h2></div></div>
            {entry.definition.actions.length === 0 && <p className="muted">This Flow declares no supervision Actions.</p>}
            {visibleSupervisionActions(entry.definition.actions, result.eligibleActions).map((action) => {
              const userFields = supervisionActionUserFields(action);
              return (
                <form className="supervision-action" key={action.rpcName} onSubmit={(event) => {
                  event.preventDefault();
                  void invokeAction(action);
                }}>
                  <div><strong>{action.label}</strong><small>Available when {action.condition.attributeKey} is one of {action.condition.values.map(String).join(', ')}</small></div>
                  {userFields.map((field) => (
                    <label key={field.fieldName}><span>{field.description}</span><ActionInput
                      field={field}
                      value={actionValues[action.rpcName]?.[field.fieldName] ?? ''}
                      onChange={(value) => setActionValues((current) => ({
                        ...current, [action.rpcName]: { ...current[action.rpcName], [field.fieldName]: value },
                      }))}
                    /></label>
                  ))}
                  <button className="button primary" disabled={!result.isActive || busyKey === action.rpcName} type="submit">
                    {busyKey === action.rpcName ? 'Working…' : action.label}
                  </button>
                </form>
              );
            })}
            {entry.definition.actions.length > 0 && result.eligibleActions.length === 0 && (
              <p className="muted">No Actions are available in the current state.</p>
            )}
          </section>
        </div>
      )}
    </div>
  );
}

export function supervisionHomePath(enabled: boolean, mode: 'supervision' | 'operations') {
  return enabled && mode === 'supervision' ? '/supervision' : '/flows';
}

export function supervisionDetailPath(flowType: string, flowID: string) {
  return `/supervision/${encodeURIComponent(flowType)}/${encodeURIComponent(flowID)}`;
}

export function supervisionListColumns(definition: FlowSupervisionDefinition) {
  return [
    ...definition.indexedAttributes.map((attribute) => ({
      source: 'indexed' as const,
      key: attribute.attributeKey,
      description: attribute.description,
    })),
    ...definition.summary.fields.map((field) => ({
      source: 'summary' as const,
      key: field.attributeKey,
      description: field.description,
    })),
  ];
}

export function visibleSupervisionActions(
  actions: FlowSupervisionAction[],
  eligibleRPCNames: string[],
) {
  const eligible = new Set(eligibleRPCNames);
  return actions.filter((action) => eligible.has(action.rpcName));
}

export function supervisionActionUserFields(action: FlowSupervisionAction) {
  return (action.input.fields ?? []).filter((field) => field.source === 'user');
}

export function supervisionActionUserInput(
  action: FlowSupervisionAction,
  values: Record<string, string>,
) {
  return Object.fromEntries(supervisionActionUserFields(action).map((field) => [
    field.fieldName,
    parseTypedValue(values[field.fieldName] ?? '', field.valueType, !field.required),
  ]));
}

function supervisionFilterFields(definition: FlowSupervisionDefinition) {
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
  onChange: React.ChangeEventHandler<HTMLInputElement | HTMLSelectElement>;
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

function parseTypedValue(value: string, valueType: SupervisionValueType, optional = false): unknown {
  if (optional && value === '') return null;
  if (valueType === 'int64') return value;
  if (valueType === 'double') return Number(value);
  if (valueType === 'bool') return value === 'true';
  if (valueType === 'datetime') return new Date(value).toISOString();
  return value;
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
