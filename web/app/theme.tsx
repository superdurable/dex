// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react';
import { useLocation } from 'react-router-dom';

export type ThemeChoice = 'light' | 'dark' | 'system';
export type Theme = 'light' | 'dark';

export const THEME_CHOICES: ThemeChoice[] = ['light', 'dark', 'system'];

/** Shared with the inline script in index.html. */
export const THEME_KEY = 'dex-web.theme';

const MEDIA = '(prefers-color-scheme: light)';

export interface ThemeState {
  choice: ThemeChoice;
  theme: Theme;
  set: (choice: ThemeChoice) => void;
}

const ThemeContext = createContext<ThemeState | null>(null);

function isV2Path(pathname: string): boolean {
  return pathname === '/v2' || pathname.startsWith('/v2/');
}

export function isThemeChoice(value: unknown): value is ThemeChoice {
  return value === 'light' || value === 'dark' || value === 'system';
}

export function parseThemeChoice(raw: string | null): ThemeChoice {
  return isThemeChoice(raw) ? raw : 'light';
}

export function resolveTheme(choice: ThemeChoice, systemIsLight: boolean): Theme {
  if (choice === 'system') return systemIsLight ? 'light' : 'dark';
  return choice;
}

function readOsIsLight(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return true;
  return window.matchMedia(MEDIA).matches;
}

function readThemeChoice(): ThemeChoice {
  try {
    return parseThemeChoice(window.localStorage.getItem(THEME_KEY));
  } catch {
    return 'light';
  }
}

function writeDocumentTheme(theme: Theme): void {
  document.documentElement.dataset.theme = theme;
}

export function ThemeProvider({ children }: { children: ReactNode }) {
  const location = useLocation();
  const [choice, setChoice] = useState<ThemeChoice>(readThemeChoice);
  const [osIsLight, setOsIsLight] = useState(readOsIsLight);
  const theme = resolveTheme(choice, osIsLight);
  // v1 never finished dark mode; keep its chrome on the light paper regardless of the stored choice.
  const applied = isV2Path(location.pathname) ? theme : 'light';

  useEffect(() => {
    writeDocumentTheme(applied);
  }, [applied]);

  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return;
    const mq = window.matchMedia(MEDIA);
    const onChange = () => setOsIsLight(mq.matches);
    mq.addEventListener('change', onChange);
    return () => mq.removeEventListener('change', onChange);
  }, []);

  const set = useCallback((next: ThemeChoice) => {
    setChoice(next);
    try {
      window.localStorage.setItem(THEME_KEY, next);
    } catch {
      // Private mode can throw; a theme is not worth taking the app down.
    }
  }, []);

  return (
    <ThemeContext.Provider value={{ choice, theme, set }}>
      {children}
    </ThemeContext.Provider>
  );
}

export function useTheme(): ThemeState {
  const value = useContext(ThemeContext);
  if (!value) throw new Error('useTheme must be used inside ThemeProvider');
  return value;
}
