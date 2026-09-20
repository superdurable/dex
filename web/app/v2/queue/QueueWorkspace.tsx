// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link, Navigate, useNavigate, useParams } from 'react-router-dom';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { v2QueuePath, v2RunPath } from '../contract';
import '../css/v2.css';
import { useWebCatalog } from '../WebCatalogProvider';
import { FlowListing } from '../workspace/FlowListing';
import { describeFilters, newFilterRow } from '../workspace/filters';
import { SelectedRunPanel } from '../workspace/SelectedRunPanel';
import { useFlowSearch } from '../workspace/useFlowSearch';
import { useStrandedRuns } from '../workspace/useStrandedRuns';
import { QUEUE_COPY } from './copy';
import { openFlowStatusLabel } from './liveness';

/**
 * Clearing a queue does not need the shape of the process, so this mode draws no
 * canvas. "See the process" opens the same run in Run mode.
 *
 * The open-work filter is a real server-side filter row rather than a client-side drop, so
 * paging stays correct and the reader can see and change what was narrowed.
 */
export function QueueWorkspace() {
  const { flowType = '', flowId = '' } = useParams();
  const navigate = useNavigate();
  const { ready, canUseV2, catalog, error } = useWebCatalog();
  const entry = catalog?.flows.find((candidate) => candidate.flowType === flowType);
  const search = useFlowSearch(flowType || undefined, entry?.definition, [
    newFilterRow('executionStatus', 'eq', openFlowStatusLabel()),
  ]);
  const { strandedFlowIDs, rememberStranded } = useStrandedRuns();

  if (!ready) return <div className="page-loading">Loading Dex Web…</div>;
  if (!canUseV2) return <Navigate to="/v1/flows" replace />;
  if (error) return <div className="page-shell"><div className="error-banner">{error}</div></div>;
  if (!catalog) return <div className="page-loading">Loading Dex Web…</div>;
  if (catalog.flows.length === 0) {
    return (
      <div className="v2-shell sv">
        <div className="v2-empty">Load a valid Flow Definition Graph 2.0 file to work a queue in v2.</div>
      </div>
    );
  }
  if (!flowType) return <Navigate to={v2QueuePath(catalog.flows[0].flowType)} replace />;
  if (!entry) return <Navigate to={v2QueuePath()} replace />;

  const selectedFlow = search.flows.find((flow) => flow.flowId === flowId);
  return (
    <div className="v2-shell sv v2-queue">
      <header className="sv-head">
        <h1 className="sv-name">{QUEUE_COPY.appName}</h1>
        <p className="sv-strap">{QUEUE_COPY.strapline}</p>
        <p className="sv-nograph">{QUEUE_COPY.noGraph}</p>
      </header>
      <div className="sv-body">
        <aside className="sq" data-has-case={flowId ? 'true' : undefined}>
          <FlowListing
            entry={entry}
            flowTypes={catalog.flows}
            headerNote="live — read from a running process"
            scope={<QueueScope definition={entry.definition} search={search} />}
            search={search}
            selectedFlowID={flowId}
            strandedFlowIDs={strandedFlowIDs}
            onSelectFlowType={(next) => navigate(v2QueuePath(next))}
            onSelectRun={(nextFlowID) => navigate(v2QueuePath(entry.flowType, nextFlowID))}
          />
        </aside>
        {flowId ? (
          <SelectedRunPanel
            definition={entry.definition}
            flowId={flowId}
            flowStatusCode={selectedFlow?.flowStatusCode}
            flowType={entry.flowType}
            order="evidence-first"
            onStranded={rememberStranded}
            footer={(
              <Link className="v2-seemore" to={v2RunPath(entry.flowType, flowId)}>
                {QUEUE_COPY.seeProcess}
              </Link>
            )}
          />
        ) : (
          <p className="sc-none">{QUEUE_COPY.selectPrompt}</p>
        )}
      </div>
    </div>
  );
}

/** Four states, never collapsed: an empty page and an unreachable process call for opposite actions. */
function QueueScope({
  definition,
  search,
}: {
  definition: FlowV2Definition;
  search: ReturnType<typeof useFlowSearch>;
}) {
  const { liveness, flows } = search;
  const clauses = describeFilters(search.filters, definition);
  const headline = liveness === 'loading'
    ? QUEUE_COPY.loading
    : liveness === 'unreachable'
      ? QUEUE_COPY.unreachable
      : liveness === 'stale'
        ? QUEUE_COPY.stale
        : flows.length === 0
          ? QUEUE_COPY.clear
          : QUEUE_COPY.onThisPage(flows.length);
  return (
    <p className="sq-state" data-liveness={liveness}>
      {headline}
      <span className="sq-why">{QUEUE_COPY.scope(clauses)}</span>
      {clauses.length === 0 && <span className="sq-why">{QUEUE_COPY.unfilteredHint}</span>}
      <span className="sq-why">{QUEUE_COPY.actionsProvenance}</span>
    </p>
  );
}
