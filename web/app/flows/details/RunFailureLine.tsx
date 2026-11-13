// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { ArrowRight, CircleX } from 'lucide-react';
import type { FlowHistoryEvent } from '@/lib/types';

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
