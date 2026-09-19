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
import { FlowListing } from '../workspace/FlowListing';
import { SelectedRunPanel } from '../workspace/SelectedRunPanel';
import { useFlowSearch } from '../workspace/useFlowSearch';

/**
 * Clearing a queue does not need the shape of the process, so this mode draws no
 * canvas. "See the process" opens the same run in Run mode.
 */
export function QueueWorkspace() {
  const { flowType = '', flowId = '' } = useParams();
  const navigate = useNavigate();
  const { ready, canUseV2, catalog, error } = useWebCatalog();
  const entry = catalog?.flows.find((candidate) => candidate.flowType === flowType);
  const search = useFlowSearch(flowType || undefined, entry?.definition);

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

  return (
    <div className="v2-shell sv v2-queue">
      <header className="sv-head">
        <h1 className="sv-name">Work queue</h1>
        <p className="sv-strap">What needs a person, read from the process itself.</p>
        <p className="sv-nograph">
          No process diagram here by design: this view shows the work, not the shape of the process.
        </p>
      </header>
      <div className="sv-body">
        <aside className="sq" data-has-case={flowId ? 'true' : undefined}>
          <FlowListing
            entry={entry}
            flowTypes={catalog.flows}
            headerNote="live — read from a running process"
            search={search}
            selectedFlowID={flowId}
            onSelectFlowType={(next) => navigate(v2QueuePath(next))}
            onSelectRun={(nextFlowID) => navigate(v2QueuePath(entry.flowType, nextFlowID))}
          />
        </aside>
        {flowId ? (
          <SelectedRunPanel
            definition={entry.definition}
            flowId={flowId}
            flowType={entry.flowType}
            footer={(
              <Link className="v2-seemore" to={v2RunPath(entry.flowType, flowId)}>
                See the process
              </Link>
            )}
          />
        ) : (
          <p className="sc-none">Select an item to see what it turns on and decide it.</p>
        )}
      </div>
    </div>
  );
}
