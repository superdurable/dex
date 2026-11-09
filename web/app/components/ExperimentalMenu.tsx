// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useId, useRef, useState } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { ChevronDown, FlaskConical, type LucideIcon } from 'lucide-react';
import { isPathOrChild } from '../routes';

/** One experimental global page the menu offers. */
export interface ExperimentalPage {
  path: string;
  label: string;
  description: string;
  Icon: LucideIcon;
}

/**
 * One header entry for the experimental global pages, so the main navigation stays on the core pages.
 * It renders nothing when no experimental page applies.
 */
export function ExperimentalMenu({ pages }: { pages: readonly ExperimentalPage[] }) {
  const [isOpen, setIsOpen] = useState(false);
  const { pathname } = useLocation();
  const menuId = useId();
  const rootRef = useRef<HTMLDivElement>(null);
  const isOnExperimentalPage = pages.some((page) => isPathOrChild(pathname, page.path));

  useEffect(() => { setIsOpen(false); }, [pathname]);
  useEffect(() => {
    if (!isOpen) return undefined;
    const closeOnOutsidePointer = (event: PointerEvent) => {
      if (!rootRef.current?.contains(event.target as Node)) setIsOpen(false);
    };
    const closeOnEscape = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setIsOpen(false);
    };
    document.addEventListener('pointerdown', closeOnOutsidePointer);
    document.addEventListener('keydown', closeOnEscape);
    return () => {
      document.removeEventListener('pointerdown', closeOnOutsidePointer);
      document.removeEventListener('keydown', closeOnEscape);
    };
  }, [isOpen]);

  if (pages.length === 0) return null;
  return (
    <div className="experimental-menu" ref={rootRef}>
      <button
        aria-controls={menuId}
        aria-expanded={isOpen}
        aria-haspopup="menu"
        className="experimental-menu-button"
        data-active={isOnExperimentalPage ? 'true' : undefined}
        onClick={() => setIsOpen((open) => !open)}
        type="button"
      >
        <FlaskConical aria-hidden="true" size={15} strokeWidth={2} />
        Experimental
        <ChevronDown aria-hidden="true" className="experimental-menu-chevron" size={14} strokeWidth={2} />
      </button>
      {isOpen && (
        <div className="experimental-menu-popover" id={menuId} role="menu" aria-label="Experimental pages">
          <p className="experimental-menu-note">Early pages that may change in a later release.</p>
          {pages.map(({ path, label, description, Icon }) => (
            <Link
              aria-current={isPathOrChild(pathname, path) ? 'page' : undefined}
              className="experimental-menu-item"
              key={path}
              role="menuitem"
              to={path}
            >
              <span className="experimental-menu-icon"><Icon aria-hidden="true" size={16} strokeWidth={2} /></span>
              <span className="experimental-menu-words">
                <b>{label}</b>
                <small>{description}</small>
              </span>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
