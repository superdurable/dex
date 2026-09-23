// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useState } from 'react';
import { Link, useParams } from 'react-router-dom';
import { RunDetailsPage } from '@/app/flows/RunDetailsPage';
import { readResponseJSON } from '@/lib/http';
import type { FlowSummary } from '@/lib/types';
import { dexFetch } from '@/lib/webConfig';
import { v2DebugPath, v2RunPath } from '../contract';
import '../css/v2.css';
import '../css/debug.css';
import { DEBUG_COPY } from './copy';

/**
 * The Deep Dive: v1's detail components, unchanged, inside the v2 shell.
 *
 * Its own runId, because Time Travel and continue-as-new navigate the run chain and the
 * Run view only ever shows the current run.
 */
export function DebugWorkspace() {
  const { flowType = '', flowId = '', runId = '' } = useParams();
  const [resolvedRunID, setResolvedRunID] = useState(runId);
  const [error, setError] = useState('');

  useEffect(() => {
    if (runId) {
      setResolvedRunID(runId);
      return undefined;
    }
    const controller = new AbortController();
    void dexFetch(`/api/flows/summary?flowId=${encodeURIComponent(flowId)}`, {
      signal: controller.signal,
    })
      .then((response) => readResponseJSON<FlowSummary>(response))
      .then((summary) => setResolvedRunID(summary.runId))
      .catch((loadError: unknown) => {
        if (!controller.signal.aborted) {
          setError(loadError instanceof Error ? loadError.message : DEBUG_COPY.runUnresolved);
        }
      });
    return () => controller.abort();
  }, [flowId, runId]);

  return (
    <div className="v2-shell v2-debug">
      <header className="v2-debug-head">
        <Link className="v2-debug-back" to={v2RunPath(flowType, flowId)}>{DEBUG_COPY.back}</Link>
        <span className="v2-debug-label">{DEBUG_COPY.label}</span>
        <span className="v2-debug-note">{DEBUG_COPY.note}</span>
      </header>
      {error && <div className="error-banner">{error}</div>}
      {!error && !resolvedRunID && <div className="page-loading">{DEBUG_COPY.resolving}</div>}
      {resolvedRunID && (
        <RunDetailsPage
          breadcrumb={(
            <div className="breadcrumbs">
              <Link to={v2RunPath(flowType)}>{flowType}</Link><span>/</span>
              <Link to={v2RunPath(flowType, flowId)}>{flowId}</Link><span>/</span>
              <span className="mono">{resolvedRunID}</span>
            </div>
          )}
          flowId={flowId}
          runId={resolvedRunID}
          runPath={(chainRunID) => v2DebugPath(flowType, flowId, chainRunID)}
        />
      )}
    </div>
  );
}
