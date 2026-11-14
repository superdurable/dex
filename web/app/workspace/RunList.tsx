// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Fragment, type ReactNode } from 'react';
import {
  ChevronDown,
  CircleCheck,
  CircleDashed,
  CircleDot,
  CircleMinus,
  CircleX,
  Inbox,
  LoaderCircle,
  Unplug,
  WifiOff,
  type LucideIcon,
} from 'lucide-react';
import { formatDate, type TimezonePreference } from '@/lib/format';
import type { V2CatalogEntry, V2Flow } from '@/lib/types';
import { usePreferences } from '../providers';
import { WORK_QUEUE_COPY } from '../work-queue/copy';
import { isOpenFlowStatusCode } from '../work-queue/liveness';
import { formatRelativeAge, runRowTone, useNowMs, type RunRowTone } from '../work-queue/runRowTone';
import { RUN_COPY } from './runCopy';
import { groupRuns } from './runOrder';
import { SEARCH_COPY } from './searchCopy';
import { runRow, uiSlotsOf, type UISlotMap } from './uiSlots';
import type { FlowSearch } from './useFlowSearch';

const TONE_ICON: Record<RunRowTone, LucideIcon> = {
  waiting: CircleDot,
  active: CircleDashed,
  success: CircleCheck,
  danger: CircleX,
  muted: CircleMinus,
};

/**
 * One run list keeps Run and Work Queue in the same visual language.
 *
 * Everything either view needs to differ on arrives as a prop: the heading, the note beside it,
 * and an optional scope control. The rows themselves are identical by construction.
 *
 * The page bar already names the page, so the list leads with the Flow type instead of a title.
 */
export function RunList({
  entry,
  flowTypes,
  search,
  selectedFlowID,
  strandedFlowIDs,
  heading,
  headerNote,
  headerAction,
  modeSwitch,
  scope,
  emptyText,
  showFlowType = false,
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
  /** Names the list for assistive technology and the collapsed rail. */
  heading: string;
  headerNote?: string;
  headerAction?: ReactNode;
  /** Runs or Agents, above everything else. */
  modeSwitch?: ReactNode;
  /** What the list is narrowed to, and the control that narrowed it. */
  scope?: ReactNode;
  emptyText: string;
  /** Names each row's Flow type, for a list that spans several. */
  showFlowType?: boolean;
  collapsed?: boolean;
  onExpand?: () => void;
  onSelectFlowType: (flowType: string) => void;
  onSelectRun: (flow: V2Flow) => void;
}) {
  const { timezone } = usePreferences();
  const nowMs = useNowMs();
  const { flows, liveness } = search;
  const groups = groupRuns(flows);
  // Per Flow type, because a row reads the slots its own Flow declared.
  const uiSlotsByFlowType = new Map(flowTypes.map((candidate) => [candidate.flowType, uiSlotsOf(candidate.definition)]));
  const stateText = liveness === 'loading'
    ? WORK_QUEUE_COPY.loading
    : liveness === 'unreachable'
      ? WORK_QUEUE_COPY.unreachable
      : liveness === 'stale'
        ? WORK_QUEUE_COPY.stale
        : '';
  const isEmpty = stateText === '' && flows.length === 0;
  return (
    <aside className="wq-list" aria-label={heading} data-collapsed={collapsed ? 'true' : undefined}>
      {collapsed ? (
        <button className="v2-rail" onClick={onExpand} title={RUN_COPY.expandList} type="button">
          {heading}
        </button>
      ) : (
        <>
      <div className="wq-head">
        {modeSwitch}
        <div className="wq-head-line">
          {flowTypes.length > 1 ? (
            // The name wraps in a span; a native select can only truncate, so it sits invisibly on top.
            <label className="wq-flowtype" title={entry.flowType}>
              <FlowTypeName flowType={entry.flowType} />
              <ChevronDown aria-hidden="true" size={16} />
              <select
                aria-label={SEARCH_COPY.flowTypeLabel}
                value={entry.flowType}
                onChange={(event) => onSelectFlowType(event.target.value)}
              >
                {flowTypes.map((candidate) => (
                  <option key={candidate.flowType} value={candidate.flowType}>{candidate.flowType}</option>
                ))}
              </select>
            </label>
          ) : (
            <h2 className="wq-flowtype" title={entry.flowType}><FlowTypeName flowType={entry.flowType} /></h2>
          )}
          {headerNote !== undefined && (
            <span className="wq-live" data-liveness={liveness}>
              <span aria-hidden="true" className="wq-live-dot" />
              {headerNote}
            </span>
          )}
          {headerAction}
        </div>
      </div>
      {scope !== undefined && <section className="wq-scope">{scope}</section>}
      {stateText !== '' && (
        <p className="wq-state" data-liveness={liveness} role="status">
          <LivenessIcon liveness={liveness} />
          {stateText}
        </p>
      )}
      <div className="wq-scroll" data-zone="list">
        {isEmpty && (
          <div className="wq-list-empty">
            <Inbox aria-hidden="true" size={22} strokeWidth={1.75} />
            <p>{emptyText}</p>
          </div>
        )}
        {groups.map((group) => (
          <section className="wq-group" data-group={group.key} key={group.key}>
            <h3 className="wq-grouphead">
              <span>{group.label}</span>
              <span className="wq-count">{group.flows.length}</span>
            </h3>
            <ul className="wq-rows">
              {group.flows.map((flow) => (
                <li
                  className="wq-item"
                  data-selected={flow.flowId === selectedFlowID ? 'true' : undefined}
                  data-stranded={strandedFlowIDs.has(flow.flowId) ? 'true' : undefined}
                  key={flow.flowId}
                >
                  <RunRowButton
                    flow={flow}
                    isSelected={flow.flowId === selectedFlowID}
                    nowMs={nowMs}
                    showFlowType={showFlowType}
                    uiSlots={uiSlotsByFlowType.get(flow.flowType) ?? {}}
                    stranded={strandedFlowIDs.has(flow.flowId)}
                    timezone={timezone}
                    onSelect={onSelectRun}
                  />
                </li>
              ))}
            </ul>
          </section>
        ))}
        {search.hasNextPage && flows.length > 0 && (
          <p className="wq-more">{SEARCH_COPY.moreRuns(flows.length)}</p>
        )}
      </div>
        </>
      )}
    </aside>
  );
}

/** A Flow type name that may break between its words, such as `AgenticCustomer` and `RefundFlow`. */
function FlowTypeName({ flowType }: { flowType: string }) {
  const words = flowType.split(/(?<=[a-z0-9])(?=[A-Z])|(?<=[._-])/);
  return (
    <span className="wq-flowtype-name">
      {words.map((word, index) => (
        <Fragment key={index}>{index > 0 && <wbr />}{word}</Fragment>
      ))}
    </span>
  );
}

function LivenessIcon({ liveness }: { liveness: FlowSearch['liveness'] }) {
  if (liveness === 'loading') return <LoaderCircle aria-hidden="true" className="wq-spin" size={14} />;
  return <WifiOff aria-hidden="true" size={14} />;
}

/**
 * Three things: what names the run, when it started, where it has got to.
 *
 * No denser than the row it replaces. What the Flow declares for `recommendation` and `reason` is
 * deliberately left to the drawer — a list of reasons stops being a list.
 */
function RunRowButton({ flow, isSelected, nowMs, showFlowType, uiSlots, stranded, timezone, onSelect }: {
  flow: V2Flow;
  isSelected: boolean;
  nowMs: number;
  showFlowType: boolean;
  uiSlots: UISlotMap;
  stranded: boolean;
  timezone: TimezonePreference;
  onSelect: (flow: V2Flow) => void;
}) {
  const row = runRow(flow, uiSlots);
  const tone = stranded ? 'muted' : runRowTone(flow.flowStatusCode, row.status);
  const ToneIcon = stranded ? Unplug : TONE_ICON[tone];
  return (
    <button
      aria-current={isSelected ? 'true' : undefined}
      className="wq-row"
      onClick={() => onSelect(flow)}
      type="button"
    >
      <span className="wq-row-icon" data-tone={tone} title={flow.flowStatus}>
        <ToneIcon aria-hidden="true" size={16} strokeWidth={2.2} />
      </span>
      <span
        className={row.titleIsFlowID ? 'wq-row-title t-mono' : 'wq-row-title'}
        title={flow.flowId}
      >
        {row.title}
      </span>
      <time className="wq-row-time" dateTime={flow.startTime ?? undefined} title={formatDate(flow.startTime, timezone)}>
        {formatRelativeAge(flow.startTime, nowMs, timezone)}
      </time>
      <span className="wq-row-meta" data-tone={tone}>
        {/* A closed run's last reported status is stale, so its execution status leads. */}
        {!isOpenFlowStatusCode(flow.flowStatusCode) && row.status !== flow.flowStatus && (
          <span className="wq-row-closed">{flow.flowStatus}</span>
        )}
        {row.status}
        {showFlowType && <span className="wq-row-flowtype">{flow.flowType}</span>}
      </span>
      {stranded && <span className="wq-row-stranded">{WORK_QUEUE_COPY.strandedRow}</span>}
    </button>
  );
}
