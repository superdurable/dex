// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import dagre from 'dagre';
import type { Edge, Node } from '@xyflow/react';

const horizontalGap = 36;
const verticalGap = 64;
const horizontalMargin = 32;
const verticalMargin = 32;
export const minimumGraphZoom = 1 / 3;

interface LayoutNode<NodeData extends Record<string, unknown>> {
  height: number;
  node: Node<NodeData>;
  order: number;
  width: number;
}

function logicalRanks<NodeData extends Record<string, unknown>>(
  nodes: Array<Node<NodeData>>,
  edges: Edge[],
): Map<string, number> {
  const nodeIDs = new Set(nodes.map((node) => node.id));
  const incomingCounts = new Map(nodes.map((node) => [node.id, 0]));
  const outgoing = new Map(nodes.map((node) => [node.id, [] as string[]]));
  for (const edge of edges) {
    if (!nodeIDs.has(edge.source) || !nodeIDs.has(edge.target)) continue;
    incomingCounts.set(edge.target, (incomingCounts.get(edge.target) ?? 0) + 1);
    outgoing.get(edge.source)?.push(edge.target);
  }
  const ranks = new Map(nodes.map((node) => [node.id, 0]));
  const ready = nodes
    .filter((node) => incomingCounts.get(node.id) === 0)
    .map((node) => node.id);
  for (let index = 0; index < ready.length; index += 1) {
    const source = ready[index];
    const nextRank = (ranks.get(source) ?? 0) + 1;
    for (const target of outgoing.get(source) ?? []) {
      ranks.set(target, Math.max(ranks.get(target) ?? 0, nextRank));
      const incomingCount = (incomingCounts.get(target) ?? 0) - 1;
      incomingCounts.set(target, incomingCount);
      if (incomingCount === 0) ready.push(target);
    }
  }
  return ranks;
}

function nodeWidth<NodeData extends Record<string, unknown>>(node: Node<NodeData>): number {
  return typeof node.style?.width === 'number' ? node.style.width : 220;
}

function nodeHeight<NodeData extends Record<string, unknown>>(node: Node<NodeData>): number {
  if (typeof node.style?.height === 'number') return node.style.height;
  return node.data.model && typeof node.data.model === 'object'
    && (node.data.model as { kind?: string }).kind === 'step' ? 158 : 90;
}

export function layoutGraph<NodeData extends Record<string, unknown>>(
  nodes: Array<Node<NodeData>>,
  edges: Edge[],
): Array<Node<NodeData>> {
  if (nodes.length === 0) return [];
  const graph = new dagre.graphlib.Graph();
  graph.setDefaultEdgeLabel(() => ({}));
  graph.setGraph({ rankdir: 'TB', ranksep: verticalGap, nodesep: horizontalGap });
  nodes.forEach((node) => graph.setNode(node.id, {
    width: nodeWidth(node),
    height: nodeHeight(node),
  }));
  edges.forEach((edge) => graph.setEdge(edge.source, edge.target));
  dagre.layout(graph);

  const nodeRanks = logicalRanks(nodes, edges);
  const rankMembers = new Map<number, Array<LayoutNode<NodeData>>>();
  for (const node of nodes) {
    const position = graph.node(node.id);
    const rank = nodeRanks.get(node.id)
      ?? Math.round(position.y);
    const members = rankMembers.get(rank) ?? [];
    members.push({
      height: nodeHeight(node),
      node,
      order: position.x,
      width: nodeWidth(node),
    });
    rankMembers.set(rank, members);
  }

  const rows = [...rankMembers.entries()]
    .sort(([left], [right]) => left - right)
    .map(([, members]) => members.sort((left, right) => left.order - right.order));
  const contentWidth = Math.max(...rows.map((row) => (
    row.reduce((width, member) => width + member.width, 0)
      + horizontalGap * Math.max(0, row.length - 1)
  )));
  const positions = new Map<string, { x: number; y: number }>();
  let rowTop = verticalMargin;
  for (const row of rows) {
    const rowHeight = Math.max(...row.map((member) => member.height));
    const rowWidth = row.reduce((width, member) => width + member.width, 0)
      + horizontalGap * Math.max(0, row.length - 1);
    let nodeLeft = horizontalMargin + (contentWidth - rowWidth) / 2;
    for (const member of row) {
      positions.set(member.node.id, {
        x: nodeLeft,
        y: rowTop + (rowHeight - member.height) / 2,
      });
      nodeLeft += member.width + horizontalGap;
    }
    rowTop += rowHeight + verticalGap;
  }

  return nodes.map((node) => ({ ...node, position: positions.get(node.id) ?? node.position }));
}

export function defaultGraphZoom(contentWidth: number, viewportWidth: number): number {
  if (contentWidth <= 0 || viewportWidth <= 0) return 1;
  const availableWidth = Math.max(0, viewportWidth - horizontalMargin * 2);
  return Math.max(minimumGraphZoom, Math.min(1, availableWidth / contentWidth));
}

export function graphBounds<NodeData extends Record<string, unknown>>(
  nodes: Array<Node<NodeData>>,
): { height: number; width: number } {
  return {
    height: Math.max(0, ...nodes.map((node) => node.position.y + nodeHeight(node))) + verticalMargin,
    width: Math.max(0, ...nodes.map((node) => node.position.x + nodeWidth(node))) + horizontalMargin,
  };
}
