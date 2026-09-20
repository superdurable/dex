// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * The searchable Attribute names itself in the placeholder, so what can be searched comes
 * from the contract rather than from prose somebody has to remember to update.
 */
export const SEARCH_COPY = {
  keywordLabel(description: string): string {
    return `Search ${description.toLowerCase()}`;
  },
  keywordPlaceholder(description: string): string {
    return `Search ${description.toLowerCase()}…`;
  },
  statusLabel: 'Execution status',
  anyStatus: 'Any status',
  sinceLabel: 'Started within',
  attributeLabel: 'Attribute',
  anyAttribute: 'Any attribute',
  attributeValueLabel: 'Attribute value',
  pickAttribute: 'Choose an attribute first',
  exactValue: 'Exact value',
  search: 'Search',
  searching: 'Searching…',
  clear: 'Clear',

  /** Said plainly, because a search box invites the assumption that it reads everything. */
  scopeNote(attributeDescriptions: readonly string[]): string {
    if (attributeDescriptions.length === 0) {
      return 'This Flow declares nothing searchable, so runs can only be filtered by status and time.';
    }
    return `Searches ${attributeDescriptions.join(', ')} — the attributes this Flow declares as indexed. Not run inputs, outputs or history.`;
  },
} as const;
