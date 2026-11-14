// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useState } from 'react';
import type { TimezonePreference } from '@/lib/format';
import { statusTone } from '../workspace/statusTone';
import { isOpenFlowStatusCode } from './liveness';

/** A run row's status colour. `waiting` and `active` are open runs; `muted` was stopped. */
export type RunRowTone = 'waiting' | 'active' | 'success' | 'danger' | 'muted';

const COMPLETED_FLOW_STATUS_CODE = 2;
const FAILED_FLOW_STATUS_CODE = 3;
const TIMED_OUT_FLOW_STATUS_CODE = 4;

/** Closed runs tone by execution status; open runs by the status chip's rules, so row and panel agree. */
export function runRowTone(flowStatusCode: number | undefined, statusText: string): RunRowTone {
  if (isOpenFlowStatusCode(flowStatusCode)) {
    const tone = statusTone(statusText);
    if (tone === 'attention') return 'waiting';
    if (tone === 'danger') return 'danger';
    return 'active';
  }
  if (flowStatusCode === COMPLETED_FLOW_STATUS_CODE) return 'success';
  if (flowStatusCode === FAILED_FLOW_STATUS_CODE || flowStatusCode === TIMED_OUT_FLOW_STATUS_CODE) return 'danger';
  return 'muted';
}

/**
 * How long ago a run started, in the fewest characters that still read naturally.
 *
 * Returns "now" under a minute, then minutes, hours, and days up to a week, such as "5m" or "3d".
 * Older or future times return the short calendar date. An empty or invalid time returns "—".
 */
export function formatRelativeAge(value: string | null, nowMs: number, timezone: TimezonePreference): string {
  if (!value) return '—';
  const startMs = new Date(value).getTime();
  if (Number.isNaN(startMs)) return '—';
  const ageSeconds = Math.floor((nowMs - startMs) / 1000);
  if (ageSeconds < -60) return shortDate(startMs, timezone);
  if (ageSeconds < 60) return 'now';
  const minutes = Math.floor(ageSeconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return `${hours}h`;
  const days = Math.floor(hours / 24);
  if (days < 7) return `${days}d`;
  return shortDate(startMs, timezone);
}

function shortDate(ms: number, timezone: TimezonePreference): string {
  return new Intl.DateTimeFormat(undefined, {
    month: 'short',
    day: 'numeric',
    timeZone: timezone === 'UTC' ? 'UTC' : undefined,
  }).format(ms);
}

/** The current time, refreshed every `intervalMs`, so relative ages do not freeze. */
export function useNowMs(intervalMs = 30_000): number {
  const [nowMs, setNowMs] = useState(() => Date.now());
  useEffect(() => {
    const timer = window.setInterval(() => setNowMs(Date.now()), intervalMs);
    return () => window.clearInterval(timer);
  }, [intervalMs]);
  return nowMs;
}
