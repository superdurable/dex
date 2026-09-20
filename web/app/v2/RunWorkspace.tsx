// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useRef, useState, type CSSProperties } from 'react';
import { Navigate, useNavigate, useParams } from 'react-router-dom';
import type { FlowSummary } from '@/lib/types';
import { v2HomePath, v2RunPath } from './contract';
import './css/v2.css';
import { RUN_COPY } from './run/copy';
import { RunDetailDrawer, type StepBand } from './run/RunDetailDrawer';
import type { StepContextView } from './run/stepContext';
import { RunList } from './workspace/RunList';
import { V2Canvas } from './V2Canvas';
import {
  DRAWER_WIDTH_DEFAULT,
  DRAWER_WIDTH_KEY,
  V2SplitHandle,
  readStoredPixels,
  writeStoredPixels,
} from './V2SplitHandle';
import { useWebCatalog } from './WebCatalogProvider';
import { useFlowSearch } from './workspace/useFlowSearch';
import { useStrandedRuns } from './workspace/useStrandedRuns';

export function HomePage() {
  const { ready, canUseV2, error } = useWebCatalog();
  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  if (!ready) return <div className="page-loading">Loading Dex Web…</div>;
  return <Navigate to={v2HomePath(canUseV2)} replace />;
}

/**
 * The Admin semantic view: pick a run, see where it is on the canvas, act on it in the drawer.
 *
 * Selection is narrow and the canvas is wide on purpose. Scoping a search belongs to the Queue,
 * and the technical record belongs to the Deep Dive.
 */
export function RunWorkspace() {
  const { flowType = '', flowId = '' } = useParams();
  const navigate = useNavigate();
  const { ready, canUseV2, catalog, error } = useWebCatalog();
  const entry = catalog?.flows.find((candidate) => candidate.flowType === flowType);
  const search = useFlowSearch(flowType || undefined, entry?.definition);
  const { strandedFlowIDs, rememberStranded } = useStrandedRuns();
  const [band, setBand] = useState<StepBand | null>(null);
  const [stepView, setStepView] = useState<StepContextView | null>(null);
  const [summary, setSummary] = useState<FlowSummary | null>(null);
  const [tick, setTick] = useState(0);
  /** Bumped to tell the canvas to drop its Step selection. */
  const [deselectKey, setDeselectKey] = useState(0);
  /** Dismissing the drawer keeps the run on the canvas; it just hands the width back. */
  const [drawerOpen, setDrawerOpen] = useState(true);
  const shellRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const [drawerWidth, setDrawerWidth] = useState(() => {
    const stored = readStoredPixels(DRAWER_WIDTH_KEY);
    return Number.isFinite(stored) ? stored : DRAWER_WIDTH_DEFAULT;
  });

  const commitDrawerWidth = useCallback((width: number) => {
    const next = Math.round(width);
    setDrawerWidth(next);
    writeStoredPixels(DRAWER_WIDTH_KEY, next);
  }, []);

  // One clock: the canvas owns the run poll and the drawer refreshes on the same beat.
  const onTick = useCallback(() => setTick((previous) => previous + 1), []);

  // A newly chosen run always opens its drawer, even if the last one was dismissed.
  useEffect(() => { setDrawerOpen(true); }, [flowId]);

  if (!ready) return <div className="page-loading">Loading Dex Web…</div>;
  if (!canUseV2) return <Navigate to="/v1/flows" replace />;
  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  if (!catalog) return <div className="page-loading">Loading Dex Web…</div>;
  if (catalog.flows.length === 0) {
    return (
      <div className="v2-shell sv">
        <div className="v2-empty">Load a valid Flow Definition Graph 2.0 file to search runs in v2.</div>
      </div>
    );
  }
  if (!flowType) return <Navigate to={v2RunPath(catalog.flows[0].flowType)} replace />;
  if (!entry) return <Navigate to={v2RunPath()} replace />;

  const selectedFlow = search.flows.find((flow) => flow.flowId === flowId);
  // A Step click alone opens the drawer: its Flow-level meaning needs no run.
  const showRun = Boolean(flowId) && drawerOpen;
  const showDrawer = showRun || stepView !== null;
  const paneStyle = { '--v2-drawer-w': `${drawerWidth}px` } as CSSProperties;
  return (
    <div className="v2-shell v2-run" ref={shellRef} style={paneStyle}>
      <div className="v2-run-body" data-has-run={showDrawer ? 'true' : undefined} ref={bodyRef}>
        <RunList
          attentionAttributeKey={entry.definition.indexedAttributes[0]?.attributeKey ?? null}
          emptyText={RUN_COPY.noRuns}
          entry={entry}
          flowTypes={catalog.flows}
          heading={RUN_COPY.runsHeading}
          search={search}
          selectedFlowID={flowId}
          strandedFlowIDs={strandedFlowIDs}
          onSelectFlowType={(next) => navigate(v2RunPath(next))}
          onSelectRun={(nextFlowID) => navigate(v2RunPath(entry.flowType, nextFlowID))}
        />
        <section className="v2-canvas" aria-label="Flow definition">
          <V2Canvas
            flowId={flowId}
            flowType={entry.flowType}
            onBand={setBand}
            onStepContext={setStepView}
            deselectKey={deselectKey}
            focusBlockingStep={showRun}
            onSummary={setSummary}
            showStepPanel={false}
            onTick={onTick}
          />
        </section>
        {showDrawer ? (
          <>
            <V2SplitHandle
              axis="column"
              cssVariable="--v2-drawer-w"
              edge="start"
              invert
              targetRef={shellRef}
              measureRef={bodyRef}
              value={drawerWidth}
              ariaLabel="Resize the run drawer"
              onCommit={commitDrawerWidth}
            />
            <RunDetailDrawer
              band={band}
              definition={entry.definition}
              flowId={showRun ? flowId : ''}
              flowStatusCode={selectedFlow?.flowStatusCode}
              flowType={entry.flowType}
              reloadKey={tick}
              stepContext={stepView}
              summary={summary}
              onClose={() => {
                setDrawerOpen(false);
                setDeselectKey((previous) => previous + 1);
              }}
              onStopped={search.runSearch}
              onStranded={rememberStranded}
            />
          </>
        ) : null}
      </div>
    </div>
  );
}
