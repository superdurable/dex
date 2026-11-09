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
import {
  latestEventIdOfWait,
  persistHistoryPageSize,
  storedHistoryPageSize,
  timelinePagePositionOf,
  withFlowStart,
  type HistoryPageSize,
} from '@/lib/history';
import { isTransientGatewayResponse, readResponseJSON } from '@/lib/http';
import { buildSelectedTimelineRelations } from '@/lib/timeline';
import { positiveRunAttempt, RUN_ATTEMPT_SEARCH_PARAM } from '@/lib/timeTravel';
import { VALUE_BLOB_UNAVAILABLE } from '@/lib/unavailable';
import type {
  FlowHistoryEvent,
  FlowState,
  FlowSummary,
} from '@/lib/types';
import {
  CalendarClock,
  Flag,
  History,
  LayoutDashboard,
  Layers,
  Link as LinkIcon,
  RefreshCw,
  Rows3,
  PencilRuler,
  Bot,
  MousePointerClick,
  Square,
  Timer,
  Waves,
  Workflow,
  type LucideIcon,
} from 'lucide-react';
import { CopyButton } from '../components/CopyButton';
import { StatusBadge } from '../components/StatusBadge';
import { LoadingIndicator } from '../components/LoadingIndicator';
import { usePreferences } from '../providers';
import { FlowOverview } from './details/FlowOverview';
import type { HistorySettingsChange } from './details/HistorySettingsCard';
import { FlowStatePanel } from './details/FlowStatePanel';
import { TimeTravelDialog, type TimeTravelResult } from './details/TimeTravelDialog';
import { StateWriteDialog, type StateWriteKind } from './details/StateWriteDialog';
import { StopFlowDialog } from './details/StopFlowDialog';
import { ExecutionGraph } from './details/ExecutionGraph';
import { RunEventsTile, type RunEventsPaging } from './details/RunEventsTile';
import { StreamPanel } from './details/StreamPanel';
import { Timeline } from './details/Timeline';
import { HorizontalTimeline } from './details/HorizontalTimeline';
import { useRunHistory } from './useRunHistory';
import { AgentSessionTab, RunActionsTab } from './details/ExperimentalRunTabs';
import { ExperimentalBadge } from '../components/ExperimentalBadge';
import { FlowDesignGraph } from '../design/FlowDesignGraph';
import { useOptionalWebCatalog } from '../catalog/WebCatalogProvider';
import { catalogEntryFor, designDefinitionFor, hasRunActions, sessionSchemaFor } from '../catalog/applicability';
import { RUN_VIEW_PARAM } from '../routes';

type RunTab = 'overview' | 'steps' | 'timeline' | 'stream' | 'design' | 'session' | 'actions';

/** History settings saved on this page, shown until a state load started after the save succeeds. */
interface SavedHistorySettings {
  runIdentity: string;
  values: HistorySettingsChange;
  savedAfterStateLoad: number;
}

/** Names one run attempt, so saved settings never apply to another run or attempt. */
function runIdentityOf(flowId: string, runId: string, runAttempt: number | undefined): string {
  return JSON.stringify([flowId, runId, runAttempt ?? 0]);
}

interface SelectedEventConnector {
  height: number;
  path: string;
  tone: 'default' | 'execute' | 'wait-for';
  width: number;
}

const terminalStatuses = new Set([2, 3, 4, 5, 6, 7]);
const noHistoryEvents: FlowHistoryEvent[] = [];
const runTabStorageKey = 'dex.run.selected-tab';
/** The horizontal Timeline needs width; narrow screens keep the vertical one. */
const horizontalTimelineQuery = '(min-width: 761px)';

/** Follows a media query; without matchMedia, such as in tests, it reports no match. */
function useMediaQuery(query: string): boolean {
  const [isMatch, setIsMatch] = useState(() => (
    typeof window !== 'undefined' && typeof window.matchMedia === 'function' && window.matchMedia(query).matches
  ));
  useEffect(() => {
    if (typeof window.matchMedia !== 'function') return undefined;
    const media = window.matchMedia(query);
    const update = () => setIsMatch(media.matches);
    update();
    media.addEventListener('change', update);
    return () => media.removeEventListener('change', update);
  }, [query]);
  return isMatch;
}
const runTabs = new Set<RunTab>(['overview', 'steps', 'timeline', 'stream', 'design', 'session', 'actions']);

function isRunTab(value: string | null): value is RunTab {
  return value !== null && runTabs.has(value as RunTab);
}
const minimumSidebarWidth = 280;

type SidebarKind = 'graph' | 'timeline';

/** Timeline rows are short, so its event panel defaults wider; each tab remembers its own width. */
const sidebarLayouts: Record<SidebarKind, { storageKey: string; defaultWidth: number; maximumWidth: number }> = {
  graph: { storageKey: 'dex.run.selected-event-width', defaultWidth: 340, maximumWidth: 720 },
  timeline: { storageKey: 'dex.run.selected-event-width.timeline', defaultWidth: 780, maximumWidth: 1040 },
};

function clampSidebarWidth(kind: SidebarKind, width: number): number {
  return Math.min(sidebarLayouts[kind].maximumWidth, Math.max(minimumSidebarWidth, width));
}

function storedSidebarWidth(kind: SidebarKind): number {
  const { storageKey, defaultWidth } = sidebarLayouts[kind];
  if (typeof window === 'undefined') return defaultWidth;
  try {
    const width = Number.parseInt(window.localStorage.getItem(storageKey) || '', 10);
    return Number.isFinite(width) ? clampSidebarWidth(kind, width) : defaultWidth;
  } catch (storageError) {
    console.warn('Unable to read the selected event width preference.', storageError);
    return defaultWidth;
  }
}

function persistSidebarWidth(kind: SidebarKind, width: number) {
  try {
    window.localStorage.setItem(sidebarLayouts[kind].storageKey, String(width));
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
  const [historyPageSize, setHistoryPageSize] = useState<HistoryPageSize>(storedHistoryPageSize);
  const runHistory = useRunHistory(flowId, runId, historyPageSize);
  const { appendNewest, clear: clearRunHistory, loadNewest } = runHistory;
  const [state, setState] = useState<FlowState | null>(null);
  const [savedHistorySettings, setSavedHistorySettings] = useState<SavedHistorySettings | null>(null);
  const requestedView = searchParams.get(RUN_VIEW_PARAM);
  const [tab, setTab] = useState<RunTab>(() => (isRunTab(requestedView) ? requestedView : storedRunTab()));
  const webCatalog = useOptionalWebCatalog();
  const designDefinition = designDefinitionFor(webCatalog?.definitions, summary?.flowType);
  const catalogEntry = catalogEntryFor(webCatalog?.catalog, webCatalog?.canUseV2 ?? false, summary?.flowType);
  const sessionSchema = sessionSchemaFor(webCatalog?.definitions, catalogEntry);
  const hasActionsTab = hasRunActions(catalogEntry);
  // A remembered or linked tab that does not apply to this run shows the Execution graph instead.
  const shownTab: RunTab = (tab === 'design' && !designDefinition)
    || (tab === 'session' && !sessionSchema)
    || (tab === 'actions' && !hasActionsTab)
    ? 'steps'
    : tab;
  const isHorizontalTimeline = useMediaQuery(horizontalTimelineQuery);
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
  const [sidebarWidths, setSidebarWidths] = useState<Record<SidebarKind, number>>(() => ({
    graph: storedSidebarWidth('graph'),
    timeline: storedSidebarWidth('timeline'),
  }));
  const [resizingSidebar, setResizingSidebar] = useState(false);
  const [selectedEventConnector, setSelectedEventConnector] = useState<SelectedEventConnector | null>(null);
  const waitGeneration = useRef(0);
  const startedStateLoad = useRef(0);
  const currentRunIdentity = useRef('');
  const publishedStateLoad = useRef(0);
  const blobCache = useRef(new Map<string, unknown>());
  const hydratingEventIDs = useRef(new Set<number>());
  const hydratedEventIDs = useRef(new Set<number>());
  const sidebarKind: SidebarKind = shownTab === 'timeline' ? 'timeline' : 'graph';
  const sidebarWidth = sidebarWidths[sidebarKind];
  const sidebarKindRef = useRef(sidebarKind);
  const sidebarWidthRef = useRef(sidebarWidth);
  const sidebarResizeStart = useRef({ pointerX: 0, width: sidebarWidth });
  sidebarKindRef.current = sidebarKind;
  sidebarWidthRef.current = sidebarWidth;
  const runContent = useRef<HTMLDivElement>(null);
  const followedRunAttempt = useRef(0);

  const summaryURL = `/api/flows/summary?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(runId)}`;
  const stateURL = `/api/flows/state?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(runId)}`;
  const addDataWarning = useCallback((warning: string) => {
    setDataWarnings((current) => current.includes(warning) ? current : [...current, warning]);
  }, []);
  const updateSidebarWidth = useCallback((width: number) => {
    const kind = sidebarKindRef.current;
    const nextWidth = clampSidebarWidth(kind, width);
    sidebarWidthRef.current = nextWidth;
    setSidebarWidths((current) => ({ ...current, [kind]: nextWidth }));
    return nextWidth;
  }, []);
  const saveSidebarWidth = useCallback((width: number) => {
    persistSidebarWidth(sidebarKindRef.current, width);
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
    else if (event.key === 'End') nextWidth = sidebarLayouts[sidebarKindRef.current].maximumWidth;
    else return;
    event.preventDefault();
    saveSidebarWidth(updateSidebarWidth(nextWidth));
  }, [saveSidebarWidth, updateSidebarWidth]);

  useEffect(() => {
    if (!resizingSidebar) return;
    const resize = (event: PointerEvent) => {
      const start = sidebarResizeStart.current;
      updateSidebarWidth(start.width + start.pointerX - event.clientX);
    };
    const finish = () => {
      saveSidebarWidth(sidebarWidthRef.current);
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
  }, [resizingSidebar, saveSidebarWidth, updateSidebarWidth]);

  const loadState = useCallback(async (statusCode: number) => {
    const stateLoad = ++startedStateLoad.current;
    // Publishes only when no later-started load has published, so older responses never win.
    const publishState = (value: FlowState | null) => {
      if (stateLoad < publishedStateLoad.current) return false;
      publishedStateLoad.current = stateLoad;
      setState(value);
      setSavedHistorySettings((saved) => saved && saved.savedAfterStateLoad < stateLoad ? null : saved);
      return true;
    };
    if (terminalStatuses.has(statusCode)) {
      publishState(null);
      return;
    }
    try {
      const rawState = await readResponseJSON<FlowState>(await fetch(stateURL, { cache: 'no-store' }));
      if (!publishState(rawState)) return;
      const hydrated = await hydrateBlobs(flowId, rawState, blobCache.current);
      if (!publishState(hydrated.value)) return;
      if (hydrated.error) addDataWarning(hydrated.error);
    } catch (stateError) {
      setError(stateError instanceof Error ? stateError.message : 'State query failed');
    }
  }, [addDataWarning, flowId, stateURL]);

  const showSavedHistorySettings = useCallback(async (
    values: HistorySettingsChange,
    savedRun: FlowSummary,
  ) => {
    const runIdentity = runIdentityOf(savedRun.flowId, savedRun.runId, savedRun.runAttempt);
    // A save that finishes after the page moved to another run or attempt must not touch it.
    if (runIdentity !== currentRunIdentity.current) return;
    setSavedHistorySettings((saved) => ({
      runIdentity,
      values: { ...(saved?.runIdentity === runIdentity ? saved.values : {}), ...values },
      savedAfterStateLoad: startedStateLoad.current,
    }));
    await loadState(savedRun.flowStatusCode);
  }, [loadState]);

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
    setSavedHistorySettings(null);
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

  const runIdentity = runIdentityOf(flowId, runId, summary?.runAttempt);
  useLayoutEffect(() => {
    currentRunIdentity.current = runIdentity;
  }, [runIdentity]);
  const displayedHistorySettings = savedHistorySettings?.runIdentity === runIdentity
    ? savedHistorySettings.values
    : undefined;

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

  // Experimental tabs follow Streams, and each appears only when it applies to this run.
  const tabs: Array<{ id: RunTab; label: string; Icon: LucideIcon; isExperimental?: boolean }> = [
    { id: 'overview', label: 'Overview', Icon: LayoutDashboard },
    { id: 'steps', label: 'Execution graph', Icon: Workflow },
    { id: 'timeline', label: 'Timeline', Icon: Rows3 },
    { id: 'stream', label: 'Streams', Icon: Waves },
    ...(designDefinition ? [{ id: 'design' as const, label: 'Design', Icon: PencilRuler, isExperimental: true }] : []),
    ...(sessionSchema ? [{ id: 'session' as const, label: 'Agent session', Icon: Bot, isExperimental: true }] : []),
    ...(hasActionsTab ? [{ id: 'actions' as const, label: 'Actions', Icon: MousePointerClick, isExperimental: true }] : []),
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
  // The Execution graph and the Timeline share one event page.
  const isSelectionOnTimeline = shownTab === 'timeline' || shownTab === 'steps';
  const pageEventsWithFlowStart = useMemo(
    () => withFlowStart(displayedTimelineEvents, flowStartEvent),
    [displayedTimelineEvents, flowStartEvent],
  );
  const pageHasEarlierEvents = (timelineWindow?.hasOlder ?? false) || hasGapAfterFlowStart(timelineEvents);
  const pagePosition = useMemo(() => (
    runHistory.hasContiguousEventIds && timelineWindow && runHistory.newestTimelinePage
      ? timelinePagePositionOf(timelineWindow, runHistory.newestTimelinePage, prunedEventIdRange, historyPageSize)
      : null
  ), [historyPageSize, prunedEventIdRange, runHistory.hasContiguousEventIds, runHistory.newestTimelinePage, timelineWindow]);
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

  const selectTimelineEvent = useCallback((event: FlowHistoryEvent) => {
    highlightEvent(null);
    setSelectedEvent(event);
  }, [highlightEvent]);

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
    navigateTimeline(async () => {
      const result = await backInTimeline();
      if (result.kind === 'restored') setSelectedEvent(result.selectedEvent);
    });
  }, [backInTimeline, navigateTimeline]);

  const changeHistoryPageSize = useCallback((size: HistoryPageSize) => {
    persistHistoryPageSize(size);
    setHistoryPageSize(size);
  }, []);

  // A new page size reloads the Latest page; ranges from the old size no longer apply.
  const loadedHistoryPageSize = useRef(historyPageSize);
  useEffect(() => {
    if (loadedHistoryPageSize.current === historyPageSize) return;
    loadedHistoryPageSize.current = historyPageSize;
    if (!summary) return;
    clearRunHistory();
    setSelectedEvent(null);
    setError('');
    void loadHistory(historyRunAttempt(summary)).catch((loadError: unknown) => {
      setError(loadError instanceof Error ? loadError.message : 'History page failed to load');
    });
  }, [clearRunHistory, historyPageSize, historyRunAttempt, loadHistory, summary]);

  const pageFirstEventId = timelineEvents[0]?.eventId;
  const pageLastEventId = timelineEvents.at(-1)?.eventId;
  const eventsPaging: RunEventsPaging | null = pageFirstEventId === undefined || pageLastEventId === undefined
    ? null
    : {
      firstEventId: pageFirstEventId,
      lastEventId: pageLastEventId,
      pageNumber: runHistory.isTimelineOnNewest ? 1 : pagePosition?.pageNumber ?? null,
      pageCount: pagePosition?.pageCount ?? null,
      hasOlder: timelineWindow?.hasOlder ?? false,
      hasNewer: runHistory.timelineHasNewer,
      isNewest: runHistory.isTimelineOnNewest,
      isLoading: runHistory.isLoadingTimelinePage,
      canGoBack: runHistory.canGoBack,
      onBack: goBackInTimeline,
      onLatestPage: runHistory.showNewest,
      onNewerPage: () => navigateTimeline(runHistory.showNewer),
      onOlderPage: () => navigateTimeline(runHistory.showOlder),
      onOldestPage: () => navigateTimeline(() => runHistory.showOldest(
        pagePosition && pagePosition.pageCount > 1 ? pagePosition.oldestPageLastEventId + 1 : undefined,
      )),
    };

  useEffect(() => {
    if (selectedRaw) void hydrateEvent(selectedRaw);
  }, [hydrateEvent, selectedRaw?.eventId]);

  useEffect(() => {
    if (shownTab === 'overview' && flowStartEvent) void hydrateEvent(flowStartEvent);
  }, [flowStartEvent, hydrateEvent, shownTab]);

  useLayoutEffect(() => {
    const content = runContent.current;
    const selectedEventID = selectedEvent?.eventId;
    if (!content || shownTab !== 'timeline' || !selectedEventID) {
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
  }, [selectedEvent, sidebarWidth, shownTab, timelineEvents]);

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
    <div className="run-page v1-page">
      <header className="run-header">
        <h1 className="sr-only">Flow {flowId}, run {runId}</h1>
        <div className="run-header-bar">
          <div className="run-identity">
            {breadcrumb}
            <div className="run-identity-copy">
              <CopyButton label="Copy Flow ID" value={flowId}>Flow ID</CopyButton>
              <CopyButton label="Copy run ID" value={runId}>Run ID</CopyButton>
            </div>
          </div>
          <div className="run-actions">
            {continuedToRunId && (
              <Link className="button primary" to={runPath(continuedToRunId)}>
                Next run
              </Link>
            )}
            <label
              className={`live-toggle${autoRefresh && summary && !terminalStatuses.has(summary.flowStatusCode) ? ' is-on' : ''}`}
            >
              <input
                type="checkbox"
                checked={autoRefresh}
                onChange={(event) => setAutoRefresh(event.target.checked)}
              />
              <span aria-hidden="true" className="live-toggle-dot" />
              Live
            </label>
            <CopyButton
              className="button quiet"
              icon={LinkIcon}
              label="Copy link"
              value={() => window.location.href}
            >
              Copy link
            </CopyButton>
            <button className="button quiet" onClick={() => void refresh()} type="button">
              <RefreshCw aria-hidden="true" size={15} strokeWidth={2} />
              Refresh
            </button>
            <button
              className="button quiet danger"
              disabled={!summary || terminalStatuses.has(summary.flowStatusCode)}
              onClick={() => setStopOpen(true)}
              type="button"
            >
              <Square aria-hidden="true" size={14} strokeWidth={2.25} />
              Stop
            </button>
            <button
              className="button quiet danger"
              onClick={() => openTimeTravel(selectedEvent ? selected : null)}
              type="button"
            >
              <History aria-hidden="true" size={15} strokeWidth={2} />
              Time Travel
            </button>
          </div>
        </div>
        {summary && (
          <div className="run-summary">
            <div className="run-summary-status">
              <span className="sr-only">Status</span>
              <StatusBadge status={summary.flowStatus} size="large" />
            </div>
            <dl className="run-facts">
              <div>
                <dt className="run-fact-label"><CalendarClock aria-hidden="true" size={14} strokeWidth={2} />Started</dt>
                <dd>{formatDate(summary.startTime, timezone)}</dd>
              </div>
              {summary.closeTime && (
                <div>
                  <dt className="run-fact-label"><Flag aria-hidden="true" size={14} strokeWidth={2} />Closed</dt>
                  <dd>{formatDate(summary.closeTime, timezone)}</dd>
                </div>
              )}
              <div>
                <dt className="run-fact-label"><Timer aria-hidden="true" size={14} strokeWidth={2} />Duration</dt>
                <dd>{formatDuration(summary.startTime, summary.closeTime)}</dd>
              </div>
              {currentRunAttempt > 0 ? (
                <div>
                  <dt className="run-fact-label"><Layers aria-hidden="true" size={14} strokeWidth={2} />Run attempt</dt>
                  <dd>
                    <RunAttemptSelect
                      currentRunAttempt={currentRunAttempt}
                      displayedRunAttempt={displayedRunAttempt}
                      onSelect={(runAttempt) => showRunAttempt(runAttempt === currentRunAttempt ? 0 : runAttempt)}
                    />
                  </dd>
                </div>
              ) : (
                <div>
                  <dt className="run-fact-label"><Layers aria-hidden="true" size={14} strokeWidth={2} />Run chain</dt>
                  <dd>{runChain.length}</dd>
                </div>
              )}
            </dl>
            <RunEventsTile
              latestEventId={latestEvent?.eventId ?? null}
              paging={eventsPaging}
              pageSize={historyPageSize}
              onPageSizeChange={changeHistoryPageSize}
            />
          </div>
        )}
      </header>

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

      <div aria-label="Run views" className="run-tabs" role="group">
        {tabs.map(({ id, label, Icon, isExperimental }) => (
          <button
            key={id}
            aria-pressed={shownTab === id}
            className={shownTab === id ? 'active' : ''}
            data-tab={id}
            onClick={() => {
              setTab(id);
              persistRunTab(id);
            }}
            type="button"
          >
            <Icon aria-hidden="true" size={15} strokeWidth={2} />
            {label}
            {isExperimental && <ExperimentalBadge className="run-tab-badge" />}
          </button>
        ))}
      </div>

      <div
        className={[
          'run-content',
          shownTab === 'overview' ? 'run-content--overview' : '',
          shownTab === 'design' ? 'run-content--design' : '',
          shownTab === 'session' || shownTab === 'actions' ? 'run-content--experimental' : '',
          resizingSidebar ? 'is-resizing-sidebar' : '',
          shownTab === 'timeline' ? 'run-content--timeline' : '',
          shownTab === 'timeline' && isHorizontalTimeline ? 'run-content--horizontal-timeline' : '',
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
          {isWaitingForHistory && (shownTab === 'steps' || shownTab === 'timeline') && (
            <LoadingIndicator className="card page-loading" label="Loading events…" />
          )}
          {shownTab === 'overview' && summary && (
            <FlowOverview
              summary={summary}
              events={displayedHistory}
              state={state}
              deletingChannelMessage={deletingChannelMessage}
              onDeleteChannelMessage={deleteChannelMessage}
              onOpenStateWrite={setStateWriteKind}
              savedHistorySettings={displayedHistorySettings}
              onHistorySettingsSaved={showSavedHistorySettings}
            />
          )}
          {shownTab === 'steps' && !isWaitingForHistory && (
            <ExecutionGraph
              flowId={flowId}
              events={pageEventsWithFlowStart}
              hasEarlierEvents={pageHasEarlierEvents}
              state={state}
              selectedEvent={selectedRaw}
              onSelectEvent={setSelectedEvent}
            />
          )}
          {shownTab === 'timeline' && !isWaitingForHistory && isHorizontalTimeline && (
            <HorizontalTimeline
              events={displayedTimelineEvents}
              selectedEvent={selectedRaw}
              isFollowingLatest={selectedEvent === null}
              onSelectEvent={selectTimelineEvent}
              relations={selectedRaw ? selectedRelations : undefined}
              paging={{
                hasOlder: timelineWindow?.hasOlder ?? false,
                hasNewer: runHistory.timelineHasNewer,
                isLoading: runHistory.isLoadingTimelinePage,
              }}
              prunedEventIdRange={prunedEventIdRange}
              highlightedEventId={runHistory.highlightedEventId}
            />
          )}
          {shownTab === 'timeline' && !isWaitingForHistory && !isHorizontalTimeline && (
            <Timeline
              flowId={flowId}
              events={displayedTimelineEvents}
              selectedEvent={selectedRaw}
              onSelectEvent={selectTimelineEvent}
              relations={selectedRaw ? selectedRelations : undefined}
              paging={{
                hasOlder: timelineWindow?.hasOlder ?? false,
                hasNewer: runHistory.timelineHasNewer,
                isLoading: runHistory.isLoadingTimelinePage,
              }}
              prunedEventIdRange={prunedEventIdRange}
              highlightedEventId={runHistory.highlightedEventId}
              onJumpToEvent={(eventId) => void jumpToEvent(eventId)}
              runStepDurability={runStepDurabilityOf(flowStartEvent)}
            />
          )}
          {shownTab === 'design' && designDefinition && (
            <FlowDesignGraph key={designDefinition.id} definition={designDefinition} />
          )}
          {shownTab === 'stream' && summary && (
            <StreamPanel flowId={flowId} flowType={summary.flowType} />
          )}
          {shownTab === 'session' && sessionSchema && catalogEntry && (
            <AgentSessionTab entry={catalogEntry} flowId={flowId} reloadKey={latestEvent?.eventId ?? 0} schema={sessionSchema} />
          )}
          {shownTab === 'actions' && catalogEntry && summary && (
            <RunActionsTab
              entry={catalogEntry}
              flowId={flowId}
              flowStatusCode={summary.flowStatusCode}
              reloadKey={latestEvent?.eventId ?? 0}
            />
          )}
        </section>
        {(shownTab === 'steps' || shownTab === 'timeline') && (
          <aside className="run-sidebar">
            <button
              type="button"
              className="run-sidebar-resizer"
              role="separator"
              aria-label="Resize selected event panel"
              aria-orientation="vertical"
              aria-valuemin={minimumSidebarWidth}
              aria-valuemax={sidebarLayouts[sidebarKind].maximumWidth}
              aria-valuenow={sidebarWidth}
              onDoubleClick={() => saveSidebarWidth(updateSidebarWidth(sidebarLayouts[sidebarKind].defaultWidth))}
              onKeyDown={resizeSidebarWithKeyboard}
              onPointerDown={startSidebarResize}
            />
            <FlowStatePanel
              selectedEvent={selected}
              history={withFlowStart(selectionEvents, flowStartEvent)}
              parentFlowId={flowId}
              runStartTime={summary?.startTime}
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

/** Reads the run's configured Step durability from its start event, when loaded. */
function runStepDurabilityOf(flowStartEvent: FlowHistoryEvent | null | undefined): unknown {
  const flowConfig = flowStartEvent?.payload.flowConfig;
  return flowConfig && typeof flowConfig === 'object' ? (flowConfig as Record<string, unknown>).stepDurability : undefined;
}

/** Whether event IDs are missing between FlowStart and the next event of a page. */
function hasGapAfterFlowStart(events: FlowHistoryEvent[]): boolean {
  return events[0]?.eventId === 1 && events.length > 1 && events[1].eventId > 2;
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
