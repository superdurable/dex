// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { Link } from 'react-router-dom';
import { usePreferences } from '../providers';

export function AppHeader() {
  const { timezone, setTimezone } = usePreferences();
  return (
    <header className="app-header">
      <div className="header-brand">
        <Link to="/" className="brand-mark" aria-label="Super Durable home">
          <img
            className="brand-logo"
            src="/super-durable-logo.png"
            alt=""
            width={72}
            height={72}
          />
        </Link>
        <Link to="/" className="brand-name">
          <span>Super Durable</span>
          <i aria-hidden="true">·</i>
          <b>Dex</b>
        </Link>
      </div>
      <nav className="header-nav" aria-label="Primary navigation">
        <Link to="/">Flows</Link>
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
