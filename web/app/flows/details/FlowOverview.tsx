// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { useState } from 'react';
import { eventTimeTravelTarget } from '@/lib/timeTravel';
import { displayEventNumber } from '@/lib/timeline';
import type { FlowHistoryEvent, FlowState, FlowSummary } from '@/lib/types';
import { JsonView } from '../../components/JsonView';
import { EventDetails, eventTitle, FailureContent, SemanticEventDetails } from './EventDetails';

const sectionExpandByKey = new Map<string, boolean>();

function useSectionExpand(persistKey: string) {
  const [expanded, setExpanded] = useState(() => sectionExpandByKey.get(persistKey) ?? false);
  const [collapseNonce, setCollapseNonce] = useState(0);
  return {
    expanded,
    collapseNonce,
    setExpanded: (next: boolean) => {
      sectionExpandByKey.set(persistKey, next);
      setExpanded(next);
      if (!next) setCollapseNonce((current) => current + 1);
    },
  };
}

function SectionExpandToggle({
  expanded,
  onChange,
  label,
}: {
  expanded: boolean;
  onChange: (next: boolean) => void;
  label: string;
}) {
  return (
    <label className="section-expand-toggle">
      <input
        type="checkbox"
        checked={expanded}
        onChange={(event) => onChange(event.target.checked)}
      />
      {expanded ? 'Collapse all' : 'Expand all'}
      <span className="sr-only">{label}</span>
    </label>
  );
}

export function FlowOverview({
  summary,
  events,
  state,
  selectedEvent,
  onTimeTravel,
}: {
  summary: FlowSummary;
  events: FlowHistoryEvent[];
  state: FlowState | null;
  selectedEvent: FlowHistoryEvent | null;
  onTimeTravel: (event: FlowHistoryEvent) => void;
}) {
  const started = events.find((event) => event.type === 'FlowStartedOrContinued');
  const closed = events.findLast((event) => event.type === 'FlowClosed');
  const startKind = started?.payload.initialStart ? 'Initial start' : 'Continued run';
  const [collapsedActiveStepIds, setCollapsedActiveStepIds] = useState<Set<string>>(
    () => new Set(),
  );
  const attributesExpand = useSectionExpand('overview:expand:attributes');
  const channelsExpand = useSectionExpand('overview:expand:channels');
  const activeStepIds = state?.activeStepExecutions.map((step) => step.stepExecutionId) ?? [];
  const allActiveStepsExpanded = activeStepIds.length > 0
    && activeStepIds.every((stepExecutionId) => !collapsedActiveStepIds.has(stepExecutionId));

  const setAllActiveStepsExpanded = (expanded: boolean) => {
    setCollapsedActiveStepIds((current) => {
      const next = new Set(current);
      for (const stepExecutionId of activeStepIds) {
        if (expanded) next.delete(stepExecutionId);
        else next.add(stepExecutionId);
      }
      return next;
    });
  };

  const toggleActiveStep = (stepExecutionId: string) => {
    setCollapsedActiveStepIds((current) => {
      const next = new Set(current);
      if (next.has(stepExecutionId)) next.delete(stepExecutionId);
      else next.add(stepExecutionId);
      return next;
    });
  };

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
                <div className="live-state-block-heading">
                  <p className="eyebrow">Active steps</p>
                  <SectionExpandToggle
                    expanded={allActiveStepsExpanded}
                    onChange={setAllActiveStepsExpanded}
                    label="active steps"
                  />
                </div>
                {state.activeStepExecutions.map((step) => (
                  <details
                    className="active-step-card"
                    key={step.stepExecutionId}
                    open={!collapsedActiveStepIds.has(step.stepExecutionId)}
                  >
                    <summary onClick={(event) => {
                      event.preventDefault();
                      toggleActiveStep(step.stepExecutionId);
                    }}>
                      <span className="active-step-heading">
                        <span className="active-step-disclosure" aria-hidden="true">▸</span>
                        <b>{step.stepType}</b>
                      </span>
                      <span className={`phase phase-${step.phase.toLowerCase()}`}>{step.phase}</span>
                    </summary>
                    <div className="active-step-content">
                      <code>{step.stepExecutionId}</code>
                      <span>From {step.fromStepExecutionId || '—'}</span>
                      {step.waitingCondition && Object.keys(step.waitingCondition).length > 0 && (
                        <JsonView
                          value={step.waitingCondition}
                          label="Waiting condition"
                          persistKey={`overview:active-step:${step.stepType}:waiting-condition`}
                          parentFlowId={summary.flowId}
                          stepExecutionId={step.stepExecutionId}
                        />
                      )}
                      {step.timers.length > 0 && (
                        <JsonView
                          value={step.timers}
                          label={`${step.timers.length} timers`}
                          persistKey={`overview:active-step:${step.stepType}:timers`}
                        />
                      )}
                      {step.lastFailureInfo && Object.keys(step.lastFailureInfo).length > 0 && (
                        <div className="semantic-subsection">
                          <h5>Last failure</h5>
                          {/* Pending backend attempts exclude local fallback attempts, so hide them until cumulative values are available. */}
                          <FailureContent value={step.lastFailureInfo} stackInitiallyExpanded showAttempt={false} />
                        </div>
                      )}
                    </div>
                  </details>
                ))}
                {state.activeStepExecutions.length === 0 && (
                  <p className="muted">No active step executions.</p>
                )}
              </div>

              <div className="live-state-block">
                <div className="live-state-block-heading">
                  <div>
                    <p className="eyebrow">Attributes</p>
                    <h3>{state.attributes.length} values</h3>
                  </div>
                  <SectionExpandToggle
                    expanded={attributesExpand.expanded}
                    onChange={attributesExpand.setExpanded}
                    label="attributes"
                  />
                </div>
                <JsonView
                  value={state.attributes}
                  label="Attributes"
                  persistKey="overview:attributes"
                  forceOpen={attributesExpand.expanded || undefined}
                  collapseNonce={attributesExpand.collapseNonce}
                />
              </div>

              <div className="live-state-block">
                <div className="live-state-block-heading">
                  <p className="eyebrow">Channels & Others Internals</p>
                  <SectionExpandToggle
                    expanded={channelsExpand.expanded}
                    onChange={channelsExpand.setExpanded}
                    label="channels and other internals"
                  />
                </div>
                <JsonView
                  value={state.queuedSteps}
                  label={`${state.queuedSteps.length} queued steps`}
                  persistKey="overview:queued-steps"
                  forceOpen={channelsExpand.expanded || undefined}
                  collapseNonce={channelsExpand.collapseNonce}
                />
                <JsonView
                  value={state.pendingChannelMessages}
                  label="Pending channel messages"
                  persistKey="overview:pending-channels"
                  forceOpen={channelsExpand.expanded || undefined}
                  collapseNonce={channelsExpand.collapseNonce}
                />
                <JsonView
                  value={state.completedSteps}
                  label="Completed outputs"
                  persistKey="overview:completed-outputs"
                  forceOpen={channelsExpand.expanded || undefined}
                  collapseNonce={channelsExpand.collapseNonce}
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
              <SemanticEventDetails
                event={started}
                parentFlowId={summary.flowId}
                showStartHeading={false}
              />
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
                  <span>Event {displayEventNumber(events, selectedEvent)}</span>
                  <span>{selectedEvent.eventTime || 'No timestamp'}</span>
                </div>
                {eventTimeTravelTarget(selectedEvent) && (
                  <button
                    className="button ghost selected-event-time-travel"
                    onClick={() => onTimeTravel(selectedEvent)}
                  >
                    Time travel here
                  </button>
                )}
              </header>
              <div className="overview-selected-event-body">
                <EventDetails
                  event={selectedEvent}
                  history={events}
                  parentFlowId={summary.flowId}
                />
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
