// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useState } from 'react';
import { webURL } from '@/lib/basePath';
import { readResponseJSON } from '@/lib/http';
import type { FlowExecution, SearchFlowsResult } from '@/lib/types';

/** The Indexed Attribute a child Flow carries with its parent's Flow ID. */
export const PARENT_FLOW_ID_ATTRIBUTE = 'DexParentFlowID';

const CHILD_PAGE_SIZE = 20;

export interface RunRelations {
  parentFlowId: string;
  children: FlowExecution[];
  hasMoreChildren: boolean;
}

/** The search query for a Flow's child Flows. */
export function childFlowsQuery(flowId: string): string {
  return `${PARENT_FLOW_ID_ATTRIBUTE} = ${JSON.stringify(flowId)}`;
}

async function searchFlows(query: string, pageSize: number, signal: AbortSignal): Promise<SearchFlowsResult> {
  const response = await fetch(webURL('/api/flows/search'), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ query, pageSize, nextPageToken: '' }),
    signal,
  });
  return readResponseJSON<SearchFlowsResult>(response);
}

/**
 * Reads a Flow's parent and child Flows once per Flow ID. A failed read shows no relations; the run page
 * itself does not depend on them.
 */
export function useRunRelations(flowId: string): RunRelations | null {
  const [relations, setRelations] = useState<RunRelations | null>(null);
  useEffect(() => {
    const controller = new AbortController();
    setRelations(null);
    Promise.all([
      searchFlows(`WorkflowId = ${JSON.stringify(flowId)}`, 1, controller.signal),
      searchFlows(childFlowsQuery(flowId), CHILD_PAGE_SIZE, controller.signal),
    ]).then(([self, children]) => {
      const parent = self.flows[0]?.indexedAttributes?.find((attribute) => attribute.key === PARENT_FLOW_ID_ATTRIBUTE);
      setRelations({
        parentFlowId: typeof parent?.value === 'string' ? parent.value : '',
        children: children.flows,
        hasMoreChildren: children.nextPageToken !== '',
      });
    }).catch(() => undefined);
    return () => controller.abort();
  }, [flowId]);
  return relations;
}
