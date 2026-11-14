// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useRef, useState } from 'react';
import { Link, useLocation } from 'react-router-dom';
import { PanelLeftClose, PanelLeftOpen } from 'lucide-react';
import { FLOWS_PATH } from '../../routes';
import { DexMark } from '../DexMark';
import { FLOWS_SECTION, sectionOf, type AppSection } from './appSections';
import type { ServerReachability, ServerReachabilityStatus } from './useServerReachability';

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

const UNREACHABLE_TEXT = 'Dex Server unreachable';

/** Names the reachability for the dot's tooltip, with the last error and last success when known. */
export function reachabilityTooltip(status: ServerReachabilityStatus): string {
  if (status.state === 'checking') return 'Checking whether Dex Web reaches the Dex Server';
  if (status.state === 'reachable') return 'Dex Server reachable';
  const lastError = status.lastError ? ` Last error: ${status.lastError}.` : '';
  const lastReached = status.lastReachableAt
    ? ` Last reached at ${new Date(status.lastReachableAt).toLocaleTimeString()}.`
    : '';
  return `Dex Web cannot reach the Dex Server.${lastError}${lastReached}`;
}

/** Announces a change of reachability once, after the first answer; polls that repeat a state say nothing. */
function useReachabilityAnnouncement(state: ServerReachability): string {
  const [announcement, setAnnouncement] = useState('');
  const previous = useRef<ServerReachability>('checking');
  useEffect(() => {
    if (state === previous.current) return;
    const wasKnown = previous.current !== 'checking';
    previous.current = state;
    if (state === 'unreachable') setAnnouncement('Dex Server unreachable.');
    else if (state === 'reachable' && wasKnown) setAnnouncement('Dex Server reachable again.');
  }, [state]);
  return announcement;
}

/** The left rail of a Dex Web on its own: home, then every section; experimental ones sit below their heading. */
export function AppRail({
  sections,
  flowsHref,
  reachability,
  isExpanded,
  onExpandedChange,
  isPhoneOpen,
  onPhoneClose,
}: {
  sections: readonly AppSection[];
  flowsHref: string;
  reachability: ServerReachabilityStatus;
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

  const reachTooltip = reachabilityTooltip(reachability);
  const isUnreachable = reachability.state === 'unreachable';
  const announcement = useReachabilityAnnouncement(reachability.state);
  // Reachable and checking stay a faint dot; only an unreachable server adds red words.
  const reachDot = (
    <span
      aria-label={reachTooltip}
      className={`app-rail-reach is-${reachability.state}`}
      role="img"
      title={reachTooltip}
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
          <Link
            aria-label={isUnreachable ? `Dex home. ${UNREACHABLE_TEXT}` : 'Dex home'}
            className="app-rail-item app-rail-host"
            title={isUnreachable ? reachTooltip : isExpanded ? undefined : 'Dex home'}
            to={FLOWS_PATH}
          >
            <span className="app-rail-icon app-rail-mark">
              <DexMark size={22} />
              {reachDot}
            </span>
            <span className="app-rail-label app-rail-host-name">
              Dex
              {isUnreachable && <span className="app-rail-reach-text">{UNREACHABLE_TEXT}</span>}
            </span>
          </Link>
          <p aria-live="polite" className="sr-only">{announcement}</p>
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
