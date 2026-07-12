'use client';

import Link from 'next/link';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import Header from '../../components/Header';
import StatusBadge from '../../components/StatusBadge';
import EventCard from '../../components/EventCard';
import { formatTimestamp } from '../../components/utils';
import type { HistoryEvent, RunDetail } from '../../api/_grpc/mappers';
import WorkflowGraph from './WorkflowGraph';
import RunStatePanel from './RunStatePanel';

type ViewMode = 'timeline' | 'graph';

interface HistoryResponse {
  events: HistoryEvent[];
}

// Status values for which we keep auto-polling /api/runs/get. Mirrors
// persistence.RunStatus: 0 Pending, 1 WaitingForWorker, 2 Running,
// 3 AllStepsWaiting are all non-terminal; 4/5 are terminal so we stop.
const NON_TERMINAL_STATUSES = new Set([0, 1, 2, 3]);
const POLL_INTERVAL_MS = 2000;

function ShowInner() {
  const router = useRouter();
  const pathname = usePathname();
  const search = useSearchParams();
  const namespace = search.get('namespace') ?? '';
  const runId = search.get('runId') ?? '';
  // Default view = graph (set ?view=timeline to opt out of the graph).
  const view: ViewMode = search.get('view') === 'timeline' ? 'timeline' : 'graph';

  const [events, setEvents] = useState<HistoryEvent[] | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Live run detail (RunsService.GetRun) — authoritative status + state +
  // unconsumed channel messages. Polled every 2s while non-terminal so the
  // panel stays in sync without a page refresh. Independent of the history
  // events fetch above (different RPC, different data source).
  const [runDetail, setRunDetail] = useState<RunDetail | null>(null);
  const [runDetailError, setRunDetailError] = useState<string | null>(null);
  const [runDetailFetchedAt, setRunDetailFetchedAt] = useState<number | null>(null);
  // Force a re-render every poll tick so the panel's "Updated 3s ago" label
  // ticks up between actual fetches without us coupling to setRunDetail.
  const [, setNowTick] = useState(0);
  // Stale history responses must not overwrite newer fetches (race on fast runs).
  const historyGenRef = useRef(0);

  const fetchHistory = useCallback(async (): Promise<HistoryEvent[] | null> => {
    if (!namespace || !runId) return null;
    const generation = ++historyGenRef.current;
    try {
      const url = `/api/runs/history?namespace=${encodeURIComponent(namespace)}&runId=${encodeURIComponent(runId)}`;
      const resp = await fetch(url);
      if (!resp.ok) return null;
      const data = (await resp.json()) as HistoryResponse;
      if (generation !== historyGenRef.current) return null;
      setEvents(data.events ?? []);
      return data.events ?? [];
    } catch {
      return null;
    }
  }, [namespace, runId]);

  const setView = (next: ViewMode) => {
    const params = new URLSearchParams(search.toString());
    if (next === 'graph') params.delete('view');
    else params.set('view', next);
    router.replace(`${pathname}?${params.toString()}`);
  };

  useEffect(() => {
    if (!namespace || !runId) return;
    historyGenRef.current += 1;
    let cancelled = false;
    (async () => {
      setLoading(true);
      setError(null);
      try {
        const generation = ++historyGenRef.current;
        const url = `/api/runs/history?namespace=${encodeURIComponent(namespace)}&runId=${encodeURIComponent(runId)}`;
        const resp = await fetch(url);
        if (!resp.ok) {
          const errBody = (await resp.json().catch(() => ({}))) as { error?: string };
          throw new Error(errBody.error || `HTTP ${resp.status}`);
        }
        const data = (await resp.json()) as HistoryResponse;
        if (!cancelled && generation === historyGenRef.current) {
          setEvents(data.events ?? []);
        }
      } catch (err) {
        if (!cancelled) setError(err instanceof Error ? err.message : 'unknown error');
      } finally {
        if (!cancelled) setLoading(false);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [namespace, runId]);

  const prevVersionRef = useRef<number | null>(null);
  const justBecameTerminalRef = useRef(false);

  // Live run-detail fetch + auto-poll. The fetcher itself is stable; the
  // polling effect arms a setInterval and tears it down whenever the run
  // status flips terminal (so we stop hammering the BFF). Refetching is
  // also triggered manually by the Refresh button.
  const fetchRunDetail = useCallback(async () => {
    if (!namespace || !runId) return;
    try {
      const resp = await fetch(
        `/api/runs/get?namespace=${encodeURIComponent(namespace)}&runId=${encodeURIComponent(runId)}`,
      );
      if (!resp.ok) {
        const errBody = (await resp.json().catch(() => ({}))) as { error?: string };
        throw new Error(errBody.error || `HTTP ${resp.status}`);
      }
      const data = (await resp.json()) as RunDetail;
      setRunDetail(data);
      setRunDetailError(null);
      setRunDetailFetchedAt(Date.now());
    } catch (err) {
      setRunDetailError(err instanceof Error ? err.message : 'unknown error');
      setRunDetailFetchedAt(Date.now());
    }
  }, [namespace, runId]);

  // Initial fetch + restart on (namespace, runId) change.
  useEffect(() => {
    if (!namespace || !runId) return;
    setRunDetail(null);
    setRunDetailError(null);
    setRunDetailFetchedAt(null);
    justBecameTerminalRef.current = false;
    prevVersionRef.current = null;
    fetchRunDetail();
  }, [namespace, runId, fetchRunDetail]);

  // Auto-poll while the run is non-terminal. We re-arm the effect off
  // runDetail.status so terminal transitions cancel the interval immediately
  // (no one extra tick after RunStop lands).
  const isPolling = runDetail !== null && NON_TERMINAL_STATUSES.has(runDetail.status);
  useEffect(() => {
    if (!isPolling) return;
    const id = setInterval(() => {
      fetchRunDetail();
      setNowTick((n) => n + 1);
    }, POLL_INTERVAL_MS);
    return () => clearInterval(id);
  }, [isPolling, fetchRunDetail]);

  // Re-fetch history events whenever the run's version advances (new
  // engine commits). Without this, the graph has a window where a step
  // disappears: the active-overlay shows it as Running, then the engine
  // deletes it from active_step_executions (e.g. sibling cancellation),
  // but the cancel's StepExecuteCompleted history event isn't in the
  // stale events array yet — so neither source has the node and it
  // vanishes until the user manually refreshes. Keying on version keeps
  // history in sync with the active map at the same poll cadence.
  useEffect(() => {
    if (!runDetail || !namespace || !runId) return;
    const version = runDetail.version;
    if (prevVersionRef.current !== null && version !== prevVersionRef.current) {
      void fetchHistory();
    }
    prevVersionRef.current = version;
  }, [runDetail, fetchHistory]);

  // Track when the run first becomes terminal so we do one final fetch
  // (catches StepExecuteCompleted that landed between the last poll and
  // the RunStop) and ensures the panel always shows the terminal-state view.
  useEffect(() => {
    if (!runDetail) return;
    const terminal = !NON_TERMINAL_STATUSES.has(runDetail.status);
    if (terminal && !justBecameTerminalRef.current) {
      justBecameTerminalRef.current = true;
      void fetchHistory();
    }
  }, [runDetail, fetchHistory]);

  // Inferred status from history events — kept as a fallback for the brief
  // window before the first GetRun response lands (or if RunsService is
  // briefly unreachable). RunDetail.status is authoritative when available.
  const inferredStatus = useMemo(() => {
    if (!events || events.length === 0) return null;
    const end = events.find((e) => e.payload.type === 'RunStop');
    if (end && end.payload.type === 'RunStop') {
      return end.payload.data.runStatus ?? 4;
    }
    return 2; // Running
  }, [events]);

  const liveOrInferredStatus = runDetail?.status ?? inferredStatus;

  const startedAt = useMemo(() => {
    const start = events?.find((e) => e.payload.type === 'RunStart');
    return start?.occurredAtMs ?? null;
  }, [events]);

  if (!namespace || !runId) {
    return (
      <div className="min-h-screen bg-gray-50">
        <Header title="Run Details" />
        <div className="max-w-[95%] 2xl:max-w-[90%] mx-auto p-4">
          <div className="bg-red-50 border border-red-200 text-red-800 rounded-lg p-4 text-sm">
            <strong>namespace</strong> and <strong>runId</strong> query parameters are required.
            <div className="mt-2">
              <Link href="/" className="text-blue-600 hover:underline">
                ← Back to runs
              </Link>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="min-h-screen bg-gray-50">
      <Header title="Run Details" />
      <div className="max-w-[95%] 2xl:max-w-[90%] mx-auto p-4">
        <div data-testid="run-summary" className="bg-white shadow-sm border border-gray-200 rounded-lg p-4 mb-4">
          <div className="flex justify-between items-start mb-3">
            <h2 className="text-lg font-semibold text-gray-900">Run summary</h2>
            <Link
              href={`/?namespace=${encodeURIComponent(namespace)}`}
              className="text-sm text-blue-600 hover:text-blue-800 hover:underline"
            >
              ← Back to runs
            </Link>
          </div>
          <dl className="grid grid-cols-1 md:grid-cols-3 gap-x-6 gap-y-3">
            <div>
              <dt className="text-xs font-medium uppercase tracking-wide text-gray-500">Run ID</dt>
              <dd data-testid="summary-runid" className="mt-0.5 font-mono text-xs break-all text-gray-900">
                {runId}
              </dd>
            </div>
            <div>
              <dt className="text-xs font-medium uppercase tracking-wide text-gray-500">Namespace</dt>
              <dd className="mt-0.5 text-gray-900">{namespace}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium uppercase tracking-wide text-gray-500">Status</dt>
              <dd className="mt-1">
                {liveOrInferredStatus !== null ? (
                  <StatusBadge status={liveOrInferredStatus} />
                ) : (
                  <span className="text-sm text-gray-400">—</span>
                )}
              </dd>
            </div>
            <div>
              <dt className="text-xs font-medium uppercase tracking-wide text-gray-500">Started</dt>
              <dd className="mt-0.5 text-gray-900">{startedAt ? formatTimestamp(startedAt) : '—'}</dd>
            </div>
            <div>
              <dt className="text-xs font-medium uppercase tracking-wide text-gray-500">Events</dt>
              <dd className="mt-0.5 text-gray-900">{events?.length ?? '—'}</dd>
            </div>
          </dl>
        </div>

        {/* 2-column row: live state panel on the left, graph/timeline on the
            right. The panel reads from /api/runs/get (RunsService.GetRun)
            and stays live; the right card reads from /api/runs/history
            (OpsService.GetHistoryEvents). They intentionally come from
            different RPCs because history is append-only and quasi-immutable
            while live state needs polling. */}
        <div className="flex gap-4 items-start">
          <div className="w-80 shrink-0">
            <RunStatePanel
              detail={runDetail}
              loading={runDetail === null && runDetailError === null}
              error={runDetailError}
              lastFetchedAt={runDetailFetchedAt}
              polling={isPolling}
            />
          </div>
          <div className="flex-1 min-w-0 bg-white shadow-sm border border-gray-200 rounded-lg p-4">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-lg font-semibold text-gray-900">
                {view === 'graph' ? 'Step graph' : 'History timeline'}
              </h2>
              <div className="inline-flex rounded-md border border-gray-300 overflow-hidden text-sm">
                <button
                  type="button"
                  data-testid="view-toggle-timeline"
                  onClick={() => setView('timeline')}
                  className={`px-3 py-1 ${
                    view === 'timeline'
                      ? 'bg-blue-600 text-white'
                      : 'bg-white text-gray-700 hover:bg-gray-100'
                  }`}
                >
                  Timeline
                </button>
                <button
                  type="button"
                  data-testid="view-toggle-graph"
                  onClick={() => setView('graph')}
                  className={`px-3 py-1 border-l border-gray-300 ${
                    view === 'graph'
                      ? 'bg-blue-600 text-white'
                      : 'bg-white text-gray-700 hover:bg-gray-100'
                  }`}
                >
                  Graph
                </button>
              </div>
            </div>
            {loading && <div className="text-sm text-gray-500">Loading…</div>}
            {error && (
              <div className="text-sm text-red-700 bg-red-50 border border-red-200 rounded p-3">
                {error}
              </div>
            )}
            {events && events.length === 0 && !loading && (
              <div className="text-sm text-gray-500">No history events yet.</div>
            )}
            {events && events.length > 0 && view === 'timeline' && (
              <ol data-testid="timeline" className="relative">
                {events.map((e) => (
                  <EventCard key={e.id} event={e} />
                ))}
              </ol>
            )}
            {events && events.length > 0 && view === 'graph' && (
              <WorkflowGraph
                events={events}
                activeStepExecutions={runDetail?.activeStepExecutions}
              />
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

export default function ShowPage() {
  return (
    <Suspense fallback={<div className="p-4 text-sm text-gray-500">Loading…</div>}>
      <ShowInner />
    </Suspense>
  );
}
