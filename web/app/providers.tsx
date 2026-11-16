// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

'use client';

import { createContext, useContext, useEffect, useMemo, useState } from 'react';
import type { TimezonePreference } from '@/lib/format';

interface Preferences {
  timezone: TimezonePreference;
  setTimezone: (value: TimezonePreference) => void;
}

const PreferencesContext = createContext<Preferences | null>(null);

export function PreferencesProvider({ children }: { children: React.ReactNode }) {
  const [timezone, setTimezone] = useState<TimezonePreference>('local');

  useEffect(() => {
    try {
      const saved = window.localStorage.getItem('dex-web-timezone');
      if (saved === 'UTC' || saved === 'local') setTimezone(saved);
    } catch (storageError) {
      console.warn('Unable to read the timezone preference.', storageError);
    }
  }, []);

  const value = useMemo(() => ({
    timezone,
    setTimezone: (next: TimezonePreference) => {
      setTimezone(next);
      try {
        window.localStorage.setItem('dex-web-timezone', next);
      } catch (storageError) {
        console.warn('Unable to save the timezone preference.', storageError);
      }
    },
  }), [timezone]);

  return <PreferencesContext.Provider value={value}>{children}</PreferencesContext.Provider>;
}

export function usePreferences() {
  const value = useContext(PreferencesContext);
  if (!value) throw new Error('usePreferences must be used inside PreferencesProvider');
  return value;
}

/** The viewer's timezone, or local time outside PreferencesProvider, such as in component tests. */
export function useTimezonePreference(): TimezonePreference {
  return useContext(PreferencesContext)?.timezone ?? 'local';
}
