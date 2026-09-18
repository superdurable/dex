// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link } from 'react-router-dom';
import { usePreferences } from '../providers';
import { useSupervision } from '../supervision/SupervisionProvider';

export function AppHeader() {
  const { timezone, setTimezone } = usePreferences();
  const { catalog, mode, setMode } = useSupervision();
  const hasSupervision = catalog?.enabled === true;
  const home = hasSupervision && mode === 'supervision' ? '/supervision' : '/flows';
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
        {hasSupervision && (
          <div className="workspace-switch" aria-label="Workspace mode">
            <Link className={mode === 'supervision' ? 'active' : ''} onClick={() => setMode('supervision')} to="/supervision">Supervision</Link>
            <Link className={mode === 'operations' ? 'active' : ''} onClick={() => setMode('operations')} to="/flows">Operations</Link>
          </div>
        )}
        {!hasSupervision && <Link to="/flows">Flows</Link>}
        <Link to="/rendering">Flow Rendering</Link>
        <span className="connection-pill">
          <span className="connection-dot" />
          Dex server
        </span>
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
