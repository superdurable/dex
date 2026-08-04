// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { FlowHistoryEvent, FlowState, FlowSummary } from '@/lib/types';
import { JsonView } from '../../components/JsonView';

export function FlowOverview({
  summary,
  events,
  state,
}: {
  summary: FlowSummary;
  events: FlowHistoryEvent[];
  state: FlowState | null;
}) {
  const started = events.find((event) => event.type === 'FlowStartedOrContinued');
  const closed = events.findLast((event) => event.type === 'FlowClosed');
  const startKind = started?.payload.initialStart ? 'Initial start' : 'Continued run';
  return (
    <div className="overview-grid">
      <section className="card overview-card">
        <div className="section-heading">
          <div><p className="eyebrow">Identity</p><h2>Execution</h2></div>
        </div>
        <dl className="definition-list">
          <div><dt>Flow ID</dt><dd className="mono">{summary.flowId}</dd></div>
          <div><dt>Run ID</dt><dd className="mono">{summary.runId}</dd></div>
          <div><dt>First run ID</dt><dd className="mono">{summary.firstRunId || '—'}</dd></div>
          <div><dt>Request ID</dt><dd className="mono">{summary.requestId || '—'}</dd></div>
          <div><dt>Flow type</dt><dd>{summary.flowType || '—'}</dd></div>
        </dl>
      </section>

      <section className="card overview-card">
        <div className="section-heading">
          <div><p className="eyebrow">Run input</p><h2>{startKind}</h2></div>
        </div>
        {started ? (
          <JsonView value={started.payload} label="Start payload" initiallyOpen />
        ) : (
          <p className="muted">The start event is not in the loaded history page.</p>
        )}
      </section>

      <section className="card overview-card wide">
        <div className="section-heading">
          <div><p className="eyebrow">Interpreter</p><h2>Flow state</h2></div>
        </div>
        <div className="metric-grid">
          <div><span>Attributes</span><b>{state?.attributes.length ?? 0}</b></div>
          <div><span>Active steps</span><b>{state?.activeStepExecutions.length ?? 0}</b></div>
          <div><span>Queued steps</span><b>{state?.queuedSteps.length ?? 0}</b></div>
          <div><span>Completed outputs</span><b>{state?.completedSteps.length ?? 0}</b></div>
          <div>
            <span>Pending channels</span>
            <b>{Object.keys(state?.pendingChannelMessages ?? {}).length}</b>
          </div>
        </div>
        {state?.flowConfig && <JsonView value={state.flowConfig} label="Flow config" />}
        {closed && <JsonView value={closed.payload} label="Close result" />}
      </section>
    </div>
  );
}
