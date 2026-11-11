// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readResponseJSON } from '@/lib/http';
import type { FlowDefinitionEntry, FlowTypeDefinitions } from '@/lib/types';
import { webURL } from '@/lib/basePath';
import { flowDefinitionShapeProblem } from '../design/definitionLimits';

/** The last answer for each Flow type, revalidated by its revision so an unchanged graph is not sent again. */
const cachedDefinitions = new Map<string, FlowTypeDefinitions>();
/** The newest request for each Flow type; an older answer never replaces a newer one in the cache. */
const latestRequests = new Map<string, number>();
let requestCount = 0;

/** The URL of one Flow type's definitions; a query parameter, because browsers rewrite "." path segments. */
export function flowTypeDefinitionsURL(flowType: string): string {
  return webURL(`/api/flow-definitions/by-type?${new URLSearchParams({ flowType })}`);
}

/**
 * Reads every definition of one Flow type, or null when no definition declares it.
 *
 * A cached answer is sent back as If-None-Match, and the server answers 304 while the revision holds.
 * When a newer request for the Flow type started meanwhile, this answer is returned but not cached.
 */
export async function fetchFlowTypeDefinitions(flowType: string, signal?: AbortSignal): Promise<FlowTypeDefinitions | null> {
  const url = flowTypeDefinitionsURL(flowType);
  requestCount += 1;
  const request = requestCount;
  latestRequests.set(url, request);
  const cached = cachedDefinitions.get(url);
  const response = await fetch(url, {
    cache: 'no-store',
    headers: cached ? { 'If-None-Match': JSON.stringify(cached.definitionRevision) } : {},
    signal,
  });
  if (response.status === 304 && cached) return cached;
  const isLatest = latestRequests.get(url) === request;
  if (response.status === 404) {
    if (isLatest) cachedDefinitions.delete(url);
    return null;
  }
  const value = await readResponseJSON<FlowTypeDefinitions>(response);
  if (isLatest && latestRequests.get(url) === request) cachedDefinitions.set(url, value);
  return value;
}

/** Forgets every cached answer; tests call it so one test's server cannot answer another's. */
export function clearFlowTypeDefinitionCache() {
  cachedDefinitions.clear();
  latestRequests.clear();
}

/** The definitions Dex Web accepted and the browser can read, which are the ones with a graph to draw. */
export function drawableDefinitions(value: FlowTypeDefinitions | null | undefined): FlowDefinitionEntry[] {
  return (value?.definitions ?? []).filter(
    (definition): definition is FlowDefinitionEntry => definition.graph !== undefined && !definition.error
      && flowDefinitionShapeProblem(definition.graph) === null,
  );
}
