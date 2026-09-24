// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react';
import { DexAPIError, readResponseJSON } from '@/lib/http';
import type { FlowDefinitionCatalog, V2Catalog } from '@/lib/types';
import { dexFetch, workQueuePermissionMode, type WorkQueuePermissionMode } from '@/lib/webConfig';

interface WebCatalogValue {
  ready: boolean;
  canUseV2: boolean;
  catalog: V2Catalog | null;
  definitions: FlowDefinitionCatalog | null;
  error: string;
  definitionUpdateKey: number;
  permissionMode: WorkQueuePermissionMode;
  handleDefinitionError: (error: unknown) => boolean;
}

const WebCatalogContext = createContext<WebCatalogValue | null>(null);

export function WebCatalogProvider({ children }: { children: ReactNode }) {
  const [catalog, setCatalog] = useState<V2Catalog | null>(null);
  const [definitions, setDefinitions] = useState<FlowDefinitionCatalog | null>(null);
  const [operatorAPIAvailable, setOperatorAPIAvailable] = useState(false);
  const [error, setError] = useState('');
  const [ready, setReady] = useState(false);
  const [definitionUpdateKey, setDefinitionUpdateKey] = useState(0);
  const [notice, setNotice] = useState('');
  const permissionMode = workQueuePermissionMode();

  const loadCatalog = useCallback(async (signal?: AbortSignal) => {
    for (let attempt = 0; attempt < 2; attempt += 1) {
      const [nextDefinitions, nextCatalog] = await Promise.all([
        dexFetch('/api/flow-definitions', { signal })
          .then((response) => readResponseJSON<FlowDefinitionCatalog>(response)),
        dexFetch('/api/v2/catalog', { signal })
          .then((response) => readResponseJSON<V2Catalog>(response))
          .then((value) => ({ ok: true as const, value }))
          .catch(() => ({
            ok: false as const,
            value: { enabled: false, flows: [], definitionRevision: '' },
          })),
      ]);
      const revisionsMatch = !nextCatalog.ok ||
        nextDefinitions.definitionRevision === nextCatalog.value.definitionRevision;
      if (revisionsMatch) {
        setDefinitions(nextDefinitions);
        setCatalog(nextCatalog.value);
        setOperatorAPIAvailable(nextCatalog.ok);
        setError('');
        return;
      }
    }
    throw new Error('Flow Definition changed repeatedly while loading the catalog');
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void loadCatalog(controller.signal).catch((loadError: unknown) => {
      if (!controller.signal.aborted) {
        setError(loadError instanceof Error ? loadError.message : 'Dex Web catalog failed to load');
      }
    }).finally(() => {
      if (!controller.signal.aborted) setReady(true);
    });
    return () => controller.abort();
  }, [loadCatalog]);

  const handleDefinitionError = useCallback((failedRequest: unknown) => {
    if (!(failedRequest instanceof DexAPIError) || failedRequest.code !== 'FLOW_DEFINITION_CHANGED') {
      return false;
    }
    setNotice('Flow Definition updated. Review the refreshed definition and confirm the operation again.');
    setDefinitionUpdateKey((current) => current + 1);
    void loadCatalog().catch((loadError: unknown) => {
      setError(loadError instanceof Error ? loadError.message : 'Dex Web catalog failed to reload');
    });
    return true;
  }, [loadCatalog]);

  const value = useMemo<WebCatalogValue>(() => ({
    ready,
    canUseV2: operatorAPIAvailable && definitions?.configured === true,
    catalog,
    definitions,
    error,
    definitionUpdateKey,
    permissionMode,
    handleDefinitionError,
  }), [
    catalog,
    definitionUpdateKey,
    definitions,
    error,
    handleDefinitionError,
    operatorAPIAvailable,
    permissionMode,
    ready,
  ]);

  return (
    <WebCatalogContext.Provider value={value}>
      {notice && <div className="error-banner" role="status">{notice}</div>}
      {children}
    </WebCatalogContext.Provider>
  );
}

export function useWebCatalog() {
  const value = useContext(WebCatalogContext);
  if (!value) throw new Error('useWebCatalog must be used inside WebCatalogProvider');
  return value;
}
