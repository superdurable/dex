// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { LogIn, LogOut, Pause, Rows3, Workflow, type LucideIcon } from 'lucide-react';
import { ExperimentalTag } from '../../components/frame/appSections';

/** One tab of a run page. */
export interface RunView<T extends string> {
  id: T;
  label: string;
  Icon: LucideIcon;
  isExperimental?: boolean;
  /** Other view ids that select this tab, such as Events' Graph view. */
  alsoSelectedFor?: readonly T[];
}

/** The run's tabs, all in one row. */
export function RunViewTabs<T extends string>({
  views,
  shownView,
  onSelect,
}: {
  views: readonly RunView<T>[];
  shownView: T;
  onSelect: (view: T) => void;
}) {
  const isSelected = (view: RunView<T>) => view.id === shownView || (view.alsoSelectedFor?.includes(shownView) ?? false);
  return (
    <div className="run-views">
      <div aria-label="Run views" className="run-tabs" role="tablist">
        {views.map((view) => (
          <button
            key={view.id}
            aria-selected={isSelected(view)}
            className={isSelected(view) ? 'active' : ''}
            data-tab={view.id}
            data-tooltip={view.isExperimental ? `${view.label} · Experimental` : view.label}
            onClick={() => onSelect(view.id)}
            role="tab"
            type="button"
          >
            <view.Icon aria-hidden="true" size={15} strokeWidth={2} />
            <span className="run-tab-label">{view.label}</span>
            {view.isExperimental && <ExperimentalTag />}
          </button>
        ))}
      </div>
    </div>
  );
}

/** Events' two views of the same event page; the choice is a preference this browser remembers. */
export type EventsView = 'timeline' | 'graph';

export function EventsViewSwitch({ view, onChange }: { view: EventsView; onChange: (view: EventsView) => void }) {
  return (
    <span aria-label="Events view" className="events-view-switch" role="group">
      <button aria-pressed={view === 'timeline'} onClick={() => onChange('timeline')} type="button">
        <Rows3 aria-hidden="true" size={13} strokeWidth={2.25} />
        Timeline
      </button>
      <button aria-pressed={view === 'graph'} onClick={() => onChange('graph')} type="button">
        <Workflow aria-hidden="true" size={13} strokeWidth={2.25} />
        Graph
      </button>
    </span>
  );
}

/** Whether the page follows new events; pausing is for this visit, and resuming reads the run again. */
export function LiveStatus({ isLive, onChange }: { isLive: boolean; onChange: (isLive: boolean) => void }) {
  return isLive ? (
    <button
      aria-label="Live: following new events. Pause updates"
      className="live-status is-on"
      onClick={() => onChange(false)}
      title="Following new events. Click to pause updates."
      type="button"
    >
      <span aria-hidden="true" className="live-status-dot" />
      Live
    </button>
  ) : (
    <button
      aria-label="Updates paused. Resume"
      className="live-status"
      onClick={() => onChange(true)}
      title="Not following new events. Click to resume and read the run again."
      type="button"
    >
      <Pause aria-hidden="true" size={12} strokeWidth={2.5} />
      Updates paused · <b>Resume</b>
    </button>
  );
}

/** Input and Result select the events that hold them: Flow started, and Flow closed once the run ends. */
export function RunInputOutputButtons({
  startEventId,
  closeEventId,
  selectedEventId,
  onShowEvent,
}: {
  startEventId: number | null;
  closeEventId: number | null;
  selectedEventId: number | null;
  onShowEvent: (eventId: number) => void;
}) {
  if (startEventId === null && closeEventId === null) return null;
  return (
    <span className="run-io">
      {startEventId !== null && (
        <button
          aria-pressed={selectedEventId === startEventId}
          className="run-io-button"
          onClick={() => onShowEvent(startEventId)}
          title={`Show the run's start input, event #${startEventId}`}
          type="button"
        >
          <LogIn aria-hidden="true" size={13} strokeWidth={2.25} />
          Input
        </button>
      )}
      {closeEventId !== null && (
        <button
          aria-pressed={selectedEventId === closeEventId}
          className="run-io-button"
          onClick={() => onShowEvent(closeEventId)}
          title={`Show how the run ended, event #${closeEventId}`}
          type="button"
        >
          <LogOut aria-hidden="true" size={13} strokeWidth={2.25} />
          Result
        </button>
      )}
    </span>
  );
}
