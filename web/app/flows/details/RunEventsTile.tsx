// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ChevronDown, ChevronsDown, ChevronsUp, ChevronUp, ListOrdered, Undo2 } from 'lucide-react';
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

/** The run header tile that names the latest event and pages through events. */
export function RunEventsTile({
  latestEventId,
  paging,
  pageSize,
  onPageSizeChange,
}: {
  latestEventId: number | null;
  paging: RunEventsPaging | null;
  /** How many events one page holds; changing it reloads the Latest page. */
  pageSize?: HistoryPageSize;
  onPageSizeChange?: (size: HistoryPageSize) => void;
}) {
  return (
    <div className="run-events-tile">
      <span className="run-fact-label"><ListOrdered aria-hidden="true" size={14} strokeWidth={2} />Events</span>
      <div className="run-events-line">
        <b className="run-events-latest">{latestEventId === null ? 'None' : `Latest #${latestEventId}`}</b>
        {paging && (
          <span className="run-events-range">
            Showing #{paging.firstEventId}–#{paging.lastEventId}
            {pageLabel(paging) ? ` · ${pageLabel(paging)}` : ''}
          </span>
        )}
      </div>
      <div className="run-events-controls">
        {paging && <RunEventsPager paging={paging} />}
        {pageSize !== undefined && onPageSizeChange && (
          <label className="run-events-page-size">
            Page size
            <select
              onChange={(event) => onPageSizeChange(Number(event.target.value) as HistoryPageSize)}
              value={pageSize}
            >
              {HISTORY_PAGE_SIZES.map((size) => <option key={size} value={size}>{size}</option>)}
            </select>
          </label>
        )}
      </div>
    </div>
  );
}

function pageLabel(paging: RunEventsPaging): string {
  if (paging.pageNumber === null) return '';
  return paging.pageCount === null ? `Page ${paging.pageNumber}` : `Page ${paging.pageNumber} of ${paging.pageCount}`;
}

function RunEventsPager({ paging }: { paging: RunEventsPaging }) {
  const isNewerDisabled = paging.isLoading || paging.isNewest;
  const isOlderDisabled = paging.isLoading || !paging.hasOlder;
  return (
    <div aria-busy={paging.isLoading} aria-label="Event pages" className="run-events-pager" role="group">
      {paging.canGoBack && (
        <button className="pager-button" disabled={paging.isLoading} onClick={paging.onBack} type="button">
          <Undo2 aria-hidden="true" size={14} strokeWidth={2} />
          Back
        </button>
      )}
      <button className="pager-button" disabled={isNewerDisabled} onClick={paging.onLatestPage} type="button">
        <ChevronsUp aria-hidden="true" size={14} strokeWidth={2} />
        Latest page
      </button>
      <button
        className="pager-button"
        disabled={isNewerDisabled || !paging.hasNewer}
        onClick={paging.onNewerPage}
        type="button"
      >
        <ChevronUp aria-hidden="true" size={14} strokeWidth={2} />
        Newer page
      </button>
      <button className="pager-button" disabled={isOlderDisabled} onClick={paging.onOlderPage} type="button">
        <ChevronDown aria-hidden="true" size={14} strokeWidth={2} />
        Older page
      </button>
      <button className="pager-button" disabled={isOlderDisabled} onClick={paging.onOldestPage} type="button">
        <ChevronsDown aria-hidden="true" size={14} strokeWidth={2} />
        Oldest page
      </button>
    </div>
  );
}
