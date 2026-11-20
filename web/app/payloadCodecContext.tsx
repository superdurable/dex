// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

'use client';

import { createContext, useContext, useEffect, useMemo, useState, useSyncExternalStore } from 'react';
import {
  CODEC_BEARER_TOKEN_STORAGE_KEY,
  fetchLocalCodecServer,
  PayloadDecodeStore,
  readCodecBearerTokenRecord,
  readCodecEndpointPreference,
  removeSessionCodecBearerTokenRecord,
  resolveCodecEndpoint,
  saveCodecBearerTokenRecord,
  saveCodecEndpointPreference,
  type CodecBearerTokenRecord,
  type CodecBearerTokenStorages,
  type CodecEndpoint,
  type CodecEndpointPreference,
  type LocalCodecServer,
  type PayloadFlowContext,
} from '@/lib/payloadCodec';
import { collectPayloadEnvelopes, containsEncryptedPayload, replaceEncryptedPayloads } from '@/lib/payloadEncryption';

interface PayloadCodecState {
  preference: CodecEndpointPreference;
  setPreference: (next: CodecEndpointPreference) => void;
  /** The Custom URL the saved bearer token belongs to; null when no token is saved. The token is never exposed. */
  savedBearerTokenEndpoint: string | null;
  /**
   * Saves a Custom configuration with its canonical URL.
   *
   * A typed token replaces the saved one. An empty typedToken keeps the saved token only for the same URL; a new
   * URL drops it. The token goes to local storage when rememberBearerToken is set, or to session storage otherwise.
   */
  saveCustomCodecEndpoint: (next: CodecEndpointPreference, typedToken: string) => void;
  /** Deletes the saved bearer token from local and session storage; this page stops using it even if that fails. */
  deleteCustomBearerToken: () => void;
  /** The last bearer token save or delete that browser storage refused; null when the last one succeeded. */
  bearerTokenStorageProblem: 'save' | 'delete' | null;
  /** The local dev codec server Dex Web reports; null until read or when the read fails. */
  localServer: LocalCodecServer | null;
  /** The endpoint in use; null when no codec is configured. */
  codec: CodecEndpoint | null;
  store: PayloadDecodeStore;
  /** Drops every decode result and failure, so values decode again, such as after signing in. */
  retryDecoding: () => void;
}

const unconfiguredStore = new PayloadDecodeStore(null);

const PayloadCodecContext = createContext<PayloadCodecState>({
  preference: { mode: 'off', endpoint: '', includeCredentials: false, rememberBearerToken: true },
  setPreference: () => undefined,
  savedBearerTokenEndpoint: null,
  saveCustomCodecEndpoint: () => undefined,
  deleteCustomBearerToken: () => undefined,
  bearerTokenStorageProblem: null,
  localServer: null,
  codec: null,
  store: unconfiguredStore,
  retryDecoding: () => undefined,
});

/** The browser tab whose storage and storage events the provider uses; tests pass their own. */
export interface PayloadCodecBrowserTab {
  readonly localStorage: Storage;
  readonly sessionStorage: Storage;
  addEventListener(type: 'storage', listener: (event: StorageEvent) => void): void;
  removeEventListener(type: 'storage', listener: (event: StorageEvent) => void): void;
}

function browserStorage(tab: PayloadCodecBrowserTab): Storage | undefined {
  try {
    return tab.localStorage;
  } catch {
    return undefined;
  }
}

function tabSessionStorage(tab: PayloadCodecBrowserTab): Storage | undefined {
  try {
    return tab.sessionStorage;
  } catch {
    return undefined;
  }
}

function bearerTokenStorages(tab: PayloadCodecBrowserTab): CodecBearerTokenStorages {
  return { local: browserStorage(tab), session: tabSessionStorage(tab) };
}

interface PayloadCodecProviderProps {
  children: React.ReactNode;
  /** Defaults to the current window. */
  browserTab?: PayloadCodecBrowserTab;
}

/** Holds the codec endpoint choice and the in-memory decode results of this page. */
export function PayloadCodecProvider({ children, browserTab = window }: PayloadCodecProviderProps) {
  const [preference, setPreferenceState] = useState(() => readCodecEndpointPreference(browserStorage(browserTab)));
  const [tokenRecord, setTokenRecord] = useState(
    () => readCodecBearerTokenRecord(bearerTokenStorages(browserTab), preference.rememberBearerToken),
  );
  const [bearerTokenStorageProblem, setBearerTokenStorageProblem] = useState<'save' | 'delete' | null>(null);
  const [localServer, setLocalServer] = useState<LocalCodecServer | null>(null);

  // Follow other tabs' shared token changes and drop any inherited session copy; a failed delete stays revoked.
  const rememberBearerToken = preference.rememberBearerToken;
  const isDeleteUnfinished = bearerTokenStorageProblem === 'delete';
  useEffect(() => {
    const followOtherTab = (event: StorageEvent) => {
      if (event.key !== null && event.key !== CODEC_BEARER_TOKEN_STORAGE_KEY) return;
      const storages = bearerTokenStorages(browserTab);
      const removedSessionCopy = removeSessionCodecBearerTokenRecord(storages);
      if (!removedSessionCopy || isDeleteUnfinished) {
        setTokenRecord(null);
        return;
      }
      setTokenRecord(readCodecBearerTokenRecord(storages, rememberBearerToken));
    };
    browserTab.addEventListener('storage', followOtherTab);
    return () => browserTab.removeEventListener('storage', followOtherTab);
  }, [browserTab, isDeleteUnfinished, rememberBearerToken]);

  useEffect(() => {
    const controller = new AbortController();
    void fetchLocalCodecServer(controller.signal).then((server) => {
      if (!controller.signal.aborted) setLocalServer(server);
    });
    return () => controller.abort();
  }, []);

  const codec = useMemo(
    () => resolveCodecEndpoint(preference, localServer, tokenRecord),
    [tokenRecord, preference, localServer],
  );
  const [retryCount, setRetryCount] = useState(0);
  // A new endpoint or a retry starts with an empty store, so earlier results and failures are dropped.
  const store = useMemo(() => {
    void retryCount;
    return new PayloadDecodeStore(codec);
  }, [codec, retryCount]);
  const value = useMemo<PayloadCodecState>(() => {
    const setPreference = (next: CodecEndpointPreference) => {
      setPreferenceState(next);
      saveCodecEndpointPreference(browserStorage(browserTab), next);
    };
    // This page uses the new record even when storage refuses it; the problem tells the viewer it was not kept.
    const saveTokenRecord = (record: CodecBearerTokenRecord | null, remember: boolean): boolean => {
      setTokenRecord(record);
      const saved = saveCodecBearerTokenRecord(bearerTokenStorages(browserTab), record, remember);
      setBearerTokenStorageProblem(saved ? null : record ? 'save' : 'delete');
      return saved;
    };
    return {
      retryDecoding: () => setRetryCount((count) => count + 1),
      preference,
      setPreference,
      savedBearerTokenEndpoint: tokenRecord?.endpoint ?? null,
      saveCustomCodecEndpoint: (next, typedToken) => {
        let record: CodecBearerTokenRecord | null = null;
        if (typedToken) record = { endpoint: next.endpoint, token: typedToken };
        else if (tokenRecord?.endpoint === next.endpoint) record = tokenRecord;
        const saved = saveTokenRecord(record, next.rememberBearerToken);
        const isFailedMove = !saved && record !== null && record === tokenRecord;
        // A token that could not move stays where it was, so the saved choice stays the old one too.
        setPreference(isFailedMove ? { ...next, rememberBearerToken: preference.rememberBearerToken } : next);
      },
      deleteCustomBearerToken: () => {
        saveTokenRecord(null, preference.rememberBearerToken);
      },
      bearerTokenStorageProblem,
      localServer,
      codec,
      store,
    };
  }, [bearerTokenStorageProblem, browserTab, codec, localServer, preference, store, tokenRecord]);
  return <PayloadCodecContext.Provider value={value}>{children}</PayloadCodecContext.Provider>;
}

export function usePayloadCodec(): PayloadCodecState {
  return useContext(PayloadCodecContext);
}

/**
 * Returns the tree with encrypted Values and texts replaced by their plaintext, or by placeholders.
 *
 * It asks the codec endpoint to decode every envelope in the tree, in one batch per Flow, and re-renders when
 * results arrive. Pass null as the Flow while it is unknown; envelopes then wait as pending placeholders.
 */
export function useDecryptedPayloads<T>(flow: PayloadFlowContext | null, tree: T): T {
  const { store } = usePayloadCodec();
  const envelopes = useMemo(() => collectPayloadEnvelopes(tree), [tree]);
  const hasEncrypted = useMemo(() => envelopes.length > 0 || containsEncryptedPayload(tree), [envelopes, tree]);
  const version = useSyncExternalStore(store.subscribe, store.getVersion, store.getVersion);
  const flowId = flow?.flowId ?? '';
  const flowType = flow?.flowType ?? '';
  useEffect(() => {
    if (flowId) store.request({ flowId, flowType }, envelopes);
  }, [envelopes, flowId, flowType, store]);
  return useMemo(() => {
    void version;
    if (!hasEncrypted) return tree;
    // Without a Flow yet, configured envelopes wait as pending; without a codec they show as not configured.
    const lookup = flowId || !store.codec ? store.lookup({ flowId, flowType }) : () => undefined;
    return replaceEncryptedPayloads(tree, lookup);
  }, [flowId, flowType, hasEncrypted, store, tree, version]);
}
