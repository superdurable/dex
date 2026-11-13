// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { Activity, ArrowRight, CircleCheck, CircleX } from 'lucide-react';
import type { FlowHistoryEvent } from '@/lib/types';
import { eventTitle } from './EventDetails';

/** Why a closed run failed, and the event to read first. */
export interface RunFailure {
  message: string;
  eventId: number;
}

const FAILED_STEP_EVENT_TYPES = new Set(['StepExecuteFailed', 'StepWaitForFailed']);

/** Returns the failure of a run whose newest events hold a FlowClosed with an error, or null. */
export function runFailureOf(events: readonly FlowHistoryEvent[]): RunFailure | null {
  const closed = events.findLast((event) => event.type === 'FlowClosed');
  const message = typeof closed?.payload.errorMessage === 'string' ? closed.payload.errorMessage.trim() : '';
  if (!closed || !message) return null;
  const failedStep = events.findLast((event) => FAILED_STEP_EVENT_TYPES.has(event.type));
  return { message, eventId: failedStep?.eventId ?? closed.eventId };
}

/** One line under the bar that says why the run failed and opens the failing event. */
export function RunFailureLine({
  failure,
  onJumpToEvent,
}: {
  failure: RunFailure;
  onJumpToEvent: (eventId: number) => void;
}) {
  return (
    <div className="run-failure-line" role="alert">
      <CircleX aria-hidden="true" size={16} strokeWidth={2.25} />
      <span className="run-failure-message">{failure.message}</span>
      <button className="run-failure-jump" onClick={() => onJumpToEvent(failure.eventId)} type="button">
        Event #{failure.eventId}
        <ArrowRight aria-hidden="true" size={14} strokeWidth={2.25} />
      </button>
    </div>
  );
}

/** What an open run is doing, or how a closed run ended, and the event that says so. */
export interface RunState {
  isOpen: boolean;
  title: string;
  stepType: string;
  eventId: number;
}

/** Returns the newest event of a run that did not fail, described for the state line; null without events. */
export function runStateOf(events: readonly FlowHistoryEvent[], isOpen: boolean): RunState | null {
  const latest = newestOf(events);
  if (!latest) return null;
  const lastStep = newestOf(events.filter((event) => typeof contextOf(event).stepType === 'string'));
  const stepType = lastStep ? String(contextOf(lastStep).stepType) : '';
  return { isOpen, title: eventTitle(latest), stepType, eventId: latest.eventId };
}

function newestOf(events: readonly FlowHistoryEvent[]): FlowHistoryEvent | null {
  return events.reduce<FlowHistoryEvent | null>(
    (newest, event) => (newest === null || event.eventId > newest.eventId ? event : newest),
    null,
  );
}

function contextOf(event: FlowHistoryEvent): Record<string, unknown> {
  const context = event.payload.context;
  return context && typeof context === 'object' ? context as Record<string, unknown> : {};
}

/** The same line for runs that did not fail: the newest event of an open run, or how it closed. */
export function RunStateLine({ state, onJumpToEvent }: { state: RunState; onJumpToEvent: (eventId: number) => void }) {
  const Icon = state.isOpen ? Activity : CircleCheck;
  return (
    <div className={`run-state-line${state.isOpen ? ' is-open' : ''}`}>
      <Icon aria-hidden="true" size={16} strokeWidth={2.25} />
      <span className="run-state-label">{state.isOpen ? 'Now' : 'Ended'}</span>
      <span className="run-state-message">
        {state.title}
        {state.stepType && <span className="mono"> · {state.stepType}</span>}
      </span>
      <button className="run-failure-jump" onClick={() => onJumpToEvent(state.eventId)} type="button">
        Event #{state.eventId}
        <ArrowRight aria-hidden="true" size={14} strokeWidth={2.25} />
      </button>
    </div>
  );
}
