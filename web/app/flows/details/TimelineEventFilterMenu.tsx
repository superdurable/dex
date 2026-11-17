// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useId, useRef, useState } from 'react';
import { Check, ListFilter, Minus } from 'lucide-react';
import { eventKindNameNouns, eventKinds } from './timelineEventKinds';
import {
  allTimelineEventsFilter,
  clearAllTimelineEventKinds,
  clearAllTimelineEventNames,
  setTimelineEventKindShown,
  toggleTimelineEventName,
  type TimelineEventFilter,
  type TimelineEventFilterKindOption,
  type TimelineKindSelection,
} from './timelineEventFilter';

function checkedState(selection: TimelineKindSelection): boolean | 'mixed' {
  if (selection === 'some') return 'mixed';
  return selection === 'all';
}

/** Names a type's name list, such as "Execute Step types" or "RPC names". */
function nameListLabel(kindName: string, nameNoun: string): string {
  return nameNoun.startsWith(kindName) ? `${nameNoun}s` : `${kindName} ${nameNoun}s`;
}

function CheckMark({ checked }: { checked: boolean | 'mixed' }) {
  return (
    <span aria-hidden="true" className="htl-filter-check" data-checked={String(checked)}>
      {checked === 'mixed' ? <Minus size={11} strokeWidth={3} /> : <Check size={11} strokeWidth={3} />}
    </span>
  );
}

/** The Timeline's Filter button and popover: event types in Legend order, and the names inside each type. */
export function TimelineEventFilterMenu({
  filter,
  options,
  filteredKindCount,
  flowType,
  onChangeFilter,
}: {
  filter: TimelineEventFilter;
  options: readonly TimelineEventFilterKindOption[];
  /** Types the filter hides in whole or in part; above zero, the button shows the filter is on. */
  filteredKindCount: number;
  flowType?: string;
  onChangeFilter: (filter: TimelineEventFilter) => void;
}) {
  const [isOpen, setIsOpen] = useState(false);
  const rootRef = useRef<HTMLDivElement>(null);
  const buttonRef = useRef<HTMLButtonElement>(null);
  const popoverId = useId();
  const isActive = filteredKindCount > 0;
  const activeText = `${filteredKindCount} event ${filteredKindCount === 1 ? 'type' : 'types'} filtered`;

  useEffect(() => {
    if (!isOpen) return undefined;
    const closeOnOutsidePress = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setIsOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key !== 'Escape') return;
      setIsOpen(false);
      buttonRef.current?.focus();
    };
    document.addEventListener('pointerdown', closeOnOutsidePress);
    document.addEventListener('keydown', closeOnEscape);
    return () => {
      document.removeEventListener('pointerdown', closeOnOutsidePress);
      document.removeEventListener('keydown', closeOnEscape);
    };
  }, [isOpen]);

  return (
    <div className="htl-filter" ref={rootRef}>
      <button
        aria-controls={popoverId}
        aria-expanded={isOpen}
        aria-label={isActive ? `Filter, ${activeText}` : undefined}
        className={`htl-filter-button${isActive ? ' is-active' : ''}`}
        onClick={() => setIsOpen((open) => !open)}
        ref={buttonRef}
        title={isActive ? activeText : undefined}
        type="button"
      >
        <ListFilter aria-hidden="true" size={13} strokeWidth={2.25} />
        Filter
        {isActive && <span aria-hidden="true" className="htl-filter-badge">{filteredKindCount}</span>}
      </button>
      <div
        aria-hidden={!isOpen}
        aria-label="Filter Timeline events"
        className="htl-filter-popover"
        data-open={isOpen}
        id={popoverId}
        inert={!isOpen}
        role="dialog"
      >
        <div className="htl-filter-section-head">
          <span className="htl-filter-heading">Event types</span>
          <span className="htl-filter-actions">
            <button onClick={() => onChangeFilter(allTimelineEventsFilter)} type="button">Select all</button>
            <button onClick={() => onChangeFilter(clearAllTimelineEventKinds())} type="button">Clear all</button>
          </span>
        </div>
        <ul className="htl-filter-kinds">
          {options.map((option) => {
            const { icon: Icon, name: kindName, family } = eventKinds[option.kind];
            const nameNoun = eventKindNameNouns[option.kind];
            const checked = checkedState(option.selection);
            return (
              <li className={option.count === 0 ? 'is-absent' : undefined} key={option.kind}>
                <button
                  aria-checked={checked}
                  className="htl-filter-row"
                  onClick={() => onChangeFilter(setTimelineEventKindShown(filter, option.kind, option.selection !== 'all'))}
                  role="checkbox"
                  type="button"
                >
                  <CheckMark checked={checked} />
                  <span aria-hidden="true" className={`htl-legend-mark htl-family-${family}`}>
                    <Icon size={13} strokeWidth={2.25} />
                  </span>
                  <span className="htl-filter-row-name">{kindName}</span>
                  <span className="htl-filter-row-count">{option.count}</span>
                </button>
                {nameNoun && option.names.length > 0 && (
                  <div className="htl-filter-names">
                    <div className="htl-filter-section-head">
                      <span className="htl-filter-noun">{nameNoun}</span>
                      {option.names.length > 1 && (
                        <span className="htl-filter-actions">
                          <button
                            aria-label={`Select all ${nameListLabel(kindName, nameNoun)}`}
                            onClick={() => onChangeFilter(setTimelineEventKindShown(filter, option.kind, true))}
                            type="button"
                          >
                            Select all
                          </button>
                          <button
                            aria-label={`Clear all ${nameListLabel(kindName, nameNoun)}`}
                            onClick={() => onChangeFilter(clearAllTimelineEventNames(filter, option.kind))}
                            type="button"
                          >
                            Clear all
                          </button>
                        </span>
                      )}
                    </div>
                    <ul aria-label={nameListLabel(kindName, nameNoun)}>
                      {option.names.map((name) => (
                        <li key={name.name}>
                          <button
                            aria-checked={name.isShown}
                            className="htl-filter-row htl-filter-name-row"
                            onClick={() => onChangeFilter(toggleTimelineEventName(filter, option.kind, name.name))}
                            role="checkbox"
                            title={name.name}
                            type="button"
                          >
                            <CheckMark checked={name.isShown} />
                            <span className="htl-filter-row-name">{name.name}</span>
                            <span className="htl-filter-row-count">{name.count}</span>
                          </button>
                        </li>
                      ))}
                    </ul>
                  </div>
                )}
              </li>
            );
          })}
        </ul>
        <p className="htl-filter-scope">
          Applies to the Timeline lane; the overview dims hidden events. A selected event always shows.
          {flowType && <> Saved in this browser for Flow type <strong>{flowType}</strong>.</>}
        </p>
      </div>
    </div>
  );
}
