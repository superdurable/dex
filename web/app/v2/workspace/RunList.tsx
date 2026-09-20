// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ReactNode } from 'react';
import { formatTimeOfDay } from '@/lib/format';
import type { V2CatalogEntry, V2Flow } from '@/lib/types';
import { usePreferences } from '../../providers';
import { QUEUE_COPY } from '../queue/copy';
import { RUN_COPY } from '../run/copy';
import { groupRuns } from '../run/runOrder';
import { SEARCH_COPY } from './searchCopy';
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
  attentionAttributeKey,
  heading,
  headerNote,
  scope,
  emptyText,
  onSelectFlowType,
  onSelectRun,
}: {
  entry: V2CatalogEntry;
  flowTypes: V2CatalogEntry[];
  search: FlowSearch;
  selectedFlowID: string;
  strandedFlowIDs: ReadonlySet<string>;
  /** First indexed Attribute, shown under the run id as its own value. */
  attentionAttributeKey: string | null;
  heading: string;
  headerNote?: string;
  /** What the list is narrowed to, and the control that narrowed it. */
  scope?: ReactNode;
  emptyText: string;
  onSelectFlowType: (flowType: string) => void;
  onSelectRun: (flowID: string) => void;
}) {
  const { timezone } = usePreferences();
  const { flows, liveness, loading } = search;
  const groups = groupRuns(flows);
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
    <aside className="rsw" aria-label={heading}>
      <div className="sq-head">
        <span className="sq-title" title={RUN_COPY.selectPrompt}>{heading}</span>
        {headerNote !== undefined && <span className="sq-live">{headerNote}</span>}
        <button className="sq-refresh" disabled={loading} onClick={search.runSearch} type="button">
          {loading ? QUEUE_COPY.loading : QUEUE_COPY.refresh}
        </button>
      </div>
      {flowTypes.length > 1 && (
        <select
          aria-label="Flow type"
          className="rsw-flowtype"
          value={entry.flowType}
          onChange={(event) => onSelectFlowType(event.target.value)}
        >
          {flowTypes.map((candidate) => (
            <option key={candidate.flowType} value={candidate.flowType}>{candidate.flowType}</option>
          ))}
        </select>
      )}
      {scope}
      <p className="rsw-scopenote">
        {SEARCH_COPY.scopeNote(entry.definition.indexedAttributes.map((a) => a.description))}
      </p>
      {stateText !== '' && (
        <p className="sq-state" data-liveness={liveness}>{stateText}</p>
      )}
      <div className="rsw-scroll">
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
                  <button className="rsw-row" onClick={() => onSelectRun(flow.flowId)} type="button">
                    <span className="rsw-id t-mono" title={flow.flowId}>{flow.flowId}</span>
                    <span className="rsw-time">{formatTimeOfDay(flow.startTime, timezone)}</span>
                    <span className="rsw-state">{runStateText(flow, attentionAttributeKey)}</span>
                    {strandedFlowIDs.has(flow.flowId) && (
                      <span className="rsw-stranded">{QUEUE_COPY.strandedRow}</span>
                    )}
                  </button>
                </li>
              ))}
            </ul>
          </section>
        ))}
      </div>
    </aside>
  );
}

/** The Flow's own indexed value when it has one, else the execution status. */
function runStateText(flow: V2Flow, attentionAttributeKey: string | null): string {
  if (attentionAttributeKey === null) return flow.flowStatus;
  const value = flow.indexedAttributes[attentionAttributeKey];
  if (value === null || value === undefined || value === '') return flow.flowStatus;
  return String(value);
}
