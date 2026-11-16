// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

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
      return `${encodeQueryFieldName(field)} ${filter.operator} ${encoded}`;
    })
    .join(' AND ');
}

const plainFieldNamePattern = /^[\p{Alphabetic}_][\p{Alphabetic}\p{N}_]*$/u;

// A bare field with one of these names would read as query syntax.
const queryKeywords = new Set([
  'AND', 'OR', 'NOT', 'IN', 'IS', 'NULL', 'BETWEEN', 'STARTS_WITH', 'ORDER', 'BY', 'ASC', 'DESC', 'TRUE', 'FALSE',
]);

/**
 * Writes a field name as the search query syntax reads it.
 *
 * Returns the name unchanged when it is a plain identifier, and otherwise the name in backticks
 * with each backslash and backtick escaped, such as `order-status` for order-status.
 */
export function encodeQueryFieldName(field: string): string {
  if (plainFieldNamePattern.test(field) && !queryKeywords.has(field.toUpperCase())) return field;
  return `\`${field.replaceAll('\\', '\\\\').replaceAll('`', '\\`')}\``;
}

/**
 * Adds a condition to a hand-written query with AND.
 *
 * The condition goes before a trailing ORDER BY. A query with a top-level OR is wrapped in parentheses,
 * so the condition applies to all of it. Returns the new query and the offset just after the condition.
 */
export function appendQueryCondition(query: string, condition: string): { query: string; cursor: number } {
  const orderByOffset = findTopLevelKeyword(query, /^ORDER\s+BY\b/i);
  const filterPart = (orderByOffset < 0 ? query : query.slice(0, orderByOffset)).trim();
  const orderPart = orderByOffset < 0 ? '' : query.slice(orderByOffset).trim();
  const hasTopLevelOr = findTopLevelKeyword(filterPart, /^OR\b/i) >= 0;
  const head = filterPart ? `${hasTopLevelOr ? `(${filterPart})` : filterPart} AND ` : '';
  const beforeCursor = `${head}${condition}`;
  return { query: orderPart ? `${beforeCursor} ${orderPart}` : beforeCursor, cursor: beforeCursor.length };
}

/** Returns the offset of the first keyword match outside quotes and parentheses, or -1. */
function findTopLevelKeyword(query: string, keyword: RegExp): number {
  let quote = '';
  let depth = 0;
  for (let index = 0; index < query.length; index += 1) {
    const character = query[index];
    if (quote) {
      if (character === '\\') index += 1;
      else if (character === quote) quote = '';
      continue;
    }
    if (character === '"' || character === "'" || character === '`') quote = character;
    else if (character === '(') depth += 1;
    else if (character === ')') depth -= 1;
    else if (depth === 0 && (index === 0 || /\s/.test(query[index - 1])) && keyword.test(query.slice(index))) return index;
  }
  return -1;
}

export function parseVisibilityQuery(query: string): BasicFilter[] | null {
  if (!query.trim()) return [];
  const clauses = query.split(/\s+AND\s+/i);
  const filters: BasicFilter[] = [];
  for (const [index, clause] of clauses.entries()) {
    const match = clause.match(
      /^\s*(?:([\p{Alphabetic}_][\p{Alphabetic}\p{N}_]*)|`((?:\\.|[^`\\])*)`)\s*(=|!=|>=|<=|>|<)\s*(?:"((?:\\.|[^"])*)"|(-?\d+(?:\.\d+)?))\s*$/u,
    );
    if (!match) return null;
    const [, plainField, quotedField, operator, quotedValue, numberValue] = match;
    filters.push({
      id: `parsed-${index}`,
      field: plainField ?? quotedField.replace(/\\(.)/gu, '$1'),
      operator: operator as QueryOperator,
      value: quotedValue !== undefined
        ? quotedValue.replaceAll('\\"', '"').replaceAll('\\\\', '\\')
        : numberValue,
    });
  }
  return filters;
}
