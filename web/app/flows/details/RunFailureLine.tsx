// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ArrowRight, CircleX } from 'lucide-react';
import { formatTimelineOffset } from '@/lib/timeline';
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

/** Why a failed run failed, for the run line. */
export function RunFailureBody({ failure }: { failure: RunFailure }) {
  return (
    <>
      <CircleX aria-hidden="true" size={16} strokeWidth={2.25} />
      <span className="run-failure-message">{failure.message}</span>
    </>
  );
}

/** The run line's button that selects the event it names. */
export function RunLineJump({ eventId, onJumpToEvent }: { eventId: number; onJumpToEvent: (eventId: number) => void }) {
  return (
    <button className="run-failure-jump" onClick={() => onJumpToEvent(eventId)} type="button">
      Event #{eventId}
      <ArrowRight aria-hidden="true" size={14} strokeWidth={2.25} />
    </button>
  );
}

/** What an open run is doing, or how a closed run ended, and the event that says so. */
export interface RunState {
  isOpen: boolean;
  title: string;
  stepType: string;
  eventId: number;
  /** The soonest timer the newest WaitFor set, when the run waits on one. */
  timer?: { conditionId: string; firesAtMs: number };
}

/** Returns the newest event of a run that did not fail, described for the state line; null without events. */
export function runStateOf(events: readonly FlowHistoryEvent[], isOpen: boolean): RunState | null {
  const latest = newestOf(events);
  if (!latest) return null;
  const lastStep = newestOf(events.filter((event) => typeof contextOf(event).stepType === 'string'));
  const stepType = lastStep ? String(contextOf(lastStep).stepType) : '';
  const timer = isOpen ? soonestTimerOf(latest) : undefined;
  return { isOpen, title: eventTitle(latest), stepType, eventId: latest.eventId, ...(timer ? { timer } : {}) };
}

function soonestTimerOf(event: FlowHistoryEvent): RunState['timer'] {
  if (event.type !== 'StepWaitForCompleted' || !event.eventTime) return undefined;
  const output = event.payload.output as Record<string, unknown> | undefined;
  const condition = output?.waitForCondition as Record<string, unknown> | undefined;
  const timers = Array.isArray(condition?.timerConditions) ? condition.timerConditions as Record<string, unknown>[] : [];
  const startMs = Date.parse(event.eventTime);
  let soonest: RunState['timer'];
  for (const timer of timers) {
    const seconds = Number(timer.durationSeconds);
    if (!Number.isFinite(seconds) || !Number.isFinite(startMs)) continue;
    const firesAtMs = startMs + seconds * 1000;
    if (!soonest || firesAtMs < soonest.firesAtMs) soonest = { conditionId: String(timer.conditionId ?? ''), firesAtMs };
  }
  return soonest;
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

/** A few words on what the run does now, such as "Now · waiting · fires in 2d 6h". */
export function runStateShortLabel(state: RunState, nowMs: number): string {
  if (!state.isOpen) return 'Ended';
  const doing = state.title === 'Waiting for conditions' ? 'waiting' : state.title.toLowerCase();
  if (!state.timer) return `Now · ${doing}`;
  return state.timer.firesAtMs > nowMs
    ? `Now · ${doing} · fires in ${formatTimelineOffset((state.timer.firesAtMs - nowMs) / 1000)}`
    : `Now · ${doing} · timer due`;
}
