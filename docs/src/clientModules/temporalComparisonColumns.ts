/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

// On the Dex vs. Temporal pages, a table column whose header names Temporal or Dex gets that engine's color.
const comparisonPath = /\/intro\/compared-to-temporal(\/|$)/;

function engineOfHeader(text: string): 'temporal' | 'dex' | undefined {
  if (text.includes('Temporal')) return 'temporal';
  if (/^Dex\b/.test(text)) return 'dex';
  return undefined;
}

function colorComparisonColumns(): void {
  document.querySelectorAll<HTMLTableElement>('.theme-doc-markdown table').forEach((table) => {
    const headers = Array.from(table.querySelectorAll<HTMLTableCellElement>('thead th'));
    headers.forEach((header, index) => {
      const engine = engineOfHeader(header.textContent?.trim() ?? '');
      if (!engine) return;
      table.querySelectorAll<HTMLTableCellElement>(`tr > :nth-child(${index + 1})`).forEach((cell) => {
        cell.dataset.engine = engine;
      });
    });
  });
}

export function onRouteDidUpdate({location}: {location: {pathname: string}}): void {
  if (comparisonPath.test(location.pathname)) colorComparisonColumns();
}
