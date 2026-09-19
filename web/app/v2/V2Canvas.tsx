// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { hydrateBlobs } from '@/lib/blobs';
import { readResponseJSON } from '@/lib/http';
import type { FlowDefinitionCatalog, FlowHistoryEvent } from '@/lib/types';
import { safeDecode } from './canvas/model/decode';
import type { RunOverlay } from './canvas/model/run';
import {
  loadCurrentRun,
  loadRunHistory,
  attemptCountFromRecord,
  overlayFromHistory,
  prependStepRecords,
  recordForExecution,
  type ExecutionRecord,
  type RunOverlayBundle,
} from './canvas/overlayFromHistory';
import { DetailPanel } from './canvas/panel/DetailPanel';
import { buildPanel, type SectionId } from './canvas/panel/panelModel';
import { ArrowDefs } from './canvas/render/ArrowDefs';
import { Stage } from './canvas/render/Stage';
import { viewById } from './canvas/views';
import type { Detail, Direction } from './canvas/views/types';
import { Controls } from './flow/Controls';
import { Legend } from './flow/Legend';
import { groupsFromDefinition } from './groupsFromGraph';
import {
  PANEL_WIDTH_DEFAULT,
  PANEL_WIDTH_KEY,
  V2SplitHandle,
  readStoredPixels,
  writeStoredPixels,
} from './V2SplitHandle';

export function V2Canvas({ flowType, flowId = '' }: { flowType: string; flowId?: string }) {
  const canvasRef = useRef<HTMLDivElement>(null);
  const [catalog, setCatalog] = useState<FlowDefinitionCatalog | null>(null);
  const [detail, setDetail] = useState<Detail>('collapsed');
  const [direction, setDirection] = useState<Direction>('tb');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [selectedGroupId, setSelectedGroupId] = useState<string | null>(null);
  const [selectedExecutionId, setSelectedExecutionId] = useState<string | null>(null);
  const [inspectSection, setInspectSection] = useState<SectionId | null>(null);
  const [legendOpen, setLegendOpen] = useState(false);
  const [panelWidth, setPanelWidth] = useState(() => {
    const stored = readStoredPixels(PANEL_WIDTH_KEY);
    return Number.isFinite(stored) ? stored : PANEL_WIDTH_DEFAULT;
  });
  const [error, setError] = useState('');
  const [runError, setRunError] = useState('');
  const [currentBundle, setCurrentBundle] = useState<RunOverlayBundle | null>(null);
  const [olderByStep, setOlderByStep] = useState<Record<string, ExecutionRecord[]>>({});
  const [previousCursorByStep, setPreviousCursorByStep] = useState<Record<string, string>>({});
  const [loadPreviousBusy, setLoadPreviousBusy] = useState(false);
  const [previousEmpty, setPreviousEmpty] = useState(false);
  const [historyEvents, setHistoryEvents] = useState<FlowHistoryEvent[]>([]);
  const [hydratedWaitEvent, setHydratedWaitEvent] = useState<FlowHistoryEvent | null>(null);
  const [hydratedExecuteEvent, setHydratedExecuteEvent] = useState<FlowHistoryEvent | null>(null);
  const blobCache = useRef(new Map<string, unknown>());

  useEffect(() => {
    const controller = new AbortController();
    void fetch('/api/flow-definitions', { signal: controller.signal })
      .then((response) => readResponseJSON<FlowDefinitionCatalog>(response))
      .then(setCatalog)
      .catch((loadError: unknown) => {
        if (!controller.signal.aborted) {
          setError(loadError instanceof Error ? loadError.message : 'Flow definition failed to load');
        }
      });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    setCurrentBundle(null);
    setOlderByStep({});
    setPreviousCursorByStep({});
    setPreviousEmpty(false);
    setHistoryEvents([]);
    setHydratedWaitEvent(null);
    setHydratedExecuteEvent(null);
    setSelectedExecutionId(null);
    setRunError('');
    if (!flowId) return undefined;
    let cancelled = false;
    let timer: number | undefined;
    const load = async () => {
      try {
        const { summary, events, state } = await loadCurrentRun(flowId);
        if (cancelled) return;
        setHistoryEvents(events);
        setCurrentBundle(overlayFromHistory({
          flowId,
          runId: summary.runId,
          status: summary.flowStatus,
          events,
          activeSteps: state?.activeStepExecutions ?? [],
        }));
        setRunError('');
        if (summary.flowStatusCode === 1 && timer === undefined) {
          timer = window.setInterval(() => { void load(); }, 5000);
        }
      } catch (loadError: unknown) {
        if (!cancelled) {
          setRunError(loadError instanceof Error ? loadError.message : 'Run history failed to load');
        }
      }
    };
    void load();
    return () => {
      cancelled = true;
      if (timer !== undefined) window.clearInterval(timer);
    };
  }, [flowId]);

  const selected = useMemo(
    () => catalog?.definitions.find((definition) => definition.flowName === flowType && definition.valid),
    [catalog, flowType],
  );

  const flow = useMemo(() => {
    if (!selected) return null;
    return safeDecode(selected.graph, {
      generated: true,
      note: selected.file,
    });
  }, [selected]);

  const groups = useMemo(() => {
    if (!selected || !flow) return [];
    return groupsFromDefinition(selected.graph, flow);
  }, [flow, selected]);

  const bundle = useMemo(() => {
    if (!currentBundle) return null;
    return Object.entries(olderByStep).reduce(
      (next, [stepType, older]) => prependStepRecords(next, older, stepType),
      currentBundle,
    );
  }, [currentBundle, olderByStep]);

  const overlay: RunOverlay | null = bundle?.overlay ?? null;
  const selectedStep = flow?.steps.find((step) => step.id === selectedId) ?? null;

  const scene = useMemo(() => {
    if (!flow) return null;
    return viewById('control').layout(flow, {
      detail,
      direction,
      selectedId,
      run: overlay,
      groups,
    });
  }, [detail, direction, flow, groups, overlay, selectedId]);

  const selectedRecord = selectedStep && bundle
    ? recordForExecution(bundle, selectedStep.stepType, selectedExecutionId)
    : undefined;
  const selectedWaitEvent = selectedRecord?.waitEvent ?? null;
  const selectedExecuteEvent = selectedRecord?.executeEvent ?? null;
  const methodEventKey = [
    selectedWaitEvent
      ? `${selectedWaitEvent.eventId}|${selectedWaitEvent.type}`
      : '',
    selectedExecuteEvent
      ? `${selectedExecuteEvent.eventId}|${selectedExecuteEvent.type}`
      : '',
    selectedRecord?.execution.stepExecutionId ?? '',
  ].join('|');

  useEffect(() => {
    if (!flowId || (!selectedWaitEvent && !selectedExecuteEvent)) {
      setHydratedWaitEvent(null);
      setHydratedExecuteEvent(null);
      return undefined;
    }
    const controller = new AbortController();
    const waitRaw = selectedWaitEvent;
    const executeRaw = selectedExecuteEvent;
    setHydratedWaitEvent(waitRaw);
    setHydratedExecuteEvent(executeRaw);
    const hydrateOne = async (
      raw: FlowHistoryEvent | null,
      setValue: (value: FlowHistoryEvent | null) => void,
    ) => {
      if (!raw) {
        setValue(null);
        return;
      }
      try {
        const result = await hydrateBlobs(flowId, raw, blobCache.current, controller.signal);
        if (!controller.signal.aborted) setValue(result.value);
      } catch {
        if (!controller.signal.aborted) setValue(raw);
      }
    };
    void hydrateOne(waitRaw, setHydratedWaitEvent);
    void hydrateOne(executeRaw, setHydratedExecuteEvent);
    return () => controller.abort();
  }, [flowId, methodEventKey, selectedExecuteEvent, selectedWaitEvent]);

  const panel = useMemo(() => {
    if (!flow || !selectedStep) return null;
    return buildPanel(
      flow,
      selectedStep,
      overlay,
      selectedRecord?.execution.stepExecutionId ?? selectedExecutionId,
      Boolean(selectedRecord?.waitEvent),
      Boolean(selectedRecord?.executeEvent),
      attemptCountFromRecord(selectedRecord),
    );
  }, [
    flow,
    overlay,
    selectedExecutionId,
    selectedRecord,
    selectedStep,
  ]);

  const previousCursor = selectedStep
    ? (previousCursorByStep[selectedStep.stepType] ?? bundle?.previousRunId ?? '')
    : '';

  const loadPrevious = useCallback(async () => {
    if (!flowId || !selectedStep || !previousCursor) return;
    setLoadPreviousBusy(true);
    setPreviousEmpty(false);
    try {
      const events = await loadRunHistory(flowId, previousCursor);
      const hop = overlayFromHistory({
        flowId,
        runId: previousCursor,
        status: 'Continued as new',
        events,
      });
      const older = hop.records.filter((record) => record.execution.stepType === selectedStep.stepType);
      setHistoryEvents((current) => [...events, ...current]);
      setOlderByStep((current) => ({
        ...current,
        [selectedStep.stepType]: [...older, ...(current[selectedStep.stepType] ?? [])],
      }));
      setPreviousCursorByStep((current) => ({
        ...current,
        [selectedStep.stepType]: hop.previousRunId,
      }));
      setPreviousEmpty(older.length === 0);
    } catch (loadError: unknown) {
      setRunError(loadError instanceof Error ? loadError.message : 'Previous run failed to load');
    } finally {
      setLoadPreviousBusy(false);
    }
  }, [flowId, previousCursor, selectedStep]);

  const commitPanelWidth = useCallback((width: number) => {
    const next = Math.round(width);
    setPanelWidth(next);
    writeStoredPixels(PANEL_WIDTH_KEY, next);
  }, []);

  if (error) return <div className="v2-empty">{error}</div>;
  if (!catalog) return <div className="v2-empty">Loading Flow definition…</div>;
  if (!selected || !flow || !scene) {
    return <div className="v2-empty">No valid Flow Definition Graph 2.0 file for this Flow type.</div>;
  }

  const canvasStyle = panel
    ? ({ '--v2-panel-w': `${Math.round(panelWidth)}px` } as CSSProperties)
    : undefined;

  return (
    <div className="pcanvas" ref={canvasRef} style={canvasStyle}>
      <ArrowDefs />
      <div className="pctlbar">
        <Controls detail={detail} direction={direction} onDetail={setDetail} onDirection={setDirection} />
      </div>
      {runError ? <p className="v2-error v2-run-error">{runError}</p> : null}
      <Stage
        scene={scene}
        detail={detail}
        direction={direction}
        selectedId={selectedId}
        selectedGroupId={selectedGroupId}
        insetRightPx={panel ? panelWidth : null}
        onSelectGroup={(id) => {
          setSelectedGroupId(id);
          setSelectedId(null);
          setSelectedExecutionId(null);
          setInspectSection(null);
        }}
        onSelect={(id) => {
          setSelectedId(id);
          setSelectedGroupId(null);
          setSelectedExecutionId(null);
          setInspectSection(null);
          setPreviousEmpty(false);
        }}
        onInspect={(id) => {
          setSelectedId(id);
          setSelectedGroupId(null);
          setInspectSection('execute-input');
        }}
        legend={() => (
          <div className="plegend-card" data-open={legendOpen ? 'true' : undefined}>
            <button
              type="button"
              className="plegend-toggle"
              onClick={() => setLegendOpen((open) => !open)}
              aria-expanded={legendOpen}
            >
              Legend {legendOpen ? '▾' : '▸'}
            </button>
            {legendOpen ? (
              <>
                <Legend scene={scene} flow={flow} />
                {scene.notes.length > 0 ? (
                  <div className="plegend-notes">
                    {scene.notes.map((note) => <p key={note}>{note}</p>)}
                  </div>
                ) : null}
              </>
            ) : null}
          </div>
        )}
        fitKey={`${flowType}|${flowId}|${detail}|${direction}|${selected.file}|${overlay?.executions.length ?? 0}|${panel ? `panel:${Math.round(panelWidth)}` : 'graph'}`}
      />
      {panel ? (
        <>
          <V2SplitHandle
            axis="column"
            cssVariable="--v2-panel-w"
            edge="start"
            invert
            targetRef={canvasRef}
            measureRef={canvasRef}
            value={panelWidth}
            ariaLabel="Resize the Step detail panel"
            onCommit={commitPanelWidth}
          />
          <DetailPanel
            key={`${panel.stepType}|${inspectSection ?? panel.defaultSection}|${selectedRecord?.execution.stepExecutionId ?? ''}`}
            model={panel}
            selectedExecutionId={selectedRecord?.execution.stepExecutionId ?? selectedExecutionId}
            initialSection={inspectSection}
            waitEvent={hydratedWaitEvent ?? selectedWaitEvent}
            executeEvent={hydratedExecuteEvent ?? selectedExecuteEvent}
            history={historyEvents}
            parentFlowId={flowId}
            onClose={() => {
              setSelectedId(null);
              setSelectedExecutionId(null);
              setInspectSection(null);
            }}
            onSelectExecution={setSelectedExecutionId}
            canLoadPrevious={Boolean(previousCursor)}
            loadPreviousBusy={loadPreviousBusy}
            previousEmpty={previousEmpty}
            onLoadPrevious={() => { void loadPrevious(); }}
          />
        </>
      ) : null}
    </div>
  );
}
