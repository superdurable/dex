// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { newFilterRow, type FilterRow } from './filters';

/**
 * The parties a Flow names, and the search each one's queue compiles to.
 *
 * There is no identity in Dex, so a role is not who you are — it is which Actions you answer. The
 * reader says which party they are and the list narrows to the runs whose state those Actions are
 * waiting on.
 */
export function rolesOf(definition: FlowV2Definition | undefined): string[] {
  const seen: string[] = [];
  for (const action of definition?.actions ?? []) {
    const role = action.role;
    if (role === undefined || role === '' || seen.includes(role)) continue;
    seen.push(role);
  }
  return seen;
}

/**
 * One filter for everything this role is waiting on, or null when that cannot be asked.
 *
 * Every Action carries exactly one condition, so a role's queue is the union of its Actions'
 * conditions. That only reduces to a single filter while they all test the same Attribute: filters
 * are ANDed, so two Attributes would need an OR the search API does not have. Returning null then is
 * the honest answer — better an unnarrowed list than a wrong one.
 *
 * A matched condition is NECESSARY, NOT SUFFICIENT. A gate also needs its live key, and this reads an
 * eventually consistent index. So this narrows to what probably needs the role; the run decides.
 */
export function roleFilter(
  definition: FlowV2Definition | undefined,
  role: string,
): FilterRow | null {
  if (role === '') return null;
  const conditions = (definition?.actions ?? [])
    .filter((action) => action.role === role)
    .map((action) => action.condition);
  if (conditions.length === 0) return null;

  const first = conditions[0];
  if (first === undefined) return null;
  const sameField = conditions.every(
    (condition) => condition.attributeKey === first.attributeKey && condition.operator === first.operator,
  );
  if (!sameField || first.operator !== 'in') return null;

  const values: string[] = [];
  for (const condition of conditions) {
    for (const value of condition.values) {
      const text = String(value);
      if (!values.includes(text)) values.push(text);
    }
  }
  if (values.length === 0) return null;
  // The filter layer splits on commas, so a value containing one cannot be asked for.
  if (values.some((value) => value.includes(','))) return null;
  return newFilterRow(first.attributeKey, 'in', values.join(','));
}

/** The Action labels this role answers, so a picker can say what choosing it means. */
export function roleActionLabels(
  definition: FlowV2Definition | undefined,
  role: string,
): string[] {
  return (definition?.actions ?? [])
    .filter((action) => action.role === role)
    .map((action) => action.label);
}
