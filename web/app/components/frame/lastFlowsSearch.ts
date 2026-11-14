// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { scopedStorageKey } from '@/lib/basePath';

const LAST_FLOWS_SEARCH_KEY = 'dex-web.last-flows-search';

/** Remembers the Flows page's query string for this tab, so the trail's Flows link returns to it. */
export function rememberFlowsSearch(search: string): void {
  try {
    window.sessionStorage.setItem(scopedStorageKey(LAST_FLOWS_SEARCH_KEY), search);
  } catch {
    // Without storage the link still opens the Flows page.
  }
}

/** The remembered query string, or '' when none was remembered. */
export function readLastFlowsSearch(): string {
  try {
    return window.sessionStorage.getItem(scopedStorageKey(LAST_FLOWS_SEARCH_KEY)) ?? '';
  } catch {
    return '';
  }
}
