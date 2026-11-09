// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

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
  onFirstPage: () => void;
  onPrevPage: () => void;
  onNextPage: () => void;
  onLastPage: () => void;
}

/** The run header tile that names the latest event and pages through events. */
export function RunEventsTile({ latestEventId, paging }: { latestEventId: number | null; paging: RunEventsPaging | null }) {
  return (
    <div className="run-events-tile">
      <span>Events</span>
      <b className="run-events-latest">{latestEventId === null ? 'None' : `Latest #${latestEventId}`}</b>
      {paging && (
        <>
          <span className="run-events-range">
            Showing #{paging.firstEventId}–#{paging.lastEventId}
            {pageLabel(paging) ? ` · ${pageLabel(paging)}` : ''}
          </span>
          <RunEventsPager paging={paging} />
        </>
      )}
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
        <button className="button ghost" disabled={paging.isLoading} onClick={paging.onBack} type="button">
          Back
        </button>
      )}
      <button className="button ghost" disabled={isNewerDisabled} onClick={paging.onFirstPage} type="button">
        First page
      </button>
      <button
        className="button ghost"
        disabled={isNewerDisabled || !paging.hasNewer}
        onClick={paging.onPrevPage}
        type="button"
      >
        Prev page
      </button>
      <button className="button ghost" disabled={isOlderDisabled} onClick={paging.onNextPage} type="button">
        Next page
      </button>
      <button className="button ghost" disabled={isOlderDisabled} onClick={paging.onLastPage} type="button">
        Last page
      </button>
    </div>
  );
}
