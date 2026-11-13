// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useMemo, useState, type ReactNode } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { Menu } from 'lucide-react';
import { readHasHostNavigation } from '@/lib/hostNavigation';
import { AppBarSlotTarget } from './AppBarSlots';
import { AppRail, useRailExpanded } from './AppRail';
import { FLOWS_SECTION, sectionOf, useAppSections, type AppSection } from './appSections';
import { SettingsMenu } from './SettingsMenu';
import { JumpTo } from './JumpTo';
import { useServerReachability } from './useServerReachability';
import './frame.css';

export { hasLoadedFlowDefinitions } from './appSections';

// Later tabs lose their names first; the active tab keeps its name.
const COMPACT_VIEW_LEVELS = ['tail', 'all'] as const;

/** Picks the least compact views level whose tabs fit; measured, since names vary per page. */
function useCompactViews(bar: HTMLElement | null) {
  useEffect(() => {
    const views = bar?.querySelector<HTMLElement>('.app-bar-views');
    // Without ResizeObserver, as in some test environments, the views keep their names.
    if (!bar || !views || typeof ResizeObserver === 'undefined') return undefined;
    const update = () => {
      const tabs = views.querySelector<HTMLElement>('.run-tabs') ?? views.firstElementChild as HTMLElement | null;
      if (!tabs) return;
      delete bar.dataset.compactViews;
      for (const level of COMPACT_VIEW_LEVELS) {
        if (tabs.scrollWidth <= views.clientWidth + 1) break;
        bar.dataset.compactViews = level;
      }
      // A tab that shows only its icon names itself in a tooltip.
      for (const tab of tabs.querySelectorAll<HTMLElement>('[role="tab"]')) {
        const label = tab.querySelector<HTMLElement>('.run-tab-label');
        const isIconOnly = label !== null && label.getBoundingClientRect().width <= 1;
        if (isIconOnly) tab.title = tab.dataset.tooltip ?? label.textContent ?? '';
        else tab.removeAttribute('title');
      }
    };
    const resize = new ResizeObserver(update);
    resize.observe(bar);
    // Actions change width with the run, such as a longer Operate label, without resizing the bar.
    const actions = bar.querySelector<HTMLElement>('.app-bar-actions');
    if (actions) resize.observe(actions);
    const mutation = new MutationObserver(update);
    mutation.observe(views, { childList: true, subtree: true });
    update();
    return () => {
      resize.disconnect();
      mutation.disconnect();
    };
  }, [bar]);
}

/** The rail and the bar around a page; both share one reading of the sections. */
export function AppFrame({ children }: { children: ReactNode }) {
  const { pathname, search } = useLocation();
  const { sections, flowsHref } = useAppSections(pathname, search);
  const hasHostNavigation = useMemo(readHasHostNavigation, []);
  const reachability = useServerReachability(pathname);
  const [isRailExpanded, setIsRailExpanded] = useRailExpanded();
  const [isPhoneRailOpen, setIsPhoneRailOpen] = useState(false);
  // Following a link closes the phone menu.
  useEffect(() => setIsPhoneRailOpen(false), [pathname]);
  return (
    // A host that draws the navigation around Dex Web is the one rail; Dex Web then draws none.
    <div className={`app-shell${hasHostNavigation ? ' has-host-navigation' : isRailExpanded ? ' has-expanded-rail' : ''}`}>
      {!hasHostNavigation && (
        <AppRail
          flowsHref={flowsHref}
          isExpanded={isRailExpanded}
          isPhoneOpen={isPhoneRailOpen}
          onExpandedChange={setIsRailExpanded}
          onPhoneClose={() => setIsPhoneRailOpen(false)}
          reachability={reachability}
          sections={sections}
        />
      )}
      <div className="app-column">
        <AppBar
          flowsHref={flowsHref}
          onOpenPhoneRail={hasHostNavigation ? undefined : () => setIsPhoneRailOpen(true)}
          sections={sections}
        />
        <main className="app-main">{children}</main>
      </div>
    </div>
  );
}

/** The one bar of every page: the section, the page's slots, then settings. */
export function AppBar({
  sections,
  flowsHref,
  onOpenPhoneRail,
}: {
  sections: readonly AppSection[];
  flowsHref: string;
  /** Opens the rail on a phone; absent when the host draws the navigation. */
  onOpenPhoneRail?: () => void;
}) {
  const { pathname } = useLocation();
  const [bar, setBar] = useState<HTMLElement | null>(null);
  useCompactViews(bar);
  const current = sectionOf(pathname, sections);
  return (
    <header className="app-bar v1-page" ref={setBar}>
      <nav aria-label="Trail" className="app-bar-trail">
        {onOpenPhoneRail && (
          <button aria-label="Open navigation" className="app-bar-icon-button app-bar-rail-button" onClick={onOpenPhoneRail} type="button">
            <Menu aria-hidden="true" size={18} strokeWidth={2} />
          </button>
        )}
        <Link className="app-bar-section-link" to={current === FLOWS_SECTION ? flowsHref : current.path}>
          {current.label}
        </Link>
        <AppBarSlotTarget className="app-bar-page" name="page" />
      </nav>
      <AppBarSlotTarget className="app-bar-views" name="views" />
      <AppBarSlotTarget className="app-bar-actions" name="actions" />
      <JumpTo sections={sections} />
      <SettingsMenu />
    </header>
  );
}
