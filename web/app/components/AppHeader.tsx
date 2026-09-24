// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link, useLocation, useNavigate } from 'react-router-dom';
import { isEmbedded } from '@/lib/webConfig';
import { usePreferences } from '../providers';
import { v2HomePath, v2ModePath, type V2Mode } from '../v2/contract';
import { useWebCatalog } from '../v2/WebCatalogProvider';
import { DexMark } from './DexMark';
import { ThemeToggle } from './ThemeToggle';

const V2_MODES: { mode: V2Mode; label: string }[] = [
  { mode: 'run', label: 'Run' },
  { mode: 'work-queue', label: 'Work Queue' },
  { mode: 'connections', label: 'Connections' },
];

export function AppHeader() {
  const { timezone, setTimezone } = usePreferences();
  const { canUseV2 } = useWebCatalog();
  const embedded = isEmbedded();
  const location = useLocation();
  const navigate = useNavigate();
  const isV2 = location.pathname === '/v2' || location.pathname.startsWith('/v2/');
  const activeMode: V2Mode = location.pathname.startsWith('/v2/work-queue')
    ? 'work-queue'
    : location.pathname.startsWith('/v2/connections') ? 'connections' : 'run';
  // Absolute timestamps are a Deep Dive concern; the other views show relative or local time.
  const isDebug = location.pathname.includes('/debug');
  const home = canUseV2 && isV2 ? v2HomePath(canUseV2) : '/v1/flows';
  return (
    <header className={`app-header${embedded ? ' embedded' : ''}`}>
      {!embedded && (
        <div className="header-brand">
          <Link to={home} className="brand-mark" aria-label="Super Durable home">
            <DexMark size={28} />
          </Link>
          {/* Dex leads and Super Durable is the maker credit. The product being
              branded is Dex; the old single line put the company first and the
              product last, behind an interpunct. */}
          <Link to={home} className="brand-name">
            <b>Dex</b>
            <span>Super Durable</span>
          </Link>
        </div>
      )}
      <nav className="header-nav" aria-label="Primary navigation">
        {!embedded && !isV2 && (
          <>
            <Link to="/v1/flows">Flows</Link>
            <Link to="/v1/rendering">Flow Rendering</Link>
          </>
        )}
        {isV2 && canUseV2 && (
          <div className="v2-modes" role="group" aria-label="Dex Web v2 mode">
            {V2_MODES.map(({ mode, label }) => (
              <button
                aria-pressed={mode === activeMode}
                className="v2-mode"
                key={mode}
                onClick={() => navigate(v2ModePath(mode))}
                type="button"
              >
                {label}
              </button>
            ))}
          </div>
        )}
        {!embedded && (
          <span className="connection-pill">
            <span className="connection-dot" />
            Dex server
          </span>
        )}
        {!embedded && canUseV2 && (
          <label className="timezone-control">
            <span>Version</span>
            <select
              aria-label="Dex Web version"
              value={isV2 ? 'v2' : 'v1'}
              onChange={(event) => navigate(
                event.target.value === 'v2' ? v2HomePath(canUseV2) : '/v1/flows',
              )}
            >
              <option value="v2">v2</option>
              <option value="v1">v1</option>
            </select>
          </label>
        )}
        {(!isV2 || isDebug) && (
        <label className="timezone-control">
          <span>Timezone</span>
          <select
            value={timezone}
            onChange={(event) => setTimezone(event.target.value as 'local' | 'UTC')}
          >
            <option value="local">Local</option>
            <option value="UTC">UTC</option>
          </select>
        </label>
        )}
        {/* v1 has no finished dark theme; only v2 exposes the control. */}
        {isV2 && <ThemeToggle />}
      </nav>
    </header>
  );
}
