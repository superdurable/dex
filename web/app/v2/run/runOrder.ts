// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { V2Flow } from '@/lib/types';
import { isOpenFlowStatusCode } from '../work-queue/liveness';

export type RunGroupKey = 'open' | 'closed';

export interface RunGroup {
  key: RunGroupKey;
  label: string;
  flows: V2Flow[];
}

const GROUP_LABEL: Record<RunGroupKey, string> = { open: 'Open', closed: 'Closed' };

/**
 * Open runs first, then the server's own newest-first order within each group.
 *
 * A stable partition, so the order is never invented: /api/v2/search already sorts by start
 * time descending, and status is read from the run rather than inferred. Which run most needs
 * somebody cannot be ranked until the contract declares a role.
 *
 * Per page only, which is honest here because Run mode has no pager.
 */
export function groupRuns(flows: readonly V2Flow[]): RunGroup[] {
  const open: V2Flow[] = [];
  const closed: V2Flow[] = [];
  for (const flow of flows) {
    (isOpenFlowStatusCode(flow.flowStatusCode) ? open : closed).push(flow);
  }
  return ([['open', open], ['closed', closed]] as const)
    .filter(([, group]) => group.length > 0)
    .map(([key, group]) => ({ key, label: GROUP_LABEL[key], flows: group }));
}
