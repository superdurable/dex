'use client';

import Link from 'next/link';
import { usePreferences } from '../providers';

export function AppHeader() {
  const { timezone, setTimezone } = usePreferences();
  return (
    <header className="app-header">
      <div className="header-brand">
        <Link href="/" className="brand-mark">D</Link>
        <div>
          <Link href="/" className="brand-name">Dex</Link>
          <div className="brand-subtitle">Durable execution console</div>
        </div>
      </div>
      <nav className="header-nav" aria-label="Primary navigation">
        <Link href="/">Flows</Link>
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
