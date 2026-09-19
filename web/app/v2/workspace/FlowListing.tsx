// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ChangeEventHandler, ReactNode } from 'react';
import type { FlowV2Definition, V2ValueType } from '@superdurable/flow-definition-renderer';
import { displayValue, formatDate } from '@/lib/format';
import type { V2CatalogEntry, V2Flow } from '@/lib/types';
import { usePreferences } from '../../providers';
import { v2ListColumns } from '../contract';
import { QUEUE_COPY } from '../queue/copy';
import {
  filterFields,
  filterIndexType,
  filterOperators,
  filterValueType,
  newFilterRow,
  updateFilter,
  type FilterRow,
} from './filters';
import type { FlowSearch } from './useFlowSearch';

export function FlowListing({
  entry,
  flowTypes,
  selectedFlowID,
  search,
  headerNote,
  scope,
  strandedFlowIDs,
  onSelectFlowType,
  onSelectRun,
  children,
}: {
  entry: V2CatalogEntry;
  flowTypes: V2CatalogEntry[];
  selectedFlowID: string;
  search: FlowSearch;
  headerNote: string;
  /** What this list is and is not showing. Rendered above the filters. */
  scope?: ReactNode;
  /** Runs this session found unreachable. Marked, not dropped: still unresolved work. */
  strandedFlowIDs?: ReadonlySet<string>;
  onSelectFlowType: (flowType: string) => void;
  onSelectRun: (flowID: string) => void;
  children?: ReactNode;
}) {
  const { timezone } = usePreferences();
  const { filters, setFilters, flows, loading, searchError } = search;
  const fields = filterFields(entry.definition);
  const columns = v2ListColumns(entry.definition);
  return (
    <>
      <div className="sq-head">
        <span className="sq-title">{entry.flowType}</span>
        <span className="sq-live">{headerNote}</span>
      </div>
      {flowTypes.length > 1 && (
        <div className="sv-choose">
          <span className="sv-chooselabel">Flow type</span>
          {flowTypes.map((candidate) => (
            <button
              aria-pressed={candidate.flowType === entry.flowType}
              className="sv-flow"
              key={candidate.flowType}
              onClick={() => onSelectFlowType(candidate.flowType)}
              type="button"
            >
              {candidate.flowType}
            </button>
          ))}
        </div>
      )}
      {scope}
      <div className="v2-filters">
        {filters.map((filter) => (
          <FilterRowControls
            definition={entry.definition}
            fields={fields}
            filter={filter}
            filters={filters}
            key={filter.id}
            setFilters={setFilters}
          />
        ))}
      </div>
      <div className="v2-filter-actions">
        <button
          className="v2-ghost"
          onClick={() => setFilters([...filters, newFilterRow('executionStatus', 'eq', 'Running')])}
          type="button"
        >
          Add filter
        </button>
        <button className="v2-primary" disabled={loading} onClick={search.runSearch} type="button">
          {loading ? 'Searching…' : 'Search'}
        </button>
      </div>
      {searchError && <p className="v2-error">{searchError}</p>}
      <ol className="v2-list sq-list">
        {flows.map((flow) => (
          <li
            className="sq-item"
            data-selected={flow.flowId === selectedFlowID ? 'true' : undefined}
            data-stranded={strandedFlowIDs?.has(flow.flowId) ? 'true' : undefined}
            key={flow.flowId}
          >
            <button className="sq-row" onClick={() => onSelectRun(flow.flowId)} type="button">
              <span className="sq-run t-mono" title={flow.flowId}>{flow.flowId}</span>
              <span className="sq-attention">{formatDate(flow.startTime, timezone)}</span>
              <span className="sq-step t-mono">{flow.flowStatus}</span>
              <FlowColumns columns={columns} flow={flow} />
              {strandedFlowIDs?.has(flow.flowId) && (
                <span className="sq-unknown">{QUEUE_COPY.strandedRow}</span>
              )}
            </button>
          </li>
        ))}
        {!loading && flows.length === 0 && !scope && <li className="v2-empty">No matching Flows</li>}
      </ol>
      <div className="v2-filter-actions">
        <button
          className="v2-ghost"
          disabled={search.page === 0 || loading}
          onClick={search.goToPreviousPage}
          type="button"
        >
          Previous
        </button>
        <span className="sq-live">Page {search.page + 1}</span>
        <button
          className="v2-ghost"
          disabled={!search.hasNextPage || loading}
          onClick={search.goToNextPage}
          type="button"
        >
          Next
        </button>
      </div>
      {children}
    </>
  );
}

function FlowColumns({
  columns,
  flow,
}: {
  columns: ReturnType<typeof v2ListColumns>;
  flow: V2Flow;
}) {
  return (
    <span className="v2-columns">
      {columns.map((column) => (
        <span className="v2-column" key={`${column.source}:${column.key}`}>
          {column.source === 'summary' && flow.summaryError
            ? 'Unavailable'
            : displayValue(column.source === 'indexed'
              ? flow.indexedAttributes[column.key]
              : flow.summary?.[column.key])}
        </span>
      ))}
    </span>
  );
}

function FilterRowControls({
  definition,
  fields,
  filter,
  filters,
  setFilters,
}: {
  definition: FlowV2Definition;
  fields: { key: string; label: string }[];
  filter: FilterRow;
  filters: FilterRow[];
  setFilters: (filters: FilterRow[]) => void;
}) {
  return (
    <div className="v2-filter-row">
      <select
        value={filter.field}
        onChange={(event) => setFilters(filters.map((item) => (
          item.id === filter.id
            ? { ...item, field: event.target.value, operator: 'eq', value: '' }
            : item
        )))}
      >
        {fields.map((field) => <option key={field.key} value={field.key}>{field.label}</option>)}
      </select>
      <select
        value={filter.operator}
        onChange={(event) => setFilters(
          updateFilter(filters, filter.id, 'operator', event.target.value),
        )}
      >
        {filterOperators(filterIndexType(filter.field, definition)).map((operator) => (
          <option key={operator.value} value={operator.value}>{operator.label}</option>
        ))}
      </select>
      <FilterInput
        operator={filter.operator}
        value={filter.value}
        valueType={filterValueType(filter.field, definition)}
        onChange={(event) => setFilters(
          updateFilter(filters, filter.id, 'value', event.target.value),
        )}
      />
      <button
        className="v2-ghost"
        onClick={() => setFilters(filters.filter((item) => item.id !== filter.id))}
        type="button"
      >
        ×
      </button>
    </div>
  );
}

function FilterInput({ operator, value, valueType, onChange }: {
  operator: string;
  value: string;
  valueType: V2ValueType;
  onChange: ChangeEventHandler<HTMLInputElement | HTMLSelectElement>;
}) {
  if (valueType === 'bool') {
    return (
      <select aria-label="Filter values" value={value} onChange={onChange}>
        <option value="true">True</option>
        <option value="false">False</option>
      </select>
    );
  }
  const allowsAlternatives = operator === 'in';
  const type = allowsAlternatives
    ? 'text'
    : valueType === 'int64' || valueType === 'double'
      ? 'number'
      : valueType === 'datetime'
        ? 'datetime-local'
        : 'text';
  return (
    <input
      aria-label="Filter values"
      placeholder={allowsAlternatives ? 'Separate alternatives with commas' : 'Value'}
      type={type}
      value={value}
      onChange={onChange}
    />
  );
}
