// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { TimezonePreference } from './format';
import type { FlowHistoryEvent } from './types';

export const TIME_TRAVEL_TYPE = {
  BEGINNING: 1,
  HISTORY_EVENT_TIME: 2,
  STEP_TYPE: 3,
  STEP_EXECUTION_ID: 4,
} as const;

export const TIME_TRAVEL_STEP_METHOD = {
  WAIT_FOR: 1,
  EXECUTE: 2,
} as const;

/** The URL search parameter that selects an earlier run attempt's history. */
export const RUN_ATTEMPT_SEARCH_PARAM = 'runAttempt';

export interface EventTimeTravelTarget {
  stepExecutionId: string;
  stepMethod: number;
}

export function eventTimeTravelTarget(event: FlowHistoryEvent | null): EventTimeTravelTarget | null {
  if (!event) return null;
  const context = event.payload.context as Record<string, unknown> | undefined;
  const stepExecutionId = context?.stepExecutionId;
  if (typeof stepExecutionId !== 'string' || !stepExecutionId) return null;
  if (event.type.startsWith('StepWaitFor')) {
    return { stepExecutionId, stepMethod: TIME_TRAVEL_STEP_METHOD.WAIT_FOR };
  }
  if (event.type.startsWith('StepExecute')) {
    return { stepExecutionId, stepMethod: TIME_TRAVEL_STEP_METHOD.EXECUTE };
  }
  return null;
}

const LOCAL_DATE_TIME_PATTERN = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})(?::(\d{2})(?:\.(\d{1,3}))?)?$/;

/**
 * Converts a `datetime-local` value to an RFC 3339 time in UTC.
 *
 * The input carries no zone, so it is read in the zone the page displays times in.
 * Returns '' for an empty or invalid value.
 */
export function historyEventTimeFromLocalInput(value: string, timezone: TimezonePreference): string {
  const match = LOCAL_DATE_TIME_PATTERN.exec(value.trim());
  if (!match) return '';
  const [year, month, day, hour, minute, second] = match.slice(1, 7).map((part) => Number(part ?? '0'));
  const milliseconds = Number((match[7] ?? '0').padEnd(3, '0'));
  const date = timezone === 'UTC'
    ? new Date(Date.UTC(year, month - 1, day, hour, minute, second, milliseconds))
    : new Date(year, month - 1, day, hour, minute, second, milliseconds);
  if (Number.isNaN(date.getTime())) return '';
  const isSameWallClock = timezone === 'UTC'
    ? date.getUTCMonth() === month - 1 && date.getUTCDate() === day
    : date.getMonth() === month - 1 && date.getDate() === day;
  return isSameWallClock ? date.toISOString() : '';
}

/** Returns a positive run attempt, or 0 when the value is absent or not a positive integer. */
export function positiveRunAttempt(value: unknown): number {
  const attempt = typeof value === 'string' && value.trim() !== '' ? Number(value) : value;
  return typeof attempt === 'number' && Number.isSafeInteger(attempt) && attempt > 0 ? attempt : 0;
}

/** The search string that opens a run attempt's history on the current run page. */
export function runAttemptSearch(runAttempt: number): string {
  return `?${new URLSearchParams({ [RUN_ATTEMPT_SEARCH_PARAM]: String(runAttempt) })}`;
}
