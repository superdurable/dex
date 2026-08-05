// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { Link } from 'react-router-dom';
import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { formatDate, formatDuration } from '@/lib/format';
import { hydrateBlobs } from '@/lib/blobs';
import type {
  FlowHistoryEvent,
  FlowState,
  FlowSummary,
  HistoryPage,
} from '@/lib/types';
import { StatusBadge } from '../components/StatusBadge';
import { usePreferences } from '../providers';
import { FlowOverview } from './details/FlowOverview';
import { FlowStatePanel } from './details/FlowStatePanel';
import { ResetFlowDialog } from './details/ResetFlowDialog';
import { StepGraph } from './details/StepGraph';
import { Timeline } from './details/Timeline';

type RunTab = 'overview' | 'steps' | 'timeline';

interface StepEventInputsResult {
  inputs: Array<{ eventId: number; request: Record<string, unknown> }>;
  unavailableEventIds?: number[] | null;
}

const terminalStatuses = new Set([2, 3, 4, 5, 6, 7]);

async function responseJSON<T>(response: Response): Promise<T> {
  const data = await response.json() as T & { error?: string };
  if (!response.ok) throw new Error(data.error || `Request failed (${response.status})`);
  return data;
}

function stepMethodType(event: FlowHistoryEvent): 'waitFor' | 'execute' | null {
  if (event.type.startsWith('StepWaitFor')) return 'waitFor';
  if (event.type.startsWith('StepExecute')) return 'execute';
  return null;
}

export function RunDetailsPage({ flowId, runId }: { flowId: string; runId: string }) {
  const { timezone } = usePreferences();
  const [summary, setSummary] = useState<FlowSummary | null>(null);
  const [history, setHistory] = useState<FlowHistoryEvent[]>([]);
  const [state, setState] = useState<FlowState | null>(null);
  const [nextPageToken, setNextPageToken] = useState('');
  const [nextInternalEventId, setNextInternalEventId] = useState(0);
  const [tab, setTab] = useState<RunTab>('timeline');
  const [selectedEvent, setSelectedEvent] = useState<FlowHistoryEvent | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState('');
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [resetOpen, setResetOpen] = useState(false);
  const [waitCycle, setWaitCycle] = useState(0);
  const [hydratedEvents, setHydratedEvents] = useState<Record<number, FlowHistoryEvent>>({});
  const [storedValueWarning, setStoredValueWarning] = useState('');
  const waitGeneration = useRef(0);
  const blobCache = useRef(new Map<string, unknown>());
  const hydratingEventIDs = useRef(new Set<number>());
  const hydratedEventIDs = useRef(new Set<number>());

  const summaryURL = `/api/flows/summary?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(runId)}`;
  const stateURL = `/api/flows/state?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(runId)}`;

  const loadState = useCallback(async (statusCode: number) => {
    if (terminalStatuses.has(statusCode)) {
      setState(null);
      return;
    }
    try {
      const rawState = await responseJSON<FlowState>(await fetch(stateURL, { cache: 'no-store' }));
      setState(rawState);
      const hydrated = await hydrateBlobs(rawState, blobCache.current);
      setState(hydrated.value);
      if (hydrated.error) setStoredValueWarning(hydrated.error);
    } catch (stateError) {
      setError(stateError instanceof Error ? stateError.message : 'State query failed');
    }
  }, [stateURL]);

  const hydrateEvent = useCallback(async (event: FlowHistoryEvent) => {
    if (hydratedEventIDs.current.has(event.eventId)
      || hydratingEventIDs.current.has(event.eventId)) return;
    hydratingEventIDs.current.add(event.eventId);
    try {
      let eventWithInput = event;
      const methodType = stepMethodType(event);
      const request = event.payload.request;
      const execution = event.payload.execution as Record<string, unknown> | undefined;
      if (methodType && (!request || Object.keys(request as Record<string, unknown>).length === 0)) {
        try {
          const result = await responseJSON<StepEventInputsResult>(await fetch('/api/flows/step-event-inputs', {
            method: 'POST',
            headers: { 'content-type': 'application/json' },
            body: JSON.stringify({
              flowId,
              runId,
              keys: [{
                eventId: event.eventId,
                stepExecutionId: String(execution?.stepExecutionId ?? ''),
                methodType,
              }],
            }),
            cache: 'no-store',
          }));
          const loaded = result.inputs.find((input) => input.eventId === event.eventId);
          const unavailableEventIDs = result.unavailableEventIds ?? [];
          eventWithInput = {
            ...event,
            payload: {
              ...event.payload,
              ...(loaded ? { request: loaded.request } : {}),
              ...(!loaded && unavailableEventIDs.includes(event.eventId)
                ? { inputUnavailable: true }
                : {}),
            },
          };
        } catch {
          eventWithInput = {
            ...event,
            payload: { ...event.payload, inputUnavailable: true },
          };
        }
      }
      const hydrated = await hydrateBlobs(eventWithInput, blobCache.current);
      setHydratedEvents((current) => ({ ...current, [event.eventId]: hydrated.value }));
      hydratedEventIDs.current.add(event.eventId);
      if (hydrated.error) setStoredValueWarning(hydrated.error);
    } catch {
      setStoredValueWarning('Stored value unavailable');
    } finally {
      hydratingEventIDs.current.delete(event.eventId);
    }
  }, [flowId, runId]);

  const loadSummary = useCallback(async () => {
    const value = await responseJSON<FlowSummary>(await fetch(summaryURL, { cache: 'no-store' }));
    setSummary(value);
    return value;
  }, [summaryURL]);

  const loadHistoryPage = useCallback(async (
    token: string,
    cursor: number,
    append: boolean,
  ) => {
    const params = new URLSearchParams({
      flowId,
      runId,
      startInternalEventId: String(cursor),
      estimatePageSize: '200',
    });
    if (token) params.set('nextPageToken', token);
    const page = await responseJSON<HistoryPage>(
      await fetch(`/api/flows/history?${params}`, { cache: 'no-store' }),
    );
    setHistory((current) => {
      const combined = append ? [...current, ...page.events] : page.events;
      return [...new Map(combined.map((event) => [event.eventId, event])).values()]
        .sort((left, right) => left.eventId - right.eventId);
    });
    setNextPageToken(page.nextPageToken);
    setNextInternalEventId(page.nextInternalEventId);
    return page;
  }, [flowId, runId]);

  const refresh = useCallback(async () => {
    setError('');
    try {
      const latestSummary = await loadSummary();
      await Promise.all([
        loadHistoryPage('', 0, false),
        loadState(latestSummary.flowStatusCode),
      ]);
    } catch (refreshError) {
      setError(refreshError instanceof Error ? refreshError.message : 'Run refresh failed');
    }
  }, [loadHistoryPage, loadState, loadSummary]);

  useEffect(() => {
    let active = true;
    setSummary(null);
    setHistory([]);
    setState(null);
    setNextPageToken('');
    setNextInternalEventId(0);
    setSelectedEvent(null);
    setHydratedEvents({});
    setStoredValueWarning('');
    blobCache.current.clear();
    hydratingEventIDs.current.clear();
    hydratedEventIDs.current.clear();
    setError('');
    setLoading(true);
    void refresh().finally(() => {
      if (active) setLoading(false);
    });
    return () => {
      active = false;
      waitGeneration.current += 1;
    };
  }, [refresh]);

  useEffect(() => {
    if (
      !autoRefresh
      || !summary
      || terminalStatuses.has(summary.flowStatusCode)
      || nextPageToken
      || nextInternalEventId <= 0
    ) return;

    const generation = ++waitGeneration.current;
    const controller = new AbortController();
    const params = new URLSearchParams({
      flowId,
      runId,
      nextInternalEventId: String(nextInternalEventId),
    });
    void fetch(`/api/flows/wait?${params}`, { cache: 'no-store', signal: controller.signal })
      .then(async (response) => {
        if (generation !== waitGeneration.current) return;
        if (response.status === 408) {
          setWaitCycle((current) => current + 1);
          return;
        }
        await responseJSON(response);
        const latestSummary = await loadSummary();
        await Promise.all([
          loadHistoryPage('', nextInternalEventId, true),
          loadState(latestSummary.flowStatusCode),
        ]);
      })
      .catch((waitError) => {
        if (waitError instanceof DOMException && waitError.name === 'AbortError') return;
        setError(waitError instanceof Error ? waitError.message : 'History wait failed');
        window.setTimeout(() => setWaitCycle((current) => current + 1), 1000);
      });
    return () => controller.abort();
  }, [
    autoRefresh,
    flowId,
    loadHistoryPage,
    loadState,
    loadSummary,
    nextInternalEventId,
    nextPageToken,
    runId,
    summary,
    waitCycle,
  ]);

  const tabs: Array<[RunTab, string]> = [
    ['overview', 'Overview'],
    ['steps', 'Step graph'],
    ['timeline', 'Timeline'],
  ];
  const latestEvent = history.at(-1);
  const selectedRaw = selectedEvent ?? latestEvent ?? null;
  const selected = selectedRaw ? hydratedEvents[selectedRaw.eventId] ?? selectedRaw : null;
  const displayedHistory = useMemo(
    () => history.map((event) => hydratedEvents[event.eventId] ?? event),
    [history, hydratedEvents],
  );

  useEffect(() => {
    if (selectedRaw) void hydrateEvent(selectedRaw);
  }, [hydrateEvent, selectedRaw?.eventId]);

  useEffect(() => {
    if (tab !== 'overview') return;
    const startEvent = history.find((event) => event.type === 'FlowStartedOrContinued');
    if (startEvent) void hydrateEvent(startEvent);
  }, [hydrateEvent, history, tab]);
  const runChain = useMemo(() => {
    if (!summary) return [];
    return [...new Set([summary.firstRunId, summary.runId].filter(Boolean))];
  }, [summary]);

  if (loading && !summary) return <div className="page-loading">Loading flow run…</div>;

  return (
    <div className="run-page">
      <section className="run-header">
        <div className="breadcrumbs">
          <Link to="/">Flows</Link><span>/</span>
          <Link to={`/flows/${encodeURIComponent(flowId)}`}>{flowId}</Link><span>/</span>
          <span className="mono">{runId}</span>
        </div>
        <div className="run-title-row">
          <div>
            <p className="eyebrow">{summary?.flowType || 'Flow execution'}</p>
            <h1>{flowId}</h1>
          </div>
          <div className="run-actions">
            <label className="toggle-label">
              <input
                type="checkbox"
                checked={autoRefresh}
                onChange={(event) => setAutoRefresh(event.target.checked)}
              />
              Live
            </label>
            <button className="button ghost" onClick={() => void navigator.clipboard.writeText(window.location.href)}>
              Copy link
            </button>
            <button className="button ghost" onClick={() => void refresh()}>Refresh</button>
            <button className="button danger" onClick={() => setResetOpen(true)}>Reset</button>
          </div>
        </div>
        {summary && (
          <div className="summary-strip">
            <div><span>Status</span><StatusBadge status={summary.flowStatus} /></div>
            <div><span>Started</span><b>{formatDate(summary.startTime, timezone)}</b></div>
            <div><span>Closed</span><b>{formatDate(summary.closeTime, timezone)}</b></div>
            <div><span>Duration</span><b>{formatDuration(summary.startTime, summary.closeTime)}</b></div>
            <div><span>Run chain</span><b>{runChain.length}</b></div>
            <div><span>Semantic events</span><b>{history.length}</b></div>
          </div>
        )}
      </section>

      {error && <div className="error-banner run-error">{error}</div>}
      {storedValueWarning && (
        <div className="warning-banner run-error">{storedValueWarning}</div>
      )}

      <div className="run-tabs" role="tablist">
        {tabs.map(([id, label]) => (
          <button key={id} className={tab === id ? 'active' : ''} onClick={() => setTab(id)}>
            {label}
          </button>
        ))}
      </div>

      <div className="run-content">
        <section className="run-primary">
          {tab === 'overview' && summary && (
            <FlowOverview summary={summary} events={displayedHistory} state={state} />
          )}
          {tab === 'steps' && (
            <StepGraph
              flowId={flowId}
              events={displayedHistory}
              state={state}
              selectedEvent={selectedEvent}
              onSelectEvent={setSelectedEvent}
            />
          )}
          {tab === 'timeline' && (
            <Timeline
              flowId={flowId}
              events={displayedHistory}
              selectedEvent={selectedEvent}
              onSelectEvent={setSelectedEvent}
            />
          )}
          {nextPageToken && (
            <div className="load-more">
              <button
                className="button ghost"
                disabled={loadingMore}
                onClick={() => {
                  setLoadingMore(true);
                  void loadHistoryPage(nextPageToken, nextInternalEventId, true)
                    .catch((loadError) => setError(loadError instanceof Error ? loadError.message : 'History load failed'))
                    .finally(() => setLoadingMore(false));
                }}
              >
                {loadingMore ? 'Loading…' : 'Load more history'}
              </button>
              <span>Graphs currently show the loaded semantic history.</span>
            </div>
          )}
        </section>
        <aside className="run-sidebar">
          <FlowStatePanel state={state} selectedEvent={selected} summary={summary} />
        </aside>
      </div>

      {summary && (
        <ResetFlowDialog
          open={resetOpen}
          summary={summary}
          events={history}
          onClose={() => setResetOpen(false)}
        />
      )}
    </div>
  );
}
