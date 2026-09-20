// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { formatTimeOfDay } from '@/lib/format';
import type { V2CatalogEntry, V2Flow } from '@/lib/types';
import { usePreferences } from '../../providers';
import { QUEUE_COPY } from '../queue/copy';
import type { FlowSearch } from '../workspace/useFlowSearch';
import { RUN_COPY } from './copy';
import { groupRuns } from './runOrder';

/**
 * Pick a run, nothing more. The filter builder, Search and pager belong to the Queue:
 * an Admin driving one run does not scope a search, and they cost the canvas its width.
 */
export function RunSwitcher({
  entry,
  flowTypes,
  search,
  selectedFlowID,
  strandedFlowIDs,
  attentionAttributeKey,
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
          ? RUN_COPY.noRuns
          : '';
  return (
    <aside className="rsw" aria-label={RUN_COPY.runsHeading}>
      <div className="sq-head">
        <span className="sq-title" title={RUN_COPY.selectPrompt}>{RUN_COPY.runsHeading}</span>
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
      {stateText !== '' && (
      <p className="sq-state" data-liveness={liveness}>
        {stateText}
      </p>
      )}
      <div className="rsw-scroll">
        {groups.map((group) => (
          <section className="rsw-group" data-group={group.key} key={group.key}>
            <h3 className="rsw-grouphead">{group.label}</h3>
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
