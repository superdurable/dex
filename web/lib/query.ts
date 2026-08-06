// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

export type QueryOperator = '=' | '!=' | '>' | '>=' | '<' | '<=';

export interface BasicFilter {
  id: string;
  field: string;
  operator: QueryOperator;
  value: string;
}

const quotedFields = new Set([
  'ExecutionStatus',
  'WorkflowId',
  'RunId',
  'FlowType',
  'StartTime',
  'CloseTime',
]);

const paradeStatusCodes: Record<string, number> = {
  Running: 1,
  Completed: 2,
  Failed: 3,
  TimedOut: 4,
  Terminated: 5,
  Canceled: 6,
  ContinuedAsNew: 7,
};

export function escapeQueryValue(value: string): string {
  return value.replaceAll('\\', '\\\\').replaceAll('"', '\\"');
}

export function buildVisibilityQuery(filters: BasicFilter[]): string {
  return filters
    .filter((filter) => filter.field.trim() && filter.value.trim())
    .map((filter) => {
      const field = filter.field.trim();
      const value = filter.value.trim();
      const encoded = quotedFields.has(field) || !/^-?\d+(\.\d+)?$/.test(value)
        ? `"${escapeQueryValue(value)}"`
        : value;
      return `${field} ${filter.operator} ${encoded}`;
    })
    .join(' AND ');
}

export function buildParadeQuery(filters: BasicFilter[]): string {
  return filters
    .filter((filter) => filter.field.trim() && filter.value.trim())
    .map((filter) => {
      const field = filter.field.trim();
      const rawValue = filter.value.trim();
      const value = field === 'FlowStatus' && paradeStatusCodes[rawValue] !== undefined
        ? String(paradeStatusCodes[rawValue])
        : rawValue;
      const encoded = /^-?\d+(\.\d+)?$/.test(value) || value === 'true' || value === 'false'
        ? value
        : `"${escapeQueryValue(value)}"`;
      if (filter.operator === '=') return `${field}:${encoded}`;
      if (filter.operator === '!=') return `NOT ${field}:${encoded}`;
      return `${field}:${filter.operator}${encoded}`;
    })
    .join(' AND ');
}

export function parseVisibilityQuery(query: string): BasicFilter[] | null {
  if (!query.trim()) return [];
  const clauses = query.split(/\s+AND\s+/i);
  const filters: BasicFilter[] = [];
  for (const [index, clause] of clauses.entries()) {
    const match = clause.match(
      /^\s*([A-Za-z_][A-Za-z0-9_]*)\s*(=|!=|>=|<=|>|<)\s*(?:"((?:\\.|[^"])*)"|(-?\d+(?:\.\d+)?))\s*$/,
    );
    if (!match) return null;
    filters.push({
      id: `parsed-${index}`,
      field: match[1],
      operator: match[2] as QueryOperator,
      value: match[3] !== undefined
        ? match[3].replaceAll('\\"', '"').replaceAll('\\\\', '\\')
        : match[4],
    });
  }
  return filters;
}
