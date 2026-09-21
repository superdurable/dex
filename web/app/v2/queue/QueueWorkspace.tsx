// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useMemo, useRef, useState, type CSSProperties } from 'react';
import { Link, Navigate, useNavigate, useParams } from 'react-router-dom';
import {
  LIST_WIDTH_DEFAULT,
  LIST_WIDTH_KEY,
  V2SplitHandle,
  useCollapsibleColumn,
} from '../V2SplitHandle';
import { v2QueuePath, v2RunPath } from '../contract';
import '../css/v2.css';
import { RUN_COPY } from '../run/copy';
import { useWebCatalog } from '../WebCatalogProvider';
import { RunList } from '../workspace/RunList';
import { RunSearch } from '../workspace/RunSearch';
import { roleActionLabels, roleFilter, rolesOf } from '../workspace/roles';
import { EMPTY_RUN_QUERY } from '../workspace/runQuery';
import { SelectedRunPanel } from '../workspace/SelectedRunPanel';
import { useFlowSearch } from '../workspace/useFlowSearch';
import { useRunQuery } from '../workspace/useRunQuery';
import { useStrandedRuns } from '../workspace/useStrandedRuns';
import { QUEUE_COPY } from './copy';
import { openFlowStatusLabel } from './liveness';

/**
 * What has arrived for the reader. No canvas: clearing work does not need the shape of the
 * process, and "See the process" opens the same run in Run mode.
 *
 * Shares its list and its case panel with Run, so the two cannot drift apart. What differs is
 * deliberate: the scope control, and evidence before the decision.
 */
export function QueueWorkspace() {
  const { flowType = '', flowId = '' } = useParams();
  const navigate = useNavigate();
  const { ready, canUseV2, catalog, error } = useWebCatalog();
  const entry = catalog?.flows.find((candidate) => candidate.flowType === flowType);
  // An inbox opens on what is still open; the control is there to widen it.
  const runQuery = useRunQuery(entry?.definition, { ...EMPTY_RUN_QUERY, status: openFlowStatusLabel() });
  const [role, setRole] = useState('');
  /**
   * The role narrows on the server, beside whatever the reader searched for.
   *
   * A role that cannot be reduced to one filter contributes nothing, so the list stays wide rather
   * than narrowing to a part of the role's work.
   */
  const filters = useMemo(() => {
    const forRole = roleFilter(entry?.definition, role);
    return forRole === null ? runQuery.appliedFilters : [...runQuery.appliedFilters, forRole];
  }, [entry?.definition, role, runQuery.appliedFilters]);
  const search = useFlowSearch(flowType || undefined, entry?.definition, filters);
  const { strandedFlowIDs, rememberStranded } = useStrandedRuns();
  const shellRef = useRef<HTMLDivElement>(null);
  const bodyRef = useRef<HTMLDivElement>(null);
  const listPane = useCollapsibleColumn(LIST_WIDTH_KEY, LIST_WIDTH_DEFAULT);

  if (!ready) return <div className="page-loading">Loading Dex Web…</div>;
  if (!canUseV2) return <Navigate to="/v1/flows" replace />;
  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  if (!catalog) return <div className="page-loading">Loading Dex Web…</div>;
  if (catalog.flows.length === 0) {
    return (
      <div className="v2-shell v2-run">
        <div className="v2-empty">Load a valid Flow Definition Graph 2.0 file to work an inbox in v2.</div>
      </div>
    );
  }
  if (!flowType) return <Navigate to={v2QueuePath(catalog.flows[0].flowType)} replace />;
  if (!entry) return <Navigate to={v2QueuePath()} replace />;

  const selectedFlow = search.flows.find((flow) => flow.flowId === flowId);
  const paneStyle = { '--v2-list-w': `${listPane.width}px` } as CSSProperties;
  return (
    <div className="v2-shell v2-run" ref={shellRef} style={paneStyle}>
      <div className="v2-run-body" ref={bodyRef}>
        <RunList
          collapsed={listPane.isCollapsed}
          emptyText={QUEUE_COPY.clear}
          entry={entry}
          flowTypes={catalog.flows}
          heading={QUEUE_COPY.appName}
          headerNote={QUEUE_COPY.liveNote}
          onExpand={listPane.expand}
          roleControl={rolesOf(entry.definition).length > 0 ? (
            <label className="rsw-role">
              <span className="rsw-zonehead">{QUEUE_COPY.roleLabel}</span>
              <select
                aria-label={QUEUE_COPY.roleLabel}
                value={role}
                onChange={(event) => setRole(event.target.value)}
              >
                <option value="">{QUEUE_COPY.anyRole}</option>
                {rolesOf(entry.definition).map((candidate) => (
                  <option key={candidate} value={candidate}>{candidate}</option>
                ))}
              </select>
              {role !== '' && (
                <small className="rsw-rolenote">
                  {QUEUE_COPY.roleAnswers(roleActionLabels(entry.definition, role))}
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
          onSelectFlowType={(next) => navigate(v2QueuePath(next))}
          onSelectRun={(nextFlowID) => navigate(v2QueuePath(entry.flowType, nextFlowID))}
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
        <section className="v2-inbox-case" aria-label={flowId || QUEUE_COPY.appName}>
          {flowId ? (
            <SelectedRunPanel
              definition={entry.definition}
              flowId={flowId}
              flowStatusCode={selectedFlow?.flowStatusCode}
              flowType={entry.flowType}
              order="evidence-first"
              onStranded={rememberStranded}
              footer={(
                <>
                  <Link className="v2-seemore" to={v2RunPath(entry.flowType, flowId)}>
                    {QUEUE_COPY.seeProcess}
                  </Link>
                  <button
                    aria-label={QUEUE_COPY.close}
                    className="rhd-close"
                    onClick={() => navigate(v2QueuePath(entry.flowType))}
                    type="button"
                  >
                    ✕
                  </button>
                </>
              )}
            />
          ) : (
            <p className="sc-none v2-inbox-empty">{QUEUE_COPY.selectPrompt}</p>
          )}
        </section>
      </div>
    </div>
  );
}
