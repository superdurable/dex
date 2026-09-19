// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link, useLocation, useNavigate } from 'react-router-dom';
import { usePreferences } from '../providers';
import { useWebCatalog } from '../v2/WebCatalogProvider';

export function AppHeader() {
  const { timezone, setTimezone } = usePreferences();
  const { canUseV2 } = useWebCatalog();
  const location = useLocation();
  const navigate = useNavigate();
  const isV2 = location.pathname === '/v2' || location.pathname.startsWith('/v2/');
  const home = canUseV2 && isV2 ? '/v2' : '/v1/flows';
  return (
    <header className="app-header">
      <div className="header-brand">
        <Link to={home} className="brand-mark" aria-label="Super Durable home">
          <img
            className="brand-logo"
            src="/super-durable-logo.png"
            alt=""
            width={72}
            height={72}
          />
        </Link>
        <Link to={home} className="brand-name">
          <span>Super Durable</span>
          <i aria-hidden="true">·</i>
          <b>Dex</b>
        </Link>
      </div>
      <nav className="header-nav" aria-label="Primary navigation">
        {!isV2 && (
          <>
            <Link to="/v1/flows">Flows</Link>
            <Link to="/v1/rendering">Flow Rendering</Link>
          </>
        )}
        <span className="connection-pill">
          <span className="connection-dot" />
          Dex server
        </span>
        {canUseV2 && (
          <label className="timezone-control">
            <span>Version</span>
            <select
              aria-label="Dex Web version"
              value={isV2 ? 'v2' : 'v1'}
              onChange={(event) => navigate(event.target.value === 'v2' ? '/v2' : '/v1/flows')}
            >
              <option value="v2">v2</option>
              <option value="v1">v1</option>
            </select>
          </label>
        )}
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
      </nav>
    </header>
  );
}
