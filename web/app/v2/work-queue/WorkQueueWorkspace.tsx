// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react';
import { Link, Navigate, useNavigate, useParams } from 'react-router-dom';
import {
  LIST_WIDTH_DEFAULT,
  LIST_WIDTH_KEY,
  V2SplitHandle,
  useCollapsibleColumn,
} from '../V2SplitHandle';
import { v2RunPath, v2WorkQueuePath } from '../contract';
import '../css/v2.css';
import { RUN_COPY } from '../run/copy';
import { useWebCatalog } from '../WebCatalogProvider';
import { RunList } from '../workspace/RunList';
import { RunSearch } from '../workspace/RunSearch';
import { permissionActionLabels, permissionsOf } from '../workspace/permissions';
import { EMPTY_RUN_QUERY } from '../workspace/runQuery';
import { SelectedRunPanel } from '../workspace/SelectedRunPanel';
import { useFlowSearch } from '../workspace/useFlowSearch';
import { useRunQuery } from '../workspace/useRunQuery';
import { useStrandedRuns } from '../workspace/useStrandedRuns';
import { WORK_QUEUE_COPY } from './copy';
import { openFlowStatusLabel } from './liveness';

/**
 * What has arrived for the reader. No canvas: clearing work does not need the shape of the
 * process, and "See the process" opens the same run in Run mode.
 *
 * Shares its list and its case panel with Run, so the two cannot drift apart. What differs is
 * deliberate: the scope control, and evidence before the decision.
 */
export function WorkQueueWorkspace() {
  const { flowType = '', flowId = '' } = useParams();
  const navigate = useNavigate();
  const { ready, canUseV2, catalog, error, definitionUpdateKey, permissionMode } = useWebCatalog();
  const entry = catalog?.flows.find((candidate) => candidate.flowType === flowType);
  // A work queue opens on active work; the control widens it.
  const runQuery = useRunQuery(entry?.definition, { ...EMPTY_RUN_QUERY, status: openFlowStatusLabel() });
  const [permission, setPermission] = useState('');
  const workQueuePermissions = useMemo(
    () => permission === '' ? [] : [permission],
    [permission],
  );
  const search = useFlowSearch(
    flowType || undefined,
    entry?.definition,
    runQuery.appliedFilters,
    workQueuePermissions,
  );
  const { strandedFlowIDs, rememberStranded } = useStrandedRuns();
  const shellRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const listPane = useCollapsibleColumn(LIST_WIDTH_KEY, LIST_WIDTH_DEFAULT);

  useEffect(() => {
    if (definitionUpdateKey > 0) navigate(v2WorkQueuePath(flowType || undefined), { replace: true });
  }, [definitionUpdateKey, flowType, navigate]);

  if (!ready) return <div className="page-loading">Loading Dex Web…</div>;
  if (!canUseV2) return <Navigate to="/v1/flows" replace />;
  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  if (!catalog) return <div className="page-loading">Loading Dex Web…</div>;
  if (catalog.flows.length === 0) {
    return (
      <div className="v2-shell v2-run">
        <div className="v2-empty">Load a valid Flow Definition Graph 2.0 file to use Work Queue.</div>
      </div>
    );
  }
  if (!flowType) return <Navigate to={v2WorkQueuePath(catalog.flows[0].flowType)} replace />;
  if (!entry) return <Navigate to={v2WorkQueuePath()} replace />;

  const selectedFlow = search.flows.find((flow) => flow.flowId === flowId);
  const paneStyle = { '--v2-list-w': `${listPane.width}px` } as CSSProperties;
  return (
    <div className="v2-shell v2-run" ref={shellRef} style={paneStyle}>
      <div className="v2-run-body" ref={bodyRef}>
        <RunList
          collapsed={listPane.isCollapsed}
          emptyText={WORK_QUEUE_COPY.clear}
          entry={entry}
          flowTypes={catalog.flows}
          heading={WORK_QUEUE_COPY.appName}
          headerNote={WORK_QUEUE_COPY.liveNote}
          onExpand={listPane.expand}
          permissionControl={permissionMode === 'local-selector' && permissionsOf(entry.definition).length > 0 ? (
            <label className="rsw-permission">
              <span className="rsw-zonehead">{WORK_QUEUE_COPY.permissionLabel}</span>
              <select
                aria-label={WORK_QUEUE_COPY.permissionLabel}
                value={permission}
                onChange={(event) => setPermission(event.target.value)}
              >
                <option value="">{WORK_QUEUE_COPY.anyPermission}</option>
                {permissionsOf(entry.definition).map((candidate) => (
                  <option key={candidate} value={candidate}>{candidate}</option>
                ))}
              </select>
              {permission !== '' && (
                <small className="rsw-permission-note">
                  {WORK_QUEUE_COPY.permissionActions(
                    permissionActionLabels(entry.definition, permission),
                  )}
                </small>
              )}
            </label>
          ) : undefined}
          scope={(
            <RunSearch
              busy={search.loading}
              definition={entry.definition}
              query={runQuery.query}
              onChange={runQuery.setQuery}
              onClear={runQuery.clear}
              onSubmit={runQuery.submit}
            />
          )}
          search={search}
          selectedFlowID={flowId}
          strandedFlowIDs={strandedFlowIDs}
          onSelectFlowType={(next) => navigate(v2WorkQueuePath(next))}
          onSelectRun={(nextFlowID) => navigate(v2WorkQueuePath(entry.flowType, nextFlowID))}
        />
        <V2SplitHandle
          axis="column"
          cssVariable="--v2-list-w"
          edge="end"
          pane="list"
          targetRef={shellRef}
          measureRef={bodyRef}
          value={listPane.width}
          ariaLabel={RUN_COPY.resizeList}
          onCommit={listPane.commit}
          onToggle={listPane.isCollapsed ? listPane.expand : listPane.collapse}
        />
        <section className="v2-work-queue-case" aria-label={flowId || WORK_QUEUE_COPY.appName}>
          {flowId ? (
            <SelectedRunPanel
              definition={entry.definition}
              flowId={flowId}
              flowStatusCode={selectedFlow?.flowStatusCode}
              flowType={entry.flowType}
              order="evidence-first"
              onStranded={rememberStranded}
              workQueuePermissions={workQueuePermissions}
              footer={(
                <>
                  <Link className="v2-seemore" to={v2RunPath(entry.flowType, flowId)}>
                    {WORK_QUEUE_COPY.seeProcess}
                  </Link>
                  <button
                    aria-label={WORK_QUEUE_COPY.close}
                    className="rhd-close"
                    onClick={() => navigate(v2WorkQueuePath(entry.flowType))}
                    type="button"
                  >
                    ✕
                  </button>
                </>
              )}
            />
          ) : (
            <p className="sc-none v2-work-queue-empty">{WORK_QUEUE_COPY.selectPrompt}</p>
          )}
        </section>
      </div>
    </div>
  );
}
