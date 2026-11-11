// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * Limits on a graph the canvas draws. They match the server's limits in web/flow_definition_limits.go,
 * which rejects a larger definition before it reaches the browser; this check guards the canvas too.
 */
export const MAX_FLOW_DEFINITION_NODES = 2000;
export const MAX_FLOW_DEFINITION_EDGES = 10000;
export const MAX_FLOW_DEFINITION_STRING_CHARACTERS = 16 * 1024;
export const MAX_FLOW_DEFINITION_DEPTH = 128;

/** Why the canvas must not draw this graph, or null when it may. */
export function flowDefinitionLimitProblem(graph: unknown): string | null {
  if (typeof graph !== 'object' || graph === null) return 'The Flow Definition Graph is not an object.';
  const { nodes, edges } = graph as { nodes?: unknown; edges?: unknown };
  if (!Array.isArray(nodes) || !Array.isArray(edges)) return 'The Flow Definition Graph has no nodes or edges array.';
  if (nodes.length > MAX_FLOW_DEFINITION_NODES) {
    return `It has ${nodes.length} nodes; Dex Web draws at most ${MAX_FLOW_DEFINITION_NODES}.`;
  }
  if (edges.length > MAX_FLOW_DEFINITION_EDGES) {
    return `It has ${edges.length} edges; Dex Web draws at most ${MAX_FLOW_DEFINITION_EDGES}.`;
  }
  // An explicit stack, so a deeply nested graph cannot exhaust the call stack.
  const pending: { value: unknown; depth: number }[] = [{ value: graph, depth: 1 }];
  while (pending.length > 0) {
    const { value, depth } = pending.pop()!;
    if (typeof value === 'string') {
      if (isLongerThan(value, MAX_FLOW_DEFINITION_STRING_CHARACTERS)) {
        return `A text in it is longer than ${MAX_FLOW_DEFINITION_STRING_CHARACTERS} characters.`;
      }
      continue;
    }
    if (typeof value !== 'object' || value === null) continue;
    if (depth > MAX_FLOW_DEFINITION_DEPTH) return `It nests deeper than ${MAX_FLOW_DEFINITION_DEPTH} levels.`;
    for (const [key, child] of Object.entries(value)) {
      if (isLongerThan(key, MAX_FLOW_DEFINITION_STRING_CHARACTERS)) {
        return `A field name in it is longer than ${MAX_FLOW_DEFINITION_STRING_CHARACTERS} characters.`;
      }
      pending.push({ value: child, depth: depth + 1 });
    }
  }
  return null;
}

/** Counts code points, as the server does, but only for a string whose UTF-16 length could exceed the limit. */
function isLongerThan(value: string, limit: number): boolean {
  if (value.length <= limit) return false;
  let codePoints = 0;
  for (const _codePoint of value) {
    codePoints += 1;
    if (codePoints > limit) return true;
  }
  return false;
}
