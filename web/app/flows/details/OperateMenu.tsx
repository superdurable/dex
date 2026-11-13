// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId } from 'react';
import { ChevronDown, History, Square } from 'lucide-react';
import { usePopover } from '../../components/frame/usePopover';

/** The run's state-changing actions, one click away from a dialog and never one click from a change. */
export function OperateMenu({
  canStop,
  onStop,
  onTimeTravel,
}: {
  /** False once the run has ended. */
  canStop: boolean;
  onStop: () => void;
  onTimeTravel: () => void;
}) {
  const { isOpen, setIsOpen, rootRef } = usePopover<HTMLDivElement>();
  const menuId = useId();
  const choose = (action: () => void) => {
    setIsOpen(false);
    action();
  };
  return (
    <div className="operate-menu" ref={rootRef}>
      <button
        aria-controls={menuId}
        aria-expanded={isOpen}
        aria-haspopup="menu"
        className="operate-menu-button"
        onClick={() => setIsOpen((open) => !open)}
        type="button"
      >
        Operate
        <ChevronDown aria-hidden="true" size={14} strokeWidth={2.25} />
      </button>
      {isOpen && (
        <div aria-label="Operate" className="app-bar-popover operate-menu-popover" id={menuId} role="menu">
          <button
            className="app-bar-menu-item is-danger"
            disabled={!canStop}
            onClick={() => choose(onStop)}
            role="menuitem"
            type="button"
          >
            <span className="app-bar-menu-icon"><Square aria-hidden="true" size={15} strokeWidth={2.25} /></span>
            <span className="app-bar-menu-words">
              <b>Stop…</b>
              <small>{canStop ? 'Cancel, terminate or fail the run' : 'The run has ended'}</small>
            </span>
          </button>
          <button
            className="app-bar-menu-item is-danger"
            onClick={() => choose(onTimeTravel)}
            role="menuitem"
            type="button"
          >
            <span className="app-bar-menu-icon"><History aria-hidden="true" size={15} strokeWidth={2} /></span>
            <span className="app-bar-menu-words">
              <b>Time travel…</b>
              <small>Restart the run from an earlier point</small>
            </span>
          </button>
        </div>
      )}
    </div>
  );
}
