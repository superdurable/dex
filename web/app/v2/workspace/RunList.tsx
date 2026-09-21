// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ReactNode } from 'react';
import { formatTimeOfDay, type TimezonePreference } from '@/lib/format';
import type { V2CatalogEntry, V2Flow } from '@/lib/types';
import { usePreferences } from '../../providers';
import { QUEUE_COPY } from '../queue/copy';
import { RUN_COPY } from '../run/copy';
import { groupRuns } from '../run/runOrder';
import { SEARCH_COPY } from './searchCopy';
import { runRow, slotsOf, type SlotMap } from './slots';
import type { FlowSearch } from './useFlowSearch';

/**
 * One list of runs, shared by Run and Inbox so the two cannot drift into two visual languages.
 *
 * Everything either view needs to differ on arrives as a prop: the heading, the note beside it,
 * and an optional scope control. The rows themselves are identical by construction.
 */
export function RunList({
  entry,
  flowTypes,
  search,
  selectedFlowID,
  strandedFlowIDs,
  heading,
  headerNote,
  scope,
  roleControl,
  emptyText,
  collapsed = false,
  onExpand,
  onSelectFlowType,
  onSelectRun,
}: {
  entry: V2CatalogEntry;
  flowTypes: V2CatalogEntry[];
  search: FlowSearch;
  selectedFlowID: string;
  strandedFlowIDs: ReadonlySet<string>;
  heading: string;
  headerNote?: string;
  /** What the list is narrowed to, and the control that narrowed it. */
  scope?: ReactNode;
  /** Which party the reader is. Only the participant view asks. */
  roleControl?: ReactNode;
  emptyText: string;
  collapsed?: boolean;
  onExpand?: () => void;
  onSelectFlowType: (flowType: string) => void;
  onSelectRun: (flowID: string) => void;
}) {
  const { timezone } = usePreferences();
  const { flows, liveness, loading } = search;
  const groups = groupRuns(flows);
  const slots = slotsOf(entry.definition);
  const stateText = liveness === 'loading'
    ? QUEUE_COPY.loading
    : liveness === 'unreachable'
      ? QUEUE_COPY.unreachable
      : liveness === 'stale'
        ? QUEUE_COPY.stale
        : flows.length === 0
          ? emptyText
          : '';
  return (
    <aside className="rsw" aria-label={heading} data-collapsed={collapsed ? 'true' : undefined}>
      {collapsed ? (
        <button className="v2-rail" onClick={onExpand} title={RUN_COPY.expandList} type="button">
          {heading}
        </button>
      ) : (
        <>
      <div className="sq-head">
        <span className="sq-title" title={RUN_COPY.selectPrompt}>{heading}</span>
        {headerNote !== undefined && <span className="sq-live">{headerNote}</span>}
      </div>
      {flowTypes.length > 1 && (
        <section className="rsw-zone" data-zone="pick">
          <select
            aria-label={SEARCH_COPY.flowTypeLabel}
            className="rsw-flowtype"
            value={entry.flowType}
            onChange={(event) => onSelectFlowType(event.target.value)}
          >
            {flowTypes.map((candidate) => (
              <option key={candidate.flowType} value={candidate.flowType}>{candidate.flowType}</option>
            ))}
          </select>
        </section>
      )}
      {roleControl !== undefined && (
        <section className="rsw-zone" data-zone="role">{roleControl}</section>
      )}
      {scope !== undefined && (
        <section className="rsw-zone" data-zone="find">
          <h3 className="rsw-zonehead">{SEARCH_COPY.findHeading}</h3>
          {scope}
        </section>
      )}
      {stateText !== '' && (
        <p className="sq-state" data-liveness={liveness}>{stateText}</p>
      )}
      <div className="rsw-scroll" data-zone="list">
        {groups.map((group) => (
          <section className="rsw-group" data-group={group.key} key={group.key}>
            {groups.length > 1 && <h3 className="rsw-grouphead">{group.label}</h3>}
            <ul className="rsw-list">
              {group.flows.map((flow) => (
                <li
                  className="rsw-item"
                  data-selected={flow.flowId === selectedFlowID ? 'true' : undefined}
                  data-stranded={strandedFlowIDs.has(flow.flowId) ? 'true' : undefined}
                  key={flow.flowId}
                >
                  <RunRowButton
                    flow={flow}
                    slots={slots}
                    stranded={strandedFlowIDs.has(flow.flowId)}
                    timezone={timezone}
                    onSelect={onSelectRun}
                  />
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
        </>
      )}
    </aside>
  );
}

/**
 * Three things: what names the run, when it started, where it has got to.
 *
 * No denser than the row it replaces. What the Flow declares for `recommendation` and `reason` is
 * deliberately left to the drawer — a list of reasons stops being a list.
 */
function RunRowButton({ flow, slots, stranded, timezone, onSelect }: {
  flow: V2Flow;
  slots: SlotMap;
  stranded: boolean;
  timezone: TimezonePreference;
  onSelect: (flowID: string) => void;
}) {
  const row = runRow(flow, slots);
  return (
    <button className="rsw-row" onClick={() => onSelect(flow.flowId)} type="button">
      <span
        className={row.titleIsFlowID ? 'rsw-id t-mono' : 'rsw-id'}
        title={flow.flowId}
      >
        {row.title}
      </span>
      <span className="rsw-time">{formatTimeOfDay(flow.startTime, timezone)}</span>
      <span className="rsw-state">{row.status}</span>
      {stranded && <span className="rsw-stranded">{QUEUE_COPY.strandedRow}</span>}
    </button>
  );
}
