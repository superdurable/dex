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
  fetchLocalCodecServer,
  PayloadDecodeStore,
  readCodecEndpointPreference,
  resolveCodecEndpoint,
  saveCodecEndpointPreference,
  type CodecEndpoint,
  type CodecEndpointPreference,
  type LocalCodecServer,
  type PayloadFlowContext,
} from '@/lib/payloadCodec';
import { collectPayloadEnvelopes, containsEncryptedPayload, replaceEncryptedPayloads } from '@/lib/payloadEncryption';

interface PayloadCodecState {
  preference: CodecEndpointPreference;
  setPreference: (next: CodecEndpointPreference) => void;
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
  preference: { mode: 'off', endpoint: '', includeCredentials: false },
  setPreference: () => undefined,
  localServer: null,
  codec: null,
  store: unconfiguredStore,
  retryDecoding: () => undefined,
});

function browserStorage(): Storage | undefined {
  try {
    return window.localStorage;
  } catch {
    return undefined;
  }
}

/** Holds the codec endpoint choice and the in-memory decode results of this page. */
export function PayloadCodecProvider({ children }: { children: React.ReactNode }) {
  const [preference, setPreferenceState] = useState(() => readCodecEndpointPreference(browserStorage()));
  const [localServer, setLocalServer] = useState<LocalCodecServer | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    void fetchLocalCodecServer(controller.signal).then((server) => {
      if (!controller.signal.aborted) setLocalServer(server);
    });
    return () => controller.abort();
  }, []);

  const codec = useMemo(() => resolveCodecEndpoint(preference, localServer), [preference, localServer]);
  const [retryCount, setRetryCount] = useState(0);
  // A new endpoint or a retry starts with an empty store, so earlier results and failures are dropped.
  const store = useMemo(() => {
    void retryCount;
    return new PayloadDecodeStore(codec);
  }, [codec, retryCount]);
  const value = useMemo<PayloadCodecState>(() => ({
    retryDecoding: () => setRetryCount((count) => count + 1),
    preference,
    setPreference: (next) => {
      setPreferenceState(next);
      saveCodecEndpointPreference(browserStorage(), next);
    },
    localServer,
    codec,
    store,
  }), [codec, localServer, preference, store]);
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
