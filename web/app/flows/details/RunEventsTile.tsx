// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ChevronLeft, ChevronRight, ListOrdered, Undo2 } from 'lucide-react';
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
  status,
  children,
}: {
  /** The page's own status, such as Live, which follows the event count. */
  status?: ReactNode;
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
        {status}
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

/** The page buttons in words, with older pages on the left like the lane. */
function RunEventsPager({ paging }: { paging: RunEventsPaging }) {
  const isNewerDisabled = paging.isLoading || paging.isNewest || !paging.hasNewer;
  const isOlderDisabled = paging.isLoading || !paging.hasOlder;
  return (
    <div aria-busy={paging.isLoading} aria-label="Event pages" className="run-events-pager run-events-pager--words" role="group">
      {paging.canGoBack && (
        <button className="pager-button" disabled={paging.isLoading} onClick={paging.onBack} title="Back to where you were" type="button">
          <Undo2 aria-hidden="true" size={13} strokeWidth={2} />
          Back
        </button>
      )}
      <button className="pager-button" disabled={isOlderDisabled} onClick={paging.onOldestPage} aria-label="Oldest page" type="button">
        Oldest
      </button>
      <button className="pager-button" disabled={isOlderDisabled} onClick={paging.onOlderPage} aria-label="Older page" type="button">
        <ChevronLeft aria-hidden="true" size={13} strokeWidth={2.25} />
        Older
      </button>
      <button className="pager-button" disabled={isNewerDisabled} onClick={paging.onNewerPage} aria-label="Newer page" type="button">
        Newer
        <ChevronRight aria-hidden="true" size={13} strokeWidth={2.25} />
      </button>
      <button
        className="pager-button"
        disabled={paging.isLoading || paging.isNewest}
        onClick={paging.onLatestPage}
        aria-label="Latest page"
        type="button"
      >
        Latest
      </button>
    </div>
  );
}
