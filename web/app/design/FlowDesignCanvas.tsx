// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useMemo, useRef, useState, type JSX, type ReactNode, type RefObject } from 'react';
import {
  ArrowDown,
  ArrowRight,
  BookOpen,
  ChevronUp,
  ChevronsDownUp,
  ChevronsUpDown,
  Minus,
  Plug,
  Plus,
  Scan,
  X,
} from 'lucide-react';
import type { FlowDefinitionGraph } from '@superdurable/flow-definition-renderer';
import { safeDecode } from './canvas/model/decode';
import type { Branch, PocFlow, StepModel } from './canvas/model/pocFlow';
import { ArrowDefs } from './canvas/render/ArrowDefs';
import { paneHeightForWholeDrawing } from './canvas/render/camera';
import { TileView } from './canvas/render/CardParts';
import { Stage } from './canvas/render/Stage';
import type { CanvasViewportHandle } from './canvas/render/viewport';
import { FIT_MAX_ZOOM, OPEN_FLOOR, WHOLE_DRAWING_MIN_ZOOM } from './canvas/render/zoom';
import { useStageScene } from './canvas/useStageScene';
import { detourMessage, hasDetour, isFailureHandling, withDetours } from './canvas/views/failureDetours';
import { stageId } from './canvas/views/stageCard';
import { stepTitle, tileOf } from './canvas/views/stepBox';
import type { Direction, Scene } from './canvas/views/types';
import { Legend } from './flow/Legend';
import { groupsFromDefinition } from './groupsFromGraph';
import { stepContext } from './stepContext';
import '../workspace/css/tokens.css';
import './canvas.css';
import './design.css';

/**
 * Draws one Flow Definition Graph: the Flow's Steps, stages and transitions, and nothing about any run.
 *
 * It reads only the definition it is given. It never loads history or stored values, so it explains
 * how a Flow is built rather than what one run did. It uses no app route, so the docs site draws it too.
 */
export function FlowDesignCanvas({
  graph,
  graphKey,
  connectorSettingsLink,
  wholeMaxZoom,
  onPreferredHeight,
  opensWholeFlow = false,
  zoomsOnGesture = true,
}: {
  graph: FlowDefinitionGraph;
  /** Identifies the definition; a new key redraws and refits the canvas. */
  graphKey: string;
  /** Shown in the Step panel of a Connector Step; omitted where no settings page exists. */
  connectorSettingsLink?: ReactNode;
  /** Largest zoom for opening on, or fitting, the whole Flow; omitted for the default. */
  wholeMaxZoom?: number;
  /** Receives the height, in pixels, that opens on the whole Flow below the toolbar, per view and width. */
  onPreferredHeight?: (heightPx: number) => void;
  /** Opens on, fits, and zooms out to the whole Flow, however small. */
  opensWholeFlow?: boolean;
  /** False keeps the zoom fixed under the wheel and pinch, so a page scrolls past the drawing. */
  zoomsOnGesture?: boolean;
}) {
  const [direction, setDirection] = useState<Direction>('lr');
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [selectedGroupId, setSelectedGroupId] = useState<string | null>(null);
  const [detourFor, setDetourFor] = useState<string | null>(null);
  const [legendOpen, setLegendOpen] = useState(false);
  const legendToggleRef = useRef<HTMLButtonElement>(null);
  const viewportRef = useRef<CanvasViewportHandle>(null);

  const flow = useMemo(
    () => safeDecode(graph, { generated: true, note: graphKey }),
    [graph, graphKey],
  );
  const groups = useMemo(() => groupsFromDefinition(graph, flow), [graph, flow]);
  const stages = useStageScene({ flow, groups, direction, run: null });
  const scene = useMemo(() => {
    if (stages.scene === null) return null;
    const sources = detourFor ? [detourFor] : [];
    return withDetours(stages.scene, flow, { direction, run: null, groups, openStages: stages.openStages }, sources);
  }, [detourFor, direction, flow, groups, stages.openStages, stages.scene]);

  const { collapseStage, openStage: openStageInPlace } = stages;
  const closeStage = useCallback((groupId: string) => {
    collapseStage(groupId);
    setSelectedGroupId(null);
  }, [collapseStage]);
  const openStage = useCallback((boxId: string) => {
    openStageInPlace(boxId);
    setSelectedGroupId(groups.find((group) => stageId(group.id) === boxId)?.id ?? null);
    setSelectedId(null);
  }, [groups, openStageInPlace]);

  const selectedStep = flow.steps.find((step) => step.id === selectedId) ?? null;
  const shellRef = useRef<HTMLDivElement>(null);
  const { overlayInsetTopPx } = useReportPreferredCanvasHeight({
    shellRef,
    viewKey: `${graphKey}|${stages.view}`,
    scene: stages.isSettled ? stages.scene : null,
    isPanelOpen: selectedStep !== null,
    wholeMaxZoom: wholeMaxZoom ?? FIT_MAX_ZOOM,
    onPreferredHeight,
    opensWholeFlow,
  });

  if (flow.steps.length === 0 && flow.entries.length === 0) {
    return <div className="dg-empty">This Flow Definition Graph has no Steps to draw.</div>;
  }
  if (!scene) return <div className="dg-empty">Drawing the Flow…</div>;

  return (
    <div ref={shellRef} className="v2-shell dg" data-panel={selectedStep ? 'true' : undefined}>
      <div className="pcanvas dg-canvas">
        <ArrowDefs />
        {/* Overlays on the canvas, not scene nodes: they stay readable at any zoom. Placed first so tab order reaches them before Step cards. */}
        <div className="dg-overlay">
          <DesignToolbar
            stages={stages.hasStages ? stages.state : null}
            direction={direction}
            onCollapseAll={stages.collapseAll}
            onExpandAll={stages.expandAll}
            onDirection={setDirection}
          />
          <div className="dg-corner">
            <DesignZoomControls viewportRef={viewportRef} />
            <div className="plegend-card dg-legend" data-open={legendOpen ? 'true' : undefined}>
              <div className="dg-legend-head">
                <button
                  ref={legendToggleRef}
                  type="button"
                  className="plegend-toggle"
                  onClick={() => setLegendOpen((open) => !open)}
                  aria-expanded={legendOpen}
                >
                  <BookOpen aria-hidden="true" size={14} strokeWidth={2} />
                  Legend
                </button>
                {legendOpen ? (
                  // Hide sits where the collapsed toggle was, so one spot opens and closes the key.
                  <button
                    type="button"
                    className="dg-legend-hide"
                    aria-label="Hide legend"
                    title="Hide legend"
                    onClick={() => {
                      setLegendOpen(false);
                      legendToggleRef.current?.focus();
                    }}
                  >
                    <ChevronUp aria-hidden="true" size={16} strokeWidth={2} />
                  </button>
                ) : null}
              </div>
              {legendOpen ? (
                <div className="dg-legend-body">
                  <Legend scene={scene} flow={flow} />
                  {scene.notes.length > 0 ? (
                    <div className="plegend-notes">
                      {scene.notes.map((note) => <p key={note}>{note}</p>)}
                    </div>
                  ) : null}
                </div>
              ) : null}
            </div>
          </div>
        </div>
        <Stage
          key={graphKey}
          isSettled={stages.isSettled}
          dimUnrelated={selectedId !== null}
          scene={scene}
          direction={direction}
          onOpenStage={openStage}
          onCloseStage={closeStage}
          cue={stages.cue}
          selectedId={selectedId}
          selectedGroupId={selectedGroupId}
          onSelectGroup={(id) => {
            setSelectedGroupId(id);
            setSelectedId(null);
          }}
          onSelect={(id) => {
            setSelectedId(id);
            // Selecting a Step that can fail draws its detour; selecting inside the detour keeps it.
            if (id === null || !isFailureHandling(flow, id)) setDetourFor(id !== null && hasDetour(flow, id) ? id : null);
            setSelectedGroupId(null);
          }}
          fitKey={`${graphKey}|${stages.view}`}
          handleRef={viewportRef}
          showsZoomControls={false}
          wholeMaxZoom={wholeMaxZoom}
          opensWholeDrawing={opensWholeFlow}
          zoomsOnGesture={zoomsOnGesture}
          insetTopPx={overlayInsetTopPx}
        />
        <p className="sr-only" aria-live="polite">{stages.cue?.message}</p>
        <p className="sr-only" aria-live="polite">{detourFor ? detourMessage(flow, detourFor) : ''}</p>
      </div>
      {selectedStep ? (
        <DesignStepPanel
          flow={flow}
          step={selectedStep}
          groups={groups}
          connectorSettingsLink={connectorSettingsLink}
          onClose={() => {
            setSelectedId(null);
            setDetourFor(null);
          }}
        />
      ) : null}
    </div>
  );
}

/**
 * Reports one height per view and width; not while the Step panel is open.
 * Returns `overlayInsetTopPx`: the pixels the overlays cover at the pane's top, or null when the host does not size the canvas.
 */
function useReportPreferredCanvasHeight({
  shellRef,
  viewKey,
  scene,
  isPanelOpen,
  wholeMaxZoom,
  onPreferredHeight,
  opensWholeFlow,
}: {
  shellRef: RefObject<HTMLDivElement | null>;
  /** Changes with the definition, the direction, and Collapsed or Expanded. */
  viewKey: string;
  /** The settled drawing, before failure detours; null while one is on its way. */
  scene: Scene | null;
  isPanelOpen: boolean;
  wholeMaxZoom: number;
  onPreferredHeight: ((heightPx: number) => void) | undefined;
  opensWholeFlow: boolean;
}): { overlayInsetTopPx: number | null } {
  const [sizedView, setSizedView] = useState<{ viewKey: string; scene: Scene } | null>(null);
  const [overlayInsetTopPx, setOverlayInsetTopPx] = useState<number | null>(null);
  if (onPreferredHeight !== undefined && scene !== null && sizedView?.viewKey !== viewKey) setSizedView({ viewKey, scene });

  useEffect(() => {
    const shell = shellRef.current;
    if (onPreferredHeight === undefined || sizedView === null || shell === null || isPanelOpen) return undefined;
    const measureAndReportHeight = (): void => {
      const pane = shell.querySelector<HTMLElement>('.react-flow');
      if (pane === null || pane.clientWidth < 8) return;
      const shellBox = shell.getBoundingClientRect();
      const paneBox = pane.getBoundingClientRect();
      // The toolbar and corner controls float over the pane's top; the drawing opens below them.
      const overlayBottom = Math.max(
        paneBox.top,
        ...Array.from(
          shell.querySelectorAll('.dg-toolbar, .dg-zoom, .dg-legend-head'),
          (overlayControl) => overlayControl.getBoundingClientRect().bottom,
        ),
      );
      const insetTop = overlayBottom - paneBox.top + OVERLAY_GAP_PX;
      setOverlayInsetTopPx(insetTop);
      const paneHeight = paneHeightForWholeDrawing(
        sizedView.scene,
        pane.clientWidth,
        wholeMaxZoom,
        insetTop,
        opensWholeFlow ? WHOLE_DRAWING_MIN_ZOOM : OPEN_FLOOR,
      );
      onPreferredHeight(Math.ceil(paneBox.top - shellBox.top + paneHeight + shellBox.bottom - paneBox.bottom));
    };
    measureAndReportHeight();
    // Only a width change moves the answer; the height this reports would otherwise re-trigger it.
    let observedShellWidth = shell.clientWidth;
    const shellObserver = new ResizeObserver(() => {
      if (shell.clientWidth === observedShellWidth) return;
      observedShellWidth = shell.clientWidth;
      measureAndReportHeight();
    });
    shellObserver.observe(shell);
    // The toolbar grows when a late web font loads; its height never depends on the shell's height.
    const toolbar = shell.querySelector<HTMLElement>('.dg-toolbar');
    let observedToolbarHeight = toolbar?.offsetHeight ?? 0;
    const toolbarObserver = new ResizeObserver(() => {
      if (toolbar === null || toolbar.offsetHeight === observedToolbarHeight) return;
      observedToolbarHeight = toolbar.offsetHeight;
      measureAndReportHeight();
    });
    if (toolbar !== null) toolbarObserver.observe(toolbar);
    return () => {
      shellObserver.disconnect();
      toolbarObserver.disconnect();
    };
  }, [isPanelOpen, onPreferredHeight, opensWholeFlow, shellRef, sizedView, wholeMaxZoom]);
  return { overlayInsetTopPx: onPreferredHeight === undefined ? null : overlayInsetTopPx };
}

/** Space below the overlays; a drawing opened on its start may lose its own frame margin. */
const OVERLAY_GAP_PX = 8;

const DIRECTION_OPTIONS: { id: Direction; label: string; Icon: typeof ArrowDown }[] = [
  { id: 'tb', label: 'Top to bottom', Icon: ArrowDown },
  { id: 'lr', label: 'Left to right', Icon: ArrowRight },
];

/** View and Direction controls as two segmented switches. */
function DesignToolbar({
  stages,
  direction,
  onCollapseAll,
  onExpandAll,
  onDirection,
}: {
  /** Null for a Flow without stages, which has nothing to open or close. */
  stages: 'collapsed' | 'expanded' | 'mixed' | null;
  direction: Direction;
  onCollapseAll: () => void;
  onExpandAll: () => void;
  onDirection: (direction: Direction) => void;
}): JSX.Element {
  return (
    <div className="dg-toolbar">
      {stages === null ? null : (
        <div className="dg-segment" role="group" aria-label="View">
          <span className="dg-segment-label">View</span>
          <button
            type="button"
            aria-pressed={stages === 'collapsed'}
            title="Close every stage and return to the opening view"
            onClick={onCollapseAll}
          >
            <ChevronsDownUp aria-hidden="true" size={14} strokeWidth={2} />
            Collapsed
          </button>
          <button
            type="button"
            aria-pressed={stages === 'expanded'}
            title="Open every stage to show all of its Steps"
            onClick={onExpandAll}
          >
            <ChevronsUpDown aria-hidden="true" size={14} strokeWidth={2} />
            Expanded
          </button>
        </div>
      )}
      <div className="dg-segment" role="radiogroup" aria-label="Direction">
        <span className="dg-segment-label">Direction</span>
        {DIRECTION_OPTIONS.map(({ id, label, Icon }) => (
          <button
            key={id}
            type="button"
            role="radio"
            aria-checked={direction === id}
            aria-label={label}
            title={label}
            onClick={() => onDirection(id)}
          >
            <Icon aria-hidden="true" size={14} strokeWidth={2} />
          </button>
        ))}
      </div>
    </div>
  );
}

/** Zoom and Fit buttons in the toolbar's pill style, driving the canvas through its viewport handle. */
function DesignZoomControls({ viewportRef }: { viewportRef: RefObject<CanvasViewportHandle | null> }): JSX.Element {
  return (
    <div className="dg-segment dg-zoom" role="group" aria-label="Zoom">
      <button type="button" aria-label="Zoom in" title="Zoom in" onClick={() => viewportRef.current?.zoomIn()}>
        <Plus aria-hidden="true" size={14} strokeWidth={2} />
      </button>
      <button type="button" aria-label="Zoom out" title="Zoom out" onClick={() => viewportRef.current?.zoomOut()}>
        <Minus aria-hidden="true" size={14} strokeWidth={2} />
      </button>
      <button
        type="button"
        aria-label="Fit the whole Flow"
        title="Fit the whole Flow"
        onClick={() => viewportRef.current?.fit()}
      >
        <Scan aria-hidden="true" size={14} strokeWidth={2} />
        Fit
      </button>
    </div>
  );
}

/** What one Step is for and where it leads, from the definition alone. */
function DesignStepPanel({
  flow,
  step,
  groups,
  connectorSettingsLink,
  onClose,
}: {
  flow: PocFlow;
  step: StepModel;
  groups: Parameters<typeof stepContext>[2];
  connectorSettingsLink: ReactNode;
  onClose: () => void;
}): JSX.Element {
  const context = stepContext(flow, step, groups);
  const paths = step.execute.branches.map((branch) => pathOf(flow, branch));
  return (
    <aside className="dg-panel" aria-label={`Step ${step.stepType}`}>
      <header className="dg-panel-head">
        <TileView tile={tileOf(flow, step)} />
        <div className="dg-panel-title">
          <h3>{stepTitle(step)}</h3>
          <code>{step.stepType}</code>
        </div>
        <button aria-label="Close the Step details" className="dg-panel-close" onClick={onClose} type="button">
          <X aria-hidden="true" size={16} strokeWidth={2} />
        </button>
      </header>
      <p className={context.explanation ? 'dg-panel-purpose' : 'dg-panel-purpose is-missing'}>
        {context.explanation ?? 'The Flow declares no explanation for this Step.'}
      </p>
      <dl className="dg-panel-facts">
        {context.facts.map((fact) => (
          <div key={fact.label}>
            <dt>{fact.label}</dt>
            <dd>{fact.value}</dd>
          </div>
        ))}
      </dl>
      {paths.length > 0 ? (
        <section className="dg-panel-paths">
          <h4>Paths out</h4>
          <ol>
            {paths.map((path, index) => (
              <li key={index} data-tone={path.tone}>
                <span className="dg-path-when">{path.when}</span>
                <span className="dg-path-then">{path.then}</span>
              </li>
            ))}
          </ol>
        </section>
      ) : null}
      {context.connector ? (
        <section className="dg-panel-connector">
          <Plug aria-hidden="true" size={16} strokeWidth={2} />
          <div>
            <b>{context.connector.connectorId}</b>
            <span>{context.connector.operationId} · {context.connector.operationKind}</span>
            <span>Connection: {context.connector.connectionName || 'Unnamed'}</span>
            {connectorSettingsLink}
          </div>
        </section>
      ) : null}
    </aside>
  );
}

interface DesignPath {
  when: string;
  then: string;
  tone: 'go' | 'end' | 'fail';
}

const CLOSE_WORDS: Record<NonNullable<Branch['closes']>, Omit<DesignPath, 'when'>> = {
  complete: { then: 'The Flow completes', tone: 'end' },
  completeIfChannelsEmpty: { then: 'The Flow completes once no messages wait', tone: 'end' },
  deadEnd: { then: 'This path ends; other paths go on', tone: 'end' },
  fail: { then: 'The Flow fails', tone: 'fail' },
};

/** One Execute branch in plain words: when it applies, and what happens next. */
function pathOf(flow: PocFlow, branch: Branch): DesignPath {
  const when = branch.guard === null ? 'Always' : branch.guard === 'otherwise' ? 'Otherwise' : `When ${branch.guard}`;
  if (branch.closes) return { when, ...CLOSE_WORDS[branch.closes] };
  const targets = branch.targets
    .map((target) => {
      const next = flow.steps.find((candidate) => candidate.id === target.stepId);
      return next ? stepTitle(next) : target.stepId.replace(/^step:/, '');
    });
  if (targets.length === 0) return { when, then: 'Nothing follows', tone: 'end' };
  const joiner = targets.length > 1 ? ' and ' : '';
  return {
    when,
    then: targets.length > 1 ? `Runs ${targets.slice(0, -1).join(', ')}${joiner}${targets.at(-1)} in parallel` : `Goes to ${targets[0]}`,
    tone: 'go',
  };
}
