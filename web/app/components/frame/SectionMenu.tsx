// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useId } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { ChevronDown, Inbox, ListTree, PencilRuler, Plug, type LucideIcon } from 'lucide-react';
import { CONNECTORS_PATH, DESIGNS_PATH, FLOWS_PATH, isPathOrChild, WORK_QUEUE_PATH } from '../../routes';
import { usePopover } from './usePopover';

/** One top-level page of Dex Web. */
export interface AppSection {
  path: string;
  label: string;
  description: string;
  Icon: LucideIcon;
  isExperimental?: boolean;
}

export const FLOWS_SECTION: AppSection = {
  path: FLOWS_PATH, label: 'Flows', Icon: ListTree, description: 'Search runs and open one',
};
export const DESIGNS_SECTION: AppSection = {
  path: DESIGNS_PATH, label: 'Designs', Icon: PencilRuler, isExperimental: true,
  description: 'Every loaded Flow design, drawn as a graph',
};
export const WORK_QUEUE_SECTION: AppSection = {
  path: WORK_QUEUE_PATH, label: 'Work Queue', Icon: Inbox, isExperimental: true,
  description: 'Runs and agent sessions that wait for a person',
};
export const CONNECTORS_SECTION: AppSection = {
  path: CONNECTORS_PATH, label: 'Connectors', Icon: Plug, isExperimental: true,
  description: 'Connections for Flows and agent tools',
};

/** The section a path belongs to; every page that is not another section belongs to Flows. */
export function sectionOf(pathname: string, sections: readonly AppSection[]): AppSection {
  return sections.find((section) => section !== FLOWS_SECTION && isPathOrChild(pathname, section.path))
    ?? FLOWS_SECTION;
}

/**
 * The trail's section: a link to the section's home and, when other sections
 * apply, a menu to switch to them.
 */
export function SectionMenu({ sections, flowsHref }: { sections: readonly AppSection[]; flowsHref: string }) {
  const { pathname } = useLocation();
  const { isOpen, setIsOpen, rootRef } = usePopover<HTMLDivElement>();
  const menuId = useId();
  const current = sectionOf(pathname, sections);
  const hrefOf = (section: AppSection) => (section === FLOWS_SECTION ? flowsHref : section.path);
  return (
    <div className="app-bar-section" ref={rootRef}>
      <Link className="app-bar-section-link" to={hrefOf(current)}>
        {current.label}
      </Link>
      {sections.length > 1 && (
        <button
          aria-controls={menuId}
          aria-expanded={isOpen}
          aria-haspopup="menu"
          aria-label="Switch page"
          className="app-bar-icon-button app-bar-section-toggle"
          onClick={() => setIsOpen((open) => !open)}
          type="button"
        >
          <ChevronDown aria-hidden="true" size={14} strokeWidth={2.25} />
        </button>
      )}
      {isOpen && (
        <div aria-label="Pages" className="app-bar-popover app-bar-section-popover" id={menuId} role="menu">
          {sections.map((section) => (
            <Link
              aria-current={section === current ? 'page' : undefined}
              className="app-bar-menu-item"
              key={section.path}
              role="menuitem"
              to={hrefOf(section)}
            >
              <span className="app-bar-menu-icon"><section.Icon aria-hidden="true" size={16} strokeWidth={2} /></span>
              <span className="app-bar-menu-words">
                <b>
                  {section.label}
                  {section.isExperimental && <span className="experimental-dot" title="Early page; may change" />}
                </b>
                <small>{section.description}</small>
              </span>
            </Link>
          ))}
        </div>
      )}
    </div>
  );
}
