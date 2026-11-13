// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link, useNavigate, useSearchParams } from 'react-router-dom';
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
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
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import {
  CornerDownRight,
  CornerLeftUp,
  LayoutDashboard,
  Rows3,
  PencilRuler,
  Bot,
  MousePointerClick,
  Waves,
  Workflow,
  type LucideIcon,
} from 'lucide-react';
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
import { EventList } from './details/EventList';
import { OperateMenu } from './details/OperateMenu';
import { middleTruncate, RunFactsMenu, RunRelations, type RunFact } from './details/RunFactsMenu';
import { childFlowsQuery, useRunRelations } from './details/useRunRelations';
import { AppBarSlot } from '../components/frame/AppBarSlots';
import { rememberRecentRun } from '../components/frame/recentRuns';
import { RunFailureBody, runFailureOf, RunLineJump, RunStateBody, runStateOf } from './details/RunFailureLine';
import { StreamPanel } from './details/StreamPanel';
import { Timeline } from './details/Timeline';
import { HorizontalTimeline } from './details/HorizontalTimeline';
import { useRunHistory } from './useRunHistory';
import { AgentSessionTab, RunActionsTab } from './details/ExperimentalRunTabs';
import { FlowDesignGraph } from '../design/FlowDesignGraph';
import { DesignCanvasErrorBoundary } from '../design/DesignCanvasErrorBoundary';
import { FlowDefinitionDiagnostics } from '../rendering/FlowDefinitionDiagnostics';
import { useFlowTypeDefinitions, useOptionalWebCatalog } from '../catalog/WebCatalogProvider';
import { catalogEntryFor, designDefinitionFor, hasRunActions, sessionSchemaFor } from '../catalog/applicability';
import { useWorkingAs } from '../workspace/useWorkingAs';
import { FLOWS_PATH, RUN_VIEW_PARAM, runPath as currentRunPath } from '../routes';
import { webURL } from '@/lib/basePath';

type RunTab = 'overview' | 'steps' | 'timeline' | 'stream' | 'design' | 'session' | 'actions';

/** Whether the run on screen is the one the operator API acts on, which takes only a Flow ID. */
type DisplayedRunPosition = 'current' | 'earlier' | 'unknown';

/** The current run of a Flow, as resolved by a summary request without a run ID. */
interface CurrentRunOfFlow {
  flowId: string;
  runId: string;
}

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
const noOperatorDefinitions: FlowV2Definition[] = [];
const runTabStorageKey = 'dex.run.selected-tab';
/** The horizontal Timeline needs width; narrow screens keep the vertical one. */
const horizontalTimelineQuery = '(min-width: 761px)';
/** On desktop the strip gains the event list under it, so events can be scanned as rows. */
const timelineEventListQuery = '(min-width: 1280px)';
const RUN_EVENT_PARAM = 'event';

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
  graph: { storageKey: 'dex.run.selected-event-width', defaultWidth: 600, maximumWidth: 720 },
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
  if (typeof window === 'undefined') return 'timeline';
  try {
    const tab = window.localStorage.getItem(runTabStorageKey);
    return tab && runTabs.has(tab as RunTab) ? tab as RunTab : 'timeline';
  } catch (storageError) {
    console.warn('Unable to read the selected run tab preference.', storageError);
    return 'timeline';
  }
}

const timelineLaneStorageKey = 'dex.run.timeline-lane';

/** The desktop event list makes the labelled lane optional; it starts hidden. */
function storedTimelineLaneShown(): boolean {
  try {
    return window.localStorage.getItem(timelineLaneStorageKey) === 'shown';
  } catch {
    return false;
  }
}

function persistTimelineLaneShown(isShown: boolean) {
  try {
    window.localStorage.setItem(timelineLaneStorageKey, isShown ? 'shown' : 'hidden');
  } catch (storageError) {
    console.warn('Unable to save the Timeline lane preference.', storageError);
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
  runPath,
}: {
  flowId: string;
  runId: string;
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
  // A run page reads only its own Flow type's definitions.
  const runDefinitions = useFlowTypeDefinitions(summary?.flowType);
  const isCatalogReady = webCatalog === null || (webCatalog.ready && runDefinitions?.status !== 'loading');
  const designDefinition = designDefinitionFor(runDefinitions?.value, summary?.flowType);
  const catalogEntry = catalogEntryFor(runDefinitions?.value, webCatalog?.canUseV2 ?? false);
  const workingAsDefinitions = useMemo(
    () => (catalogEntry ? [catalogEntry.definition] : noOperatorDefinitions),
    [catalogEntry],
  );
  const workingAs = useWorkingAs(workingAsDefinitions);
  const relations = useRunRelations(flowId);
  useEffect(() => {
    if (summary) rememberRecentRun({ flowId: summary.flowId, runId: summary.runId, flowType: summary.flowType, flowStatus: summary.flowStatus });
  }, [summary]);
  const now = useSecondClock(summary !== null && !terminalStatuses.has(summary.flowStatusCode));
  const [currentRunOfFlow, setCurrentRunOfFlow] = useState<CurrentRunOfFlow | null>(null);
  const history = runHistory.newest?.events ?? noHistoryEvents;
  const isViewingEarlierRunAttempt = summary !== null
    && requestedRunAttempt > 0
    && requestedRunAttempt < positiveRunAttempt(summary.runAttempt);
  const displayedRunPosition = displayedRunPositionOf(
    summary,
    isViewingEarlierRunAttempt,
    currentRunOfFlow,
    flowId,
    runId,
  );
  const runSessionSchema = sessionSchemaFor(catalogEntry);
  const runHasActions = hasRunActions(catalogEntry);
  // Agent session and Actions read and write the current run, so an earlier run does not offer them.
  const sessionSchema = displayedRunPosition === 'current' ? runSessionSchema : null;
  const hasActionsTab = displayedRunPosition === 'current' && runHasActions;
  const isCurrentRunOnlyTabRequested = displayedRunPosition === 'earlier'
    && ((tab === 'session' && runSessionSchema !== null) || (tab === 'actions' && runHasActions));
  // A remembered or linked tab that does not apply to this run shows the Timeline instead.
  const shownTab: RunTab = (tab === 'design' && !designDefinition)
    || (tab === 'session' && !sessionSchema)
    || (tab === 'actions' && !hasActionsTab)
    ? 'timeline'
    : tab;
  // Design draws only the definition: history, state and blobs wait until the reader leaves it.
  const isHoldingRunData = tab === 'design' && (designDefinition !== null || summary === null || !isCatalogReady);
  const isHorizontalTimeline = useMediaQuery(horizontalTimelineQuery);
  const hasTimelineEventList = useMediaQuery(timelineEventListQuery);
  const [isTimelineLaneShown, setIsTimelineLaneShown] = useState(storedTimelineLaneShown);
  const toggleTimelineLane = useCallback(() => {
    setIsTimelineLaneShown((isShown) => {
      persistTimelineLaneShown(!isShown);
      return !isShown;
    });
  }, []);
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
  const [runDataRequest, setRunDataRequest] = useState(0);
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
  const loadedRunDataRequest = useRef(0);

  const summaryURL = webURL(`/api/flows/summary?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(runId)}`);
  const stateURL = webURL(`/api/flows/state?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(runId)}`);
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
      await readResponseJSON(await fetch(webURL('/api/flows/channels/messages'), {
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

  /** Loads the summary, then asks for history and state, which load once the shown tab needs them. */
  const refresh = useCallback(async () => {
    setPendingRunRequestCount((current) => current + 1);
    setError('');
    try {
      const latestSummary = await loadSummary();
      const latestRunAttempt = positiveRunAttempt(latestSummary.runAttempt);
      // Another client may have time traveled while Live was off.
      if (latestRunAttempt !== followedRunAttempt.current) clearLoadedRun();
      followedRunAttempt.current = latestRunAttempt;
      setRunDataRequest((current) => current + 1);
    } catch (refreshError) {
      setError(refreshError instanceof Error ? refreshError.message : 'Run refresh failed');
    } finally {
      setPendingRunRequestCount((current) => current - 1);
    }
  }, [clearLoadedRun, loadSummary]);

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
  }, [clearLoadedRun, refresh, requestedRunAttempt]);

  // A layout effect, so Loading events shows before an empty graph can paint.
  useLayoutEffect(() => {
    if (isHoldingRunData || !summary || loadedRunDataRequest.current === runDataRequest) return;
    loadedRunDataRequest.current = runDataRequest;
    void Promise.all([
      loadHistory(historyRunAttempt(summary)),
      loadState(summary.flowStatusCode),
    ]).catch((loadError: unknown) => {
      setError(loadError instanceof Error ? loadError.message : 'Run refresh failed');
    });
  }, [historyRunAttempt, isHoldingRunData, loadHistory, loadState, runDataRequest, summary]);

  // The operator API acts on the current run, so learn which run that is before offering it.
  const summaryStatusCode = summary?.flowStatusCode;
  const needsCurrentRun = catalogEntry !== null;
  useEffect(() => {
    if (!needsCurrentRun || summaryStatusCode === undefined) return undefined;
    const controller = new AbortController();
    void fetch(webURL(`/api/flows/summary?flowId=${encodeURIComponent(flowId)}`), { cache: 'no-store', signal: controller.signal })
      .then((response) => readResponseJSON<FlowSummary>(response))
      .then((current) => setCurrentRunOfFlow({ flowId, runId: current.runId }))
      .catch((lookupError: unknown) => {
        if (controller.signal.aborted) return;
        // Agent session and Actions stay hidden; nothing else on the page needs the current run.
        console.warn('Unable to resolve the current run of the Flow.', lookupError);
      });
    return () => controller.abort();
  }, [flowId, needsCurrentRun, runId, summaryStatusCode]);

  const runIdentity = runIdentityOf(flowId, runId, summary?.runAttempt);
  useLayoutEffect(() => {
    currentRunIdentity.current = runIdentity;
  }, [runIdentity]);
  const displayedHistorySettings = savedHistorySettings?.runIdentity === runIdentity
    ? savedHistorySettings.values
    : undefined;

  const nextInternalEventId = runHistory.newest?.nextInternalEventId ?? 0;

  useEffect(() => {
    if (
      !autoRefresh
      || isHoldingRunData
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
    void fetch(webURL(`/api/flows/wait?${params}`), { cache: 'no-store', signal: controller.signal })
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
    isHoldingRunData,
    isViewingEarlierRunAttempt,
    loadHistory,
    loadState,
    loadSummary,
    nextInternalEventId,
    runId,
    summary,
    waitCycle,
  ]);

  // Timeline leads; experimental tabs follow Streams, and each appears only when it applies to this run.
  const tabs: Array<{ id: RunTab; label: string; Icon: LucideIcon; isExperimental?: boolean }> = [
    { id: 'timeline', label: 'Timeline', Icon: Rows3 },
    { id: 'steps', label: 'Execution graph', Icon: Workflow },
    { id: 'overview', label: 'Overview', Icon: LayoutDashboard },
    { id: 'stream', label: 'Streams', Icon: Waves },
    ...(sessionSchema ? [{ id: 'session' as const, label: 'Agent session', Icon: Bot, isExperimental: true }] : []),
    ...(hasActionsTab ? [{ id: 'actions' as const, label: 'Actions', Icon: MousePointerClick, isExperimental: true }] : []),
    ...(designDefinition ? [{ id: 'design' as const, label: 'Design', Icon: PencilRuler, isExperimental: true }] : []),
  ];
  const timelineWindow = runHistory.timelineWindow;
  const timelineEvents = timelineWindow?.events ?? noHistoryEvents;
  const flowStartEvent = runHistory.flowStartEvent;
  const prunedEventIdRange = runHistory.prunedEventIdRange;
  const latestEvent = history.at(-1);
  const runFailure = summary && terminalStatuses.has(summary.flowStatusCode) ? runFailureOf(history) : null;
  const failedEvent = runFailure ? history.find((event) => event.eventId === runFailure.eventId) ?? null : null;
  // A failed run opens on the event that failed, the one a reader came for.
  const selectedRaw = selectedEvent ?? failedEvent ?? latestEvent ?? null;
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

  /** Shows a tab and keeps it in the URL, so reloading or sharing the page opens the same tab. */
  const selectTab = useCallback((nextTab: RunTab) => {
    setTab(nextTab);
    persistRunTab(nextTab);
    setSearchParams((current) => {
      const next = new URLSearchParams(current);
      next.set(RUN_VIEW_PARAM, nextTab);
      return next;
    }, { replace: true });
  }, [setSearchParams]);

  useEffect(() => {
    if (isRunTab(requestedView)) setTab(requestedView);
  }, [requestedView]);

  const jumpToEvent = useCallback(async (eventId: number) => {
    setError('');
    selectTab('timeline');
    try {
      const result = await jumpInTimeline(eventId, selectedEvent);
      // A pruned target needs no error: the refreshed pruned range turns its stub into a pruned stub.
      if (result.kind === 'loaded') setSelectedEvent(result.event);
      else if (result.kind === 'missing') setError(`Event #${eventId} is not in the retained history.`);
    } catch (jumpError) {
      setError(jumpError instanceof Error ? jumpError.message : 'History page failed to load');
    }
  }, [jumpInTimeline, selectTab, selectedEvent]);

  // The selected event lives in the URL, so a link or a reload opens the same event.
  const requestedEventId = Number(searchParams.get(RUN_EVENT_PARAM)) || 0;
  const hasOpenedRequestedEvent = useRef(false);
  useEffect(() => {
    if (hasOpenedRequestedEvent.current || requestedEventId <= 0 || history.length === 0) return;
    hasOpenedRequestedEvent.current = true;
    if (shownTab === 'timeline' || shownTab === 'steps') void jumpToEvent(requestedEventId);
  }, [history.length, jumpToEvent, requestedEventId, shownTab]);
  useEffect(() => {
    if (!selectedEvent || (!hasOpenedRequestedEvent.current && requestedEventId > 0)) return;
    hasOpenedRequestedEvent.current = true;
    setSearchParams((current) => {
      if (current.get(RUN_EVENT_PARAM) === String(selectedEvent.eventId)) return current;
      const next = new URLSearchParams(current);
      next.set(RUN_EVENT_PARAM, String(selectedEvent.eventId));
      return next;
    }, { replace: true });
  }, [requestedEventId, selectedEvent, setSearchParams]);

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
    setRunDataRequest((current) => current + 1);
  }, [clearRunHistory, historyPageSize, summary]);

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
    if (selectedRaw && !isHoldingRunData) void hydrateEvent(selectedRaw);
  }, [hydrateEvent, isHoldingRunData, selectedRaw?.eventId]);

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

  /** Resuming Live reads the run again first, since another client may have changed it meanwhile. */
  const resumeOrPauseLive = useCallback((isOn: boolean) => {
    setAutoRefresh(isOn);
    if (isOn) void refresh();
  }, [refresh]);

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

  const isRunOpen = summary !== null && !terminalStatuses.has(summary.flowStatusCode);
  const childFlowsHref = `${FLOWS_PATH}?${new URLSearchParams({ q: childFlowsQuery(flowId) })}`;
  const runState = summary && !runFailure && !isViewingEarlierRunAttempt ? runStateOf(history, isRunOpen) : null;
  const isRunStateShown = runState !== null && (shownTab === 'timeline' || shownTab === 'steps' || shownTab === 'overview');
  const isLive = autoRefresh && summary !== null && !terminalStatuses.has(summary.flowStatusCode);
  const runFacts: RunFact[] = summary ? [
    { label: 'Started', value: formatDate(summary.startTime, timezone), icon: 'started' },
    ...(summary.closeTime ? [{ label: 'Closed', value: formatDate(summary.closeTime, timezone), icon: 'closed' as const }] : []),
    { label: 'Duration', value: formatDuration(summary.startTime, summary.closeTime), icon: 'duration' },
    // Past attempt 1 the popover's attempt picker says it, so the fact would repeat it.
    ...(currentRunAttempt > 1 ? [] : [currentRunAttempt > 0
      ? { label: 'Run attempt', value: `${displayedRunAttempt} of ${currentRunAttempt}`, icon: 'attempt' as const }
      : { label: 'Run chain', value: String(runChain.length), icon: 'attempt' as const }]),
    { label: 'Latest event', value: latestEvent ? `#${latestEvent.eventId}` : 'None yet', icon: 'events' },
    { label: 'Flow type', value: summary.flowType, icon: 'type' },
  ] : [];

  // The run's relations and attempt sit in the line under the bar, so the bar is the same on every run.
  const hasRunContext = Boolean(relations?.parentFlowId) || (relations?.children.length ?? 0) > 0
    || (summary !== null && currentRunAttempt > 1);
  const runContext = hasRunContext ? (
    <span className="run-context">
      {relations?.parentFlowId && (
        <Link className="run-context-item" title={`Parent Flow ${relations.parentFlowId}`} to={currentRunPath(relations.parentFlowId)}>
          <CornerLeftUp aria-hidden="true" size={13} strokeWidth={2.25} />
          Child of <span className="mono">{middleTruncate(relations.parentFlowId, 32)}</span>
        </Link>
      )}
      {relations && relations.children.length > 0 && (
        <Link
          className="run-context-item"
          title="Child Flows of this run"
          to={relations.children.length === 1 && !relations.hasMoreChildren
            ? currentRunPath(relations.children[0].flowId, relations.children[0].runId)
            : childFlowsHref}
        >
          <CornerDownRight aria-hidden="true" size={13} strokeWidth={2.25} />
          {relations.children.length === 1 && !relations.hasMoreChildren
            ? `1 child · ${relations.children[0].flowStatus}`
            : `${relations.children.length}${relations.hasMoreChildren ? '+' : ''} children`}
        </Link>
      )}
      {summary && currentRunAttempt > 1 && (
        <RunAttemptSelect
          currentRunAttempt={currentRunAttempt}
          displayedRunAttempt={displayedRunAttempt}
          onSelect={(runAttempt) => showRunAttempt(runAttempt === currentRunAttempt ? 0 : runAttempt)}
        />
      )}
    </span>
  ) : null;

  if (loading && !summary) return <LoadingIndicator label="Loading flow run…" />;
  const isWaitingForHistory = history.length === 0 && (loading || pendingRunRequestCount > 0);

  return (
    <div className="run-page v1-page" data-status={statusTone(summary?.flowStatus)}>
      <h1 className="sr-only">Flow {flowId}, run {runId}</h1>
      <AppBarSlot name="page">
        <span aria-hidden="true" className="app-bar-separator">/</span>
        {summary && <StatusBadge status={summary.flowStatus} />}
        {summary && (
          <span className="app-bar-duration" data-tick={now} title={`Started ${formatDate(summary.startTime, timezone)}`}>
            {formatDuration(summary.startTime, summary.closeTime)}
          </span>
        )}
        <RunFactsMenu
          childFlows={relations?.children}
          childFlowsHref={childFlowsHref}
          facts={runFacts}
          flowId={flowId}
          hasMoreChildFlows={relations?.hasMoreChildren}
          runId={runId}
          attemptControl={summary && currentRunAttempt > 1 ? (
            <RunAttemptSelect
              currentRunAttempt={currentRunAttempt}
              displayedRunAttempt={displayedRunAttempt}
              label="Run attempt in run details"
              onSelect={(runAttempt) => showRunAttempt(runAttempt === currentRunAttempt ? 0 : runAttempt)}
            />
          ) : undefined}
        />
      </AppBarSlot>
      <AppBarSlot name="views">
        <div aria-label="Run views" className="run-tabs" role="tablist">
          {tabs.map(({ id, label, Icon, isExperimental }) => (
            <button
              key={id}
              aria-selected={shownTab === id}
              className={shownTab === id ? 'active' : ''}
              data-tab={id}
              data-tooltip={isExperimental ? `${label} · early view` : label}
              onClick={() => selectTab(id)}
              role="tab"
              type="button"
            >
              <Icon aria-hidden="true" size={15} strokeWidth={2} />
              <span className="run-tab-label">{label}</span>
              {isExperimental && <span aria-label="Experimental" className="experimental-dot" role="img" />}
            </button>
          ))}
        </div>
      </AppBarSlot>
      <AppBarSlot name="actions">
        {summary && !terminalStatuses.has(summary.flowStatusCode) && (
          <label
            className={`live-toggle${isLive ? ' is-on' : ''}`}
            title={isLive ? 'Following new events. Click to stop following.' : 'Not following new events. Click to follow and read the run again.'}
          >
            <input
              aria-label="Live"
              checked={autoRefresh}
              onChange={(event) => resumeOrPauseLive(event.target.checked)}
              type="checkbox"
            />
            <span aria-hidden="true" className="live-toggle-dot" />
            {isLive ? 'Live' : 'Live off'}
          </label>
        )}
        <OperateMenu
          isRunning={isRunOpen}
          failedEventId={failedEvent?.eventId}
          onTimeTravelFromFailure={failedEvent ? () => openTimeTravel(failedEvent) : undefined}
          onStop={() => setStopOpen(true)}
          onTimeTravel={() => openTimeTravel(selectedEvent ? selected : null)}
        />
      </AppBarSlot>

      {/* One line with fixed slots, so the attempt picker in it never remounts as the run loads. */}
      {(runFailure || isRunStateShown || runContext) && (
        <div
          className={runFailure ? 'run-failure-line' : isRunStateShown ? `run-state-line${runState?.isOpen ? ' is-open' : ''}` : 'run-state-line run-context-line'}
          role={runFailure ? 'alert' : undefined}
        >
          {runFailure ? <RunFailureBody failure={runFailure} /> : isRunStateShown && runState ? <RunStateBody state={runState} /> : null}
          {runContext}
          {(runFailure || (isRunStateShown && runState)) && (
            <RunLineJump
              eventId={runFailure ? runFailure.eventId : runState?.eventId ?? 0}
              onJumpToEvent={(eventId) => void jumpToEvent(eventId)}
            />
          )}
        </div>
      )}
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

      {isCurrentRunOnlyTabRequested && (
        <p className="run-experimental-hint">
          Agent session and Actions act on the current run, so this earlier run does not offer them.{' '}
          <Link to={`${currentRunPath(flowId)}?${new URLSearchParams({ [RUN_VIEW_PARAM]: tab })}`}>
            Open the current run
          </Link>
        </p>
      )}

      <div
        className={[
          'run-content',
          shownTab === 'overview' ? 'run-content--overview' : '',
          shownTab === 'stream' ? 'run-content--stream' : '',
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
          {(shownTab === 'steps' || shownTab === 'timeline') && summary && (
            <RunEventsTile
              latestEventId={latestEvent?.eventId ?? null}
              paging={eventsPaging}
              pageSize={historyPageSize}
              onPageSizeChange={changeHistoryPageSize}
            >
              {shownTab === 'timeline' && isHorizontalTimeline && hasTimelineEventList && (
                <button
                  aria-pressed={isTimelineLaneShown}
                  className="pager-button run-events-lane-toggle"
                  onClick={() => toggleTimelineLane()}
                  type="button"
                >
                  {isTimelineLaneShown ? 'Hide lane' : 'Show lane'}
                </button>
              )}
            </RunEventsTile>
          )}
          {isWaitingForHistory && (shownTab === 'steps' || shownTab === 'timeline') && (
            <LoadingIndicator className="card page-loading" label="Loading events…" />
          )}
          {shownTab === 'overview' && summary && relations && (
            <RunRelations
              childFlows={relations.children}
              childFlowsHref={childFlowsHref}
              hasMoreChildFlows={relations.hasMoreChildren}
              parentFlowId={relations.parentFlowId}
            />
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
            <div className={`htl-host${hasTimelineEventList && !isTimelineLaneShown ? ' is-lane-hidden' : ''}`}>
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
            </div>
          )}
          {shownTab === 'timeline' && !isWaitingForHistory && isHorizontalTimeline && hasTimelineEventList && (
            <EventList
              childFlows={relations?.children}
              flowId={flowId}
              events={displayedTimelineEvents}
              onSelectEvent={selectTimelineEvent}
              onTimeTravelFromEvent={openTimeTravel}
              runStartTime={summary?.startTime}
              selectedEvent={selectedRaw}
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
            <DesignCanvasErrorBoundary key={designDefinition.id}>
              <FlowDesignGraph definition={designDefinition} />
              <FlowDefinitionDiagnostics definition={designDefinition} />
            </DesignCanvasErrorBoundary>
          )}
          {shownTab === 'stream' && summary && (
            <DesignCanvasErrorBoundary key={designDefinition?.id ?? 'stream'} title="Streams could not be shown">
              <StreamPanel definition={designDefinition} flowId={flowId} flowType={summary.flowType} />
            </DesignCanvasErrorBoundary>
          )}
          {shownTab === 'session' && sessionSchema && catalogEntry && (
            <AgentSessionTab
              entry={catalogEntry}
              flowId={flowId}
              reloadKey={latestEvent?.eventId ?? 0}
              schema={sessionSchema}
              workingAs={workingAs}
            />
          )}
          {shownTab === 'actions' && catalogEntry && summary && (
            <RunActionsTab
              entry={catalogEntry}
              flowId={flowId}
              flowStatusCode={summary.flowStatusCode}
              reloadKey={latestEvent?.eventId ?? 0}
              workingAs={workingAs}
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
              onTimeTravelFromEvent={openTimeTravel}
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

/**
 * Returns 'earlier' when the run on screen is not the Flow's current run: an
 * earlier run attempt, or a run other than the one its Flow ID resolves to. Returns 'current'
 * when the resolved current run is this run, and 'unknown' while that is still loading.
 */
function displayedRunPositionOf(
  summary: FlowSummary | null,
  isViewingEarlierRunAttempt: boolean,
  currentRunOfFlow: CurrentRunOfFlow | null,
  flowId: string,
  runId: string,
): DisplayedRunPosition {
  if (!summary) return 'unknown';
  if (isViewingEarlierRunAttempt) return 'earlier';
  if (currentRunOfFlow?.flowId !== flowId) return 'unknown';
  return currentRunOfFlow.runId === runId ? 'current' : 'earlier';
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

/** The current time, ticking each second only while isTicking, so an open run's duration counts up. */
function useSecondClock(isTicking: boolean): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!isTicking) return undefined;
    const timer = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(timer);
  }, [isTicking]);
  return now;
}

/** The tone of a Flow status, which tints the bar; matches StatusBadge's class names. */
function statusTone(status: string | undefined): string | undefined {
  return status ? status.toLowerCase().replaceAll(' ', '-') : undefined;
}

/** Lists every run attempt of a run, newest first. Run attempts are numbered from 1 without gaps. */
function RunAttemptSelect({
  currentRunAttempt,
  displayedRunAttempt,
  onSelect,
  label = 'Run attempt',
}: {
  /** The picker's accessible name; the popover's copy needs its own. */
  label?: string;
  currentRunAttempt: number;
  displayedRunAttempt: number;
  onSelect: (runAttempt: number) => void;
}) {
  const attempts = Array.from({ length: currentRunAttempt }, (_, index) => currentRunAttempt - index);
  return (
    <select
      aria-label={label}
      className="run-attempt-select"
      value={displayedRunAttempt}
      onChange={(event) => onSelect(Number(event.target.value))}
    >
      {attempts.map((attempt) => (
        <option key={attempt} value={attempt}>
          {`Attempt ${attempt} of ${currentRunAttempt}`}
        </option>
      ))}
    </select>
  );
}
