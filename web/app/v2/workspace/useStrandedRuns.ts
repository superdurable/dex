// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useState } from 'react';

/**
 * Learned, never searched: a run reports itself unreachable only when somebody opens it.
 * Session-scoped because a worker could in principle come back.
 */
export function useStrandedRuns() {
  const [strandedFlowIDs, setStrandedFlowIDs] = useState<ReadonlySet<string>>(() => new Set());
  const rememberStranded = useCallback((strandedFlowID: string) => {
    setStrandedFlowIDs((prior) => (
      prior.has(strandedFlowID) ? prior : new Set([...prior, strandedFlowID])
    ));
  }, []);
  return { strandedFlowIDs, rememberStranded };
}
