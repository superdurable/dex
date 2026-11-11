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
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { DexAPIError, readResponseJSON } from '@/lib/http';
import type { FlowDefinitionTypeList, FlowTypeDefinitions, V2Catalog } from '@/lib/types';
import { webURL } from '@/lib/basePath';
import { fetchFlowTypeDefinitions } from './flowTypeDefinitionCache';

/** One Flow type's definitions as a page reads them. */
export interface FlowTypeDefinitionsState {
  status: 'loading' | 'ready' | 'missing' | 'failed';
  value: FlowTypeDefinitions | null;
  error: string;
}

interface WebCatalogValue {
  /** True once the Flow type list answered or failed. */
  ready: boolean;
  /** True when a Dex server answers, whether or not Flow definitions are loaded. */
  canUseOperatorAPI: boolean;
  canUseV2: boolean;
  /** Every Flow type with a definition; reading it reads no definition. */
  flowTypes: FlowDefinitionTypeList | null;
  /** Every v2 contract, loaded only after a page calls useV2Catalog. */
  catalog: V2Catalog | null;
  catalogError: string;
  requestCatalog: () => void;
  /** The Flow types a page asked for with useFlowTypeDefinitions. */
  flowTypeDefinitions: Readonly<Record<string, FlowTypeDefinitionsState>>;
  requestFlowTypeDefinitions: (flowType: string) => void;
  /** The X-Dex-Flow-Definition-Revision value for a request about these Flow types. */
  revisionFor: (flowTypes: readonly string[]) => string;
  error: string;
  definitionUpdateKey: number;
  handleDefinitionError: (error: unknown) => boolean;
}

const WebCatalogContext = createContext<WebCatalogValue | null>(null);

/**
 * Loads Flow definitions only when a page needs them.
 *
 * The app reads the Flow type list once. A run page reads its own Flow type, and only pages that list
 * every v2 contract, such as Work Queue, read the whole catalog.
 */
export function WebCatalogProvider({ children }: { children: ReactNode }) {
  const [flowTypes, setFlowTypes] = useState<FlowDefinitionTypeList | null>(null);
  const [error, setError] = useState('');
  const [ready, setReady] = useState(false);
  const [catalog, setCatalog] = useState<V2Catalog | null>(null);
  const [catalogError, setCatalogError] = useState('');
  const [flowTypeDefinitions, setFlowTypeDefinitions] = useState<Record<string, FlowTypeDefinitionsState>>({});
  const [definitionUpdateKey, setDefinitionUpdateKey] = useState(0);
  const [notice, setNotice] = useState('');
  const isCatalogRequested = useRef(false);
  const requestedFlowTypes = useRef(new Set<string>());

  const loadFlowTypes = useCallback(async (signal?: AbortSignal) => {
    const response = await fetch(webURL('/api/flow-definition-types'), { cache: 'no-store', signal });
    setFlowTypes(await readResponseJSON<FlowDefinitionTypeList>(response));
    setError('');
  }, []);

  const loadCatalog = useCallback(async () => {
    try {
      const response = await fetch(webURL('/api/v2/catalog'), { cache: 'no-store' });
      setCatalog(await readResponseJSON<V2Catalog>(response));
      setCatalogError('');
    } catch (loadError) {
      setCatalogError(loadError instanceof Error ? loadError.message : 'Dex Web catalog failed to load');
    }
  }, []);

  const loadFlowTypeDefinitions = useCallback(async (flowType: string) => {
    try {
      const value = await fetchFlowTypeDefinitions(flowType);
      setFlowTypeDefinitions((current) => ({
        ...current,
        [flowType]: { status: value ? 'ready' : 'missing', value, error: '' },
      }));
    } catch (loadError) {
      const message = loadError instanceof Error ? loadError.message : 'Flow definition failed to load';
      setFlowTypeDefinitions((current) => ({
        ...current,
        [flowType]: { status: 'failed', value: current[flowType]?.value ?? null, error: message },
      }));
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void loadFlowTypes(controller.signal).catch((loadError: unknown) => {
      if (!controller.signal.aborted) {
        setError(loadError instanceof Error ? loadError.message : 'Dex Web catalog failed to load');
      }
    }).finally(() => {
      if (!controller.signal.aborted) setReady(true);
    });
    return () => controller.abort();
  }, [loadFlowTypes]);

  const requestCatalog = useCallback(() => {
    if (isCatalogRequested.current) return;
    isCatalogRequested.current = true;
    void loadCatalog();
  }, [loadCatalog]);

  const requestFlowTypeDefinitions = useCallback((flowType: string) => {
    if (flowType === '' || requestedFlowTypes.current.has(flowType)) return;
    requestedFlowTypes.current.add(flowType);
    setFlowTypeDefinitions((current) => ({ ...current, [flowType]: { status: 'loading', value: null, error: '' } }));
    void loadFlowTypeDefinitions(flowType);
  }, [loadFlowTypeDefinitions]);

  const revisionFor = useCallback((requested: readonly string[]) => {
    const revisions = new Set<string>();
    for (const flowType of requested) {
      const revision = flowTypeDefinitions[flowType]?.value?.v2?.definitionRevision
        ?? catalog?.flows.find((entry) => entry.flowType === flowType)?.definitionRevision
        ?? '';
      if (revision !== '') revisions.add(revision);
    }
    return [...revisions].join(',');
  }, [catalog, flowTypeDefinitions]);

  const handleDefinitionError = useCallback((failedRequest: unknown) => {
    if (!(failedRequest instanceof DexAPIError) || failedRequest.code !== 'FLOW_DEFINITION_CHANGED') {
      return false;
    }
    setNotice('Flow Definition updated. Review the refreshed definition and confirm the operation again.');
    setDefinitionUpdateKey((current) => current + 1);
    void loadFlowTypes().catch((loadError: unknown) => {
      setError(loadError instanceof Error ? loadError.message : 'Dex Web catalog failed to reload');
    });
    if (isCatalogRequested.current) void loadCatalog();
    for (const flowType of requestedFlowTypes.current) void loadFlowTypeDefinitions(flowType);
    return true;
  }, [loadCatalog, loadFlowTypeDefinitions, loadFlowTypes]);

  const value = useMemo<WebCatalogValue>(() => ({
    ready,
    canUseOperatorAPI: flowTypes?.operatorAPI === true,
    canUseV2: flowTypes?.operatorAPI === true && flowTypes.configured,
    flowTypes,
    catalog,
    catalogError,
    requestCatalog,
    flowTypeDefinitions,
    requestFlowTypeDefinitions,
    revisionFor,
    error,
    definitionUpdateKey,
    handleDefinitionError,
  }), [
    catalog,
    catalogError,
    definitionUpdateKey,
    error,
    flowTypeDefinitions,
    flowTypes,
    handleDefinitionError,
    ready,
    requestCatalog,
    requestFlowTypeDefinitions,
    revisionFor,
  ]);

  return (
    <WebCatalogContext.Provider value={value}>
      {notice && <div className="error-banner" role="status">{notice}</div>}
      {children}
    </WebCatalogContext.Provider>
  );
}

/** The catalog, or null outside a WebCatalogProvider, such as a page rendered on its own in a test. */
export function useOptionalWebCatalog() {
  return useContext(WebCatalogContext);
}

export function useWebCatalog() {
  const value = useContext(WebCatalogContext);
  if (!value) throw new Error('useWebCatalog must be used inside WebCatalogProvider');
  return value;
}

/** The catalog with every v2 contract loaded; for pages that list every Flow type. */
export function useV2Catalog() {
  const value = useWebCatalog();
  const { requestCatalog } = value;
  useEffect(() => { requestCatalog(); }, [requestCatalog]);
  return value;
}

/** One Flow type's definitions, loaded on first use; null without a Flow type or a provider. */
export function useFlowTypeDefinitions(flowType: string | undefined): FlowTypeDefinitionsState | null {
  const value = useOptionalWebCatalog();
  const requestFlowTypeDefinitions = value?.requestFlowTypeDefinitions;
  useEffect(() => {
    if (flowType) requestFlowTypeDefinitions?.(flowType);
  }, [flowType, requestFlowTypeDefinitions]);
  if (!flowType || !value) return null;
  return value.flowTypeDefinitions[flowType] ?? { status: 'loading', value: null, error: '' };
}

/** The definitions of several Flow types, loaded on first use, for controls that read their graphs. */
export function useFlowTypesDefinitions(flowTypes: readonly string[]): FlowTypeDefinitions[] {
  const { flowTypeDefinitions, requestFlowTypeDefinitions } = useWebCatalog();
  const flowTypesKey = flowTypes.join('\n');
  useEffect(() => {
    for (const flowType of flowTypesKey.split('\n')) requestFlowTypeDefinitions(flowType);
  }, [flowTypesKey, requestFlowTypeDefinitions]);
  return flowTypes
    .map((flowType) => flowTypeDefinitions[flowType]?.value)
    .filter((value): value is FlowTypeDefinitions => value !== null && value !== undefined);
}
