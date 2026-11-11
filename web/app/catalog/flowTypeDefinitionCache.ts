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

/** The last answer for each Flow type, revalidated by its revision so an unchanged graph is not sent again. */
const cachedDefinitions = new Map<string, FlowTypeDefinitions>();

/**
 * Reads every definition of one Flow type, or null when no definition declares it.
 *
 * A cached answer is sent back as If-None-Match, and the server answers 304 while the revision holds.
 */
export async function fetchFlowTypeDefinitions(flowType: string, signal?: AbortSignal): Promise<FlowTypeDefinitions | null> {
  const url = webURL(`/api/flow-definitions/${encodeURIComponent(flowType)}`);
  const cached = cachedDefinitions.get(url);
  const response = await fetch(url, {
    cache: 'no-store',
    headers: cached ? { 'If-None-Match': JSON.stringify(cached.definitionRevision) } : {},
    signal,
  });
  if (response.status === 304 && cached) return cached;
  if (response.status === 404) {
    cachedDefinitions.delete(url);
    return null;
  }
  const value = await readResponseJSON<FlowTypeDefinitions>(response);
  cachedDefinitions.set(url, value);
  return value;
}

/** Forgets every cached answer; tests call it so one test's server cannot answer another's. */
export function clearFlowTypeDefinitionCache() {
  cachedDefinitions.clear();
}

/** The definitions Dex Web accepted, which are the ones with a graph to draw. */
export function drawableDefinitions(value: FlowTypeDefinitions | null | undefined): FlowDefinitionEntry[] {
  return (value?.definitions ?? []).filter(
    (definition): definition is FlowDefinitionEntry => definition.graph !== undefined && !definition.error,
  );
}
