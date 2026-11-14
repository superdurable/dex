// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { scopedStorageKey } from '@/lib/basePath';

/** A run this browser opened, newest first in the list. */
export interface RecentRun {
  flowId: string;
  runId: string;
  flowType: string;
  flowStatus: string;
}

const RECENT_RUNS_KEY = 'dex-web.recent-runs';
const RECENT_RUN_LIMIT = 8;

export function readRecentRuns(): RecentRun[] {
  try {
    const parsed: unknown = JSON.parse(window.localStorage.getItem(scopedStorageKey(RECENT_RUNS_KEY)) ?? '[]');
    return Array.isArray(parsed) ? parsed.filter((run): run is RecentRun => typeof run?.flowId === 'string') : [];
  } catch {
    return [];
  }
}

/** Puts a run first in this browser's recent runs. */
export function rememberRecentRun(run: RecentRun): void {
  try {
    const next = [run, ...readRecentRuns().filter((item) => item.flowId !== run.flowId)].slice(0, RECENT_RUN_LIMIT);
    window.localStorage.setItem(scopedStorageKey(RECENT_RUNS_KEY), JSON.stringify(next));
  } catch {
    // Without storage the jump box offers no recent runs.
  }
}
