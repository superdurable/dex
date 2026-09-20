// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Link, Navigate, useNavigate, useParams } from 'react-router-dom';
import { v2QueuePath, v2RunPath } from '../contract';
import '../css/v2.css';
import { useWebCatalog } from '../WebCatalogProvider';
import { FilterBuilder } from '../workspace/FilterBuilder';
import { describeFilters, newFilterRow } from '../workspace/filters';
import { RunList } from '../workspace/RunList';
import { SelectedRunPanel } from '../workspace/SelectedRunPanel';
import { useFlowSearch } from '../workspace/useFlowSearch';
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
      <div className="v2-shell v2-run">
        <div className="v2-empty">Load a valid Flow Definition Graph 2.0 file to work an inbox in v2.</div>
      </div>
    );
  }
  if (!flowType) return <Navigate to={v2QueuePath(catalog.flows[0].flowType)} replace />;
  if (!entry) return <Navigate to={v2QueuePath()} replace />;

  const selectedFlow = search.flows.find((flow) => flow.flowId === flowId);
  const clauses = describeFilters(search.filters, entry.definition);
  return (
    <div className="v2-shell v2-run">
      <div className="v2-run-body" data-has-run={flowId ? 'true' : undefined}>
        <RunList
          attentionAttributeKey={entry.definition.indexedAttributes[0]?.attributeKey ?? null}
          emptyText={QUEUE_COPY.clear}
          entry={entry}
          flowTypes={catalog.flows}
          heading={QUEUE_COPY.appName}
          headerNote={QUEUE_COPY.liveNote}
          scope={(
            <FilterBuilder
              definition={entry.definition}
              search={search}
              summary={QUEUE_COPY.scope(clauses)}
            />
          )}
          search={search}
          selectedFlowID={flowId}
          strandedFlowIDs={strandedFlowIDs}
          onSelectFlowType={(next) => navigate(v2QueuePath(next))}
          onSelectRun={(nextFlowID) => navigate(v2QueuePath(entry.flowType, nextFlowID))}
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
