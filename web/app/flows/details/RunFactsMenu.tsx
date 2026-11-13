// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId, type ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { CalendarClock, ChevronDown, CornerDownRight, Flag, Layers, ListOrdered, Tag, Timer } from 'lucide-react';
import type { FlowExecution } from '@/lib/types';
import { runPath } from '../../routes';
import { CopyButton } from '../../components/CopyButton';
import { usePopover } from '../../components/frame/usePopover';

/** One fact about the run, shown in the facts popover. */
export interface RunFact {
  label: string;
  value: ReactNode;
  icon: 'started' | 'closed' | 'duration' | 'attempt' | 'events' | 'type';
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
  childFlowsHref,
}: {
  flowId: string;
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
        <span className="run-facts-name mono">{middleTruncate(flowId, 30)}</span>
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
          {childFlows.length > 0 && (
            <div className="run-facts-children">
              <span>Child Flows</span>
              {childFlows.slice(0, 5).map((child) => (
                <Link key={child.flowId} title={child.flowId} to={runPath(child.flowId, child.runId)}>
                  <CornerDownRight aria-hidden="true" size={13} strokeWidth={2} />
                  <span className="mono">{middleTruncate(child.flowId, 40)}</span>
                  <small>{child.flowStatus}</small>
                </Link>
              ))}
              {childFlowsHref && childFlows.length > 5 && <Link to={childFlowsHref}>See all child Flows</Link>}
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
