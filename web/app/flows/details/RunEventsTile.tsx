// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ChevronDown, ChevronsDown, ChevronsUp, ChevronUp, ListOrdered, Undo2 } from 'lucide-react';
import type { ReactNode } from 'react';
import { HISTORY_PAGE_SIZES, type HistoryPageSize } from '@/lib/history';

/** The event page that the Execution graph and the Timeline share, and its page buttons. */
export interface RunEventsPaging {
  /** The oldest and newest event IDs on the page. */
  firstEventId: number;
  lastEventId: number;
  /** The 1-based page number, newest first, or null when it is not exact. */
  pageNumber: number | null;
  /** The page count, or null when it is not exact. */
  pageCount: number | null;
  hasOlder: boolean;
  hasNewer: boolean;
  /** Whether the page is the newest page, which Live extends. */
  isNewest: boolean;
  isLoading: boolean;
  canGoBack: boolean;
  onBack: () => void;
  onLatestPage: () => void;
  onNewerPage: () => void;
  onOlderPage: () => void;
  onOldestPage: () => void;
}

/** The event toolbar of the Timeline and Execution graph: the shown range, page buttons and page size. */
export function RunEventsTile({
  latestEventId,
  paging,
  pageSize,
  onPageSizeChange,
  children,
}: {
  /** Controls of the view the tile heads, after the page controls. */
  children?: ReactNode;
  latestEventId: number | null;
  paging: RunEventsPaging | null;
  /** How many events one page holds; changing it reloads the Latest page. */
  pageSize?: HistoryPageSize;
  onPageSizeChange?: (size: HistoryPageSize) => void;
}) {
  return (
    <div className="run-events-tile" role="group" aria-label="Events">
      <span className="run-fact-label"><ListOrdered aria-hidden="true" size={14} strokeWidth={2} />Events</span>
      <div className="run-events-line">
        {paging && (
          <span className="run-events-range">
            Showing #{paging.firstEventId}–#{paging.lastEventId}
            {pageLabel(paging) ? ` · ${pageLabel(paging)}` : ''}
          </span>
        )}
        <span className="run-events-latest">{latestEventId === null ? 'None yet' : `Latest #${latestEventId}`}</span>
      </div>
      <div className="run-events-controls">
        {paging && hasOtherPages(paging) && <RunEventsPager paging={paging} />}
        {pageSize !== undefined && onPageSizeChange && !fitsSmallestPage(latestEventId, paging) && (
          <label className="run-events-page-size">
            <span className="run-events-page-size-word">Page size</span>
            <select
              onChange={(event) => onPageSizeChange(Number(event.target.value) as HistoryPageSize)}
              value={pageSize}
            >
              {HISTORY_PAGE_SIZES.map((size) => <option key={size} value={size}>{size}</option>)}
            </select>
          </label>
        )}
        {children}
      </div>
    </div>
  );
}

/** Page buttons matter only when another page exists or the reader has paged away. */
function hasOtherPages(paging: RunEventsPaging): boolean {
  return paging.hasOlder || paging.hasNewer || !paging.isNewest || paging.canGoBack;
}

/** A run whose whole history fits the smallest page has no use for a page size. */
function fitsSmallestPage(latestEventId: number | null, paging: RunEventsPaging | null): boolean {
  if (latestEventId === null || (paging && hasOtherPages(paging))) return false;
  return latestEventId <= Math.min(...HISTORY_PAGE_SIZES);
}

function pageLabel(paging: RunEventsPaging): string {
  if (paging.pageNumber === null) return '';
  return paging.pageCount === null ? `Page ${paging.pageNumber}` : `Page ${paging.pageNumber} of ${paging.pageCount}`;
}

function RunEventsPager({ paging }: { paging: RunEventsPaging }) {
  const isNewerDisabled = paging.isLoading || paging.isNewest;
  const isOlderDisabled = paging.isLoading || !paging.hasOlder;
  return (
    <div aria-busy={paging.isLoading} aria-label="Event pages" className="run-events-pager run-events-pager--icons" role="group">
      {paging.canGoBack && (
        <button className="pager-button" disabled={paging.isLoading} onClick={paging.onBack} title="Back" type="button">
          <Undo2 aria-hidden="true" size={14} strokeWidth={2} />
          <span className="pager-label">Back</span>
        </button>
      )}
      <button className="pager-button" disabled={isNewerDisabled} onClick={paging.onLatestPage} title="Latest page" type="button">
        <ChevronsUp aria-hidden="true" size={14} strokeWidth={2} />
        <span className="pager-label">Latest page</span>
      </button>
      <button
        className="pager-button"
        disabled={isNewerDisabled || !paging.hasNewer}
        onClick={paging.onNewerPage} title="Newer page"
        type="button"
      >
        <ChevronUp aria-hidden="true" size={14} strokeWidth={2} />
        <span className="pager-label">Newer page</span>
      </button>
      <button className="pager-button" disabled={isOlderDisabled} onClick={paging.onOlderPage} title="Older page" type="button">
        <ChevronDown aria-hidden="true" size={14} strokeWidth={2} />
        <span className="pager-label">Older page</span>
      </button>
      <button className="pager-button" disabled={isOlderDisabled} onClick={paging.onOldestPage} title="Oldest page" type="button">
        <ChevronsDown aria-hidden="true" size={14} strokeWidth={2} />
        <span className="pager-label">Oldest page</span>
      </button>
    </div>
  );
}
