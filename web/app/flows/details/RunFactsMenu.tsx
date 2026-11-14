// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { CalendarClock, ChevronDown, CornerDownRight, CornerLeftUp, Flag, Layers, ListOrdered, Tag, Timer } from 'lucide-react';
import type { FlowExecution } from '@/lib/types';
import { runPath } from '../../routes';
import { CopyButton } from '../../components/CopyButton';
import { StatusBadge } from '../../components/StatusBadge';
import { usePopover } from '../../components/frame/usePopover';

/** One fact about the run, shown in the facts popover. */
export interface RunFact {
  label: string;
  value: ReactNode;
  icon: 'started' | 'closed' | 'duration' | 'attempt' | 'events' | 'type';
}

/** The Flow ID the bar shows: a SubFlow of the named parent keeps only the part after its parent's prefix. */
export function barFlowIdOf(flowId: string, parentFlowId?: string): string {
  const prefix = parentFlowId ? `SubFlow:${parentFlowId}-` : '';
  return prefix && flowId.startsWith(prefix) && flowId.length > prefix.length ? flowId.slice(prefix.length) : flowId;
}

/** Shortens a long ID in the middle, so the distinguishing tail stays visible. */
export function middleTruncate(text: string, maxLength: number): string {
  if (text.length <= maxLength) return text;
  const tail = Math.ceil((maxLength - 1) / 2);
  return `${text.slice(0, maxLength - 1 - tail)}…${text.slice(-tail)}`;
}

const FACT_ICONS = { started: CalendarClock, closed: Flag, duration: Timer, attempt: Layers, events: ListOrdered, type: Tag };

/** The run's name in the bar; it opens the run's facts and its IDs to copy. */
export function RunFactsMenu({
  flowId,
  runId,
  facts,
  childFlows = [],
  hasMoreChildFlows = false,
  childFlowsHref,
  attemptControl,
  parentFlowId,
}: {
  /** The parent's Flow ID; a SubFlow ID drops its "SubFlow:<parent>-" prefix in the bar, which the run line names. */
  parentFlowId?: string;
  /** The attempt picker, repeated in the popover for when the bar hides it. */
  attemptControl?: ReactNode;
  flowId: string;
  /** True when the run has more child Flows than childFlows lists. */
  hasMoreChildFlows?: boolean;
  runId: string;
  facts: readonly RunFact[];
  childFlows?: readonly FlowExecution[];
  /** The Flows search that lists every child Flow. */
  childFlowsHref?: string;
}) {
  const { isOpen, setIsOpen, rootRef } = usePopover<HTMLDivElement>();
  const popoverId = useId();
  return (
    <div className="run-facts-menu" ref={rootRef}>
      <button
        aria-controls={popoverId}
        aria-expanded={isOpen}
        aria-haspopup="dialog"
        aria-label={`Run details for ${flowId}`}
        className="run-facts-button"
        onClick={() => setIsOpen((open) => !open)}
        title={flowId}
        type="button"
      >
        <span className="run-facts-name mono">
          {middleTruncate(barFlowIdOf(flowId, parentFlowId), 26)}
        </span>
        <ChevronDown aria-hidden="true" size={14} strokeWidth={2.25} />
      </button>
      {isOpen && (
        <div aria-label="Run details" className="app-bar-popover run-facts-popover" id={popoverId} role="dialog">
          <dl className="run-facts-list">
            {facts.map(({ label, value, icon }) => {
              const Icon = FACT_ICONS[icon];
              return (
                <div data-fact={icon} key={label}>
                  <dt><Icon aria-hidden="true" size={14} strokeWidth={2} />{label}</dt>
                  <dd>{value}</dd>
                </div>
              );
            })}
          </dl>
          {attemptControl && <div className="run-facts-attempt-control">{attemptControl}</div>}
          {childFlows.length > 0 && (
            <div className="run-facts-children">
              <span>Child Flows · {childFlowCountLabel(childFlows.length, hasMoreChildFlows)}</span>
              {childFlows.slice(0, 5).map((child) => (
                <Link key={child.flowId} title={child.flowId} to={runPath(child.flowId, child.runId)}>
                  <CornerDownRight aria-hidden="true" size={13} strokeWidth={2} />
                  <span className="mono">{barFlowIdOf(child.flowId, flowId)}</span>
                  <StatusBadge status={child.flowStatus} />
                </Link>
              ))}
              {childFlowsHref && (childFlows.length > 5 || hasMoreChildFlows) && <Link to={childFlowsHref}>See all child Flows</Link>}
            </div>
          )}
          <div className="run-facts-ids">
            <div>
              <span>Flow ID</span>
              <code className="mono">{flowId}</code>
              <CopyButton label="Copy Flow ID" value={flowId}>Copy</CopyButton>
            </div>
            <div>
              <span>Run ID</span>
              <code className="mono">{runId}</code>
              <CopyButton label="Copy run ID" value={runId}>Copy</CopyButton>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

function childFlowCountLabel(count: number, hasMore: boolean): string {
  return `${count}${hasMore ? '+' : ''} ${count === 1 && !hasMore ? 'child' : 'children'}`;
}

/** The run's parent and child Flows, for Overview. */
export function RunRelations({
  parentFlowId,
  childFlows,
  hasMoreChildFlows,
  childFlowsHref,
}: {
  parentFlowId?: string;
  childFlows: readonly FlowExecution[];
  hasMoreChildFlows: boolean;
  childFlowsHref: string;
}) {
  if (!parentFlowId && childFlows.length === 0) return null;
  return (
    <section aria-label="Related Flows" className="card run-relations">
      <h3>Related Flows</h3>
      {parentFlowId && (
        <Link className="run-relation" title={parentFlowId} to={runPath(parentFlowId)}>
          <CornerLeftUp aria-hidden="true" size={14} strokeWidth={2} />
          <span className="run-relation-kind">Parent</span>
          <span className="mono">{middleTruncate(parentFlowId, 60)}</span>
        </Link>
      )}
      {childFlows.map((child) => (
        <Link className="run-relation" key={child.flowId} title={child.flowId} to={runPath(child.flowId, child.runId)}>
          <CornerDownRight aria-hidden="true" size={14} strokeWidth={2} />
          <span className="run-relation-kind">Child</span>
          <span className="mono">{middleTruncate(child.flowId, 60)}</span>
          <StatusBadge status={child.flowStatus} />
        </Link>
      ))}
      {hasMoreChildFlows && <Link to={childFlowsHref}>See all {childFlowCountLabel(childFlows.length, true)}</Link>}
    </section>
  );
}
