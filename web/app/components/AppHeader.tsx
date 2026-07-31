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
        <div>
          <Link to="/" className="brand-name">Super Durable</Link>
        </div>
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
