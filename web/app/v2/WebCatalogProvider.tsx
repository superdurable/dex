// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { readResponseJSON } from '@/lib/http';
import type { FlowDefinitionCatalog, SupervisionCatalog } from '@/lib/types';

interface WebCatalogValue {
  ready: boolean;
  canUseV2: boolean;
  catalog: SupervisionCatalog | null;
  definitions: FlowDefinitionCatalog | null;
  error: string;
}

const WebCatalogContext = createContext<WebCatalogValue | null>(null);

export function WebCatalogProvider({ children }: { children: ReactNode }) {
  const [catalog, setCatalog] = useState<SupervisionCatalog | null>(null);
  const [definitions, setDefinitions] = useState<FlowDefinitionCatalog | null>(null);
  const [operatorAPIAvailable, setOperatorAPIAvailable] = useState(false);
  const [error, setError] = useState('');
  const [ready, setReady] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([
      fetch('/api/flow-definitions', { signal: controller.signal })
        .then((response) => readResponseJSON<FlowDefinitionCatalog>(response)),
      fetch('/api/supervision/catalog', { signal: controller.signal })
        .then((response) => readResponseJSON<SupervisionCatalog>(response))
        .then((value) => ({ ok: true as const, value }))
        .catch(() => ({ ok: false as const, value: { enabled: false, flows: [] } })),
    ]).then(([nextDefinitions, nextCatalog]) => {
      setDefinitions(nextDefinitions);
      setCatalog(nextCatalog.value);
      setOperatorAPIAvailable(nextCatalog.ok);
    }).catch((loadError: unknown) => {
      if (!controller.signal.aborted) {
        setError(loadError instanceof Error ? loadError.message : 'Dex Web catalog failed to load');
      }
    }).finally(() => {
      if (!controller.signal.aborted) setReady(true);
    });
    return () => controller.abort();
  }, []);

  const value = useMemo<WebCatalogValue>(() => ({
    ready,
    canUseV2: operatorAPIAvailable && definitions?.configured === true,
    catalog,
    definitions,
    error,
  }), [catalog, definitions, error, operatorAPIAvailable, ready]);

  return <WebCatalogContext.Provider value={value}>{children}</WebCatalogContext.Provider>;
}

export function useWebCatalog() {
  const value = useContext(WebCatalogContext);
  if (!value) throw new Error('useWebCatalog must be used inside WebCatalogProvider');
  return value;
}
