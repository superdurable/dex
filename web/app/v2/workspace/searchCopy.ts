// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** Control names, not field names: what can be searched comes from the Flow's own contract. */
export const SEARCH_COPY = {
  statusLabel: 'Execution status',
  anyStatus: 'Any status',
  sinceLabel: 'Started within',
  advanced: 'Advanced search',
  flowIdLabel: 'Flow ID',
  flowIdPlaceholder: 'Exact Flow ID',
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
  flowTypeLabel: 'Flow type',
} as const;
