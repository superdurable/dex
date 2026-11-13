// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId } from 'react';
import { ChevronDown, History, Square, type LucideIcon } from 'lucide-react';
import { usePopover } from '../../components/frame/usePopover';

interface OperateAction {
  id: 'stop' | 'time-travel';
  /** The short name on the main button; its description is the button's tooltip. */
  label: string;
  menuLabel: string;
  description: string;
  Icon: LucideIcon;
  onSelect: () => void;
}

/** The run's actions as one split button; the main action follows the run's state. */
export function OperateMenu({
  isRunning,
  onStop,
  onTimeTravel,
  onTimeTravelFromFailure,
}: {
  isRunning: boolean;
  onStop: () => void;
  onTimeTravel: () => void;
  /** Set on a failed run: time travel prefilled at the failed event. */
  onTimeTravelFromFailure?: () => void;
}) {
  const { isOpen, setIsOpen, rootRef } = usePopover<HTMLDivElement>();
  const menuId = useId();
  const stop: OperateAction = {
    id: 'stop', label: 'Stop', menuLabel: 'Stop…', description: 'Cancel, terminate or fail the run', Icon: Square, onSelect: onStop,
  };
  const timeTravel: OperateAction = {
    id: 'time-travel', label: 'Time travel', menuLabel: 'Time travel…', description: 'Run again from an earlier point', Icon: History,
    onSelect: onTimeTravel,
  };
  const fromFailure: OperateAction | null = onTimeTravelFromFailure ? {
    id: 'time-travel', label: 'Time travel', menuLabel: 'Time travel from failure…', description: 'Run again from the failed event',
    Icon: History, onSelect: onTimeTravelFromFailure,
  } : null;
  const [primary, ...others] = isRunning ? [stop, timeTravel] : fromFailure ? [fromFailure, timeTravel] : [timeTravel];
  return (
    <div className="operate-menu" ref={rootRef}>
      <div aria-label="Operate" className="operate-split" role="group">
        <button
          className={`operate-primary operate-primary--${primary.id}`}
          aria-label={primary.menuLabel}
          onClick={primary.onSelect}
          title={primary.description}
          type="button"
        >
          <primary.Icon aria-hidden="true" size={14} strokeWidth={2.25} />
          <span className="operate-primary-label">{primary.label}</span>
        </button>
        {others.length > 0 && (
          <button
            aria-controls={menuId}
            aria-expanded={isOpen}
            aria-haspopup="menu"
            aria-label="More run actions"
            className="operate-more"
            onClick={() => setIsOpen((open) => !open)}
            type="button"
          >
            <ChevronDown aria-hidden="true" size={14} strokeWidth={2.25} />
          </button>
        )}
      </div>
      {isOpen && (
        <div aria-label="Run actions" className="app-bar-popover operate-menu-popover" id={menuId} role="menu">
          {others.map((action) => (
            <button
              className="app-bar-menu-item"
              key={action.menuLabel}
              onClick={() => {
                setIsOpen(false);
                action.onSelect();
              }}
              role="menuitem"
              type="button"
            >
              <span className="app-bar-menu-icon"><action.Icon aria-hidden="true" size={15} strokeWidth={2} /></span>
              <span className="app-bar-menu-words">
                <b>{action.menuLabel}</b>
                <small>{action.description}</small>
              </span>
            </button>
          ))}
        </div>
      )}
    </div>
  );
}
