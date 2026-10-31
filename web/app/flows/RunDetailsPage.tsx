// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from 'react';
import type {
  CSSProperties,
  KeyboardEvent as ReactKeyboardEvent,
  PointerEvent as ReactPointerEvent,
} from 'react';
import { formatDate, formatDuration } from '@/lib/format';
import { hydrateBlobs } from '@/lib/blobs';
import { latestEventIdOfWait, withFlowStart } from '@/lib/history';
import { isTransientGatewayResponse, readResponseJSON } from '@/lib/http';
import { buildSelectedTimelineRelations } from '@/lib/timeline';
import { positiveRunAttempt, RUN_ATTEMPT_SEARCH_PARAM } from '@/lib/timeTravel';
import { VALUE_BLOB_UNAVAILABLE } from '@/lib/unavailable';
import type {
  FlowHistoryEvent,
  FlowState,
  FlowSummary,
} from '@/lib/types';
import { StatusBadge } from '../components/StatusBadge';
import { LoadingIndicator } from '../components/LoadingIndicator';
import { usePreferences } from '../providers';
import { FlowOverview } from './details/FlowOverview';
import { FlowStatePanel } from './details/FlowStatePanel';
import { TimeTravelDialog, type TimeTravelResult } from './details/TimeTravelDialog';
import { StateWriteDialog, type StateWriteKind } from './details/StateWriteDialog';
import { StopFlowDialog } from './details/StopFlowDialog';
import { ExecutionGraph } from './details/ExecutionGraph';
import { StreamPanel } from './details/StreamPanel';
import { Timeline } from './details/Timeline';
import { useRunHistory } from './useRunHistory';

type RunTab = 'overview' | 'steps' | 'timeline' | 'stream';

interface SelectedEventConnector {
  height: number;
  path: string;
  tone: 'default' | 'execute' | 'wait-for';
  width: number;
}

const terminalStatuses = new Set([2, 3, 4, 5, 6, 7]);
const noHistoryEvents: FlowHistoryEvent[] = [];
const sidebarWidthStorageKey = 'dex.run.selected-event-width';
const runTabStorageKey = 'dex.run.selected-tab';
const runTabs = new Set<RunTab>(['overview', 'steps', 'timeline', 'stream']);
const defaultSidebarWidth = 340;
const minimumSidebarWidth = 280;
const maximumSidebarWidth = 720;

function clampSidebarWidth(width: number): number {
  return Math.min(maximumSidebarWidth, Math.max(minimumSidebarWidth, width));
}

function storedSidebarWidth(): number {
  if (typeof window === 'undefined') return defaultSidebarWidth;
  try {
    const width = Number.parseInt(window.localStorage.getItem(sidebarWidthStorageKey) || '', 10);
    return Number.isFinite(width) ? clampSidebarWidth(width) : defaultSidebarWidth;
  } catch (storageError) {
    console.warn('Unable to read the selected event width preference.', storageError);
    return defaultSidebarWidth;
  }
}

function persistSidebarWidth(width: number) {
  try {
    window.localStorage.setItem(sidebarWidthStorageKey, String(width));
  } catch (storageError) {
    console.warn('Unable to save the selected event width preference.', storageError);
  }
}

function storedRunTab(): RunTab {
  if (typeof window === 'undefined') return 'steps';
  try {
    const tab = window.localStorage.getItem(runTabStorageKey);
    return tab && runTabs.has(tab as RunTab) ? tab as RunTab : 'steps';
  } catch (storageError) {
    console.warn('Unable to read the selected run tab preference.', storageError);
    return 'steps';
  }
}

function persistRunTab(tab: RunTab) {
  try {
    window.localStorage.setItem(runTabStorageKey, tab);
  } catch (storageError) {
    console.warn('Unable to save the selected run tab preference.', storageError);
  }
}

function selectedEventConnectorTone(event: FlowHistoryEvent): SelectedEventConnector['tone'] {
  if (event.type.startsWith('StepWaitFor')) return 'wait-for';
  if (event.type.startsWith('StepExecute')) return 'execute';
  return 'default';
}

export function RunDetailsPage({
  flowId,
  runId,
  breadcrumb,
  runPath,
}: {
  flowId: string;
  runId: string;
  /** Supplied by the host shell, which owns where "up" goes. */
  breadcrumb: ReactNode;
  /** Where a sibling run in this chain lives under the host's routes. */
  runPath: (chainRunID: string) => string;
}) {
  const { timezone } = usePreferences();
  const navigate = useNavigate();
  const [searchParams, setSearchParams] = useSearchParams();
  const requestedRunAttempt = positiveRunAttempt(searchParams.get(RUN_ATTEMPT_SEARCH_PARAM));
  const [summary, setSummary] = useState<FlowSummary | null>(null);
  const runHistory = useRunHistory(flowId, runId);
  const { appendNewest, clear: clearRunHistory, loadNewest } = runHistory;
  const [state, setState] = useState<FlowState | null>(null);
  const [tab, setTab] = useState<RunTab>(storedRunTab);
  const [selectedEvent, setSelectedEvent] = useState<FlowHistoryEvent | null>(null);
  const [loading, setLoading] = useState(true);
  const [pendingRunRequestCount, setPendingRunRequestCount] = useState(0);
  const [deletingChannelMessage, setDeletingChannelMessage] = useState('');
  const [error, setError] = useState('');
  const [autoRefresh, setAutoRefresh] = useState(true);
  const [timeTravelOpen, setTimeTravelOpen] = useState(false);
  const [timeTravelEvent, setTimeTravelEvent] = useState<FlowHistoryEvent | null>(null);
  const [stopOpen, setStopOpen] = useState(false);
  const [stateWriteKind, setStateWriteKind] = useState<StateWriteKind | null>(null);
  const [waitCycle, setWaitCycle] = useState(0);
  const [hydratedEvents, setHydratedEvents] = useState<Record<number, FlowHistoryEvent>>({});
  const [dataWarnings, setDataWarnings] = useState<string[]>([]);
  const [sidebarWidth, setSidebarWidth] = useState(storedSidebarWidth);
  const [resizingSidebar, setResizingSidebar] = useState(false);
  const [selectedEventConnector, setSelectedEventConnector] = useState<SelectedEventConnector | null>(null);
  const waitGeneration = useRef(0);
  const blobCache = useRef(new Map<string, unknown>());
  const hydratingEventIDs = useRef(new Set<number>());
  const hydratedEventIDs = useRef(new Set<number>());
  const sidebarWidthRef = useRef(sidebarWidth);
  const sidebarResizeStart = useRef({ pointerX: 0, width: sidebarWidth });
  const runContent = useRef<HTMLDivElement>(null);
  const followedRunAttempt = useRef(0);

  const summaryURL = `/api/flows/summary?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(runId)}`;
  const stateURL = `/api/flows/state?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(runId)}`;
  const addDataWarning = useCallback((warning: string) => {
    setDataWarnings((current) => current.includes(warning) ? current : [...current, warning]);
  }, []);
  const updateSidebarWidth = useCallback((width: number) => {
    const nextWidth = clampSidebarWidth(width);
    sidebarWidthRef.current = nextWidth;
    setSidebarWidth(nextWidth);
    return nextWidth;
  }, []);
  const startSidebarResize = useCallback((event: ReactPointerEvent<HTMLButtonElement>) => {
    if (event.button !== 0) return;
    event.preventDefault();
    sidebarResizeStart.current = {
      pointerX: event.clientX,
      width: sidebarWidthRef.current,
    };
    setResizingSidebar(true);
  }, []);
  const resizeSidebarWithKeyboard = useCallback((event: ReactKeyboardEvent<HTMLButtonElement>) => {
    let nextWidth = sidebarWidthRef.current;
    if (event.key === 'ArrowLeft') nextWidth += 24;
    else if (event.key === 'ArrowRight') nextWidth -= 24;
    else if (event.key === 'Home') nextWidth = minimumSidebarWidth;
    else if (event.key === 'End') nextWidth = maximumSidebarWidth;
    else return;
    event.preventDefault();
    persistSidebarWidth(updateSidebarWidth(nextWidth));
  }, [updateSidebarWidth]);

  useEffect(() => {
    if (!resizingSidebar) return;
    const resize = (event: PointerEvent) => {
      const start = sidebarResizeStart.current;
      updateSidebarWidth(start.width + start.pointerX - event.clientX);
    };
    const finish = () => {
      persistSidebarWidth(sidebarWidthRef.current);
      setResizingSidebar(false);
    };
    window.addEventListener('pointermove', resize);
    window.addEventListener('pointerup', finish, { once: true });
    window.addEventListener('pointercancel', finish, { once: true });
    return () => {
      window.removeEventListener('pointermove', resize);
      window.removeEventListener('pointerup', finish);
      window.removeEventListener('pointercancel', finish);
    };
  }, [resizingSidebar, updateSidebarWidth]);

  const loadState = useCallback(async (statusCode: number) => {
    if (terminalStatuses.has(statusCode)) {
      setState(null);
      return;
    }
    try {
      const rawState = await readResponseJSON<FlowState>(await fetch(stateURL, { cache: 'no-store' }));
      setState(rawState);
      const hydrated = await hydrateBlobs(flowId, rawState, blobCache.current);
      setState(hydrated.value);
      if (hydrated.error) addDataWarning(hydrated.error);
    } catch (stateError) {
      setError(stateError instanceof Error ? stateError.message : 'State query failed');
    }
  }, [addDataWarning, flowId, stateURL]);

  const deleteChannelMessage = useCallback(async (channelName: string, messageId: string) => {
    if (!summary) return;
    setDeletingChannelMessage(messageId);
    setError('');
    try {
      await readResponseJSON(await fetch('/api/flows/channels/messages', {
        method: 'DELETE',
        headers: { 'content-type': 'application/json' },
        body: JSON.stringify({ flowId, runId, channelName, messageId }),
      }));
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : 'Channel deletion failed');
    } finally {
      setDeletingChannelMessage('');
      await loadState(summary.flowStatusCode);
    }
  }, [flowId, loadState, runId, summary]);

  const hydrateEvent = useCallback(async (event: FlowHistoryEvent) => {
    if (hydratedEventIDs.current.has(event.eventId)
      || hydratingEventIDs.current.has(event.eventId)) return;
    hydratingEventIDs.current.add(event.eventId);
    try {
      const hydrated = await hydrateBlobs(flowId, event, blobCache.current);
      setHydratedEvents((current) => ({ ...current, [event.eventId]: hydrated.value }));
      hydratedEventIDs.current.add(event.eventId);
      if (hydrated.error) addDataWarning(hydrated.error);
    } catch {
      addDataWarning(VALUE_BLOB_UNAVAILABLE);
    } finally {
      hydratingEventIDs.current.delete(event.eventId);
    }
  }, [addDataWarning, flowId]);

  /** Zero follows the current run attempt; a requested earlier attempt pins its history. */
  const historyRunAttempt = useCallback((latestSummary: FlowSummary) => {
    const currentRunAttempt = positiveRunAttempt(latestSummary.runAttempt);
    return requestedRunAttempt > 0 && requestedRunAttempt < currentRunAttempt ? requestedRunAttempt : 0;
  }, [requestedRunAttempt]);

  const clearLoadedRun = useCallback(() => {
    clearRunHistory();
    setSelectedEvent(null);
    setHydratedEvents({});
    setDataWarnings([]);
    blobCache.current.clear();
    hydratingEventIDs.current.clear();
    hydratedEventIDs.current.clear();
  }, [clearRunHistory]);

  const loadSummary = useCallback(async () => {
    const value = await readResponseJSON<FlowSummary>(await fetch(summaryURL, { cache: 'no-store' }));
    setSummary(value);
    return value;
  }, [summaryURL]);

  /** Loads only the newest history window, so a refresh never rereads the whole history. */
  const loadHistory = useCallback(async (runAttempt: number) => {
    setPendingRunRequestCount((current) => current + 1);
    try {
      await loadNewest(runAttempt);
    } finally {
      setPendingRunRequestCount((current) => current - 1);
    }
  }, [loadNewest]);

  const refresh = useCallback(async () => {
    setPendingRunRequestCount((current) => current + 1);
    setError('');
    try {
      const latestSummary = await loadSummary();
      const latestRunAttempt = positiveRunAttempt(latestSummary.runAttempt);
      // Another client may have time traveled while Live was off.
      if (latestRunAttempt !== followedRunAttempt.current) clearLoadedRun();
      followedRunAttempt.current = latestRunAttempt;
      await Promise.all([
        loadHistory(historyRunAttempt(latestSummary)),
        loadState(latestSummary.flowStatusCode),
      ]);
    } catch (refreshError) {
      setError(refreshError instanceof Error ? refreshError.message : 'Run refresh failed');
    } finally {
      setPendingRunRequestCount((current) => current - 1);
    }
  }, [clearLoadedRun, historyRunAttempt, loadHistory, loadState, loadSummary]);

  useEffect(() => {
    let active = true;
    setSummary(null);
    setState(null);
    clearLoadedRun();
    setError('');
    setLoading(true);
    void refresh().finally(() => {
      if (active) setLoading(false);
    });
    return () => {
      active = false;
      waitGeneration.current += 1;
    };
  }, [clearLoadedRun, refresh]);

  const isViewingEarlierRunAttempt = summary !== null && historyRunAttempt(summary) > 0;
  const nextInternalEventId = runHistory.newest?.nextInternalEventId ?? 0;

  useEffect(() => {
    if (
      !autoRefresh
      || !summary
      || terminalStatuses.has(summary.flowStatusCode)
      || nextInternalEventId <= 0
      || isViewingEarlierRunAttempt
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
        if (isTransientGatewayResponse(response)) {
          window.setTimeout(() => setWaitCycle((current) => current + 1), 1000);
          return;
        }
        const latestEventId = latestEventIdOfWait(await readResponseJSON(response));
        const latestSummary = await loadSummary();
        const latestRunAttempt = positiveRunAttempt(latestSummary.runAttempt);
        if (latestRunAttempt !== followedRunAttempt.current) {
          // Time travel replaced the run attempt, so loaded events after its fork point are stale.
          followedRunAttempt.current = latestRunAttempt;
          clearLoadedRun();
          await Promise.all([
            loadHistory(historyRunAttempt(latestSummary)),
            loadState(latestSummary.flowStatusCode),
          ]);
          return;
        }
        await Promise.all([
          appendNewest(latestEventId),
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
    appendNewest,
    autoRefresh,
    clearLoadedRun,
    flowId,
    historyRunAttempt,
    isViewingEarlierRunAttempt,
    loadHistory,
    loadState,
    loadSummary,
    nextInternalEventId,
    runId,
    summary,
    waitCycle,
  ]);

  const tabs: Array<[RunTab, string]> = [
    ['overview', 'Overview'],
    ['steps', 'Execution graph'],
    ['timeline', 'Timeline'],
    ['stream', 'Streams'],
  ];
  const history = runHistory.newest?.events ?? noHistoryEvents;
  const timelineWindow = runHistory.timelineWindow;
  const timelineEvents = timelineWindow?.events ?? noHistoryEvents;
  const flowStartEvent = runHistory.flowStartEvent;
  const prunedEventIdRange = runHistory.prunedEventIdRange;
  const latestEvent = history.at(-1);
  const selectedRaw = selectedEvent ?? latestEvent ?? null;
  const selected = selectedRaw ? hydratedEvents[selectedRaw.eventId] ?? selectedRaw : null;
  // Views outside Timeline read the newest window, with FlowStart for start details and routing.
  const historyWithFlowStart = useMemo(() => withFlowStart(history, flowStartEvent), [flowStartEvent, history]);
  const displayedHistory = useMemo(
    () => historyWithFlowStart.map((event) => hydratedEvents[event.eventId] ?? event),
    [historyWithFlowStart, hydratedEvents],
  );
  const displayedTimelineEvents = useMemo(
    () => timelineEvents.map((event) => hydratedEvents[event.eventId] ?? event),
    [hydratedEvents, timelineEvents],
  );
  const isSelectionOnTimeline = tab === 'timeline';
  const selectionEvents = isSelectionOnTimeline ? displayedTimelineEvents : displayedHistory;
  const selectionWindow = isSelectionOnTimeline ? timelineWindow : runHistory.newest;
  const selectionHasOlder = selectionWindow?.hasOlder ?? false;
  const selectionHasNewer = isSelectionOnTimeline && runHistory.timelineHasNewer;
  const selectedRelations = useMemo(() => buildSelectedTimelineRelations(selectionEvents, selectedRaw, {
    hasOlder: selectionHasOlder,
    hasNewer: selectionHasNewer,
    prunedEventIdRange,
  }), [prunedEventIdRange, selectedRaw, selectionEvents, selectionHasNewer, selectionHasOlder]);
  const { back: backInTimeline, highlightEvent, jumpToEvent: jumpInTimeline } = runHistory;

  const jumpToEvent = useCallback(async (eventId: number) => {
    setError('');
    setTab('timeline');
    persistRunTab('timeline');
    try {
      const result = await jumpInTimeline(eventId, selectedEvent);
      // A pruned target needs no error: the refreshed pruned range turns its stub into a pruned stub.
      if (result.kind === 'loaded') setSelectedEvent(result.event);
      else if (result.kind === 'missing') setError(`Event #${eventId} is not in the retained history.`);
    } catch (jumpError) {
      setError(jumpError instanceof Error ? jumpError.message : 'History page failed to load');
    }
  }, [jumpInTimeline, selectedEvent]);

  const selectRelatedEvent = useCallback((eventId: number) => {
    const target = selectionEvents.find((event) => event.eventId === eventId);
    if (!target) return;
    setSelectedEvent(target);
    highlightEvent(eventId);
  }, [highlightEvent, selectionEvents]);

  const navigateTimeline = useCallback((navigation: () => Promise<void>) => {
    setError('');
    void navigation().catch((navigationError: unknown) => {
      setError(navigationError instanceof Error ? navigationError.message : 'History page failed to load');
    });
  }, []);

  const goBackInTimeline = useCallback(() => {
    navigateTimeline(async () => setSelectedEvent(await backInTimeline()));
  }, [backInTimeline, navigateTimeline]);

  useEffect(() => {
    if (selectedRaw) void hydrateEvent(selectedRaw);
  }, [hydrateEvent, selectedRaw?.eventId]);

  useEffect(() => {
    if (tab === 'overview' && flowStartEvent) void hydrateEvent(flowStartEvent);
  }, [flowStartEvent, hydrateEvent, tab]);

  useLayoutEffect(() => {
    const content = runContent.current;
    const selectedEventID = selectedEvent?.eventId;
    if (!content || tab !== 'timeline' || !selectedEventID) {
      setSelectedEventConnector(null);
      return;
    }
    const source = content.querySelector<HTMLElement>(`[data-event-id="${selectedEventID}"] .event-card`);
    const target = content.querySelector<HTMLElement>('[data-selected-event-target]');
    if (!source || !target) {
      setSelectedEventConnector(null);
      return;
    }
    let animationFrame = 0;
    const update = () => {
      animationFrame = 0;
      if (window.innerWidth <= 1100) {
        setSelectedEventConnector(null);
        return;
      }
      const contentRect = content.getBoundingClientRect();
      const sourceRect = source.getBoundingClientRect();
      const targetRect = target.getBoundingClientRect();
      const startX = sourceRect.right - contentRect.left + 4;
      const startY = sourceRect.top + sourceRect.height / 2 - contentRect.top;
      const endX = targetRect.left - contentRect.left - 4;
      const endY = targetRect.top + Math.min(48, targetRect.height / 2) - contentRect.top;
      const bendX = startX + (endX - startX) / 2;
      setSelectedEventConnector({
        height: content.scrollHeight,
        path: `M ${startX} ${startY} H ${bendX} V ${endY} H ${endX}`,
        tone: selectedEventConnectorTone(selectedEvent),
        width: content.scrollWidth,
      });
    };
    const scheduleUpdate = () => {
      if (!animationFrame) animationFrame = window.requestAnimationFrame(update);
    };
    const observer = new ResizeObserver(scheduleUpdate);
    observer.observe(content);
    observer.observe(source);
    observer.observe(target);
    window.addEventListener('resize', scheduleUpdate);
    window.addEventListener('scroll', scheduleUpdate, true);
    scheduleUpdate();
    return () => {
      if (animationFrame) window.cancelAnimationFrame(animationFrame);
      observer.disconnect();
      window.removeEventListener('resize', scheduleUpdate);
      window.removeEventListener('scroll', scheduleUpdate, true);
    };
  }, [selectedEvent, sidebarWidth, tab, timelineEvents]);

  const runChain = useMemo(() => {
    if (!summary) return [];
    return [...new Set([summary.firstRunId, summary.runId].filter(Boolean))];
  }, [summary]);
  const continuedToRunId = useMemo(() => {
    const closed = [...history].reverse().find((event) => event.type === 'FlowClosed');
    const nextRunId = closed?.payload.continuedToRunId;
    return typeof nextRunId === 'string' ? nextRunId : '';
  }, [history]);

  const openTimeTravel = useCallback((event: FlowHistoryEvent | null) => {
    setTimeTravelEvent(event);
    setTimeTravelOpen(true);
  }, []);

  const closeTimeTravel = useCallback(() => {
    setTimeTravelOpen(false);
    setTimeTravelEvent(null);
  }, []);

  const showRunAttempt = useCallback((runAttempt: number) => {
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      if (runAttempt > 0) next.set(RUN_ATTEMPT_SEARCH_PARAM, String(runAttempt));
      else next.delete(RUN_ATTEMPT_SEARCH_PARAM);
      return next;
    });
  }, [setSearchParams]);

  const handleTimeTraveled = useCallback((result: TimeTravelResult) => {
    if (result.runId !== runId) {
      navigate(runPath(result.runId));
      return;
    }
    if (requestedRunAttempt > 0) {
      showRunAttempt(0);
      return;
    }
    void refresh();
  }, [navigate, refresh, requestedRunAttempt, runId, runPath, showRunAttempt]);

  const currentRunAttempt = positiveRunAttempt(summary?.runAttempt);
  const displayedRunAttempt = summary && isViewingEarlierRunAttempt ? historyRunAttempt(summary) : currentRunAttempt;

  if (loading && !summary) return <LoadingIndicator label="Loading flow run…" />;
  const isWaitingForHistory = history.length === 0 && (loading || pendingRunRequestCount > 0);

  return (
    <div className="run-page">
      <section className="run-header">
        {breadcrumb}
        <div className="run-title-row">
          <div>
            <p className="eyebrow">{summary?.flowType || 'Flow execution'}</p>
            <h1>{flowId}</h1>
          </div>
          <div className="run-actions">
            {continuedToRunId && (
              <Link className="button primary" to={runPath(continuedToRunId)}>
                Next run
              </Link>
            )}
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
            <button
              className="button danger"
              disabled={!summary || terminalStatuses.has(summary.flowStatusCode)}
              onClick={() => setStopOpen(true)}
            >
              Stop
            </button>
            <button
              className="button danger"
              onClick={() => openTimeTravel(selectedEvent ? selected : null)}
            >
              Time Travel
            </button>
          </div>
        </div>
        {summary && (
          <div className="summary-strip">
            <div><span>Status</span><StatusBadge status={summary.flowStatus} /></div>
            <div><span>Started</span><b>{formatDate(summary.startTime, timezone)}</b></div>
            <div><span>Closed</span><b>{formatDate(summary.closeTime, timezone)}</b></div>
            <div><span>Duration</span><b>{formatDuration(summary.startTime, summary.closeTime)}</b></div>
            {currentRunAttempt > 0 ? (
              <div>
                <span>Run attempt</span>
                <RunAttemptSelect
                  currentRunAttempt={currentRunAttempt}
                  displayedRunAttempt={displayedRunAttempt}
                  onSelect={(runAttempt) => showRunAttempt(runAttempt === currentRunAttempt ? 0 : runAttempt)}
                />
              </div>
            ) : (
              <div><span>Run chain</span><b>{runChain.length}</b></div>
            )}
            <div><span>Latest event</span><b>{latestEvent ? `#${latestEvent.eventId}` : 'None'}</b></div>
          </div>
        )}
      </section>

      {isViewingEarlierRunAttempt && (
        <div className="warning-banner run-error">
          Showing the history of run attempt {displayedRunAttempt}. Time travel replaced it with run
          attempt {currentRunAttempt}. Live state and controls belong to the current attempt.{' '}
          <button className="button ghost" type="button" onClick={() => showRunAttempt(0)}>
            View current attempt
          </button>
        </div>
      )}
      {error && <div className="error-banner run-error">{error}</div>}
      {dataWarnings.map((warning) => (
        <div className="warning-banner run-error" key={warning}>{warning}</div>
      ))}

      <div className="run-tabs" role="tablist">
        {tabs.map(([id, label]) => (
          <button
            key={id}
            className={tab === id ? 'active' : ''}
            onClick={() => {
              setTab(id);
              persistRunTab(id);
            }}
          >
            {label}
          </button>
        ))}
      </div>

      <div
        className={[
          'run-content',
          tab === 'overview' ? 'run-content--overview' : '',
          resizingSidebar ? 'is-resizing-sidebar' : '',
        ].filter(Boolean).join(' ')}
        ref={runContent}
        style={{ '--run-sidebar-width': `${sidebarWidth}px` } as CSSProperties}
      >
        {selectedEventConnector && (
          <svg
            aria-hidden="true"
            className={`selected-event-connector tone-${selectedEventConnector.tone}`}
            height={selectedEventConnector.height}
            viewBox={`0 0 ${selectedEventConnector.width} ${selectedEventConnector.height}`}
            width={selectedEventConnector.width}
          >
            <path d={selectedEventConnector.path} />
          </svg>
        )}
        <section className="run-primary">
          {isWaitingForHistory && (tab === 'steps' || tab === 'timeline') && (
            <LoadingIndicator className="card page-loading" label="Loading events…" />
          )}
          {tab === 'overview' && summary && (
            <FlowOverview
              summary={summary}
              events={displayedHistory}
              state={state}
              selectedEvent={selected}
              deletingChannelMessage={deletingChannelMessage}
              onDeleteChannelMessage={deleteChannelMessage}
              onOpenStateWrite={setStateWriteKind}
            />
          )}
          {tab === 'steps' && !isWaitingForHistory && runHistory.newest?.hasOlder && (
            <p className="muted run-window-note">
              The graph shows Steps from events #{history[0]?.eventId} to #{latestEvent?.eventId}. Older events are
              on earlier Timeline pages.
            </p>
          )}
          {tab === 'steps' && !isWaitingForHistory && (
            <ExecutionGraph
              flowId={flowId}
              events={displayedHistory}
              hasEarlierEvents={runHistory.newest?.hasOlder ?? false}
              state={state}
              selectedEvent={selectedEvent}
              onSelectEvent={setSelectedEvent}
            />
          )}
          {tab === 'timeline' && !isWaitingForHistory && (
            <Timeline
              flowId={flowId}
              events={displayedTimelineEvents}
              selectedEvent={selectedEvent}
              onSelectEvent={(event) => {
                highlightEvent(null);
                setSelectedEvent(event);
              }}
              relations={selectedEvent ? selectedRelations : undefined}
              paging={{
                hasOlder: timelineWindow?.hasOlder ?? false,
                hasNewer: runHistory.timelineHasNewer,
                isNewest: runHistory.isTimelineOnNewest,
                isLoading: runHistory.isLoadingTimelinePage,
                canGoBack: runHistory.canGoBack,
                onOlder: () => navigateTimeline(runHistory.showOlder),
                onNewer: () => navigateTimeline(runHistory.showNewer),
                onNewest: runHistory.showNewest,
                onBack: goBackInTimeline,
              }}
              prunedEventIdRange={prunedEventIdRange}
              highlightedEventId={runHistory.highlightedEventId}
              onJumpToEvent={(eventId) => void jumpToEvent(eventId)}
            />
          )}
          {tab === 'stream' && summary && (
            <StreamPanel flowId={flowId} flowType={summary.flowType} />
          )}
        </section>
        {(tab === 'steps' || tab === 'timeline') && (
          <aside className="run-sidebar">
            <button
              type="button"
              className="run-sidebar-resizer"
              role="separator"
              aria-label="Resize selected event panel"
              aria-orientation="vertical"
              aria-valuemin={minimumSidebarWidth}
              aria-valuemax={maximumSidebarWidth}
              aria-valuenow={sidebarWidth}
              onDoubleClick={() => persistSidebarWidth(updateSidebarWidth(defaultSidebarWidth))}
              onKeyDown={resizeSidebarWithKeyboard}
              onPointerDown={startSidebarResize}
            />
            <FlowStatePanel
              selectedEvent={selected}
              history={withFlowStart(selectionEvents, flowStartEvent)}
              parentFlowId={flowId}
              relatedEvents={selectedRelations.related}
              onSelectRelatedEvent={selectRelatedEvent}
              onJumpToEvent={(eventId) => void jumpToEvent(eventId)}
              isJumpDisabled={runHistory.isLoadingTimelinePage}
            />
          </aside>
        )}
      </div>

      {summary && (
        <TimeTravelDialog
          key={timeTravelEvent?.eventId ?? 'manual'}
          open={timeTravelOpen}
          summary={summary}
          events={historyWithFlowStart}
          initialEvent={timeTravelEvent}
          runAttempt={isViewingEarlierRunAttempt ? displayedRunAttempt : 0}
          timezone={timezone}
          onClose={closeTimeTravel}
          onTimeTraveled={handleTimeTraveled}
        />
      )}
      {summary && (
        <StopFlowDialog
          open={stopOpen}
          summary={summary}
          onClose={() => setStopOpen(false)}
          onStopped={() => void refresh()}
        />
      )}
      {summary && (
        <StateWriteDialog
          key={stateWriteKind ?? 'closed'}
          kind={stateWriteKind}
          summary={summary}
          onClose={() => setStateWriteKind(null)}
          onWritten={() => loadState(summary.flowStatusCode)}
        />
      )}
    </div>
  );
}

/** Lists every run attempt of a run, newest first. Run attempts are numbered from 1 without gaps. */
function RunAttemptSelect({
  currentRunAttempt,
  displayedRunAttempt,
  onSelect,
}: {
  currentRunAttempt: number;
  displayedRunAttempt: number;
  onSelect: (runAttempt: number) => void;
}) {
  const attempts = Array.from({ length: currentRunAttempt }, (_, index) => currentRunAttempt - index);
  return (
    <select
      aria-label="Run attempt"
      value={displayedRunAttempt}
      onChange={(event) => onSelect(Number(event.target.value))}
    >
      {attempts.map((attempt) => (
        <option key={attempt} value={attempt}>
          {attempt === currentRunAttempt ? `${attempt} (current)` : String(attempt)}
        </option>
      ))}
    </select>
  );
}
