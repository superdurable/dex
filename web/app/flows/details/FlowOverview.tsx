// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { FlowHistoryEvent, FlowState, FlowSummary } from '@/lib/types';
import { JsonView } from '../../components/JsonView';
import { EventDetails, eventTitle, SemanticEventDetails } from './EventDetails';

export function FlowOverview({
  summary,
  events,
  state,
  selectedEvent,
}: {
  summary: FlowSummary;
  events: FlowHistoryEvent[];
  state: FlowState | null;
  selectedEvent: FlowHistoryEvent | null;
}) {
  const started = events.find((event) => event.type === 'FlowStartedOrContinued');
  const closed = events.findLast((event) => event.type === 'FlowClosed');
  const startKind = started?.payload.initialStart ? 'Initial start' : 'Continued run';
  return (
    <div className="overview-grid">
      <div className="overview-column">
        <section className="card overview-card overview-live-state">
          <div className="section-heading">
            <div>
              <p className="eyebrow">{state ? 'Current' : summary.flowStatus || 'Unavailable'}</p>
              <h2>Live Flow State</h2>
            </div>
          </div>
          {!state && <p className="muted">Live state is queried only while this run is active.</p>}
          {state && (
            <>
              <div className="metric-grid">
                <div><span>Attributes</span><b>{state.attributes.length}</b></div>
                <div><span>Active steps</span><b>{state.activeStepExecutions.length}</b></div>
                <div><span>Queued steps</span><b>{state.queuedSteps.length}</b></div>
                <div><span>Completed outputs</span><b>{state.completedSteps.length}</b></div>
                <div>
                  <span>Pending channels</span>
                  <b>{Object.keys(state.pendingChannelMessages).length}</b>
                </div>
              </div>

              <div className="live-state-block">
                <p className="eyebrow">Active steps</p>
                {state.activeStepExecutions.map((step) => (
                  <div className="active-step-card" key={step.stepExecutionId}>
                    <div>
                      <b>{step.stepType}</b>
                      <span className={`phase phase-${step.phase.toLowerCase()}`}>{step.phase}</span>
                    </div>
                    <code>{step.stepExecutionId}</code>
                    <span>From {step.fromStepExecutionId || '—'}</span>
                    {step.waitingCondition && Object.keys(step.waitingCondition).length > 0 && (
                      <JsonView
                        value={step.waitingCondition}
                        label="Waiting condition"
                        persistKey={`overview:active-step:${step.stepExecutionId}:waiting-condition`}
                      />
                    )}
                    {step.timers.length > 0 && (
                      <JsonView
                        value={step.timers}
                        label={`${step.timers.length} timers`}
                        persistKey={`overview:active-step:${step.stepExecutionId}:timers`}
                      />
                    )}
                  </div>
                ))}
                {state.activeStepExecutions.length === 0 && (
                  <p className="muted">No active step executions.</p>
                )}
              </div>

              <div className="live-state-block">
                <p className="eyebrow">Attributes</p>
                <h3>{state.attributes.length} values</h3>
                <JsonView
                  value={state.attributes}
                  label="Attributes"
                  persistKey="overview:attributes"
                />
              </div>

              <div className="live-state-block">
                <p className="eyebrow">Queues & channels</p>
                <JsonView
                  value={state.queuedSteps}
                  label={`${state.queuedSteps.length} queued steps`}
                  persistKey="overview:queued-steps"
                />
                <JsonView
                  value={state.pendingChannelMessages}
                  label="Pending channel messages"
                  persistKey="overview:pending-channels"
                />
                <JsonView
                  value={state.completedSteps}
                  label="Completed outputs"
                  persistKey="overview:completed-outputs"
                />
              </div>

              {state.flowConfig && (
                <JsonView
                  value={state.flowConfig}
                  label="Flow config"
                  persistKey="overview:flow-config"
                />
              )}
            </>
          )}
          {closed && (
            <JsonView
              value={closed.payload.results ?? []}
              label="Close result"
              persistKey="overview:close-result"
            />
          )}
        </section>

        <section className="card overview-card">
          <div className="section-heading">
            <div><p className="eyebrow">Run input</p><h2>{startKind}</h2></div>
          </div>
          {started ? (
            <div className="semantic-event">
              <SemanticEventDetails event={started} showStartHeading={false} />
            </div>
          ) : (
            <p className="muted">The start event is not in the loaded history page.</p>
          )}
        </section>
      </div>

      <div className="overview-column">
        <section className="card overview-card overview-selected-event">
          {selectedEvent ? (
            <>
              <header className="overview-selected-event-header" data-selected-event-target>
                <p className="eyebrow">Selected event</p>
                <h2>{eventTitle(selectedEvent)}</h2>
                <div className="event-meta">
                  <span>Event {selectedEvent.eventId}</span>
                  <span>{selectedEvent.eventTime || 'No timestamp'}</span>
                </div>
              </header>
              <div className="overview-selected-event-body">
                <EventDetails event={selectedEvent} history={events} />
              </div>
            </>
          ) : (
            <>
              <div className="section-heading">
                <div><p className="eyebrow">Selected event</p><h2>None</h2></div>
              </div>
              <p className="muted">Select an event from Timeline or Step graph.</p>
            </>
          )}
        </section>

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
      </div>
    </div>
  );
}
