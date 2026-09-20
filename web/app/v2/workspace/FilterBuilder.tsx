// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ChangeEventHandler } from 'react';
import type { FlowV2Definition, V2ValueType } from '@superdurable/flow-definition-renderer';
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

/**
 * The scope control, behind a disclosure.
 *
 * Collapsed by default so the list reads as a list, but never hidden: the view states what it
 * narrowed to, and a reader who disagrees has to be able to reach the thing that narrowed it.
 */
export function FilterBuilder({
  definition,
  search,
  summary,
}: {
  definition: FlowV2Definition;
  search: FlowSearch;
  /** What the filters currently mean, shown on the closed summary line. */
  summary: string;
}) {
  const { filters, setFilters, loading } = search;
  const fields = filterFields(definition);
  return (
    <details className="fb">
      <summary className="fb-summary">{summary}</summary>
      <div className="v2-filters">
        {filters.map((filter) => (
          <div className="v2-filter-row" key={filter.id}>
            <select
              value={filter.field}
              onChange={(event) => setFilters(filters.map((item) => (
                item.id === filter.id
                  ? { ...item, field: event.target.value, operator: 'eq', value: '' }
                  : item
              )))}
            >
              {fields.map((field) => (
                <option key={field.key} value={field.key}>{field.label}</option>
              ))}
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
    </details>
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
