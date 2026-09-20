// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useRef, useState, type CSSProperties } from 'react';
import { Link, Navigate, useNavigate, useParams } from 'react-router-dom';
import { v2DebugPath, v2HomePath, v2RunPath } from './contract';
import './css/v2.css';
import { DEBUG_COPY } from './debug/copy';
import { V2Canvas } from './V2Canvas';
import {
  CASE_HEIGHT_KEY,
  LIST_WIDTH_DEFAULT,
  LIST_WIDTH_KEY,
  V2SplitHandle,
  readStoredPixels,
  writeStoredPixels,
} from './V2SplitHandle';
import { useWebCatalog } from './WebCatalogProvider';
import { FlowListing } from './workspace/FlowListing';
import { SelectedRunPanel } from './workspace/SelectedRunPanel';
import { useFlowSearch } from './workspace/useFlowSearch';
import { useStrandedRuns } from './workspace/useStrandedRuns';

export function HomePage() {
  const { ready, canUseV2, error } = useWebCatalog();
  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  if (!ready) return <div className="page-loading">Loading Dex Web…</div>;
  return <Navigate to={v2HomePath(canUseV2)} replace />;
}

export function RunWorkspace() {
  const { flowType = '', flowId = '' } = useParams();
  const navigate = useNavigate();
  const { ready, canUseV2, catalog, error } = useWebCatalog();
  const entry = catalog?.flows.find((candidate) => candidate.flowType === flowType);
  const search = useFlowSearch(flowType || undefined, entry?.definition);
  const { strandedFlowIDs, rememberStranded } = useStrandedRuns();
  const shellRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const listPaneRef = useRef<HTMLElement>(null);
  const [listWidth, setListWidth] = useState(() => {
    const stored = readStoredPixels(LIST_WIDTH_KEY);
    return Number.isFinite(stored) ? stored : LIST_WIDTH_DEFAULT;
  });
  const [caseHeight, setCaseHeight] = useState(() => readStoredPixels(CASE_HEIGHT_KEY));

  const commitListWidth = useCallback((width: number) => {
    const next = Math.round(width);
    setListWidth(next);
    writeStoredPixels(LIST_WIDTH_KEY, next);
  }, []);

  const commitCaseHeight = useCallback((height: number) => {
    const next = Math.round(height);
    setCaseHeight(next);
    writeStoredPixels(CASE_HEIGHT_KEY, next);
  }, []);

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
  const paneStyle = {
    '--v2-list-w': `${listWidth}px`,
    ...(Number.isFinite(caseHeight) && caseHeight > 0 ? { '--v2-case-h': `${caseHeight}px` } : {}),
  } as CSSProperties;
  return (
    <div className="v2-shell sv" ref={shellRef} style={paneStyle}>
      <div className="sv-body" ref={bodyRef}>
        <aside className="sq" ref={listPaneRef} data-has-case={flowId ? 'true' : undefined}>
          <FlowListing
            entry={entry}
            flowTypes={catalog.flows}
            headerNote="current runs"
            search={search}
            selectedFlowID={flowId}
            strandedFlowIDs={strandedFlowIDs}
            onSelectFlowType={(next) => navigate(v2RunPath(next))}
            onSelectRun={(nextFlowID) => navigate(v2RunPath(entry.flowType, nextFlowID))}
          >
            {flowId ? (
              <>
                <V2SplitHandle
                  axis="row"
                  cssVariable="--v2-case-h"
                  targetRef={shellRef}
                  measureRef={listPaneRef}
                  value={Number.isFinite(caseHeight) ? caseHeight : 0}
                  ariaLabel="Resize the Display pane"
                  onCommit={commitCaseHeight}
                />
                <SelectedRunPanel
                  definition={entry.definition}
                  flowId={flowId}
                  flowStatusCode={selectedFlow?.flowStatusCode}
                  flowType={entry.flowType}
                  onStranded={rememberStranded}
                  footer={(
                    <Link className="v2-seemore" to={v2DebugPath(entry.flowType, flowId)}>
                      {DEBUG_COPY.openLabel}
                    </Link>
                  )}
                />
              </>
            ) : (
              <p className="sq-state">Select a run to edit fields and invoke Actions.</p>
            )}
          </FlowListing>
          <V2SplitHandle
            axis="column"
            cssVariable="--v2-list-w"
            targetRef={shellRef}
            measureRef={bodyRef}
            value={listWidth}
            ariaLabel="Resize the listing pane"
            onCommit={commitListWidth}
          />
        </aside>
        <section className="v2-canvas" aria-label="Flow definition">
          <V2Canvas flowType={entry.flowType} flowId={flowId} />
        </section>
      </div>
    </div>
  );
}
