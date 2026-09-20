// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * What can be searched comes from the contract, so the words here name controls rather than fields.
 */
export const SEARCH_COPY = {
  statusLabel: 'Execution status',
  anyStatus: 'Any status',
  sinceLabel: 'Started within',
  advanced: 'Advanced search',
  runIdLabel: 'Run ID',
  runIdPlaceholder: 'Exact run id',
  attributeLabel: 'Attribute',
  anyAttribute: 'Any attribute',
  operatorLabel: 'Comparison',
  attributeValueLabel: 'Attribute value',
  pickAttribute: 'Choose an attribute first',
  value: 'Value',
  anyValue: 'Any',
  boolTrue: 'True',
  boolFalse: 'False',
  search: 'Search',
  searching: 'Searching…',
  clear: 'Clear',
  findHeading: 'Find runs',
  resultsHeading: 'Results',
  flowTypeLabel: 'Flow type',

  /** Said plainly, because a search box invites the assumption that it reads everything. */
  scopeNote(attributeDescriptions: readonly string[]): string {
    if (attributeDescriptions.length === 0) {
      return 'This Flow declares nothing searchable, so runs can only be filtered by status and time.';
    }
    return `Searches ${attributeDescriptions.join(', ')} — the attributes this Flow declares as indexed. Not run inputs, outputs or history.`;
  },
} as const;
