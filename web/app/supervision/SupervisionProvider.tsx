// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createContext, useContext, useEffect, useMemo, useState } from 'react';
import { readResponseJSON } from '@/lib/http';
import type { SupervisionCatalog } from '@/lib/types';

interface SupervisionContextValue {
  catalog: SupervisionCatalog | null;
  error: string;
  mode: 'supervision' | 'operations';
  setMode: (mode: 'supervision' | 'operations') => void;
}

const SupervisionContext = createContext<SupervisionContextValue | null>(null);

export function SupervisionProvider({ children }: { children: React.ReactNode }) {
  const [catalog, setCatalog] = useState<SupervisionCatalog | null>(null);
  const [error, setError] = useState('');
  const [mode, setModeState] = useState<'supervision' | 'operations'>(() => (
    window.localStorage.getItem('dex-web-mode') === 'operations' ? 'operations' : 'supervision'
  ));

  useEffect(() => {
    const controller = new AbortController();
    void fetch('/api/supervision/catalog', { signal: controller.signal })
      .then((response) => readResponseJSON<SupervisionCatalog>(response))
      .then(setCatalog)
      .catch((loadError: unknown) => {
        if (!controller.signal.aborted) {
          setError(loadError instanceof Error ? loadError.message : 'Supervision catalog failed to load');
        }
      });
    return () => controller.abort();
  }, []);

  const value = useMemo<SupervisionContextValue>(() => ({
    catalog,
    error,
    mode,
    setMode: (nextMode) => {
      setModeState(nextMode);
      window.localStorage.setItem('dex-web-mode', nextMode);
    },
  }), [catalog, error, mode]);

  return <SupervisionContext.Provider value={value}>{children}</SupervisionContext.Provider>;
}

export function useSupervision() {
  const value = useContext(SupervisionContext);
  if (!value) throw new Error('useSupervision must be used inside SupervisionProvider');
  return value;
}
