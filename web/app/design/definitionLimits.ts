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

const NODE_STRINGS = ['id', 'kind', 'name'] as const;
const EDGE_STRINGS = ['id', 'kind', 'from', 'to'] as const;
const DIAGNOSTIC_STRINGS = ['severity', 'code', 'message'] as const;

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

/**
 * Why the graph's elements cannot be read, or null when they can.
 *
 * The server checks the same shapes in web/flow_definition_shape.go; the browser checks again before drawing.
 */
export function flowDefinitionShapeProblem(graph: unknown): string | null {
  if (!isRecord(graph)) return 'The Flow Definition Graph is not an object.';
  const lists: [string, unknown, readonly string[]][] = [
    ['node', graph.nodes, NODE_STRINGS],
    ['edge', graph.edges, EDGE_STRINGS],
    ['diagnostic', graph.diagnostics, DIAGNOSTIC_STRINGS],
  ];
  for (const [label, list, requiredStrings] of lists) {
    if (!Array.isArray(list)) return `The Flow Definition Graph has no ${label}s array.`;
    for (const [index, element] of list.entries()) {
      if (!isRecord(element) || requiredStrings.some((field) => typeof element[field] !== 'string')) {
        return `Its ${label} ${index} is malformed.`;
      }
    }
  }
  for (const [index, node] of (graph.nodes as Record<string, unknown>[]).entries()) {
    if (node.wait !== undefined && !isReadableWait(node.wait)) return `Its node ${index} has a malformed wait.`;
  }
  if (graph.groups !== undefined) {
    if (!Array.isArray(graph.groups)) return 'Its groups are malformed.';
    for (const [index, group] of graph.groups.entries()) {
      if (!isRecord(group) || typeof group.id !== 'string' || typeof group.label !== 'string'
        || !Array.isArray(group.stepIds) || group.stepIds.some((stepId) => typeof stepId !== 'string')) {
        return `Its group ${index} is malformed.`;
      }
    }
  }
  return null;
}

/** A wait the canvas can read; null conditions, which dexcli visualize 1.44 and older writes, read as none. */
function isReadableWait(wait: unknown): boolean {
  if (!isRecord(wait) || typeof wait.type !== 'string') return false;
  if (wait.conditions === null) return true;
  return Array.isArray(wait.conditions) && wait.conditions.every((condition) => (
    isRecord(condition) && typeof condition.kind === 'string' && typeof condition.label === 'string'
  ));
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
