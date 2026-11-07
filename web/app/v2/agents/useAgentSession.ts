// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useRef, useState } from 'react';
import { hydrateBlobs } from '@/lib/blobs';
import { readResponseJSON } from '@/lib/http';
import { positiveRunAttempt } from '@/lib/timeTravel';
import type { ActiveStepExecution, FlowState, FlowSummary, KeyValue } from '@/lib/types';
import {
  loadRecentRunHistory,
  recentRunEvents,
  type RecentRunHistory,
} from '../canvas/overlayFromHistory';
import { absorb, nothingHeld, readFailureReason, type Held } from '../work-queue/liveness';
import { messagesOfChunks, readSession, type SessionView } from './sessionModel';
import { SESSION_ATTRIBUTE, attributeMapInstance, type AgentMessageRecord } from './sessionSchema';

export interface LoadedAgentSession {
  summary: FlowSummary;
  view: SessionView;
  activeSteps: ActiveStepExecution[];
  /** False when Flow history could not be read; past calls then show message text only. */
  hasHistory: boolean;
  unreadableChunks: number;
  readAt: number;
}

/** Reads one session without calling a session RPC, so it works while Workers are down. */
export function useAgentSession(flowId: string, reloadKey: number) {
  const [held, setHeld] = useState<Held<LoadedAgentSession>>(() => nothingHeld());
  const [includeChunks, setIncludeChunks] = useState(false);
  const [manualKey, setManualKey] = useState(0);
  const blobCache = useRef(new Map<string, unknown>());
  const history = useRef<RecentRunHistory | undefined>(undefined);

  useEffect(() => {
    setHeld(nothingHeld());
    setIncludeChunks(false);
    blobCache.current = new Map();
    history.current = undefined;
  }, [flowId]);

  useEffect(() => {
    if (!flowId) return undefined;
    const controller = new AbortController();
    void loadAgentSession(flowId, includeChunks, blobCache.current, history.current, controller.signal)
      .then(({ loaded, recentHistory }) => {
        if (controller.signal.aborted) return;
        history.current = recentHistory;
        setHeld((prior) => absorb(prior, { state: 'ok', value: loaded }));
      })
      .catch((error: unknown) => {
        if (controller.signal.aborted) return;
        setHeld((prior) => absorb(prior, { state: 'unreachable', reason: readFailureReason(error) }));
      });
    return () => controller.abort();
  }, [flowId, includeChunks, reloadKey, manualKey]);

  const reload = useCallback(() => setManualKey((key) => key + 1), []);
  const loadEarlierMessages = useCallback(() => setIncludeChunks(true), []);
  return { held, reload, includeChunks, loadEarlierMessages };
}

async function loadAgentSession(
  flowId: string,
  includeChunks: boolean,
  blobCache: Map<string, unknown>,
  previousHistory: RecentRunHistory | undefined,
  signal: AbortSignal,
): Promise<{ loaded: LoadedAgentSession; recentHistory: RecentRunHistory | undefined }> {
  const summary = await readResponseJSON<FlowSummary>(
    await fetch(`/api/flows/summary?flowId=${encodeURIComponent(flowId)}`, { cache: 'no-store', signal }),
  );
  const state = await readResponseJSON<FlowState>(
    await fetch(
      `/api/flows/state?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(summary.runId)}`,
      { cache: 'no-store', signal },
    ),
  );
  let recentHistory: RecentRunHistory | undefined;
  try {
    recentHistory = await loadRecentRunHistory(
      flowId,
      summary.runId,
      positiveRunAttempt(summary.runAttempt),
      previousHistory,
    );
  } catch (error) {
    if (signal.aborted) throw error;
    recentHistory = undefined;
  }

  const isChunk = (entry: KeyValue) => attributeMapInstance(entry.key, SESSION_ATTRIBUTE.messageChunks) !== null;
  const read = state.attributes.filter((entry) => !isChunk(entry));
  const hydrated = await hydrateBlobs(flowId, read, blobCache, signal);
  let chunkMessages: AgentMessageRecord[] = [];
  let unreadableChunks = 0;
  if (includeChunks) {
    const chunks = state.attributes.filter(isChunk);
    const hydratedChunks = await hydrateBlobs(flowId, chunks.map((entry) => entry.value), blobCache, signal);
    ({ messages: chunkMessages, unreadable: unreadableChunks } = messagesOfChunks(hydratedChunks.value));
  }
  const view = readSession({
    attributes: [...hydrated.value, ...state.attributes.filter(isChunk)],
    activeSteps: state.activeStepExecutions,
    events: recentHistory ? recentRunEvents(recentHistory) : [],
    chunkMessages,
  });
  if (!recentHistory) view.missing.push('Flow history could not be read, so past calls show message text only');
  return {
    loaded: {
      summary,
      view,
      activeSteps: state.activeStepExecutions,
      hasHistory: recentHistory !== undefined,
      unreadableChunks,
      readAt: Date.now(),
    },
    recentHistory,
  };
}
