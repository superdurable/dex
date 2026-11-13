// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useState } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { ArrowUpLeft, ChevronLeft, PanelLeftClose, PanelLeftOpen } from 'lucide-react';
import type { HostLink } from '@/lib/hostLink';
import { FLOWS_PATH } from '../../routes';
import { DexMark } from '../DexMark';
import { FLOWS_SECTION, sectionOf, type AppSection } from './appSections';
import type { ServerReachability } from './useServerReachability';

const RAIL_EXPANDED_STORAGE_KEY = 'dex.rail.expanded';

function readIsRailExpanded(): boolean {
  try {
    return window.localStorage.getItem(RAIL_EXPANDED_STORAGE_KEY) === 'true';
  } catch {
    return false;
  }
}

/** Whether the rail shows its labels, remembered in this browser; it starts collapsed. */
export function useRailExpanded(): [boolean, (isExpanded: boolean) => void] {
  const [isExpanded, setIsExpanded] = useState(readIsRailExpanded);
  const update = (next: boolean) => {
    setIsExpanded(next);
    try {
      window.localStorage.setItem(RAIL_EXPANDED_STORAGE_KEY, String(next));
    } catch {
      // A browser that refuses storage keeps the choice for this visit only.
    }
  };
  return [isExpanded, update];
}

const reachabilityLabels: Record<Exclude<ServerReachability, 'checking'>, string> = {
  reachable: 'Dex Server reachable',
  unreachable: 'Dex Web cannot reach the Dex Server',
};

/** The left rail: the server, then every section; experimental sections sit below their own heading. */
export function AppRail({
  sections,
  flowsHref,
  hostLink,
  reachability,
  isExpanded,
  onExpandedChange,
  isPhoneOpen,
  onPhoneClose,
}: {
  sections: readonly AppSection[];
  flowsHref: string;
  hostLink: HostLink | null;
  reachability: ServerReachability;
  isExpanded: boolean;
  onExpandedChange: (isExpanded: boolean) => void;
  isPhoneOpen: boolean;
  onPhoneClose: () => void;
}) {
  const { pathname } = useLocation();
  const current = sectionOf(pathname, sections);
  const hrefOf = (section: AppSection) => (section === FLOWS_SECTION ? flowsHref : section.path);
  const stable = sections.filter((section) => !section.isExperimental);
  const experimental = sections.filter((section) => section.isExperimental);

  const reachDot = reachability === 'checking' ? null : (
    <span
      aria-label={reachabilityLabels[reachability]}
      className={`app-rail-reach is-${reachability}`}
      role="img"
      title={reachabilityLabels[reachability]}
    />
  );
  const sectionLink = (section: AppSection) => (
    <Link
      aria-current={section === current ? 'page' : undefined}
      className="app-rail-item"
      key={section.path}
      title={isExpanded ? undefined : `${section.label}${section.isExperimental ? ' · Experimental' : ''}`}
      to={hrefOf(section)}
    >
      <section.Icon aria-hidden="true" className="app-rail-icon" size={18} strokeWidth={2} />
      <span className="app-rail-label">{section.label}</span>
    </Link>
  );

  return (
    <>
      {isPhoneOpen && <button aria-label="Close navigation" className="app-rail-backdrop" onClick={onPhoneClose} type="button" />}
      <nav
        aria-label="Dex Web"
        className={`app-rail${isExpanded ? ' is-expanded' : ''}${isPhoneOpen ? ' is-phone-open' : ''}`}
      >
        <div className="app-rail-server">
          {hostLink ? (
            <a className="app-rail-item app-rail-host" href={hostLink.url} title={isExpanded ? undefined : hostLink.name}>
              <span className="app-rail-icon app-rail-mark">
                <ChevronLeft aria-hidden="true" size={18} strokeWidth={2.25} />
                {reachDot}
              </span>
              <span className="app-rail-label app-rail-host-name">{hostLink.name}</span>
            </a>
          ) : (
            <Link aria-label="Dex home" className="app-rail-item app-rail-host" title={isExpanded ? undefined : 'Dex home'} to={FLOWS_PATH}>
              <span className="app-rail-icon app-rail-mark">
                <DexMark size={22} />
                {reachDot}
              </span>
              <span className="app-rail-label app-rail-host-name">Dex</span>
            </Link>
          )}
          {hostLink?.pages.map((page) => (
            <a className="app-rail-item app-rail-host-page" href={page.url} key={page.url} title={isExpanded ? undefined : page.name}>
              <ArrowUpLeft aria-hidden="true" className="app-rail-icon" size={16} strokeWidth={2} />
              <span className="app-rail-label">{page.name}</span>
            </a>
          ))}
        </div>
        <div className="app-rail-sections">
          {stable.map(sectionLink)}
          {experimental.length > 0 && (
            <>
              <div className="app-rail-heading" role="presentation">
                <span className="app-rail-label">Experimental</span>
              </div>
              {experimental.map(sectionLink)}
            </>
          )}
        </div>
        <button
          aria-expanded={isExpanded}
          aria-label={isExpanded ? 'Collapse navigation' : 'Expand navigation'}
          className="app-rail-item app-rail-toggle"
          onClick={() => onExpandedChange(!isExpanded)}
          title={isExpanded ? 'Collapse navigation' : 'Expand navigation'}
          type="button"
        >
          {isExpanded
            ? <PanelLeftClose aria-hidden="true" className="app-rail-icon" size={18} strokeWidth={2} />
            : <PanelLeftOpen aria-hidden="true" className="app-rail-icon" size={18} strokeWidth={2} />}
          <span className="app-rail-label">Collapse</span>
        </button>
      </nav>
    </>
  );
}
