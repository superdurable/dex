import React, {type ReactNode} from 'react';
import clsx from 'clsx';
import {useColorMode} from '@docusaurus/theme-common';

const COOKIE_MAX_AGE = 60 * 60 * 24 * 365;

function persistSharedTheme(theme: 'light' | 'dark') {
  const shared =
    window.location.hostname === 'superdurable.io' ||
    window.location.hostname.endsWith('.superdurable.io');
  document.cookie = `superdurable-theme=${theme}; Path=/; Max-Age=${COOKIE_MAX_AGE}; SameSite=Lax${
    shared ? '; Domain=.superdurable.io; Secure' : ''
  }`;
}

export default function BrandThemeToggle({
  className,
}: {
  className?: string;
}): ReactNode {
  const {colorMode, setColorMode} = useColorMode();

  function toggleTheme() {
    const nextTheme = colorMode === 'dark' ? 'light' : 'dark';
    setColorMode(nextTheme);
    persistSharedTheme(nextTheme);
  }

  return (
    <button
      className={clsx('theme-toggle', className)}
      type="button"
      onClick={toggleTheme}
      aria-label="Toggle color theme"
      title="Toggle color theme">
      <span className="theme-toggle-icon" aria-hidden="true">
        <span className="theme-toggle-moon">◐</span>
        <span className="theme-toggle-sun">☀</span>
      </span>
      <span className="theme-toggle-night">Night</span>
      <span className="theme-toggle-light">Light</span>
    </button>
  );
}
