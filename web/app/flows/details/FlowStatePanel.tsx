// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { useEffect, useRef } from 'react';
import type { FlowHistoryEvent, FlowState, FlowSummary } from '@/lib/types';
import { waitingConditionTypeLabel } from '@/lib/semantic';
import { JsonView } from '../../components/JsonView';
import { EventDetails, eventTitle } from './EventDetails';

type Data = Record<string, unknown>;

function data(value: unknown): Data {
  return value && typeof value === 'object' && !Array.isArray(value) ? value as Data : {};
}

function normalizeWaitingCondition(value: unknown): Data {
  const condition = data(value);
  return {
    ...condition,
    waitingConditionType: waitingConditionTypeLabel(condition.waitingConditionType),
  };
}

function canScroll(element: HTMLElement, deltaY: number): boolean {
  if (deltaY < 0) return element.scrollTop > 0;
  if (deltaY > 0) return element.scrollTop + element.clientHeight < element.scrollHeight;
  return false;
}

function containSidebarWheel(event: WheelEvent) {
  const sidebar = event.currentTarget;
  if (!(sidebar instanceof HTMLElement) || sidebar.scrollHeight <= sidebar.clientHeight) return;
  const nested = event.target instanceof Element
    ? event.target.closest<HTMLElement>('.raw-event-json, .semantic-value pre, .semantic-alert pre, .json-view pre')
    : null;
  if (nested && nested !== sidebar && canScroll(nested, event.deltaY)) return;
  if (Math.abs(event.deltaX) > Math.abs(event.deltaY)) return;
  event.preventDefault();
  event.stopPropagation();
  const multiplier = event.deltaMode === WheelEvent.DOM_DELTA_LINE
    ? 16
    : event.deltaMode === WheelEvent.DOM_DELTA_PAGE
      ? sidebar.clientHeight
      : 1;
  sidebar.scrollTop += event.deltaY * multiplier;
}

export function FlowStatePanel({
  state,
  selectedEvent,
  summary,
  history,
}: {
  state: FlowState | null;
  selectedEvent: FlowHistoryEvent | null;
  summary: FlowSummary | null;
  history: FlowHistoryEvent[];
}) {
  const sidebar = useRef<HTMLDivElement>(null);

  useEffect(() => {
    const element = sidebar.current;
    if (!element) return;
    element.addEventListener('wheel', containSidebarWheel, { passive: false });
    return () => element.removeEventListener('wheel', containSidebarWheel);
  }, []);

  return (
    <div className="sidebar-stack" ref={sidebar}>
      {selectedEvent && (
        <section className="sidebar-section">
          <p className="eyebrow">Selected event</p>
          <h3>{eventTitle(selectedEvent)}</h3>
          <div className="event-meta">
            <span>Event {selectedEvent.eventId}</span>
            <span>{selectedEvent.eventTime || 'No timestamp'}</span>
          </div>
          <EventDetails event={selectedEvent} history={history} />
        </section>
      )}

      <section className="sidebar-section">
        <p className="eyebrow">Live state</p>
        <h3>{state ? 'Interpreter snapshot' : `${summary?.flowStatus || 'Unavailable'}`}</h3>
        {!state && <p className="muted">Live state is queried only while this run is active.</p>}
        {state?.activeStepExecutions.map((step) => (
          <div className="active-step-card" key={step.stepExecutionId}>
            <div>
              <b>{step.stepType}</b>
              <span className={`phase phase-${step.phase.toLowerCase()}`}>{step.phase}</span>
            </div>
            <code>{step.stepExecutionId}</code>
            <span>From {step.fromStepExecutionId || '—'}</span>
            {step.waitingCondition && Object.keys(step.waitingCondition).length > 0 && (
              <JsonView value={normalizeWaitingCondition(step.waitingCondition)} label="Waiting condition" />
            )}
            {step.timers.length > 0 && <JsonView value={step.timers} label={`${step.timers.length} timers`} />}
          </div>
        ))}
        {state && state.activeStepExecutions.length === 0 && (
          <p className="muted">No active step executions.</p>
        )}
      </section>

      {state && (
        <>
          <section className="sidebar-section">
            <p className="eyebrow">Attributes</p>
            <h3>{state.attributes.length} values</h3>
            <JsonView value={state.attributes} label="Attributes" />
          </section>
          <section className="sidebar-section">
            <p className="eyebrow">Queues & channels</p>
            <JsonView value={state.queuedSteps} label={`${state.queuedSteps.length} queued steps`} />
            <JsonView value={state.pendingChannelMessages} label="Pending channel messages" />
            <JsonView value={state.completedSteps} label="Completed outputs" />
          </section>
        </>
      )}
    </div>
  );
}
