// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId, useRef, useState } from 'react';
import { ChevronDown, LogIn, LogOut, Pause, type LucideIcon } from 'lucide-react';
import { ExperimentalTag } from '../../components/frame/appSections';
import { usePopover } from '../../components/frame/usePopover';

/** One view of a run: a tab, or an item of the More menu under its group. */
export interface RunView<T extends string> {
  id: T;
  label: string;
  Icon: LucideIcon;
  isExperimental?: boolean;
  /** The More menu's group; a view without one is a tab. */
  moreGroup?: 'Advanced' | 'Experimental';
  /** Other view ids that select this tab, such as Graph's Design view. */
  alsoSelectedFor?: readonly T[];
}

/** The run's tabs, then More for the views few runs need; More names the view it holds when one is open. */
export function RunViewTabs<T extends string>({
  views,
  shownView,
  onSelect,
}: {
  views: readonly RunView<T>[];
  shownView: T;
  onSelect: (view: T) => void;
}) {
  const { isOpen, setIsOpen, rootRef } = usePopover<HTMLDivElement>();
  const menuId = useId();
  const moreButton = useRef<HTMLButtonElement>(null);
  const [menuLeft, setMenuLeft] = useState(0);
  const tabs = views.filter((view) => !view.moreGroup);
  const more = views.filter((view) => view.moreGroup);
  const isSelected = (view: RunView<T>) => view.id === shownView || (view.alsoSelectedFor?.includes(shownView) ?? false);
  const openMoreView = more.find(isSelected);
  const groups = (['Advanced', 'Experimental'] as const)
    .map((group) => ({ group, items: more.filter((view) => view.moreGroup === group) }))
    .filter(({ items }) => items.length > 0);
  // The menu sits outside the scrolling tab list, so the list never clips it.
  return (
    <div className="run-views" ref={rootRef}>
      <div aria-label="Run views" className="run-tabs" role="tablist">
        {tabs.map((view) => (
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
        {more.length > 0 && (
          <button
            aria-controls={menuId}
            aria-expanded={isOpen}
            aria-haspopup="menu"
            aria-selected={openMoreView !== undefined}
            className={openMoreView ? 'active' : ''}
            data-tab="more"
            onClick={() => {
              const button = moreButton.current;
              if (button) setMenuLeft(button.getBoundingClientRect().left - (rootRef.current?.getBoundingClientRect().left ?? 0));
              setIsOpen((open) => !open);
            }}
            ref={moreButton}
            role="tab"
            type="button"
          >
            {openMoreView && <openMoreView.Icon aria-hidden="true" size={15} strokeWidth={2} />}
            <span className="run-tab-label">{openMoreView ? openMoreView.label : 'More'}</span>
            <ChevronDown aria-hidden="true" size={14} strokeWidth={2.25} />
          </button>
        )}
      </div>
      {isOpen && (
        <div aria-label="More views" className="app-bar-popover run-tabs-more-popover" id={menuId} role="menu" style={{ left: menuLeft }}>
          {groups.map(({ group, items }) => (
            <div className="run-tabs-more-group" key={group} role="group" aria-label={group}>
              <span aria-hidden="true" className="run-tabs-more-heading">{group}</span>
              {items.map((view) => (
                <button
                  aria-current={isSelected(view) ? 'true' : undefined}
                  className="app-bar-menu-item"
                  data-tab={view.id}
                  key={view.id}
                  onClick={() => {
                    setIsOpen(false);
                    onSelect(view.id);
                  }}
                  role="menuitem"
                  type="button"
                >
                  <span className="app-bar-menu-icon"><view.Icon aria-hidden="true" size={15} strokeWidth={2} /></span>
                  <span className="app-bar-menu-words"><b>{view.label}{view.isExperimental && <ExperimentalTag />}</b></span>
                </button>
              ))}
            </div>
          ))}
        </div>
      )}
    </div>
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
