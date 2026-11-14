// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import dagre from 'dagre';
import type { Edge, Node } from '@xyflow/react';

const horizontalGap = 36;
const verticalGap = 64;
const horizontalMargin = 32;
const verticalMargin = 32;
export const minimumGraphZoom = 1 / 3;

/** Top to bottom, or left to right with ranks as columns. */
export type GraphDirection = 'tb' | 'lr';

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
  direction: GraphDirection = 'tb',
): Array<Node<NodeData>> {
  if (nodes.length === 0) return [];
  const topologyNodes = nodes.filter((node) => !isSubFlowNode(node));
  const subFlowNodes = nodes.filter(isSubFlowNode);
  const topologyIDs = new Set(topologyNodes.map((node) => node.id));
  const topologyEdges = edges.filter((edge) => (
    topologyIDs.has(edge.source) && topologyIDs.has(edge.target)
  ));
  const childrenByParent = subFlowChildrenByParent(subFlowNodes, topologyIDs);
  const positions = direction === 'lr'
    ? layoutTopologyLeftToRight(topologyNodes, topologyEdges, childrenByParent)
    : layoutTopology(topologyNodes, topologyEdges);
  // SubFlow conditions are not next Steps; keep them off the topology ranks.
  if (direction === 'lr') placeSubFlowNodesBelow(childrenByParent, topologyNodes, positions);
  else placeSubFlowNodes(subFlowNodes, topologyNodes, positions);
  return nodes.map((node) => ({ ...node, position: positions.get(node.id) ?? node.position }));
}

function layoutTopology<NodeData extends Record<string, unknown>>(
  nodes: Array<Node<NodeData>>,
  edges: Edge[],
): Map<string, { x: number; y: number }> {
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
  return positions;
}

/** Lays ranks out as columns; each Step reserves room under itself for its SubFlow nodes. */
function layoutTopologyLeftToRight<NodeData extends Record<string, unknown>>(
  nodes: Array<Node<NodeData>>,
  edges: Edge[],
  childrenByParent: Map<string, Array<Node<NodeData>>>,
): Map<string, { x: number; y: number }> {
  const slotHeight = (node: Node<NodeData>) => nodeHeight(node) + subFlowStackHeight(childrenByParent.get(node.id));
  const graph = new dagre.graphlib.Graph();
  graph.setDefaultEdgeLabel(() => ({}));
  graph.setGraph({ rankdir: 'LR', ranksep: verticalGap, nodesep: horizontalGap });
  nodes.forEach((node) => graph.setNode(node.id, { width: nodeWidth(node), height: slotHeight(node) }));
  edges.forEach((edge) => graph.setEdge(edge.source, edge.target));
  dagre.layout(graph);

  const nodeRanks = logicalRanks(nodes, edges);
  const rankMembers = new Map<number, Array<LayoutNode<NodeData>>>();
  for (const node of nodes) {
    const position = graph.node(node.id);
    const rank = nodeRanks.get(node.id) ?? Math.round(position.x);
    const members = rankMembers.get(rank) ?? [];
    members.push({ height: slotHeight(node), node, order: position.y, width: nodeWidth(node) });
    rankMembers.set(rank, members);
  }

  const columns = [...rankMembers.entries()]
    .sort(([left], [right]) => left - right)
    .map(([, members]) => members.sort((left, right) => left.order - right.order));
  const columnHeight = (column: Array<LayoutNode<NodeData>>) => (
    column.reduce((height, member) => height + member.height, 0) + horizontalGap * Math.max(0, column.length - 1)
  );
  const contentHeight = Math.max(...columns.map(columnHeight));
  const positions = new Map<string, { x: number; y: number }>();
  let columnLeft = horizontalMargin;
  for (const column of columns) {
    const columnWidth = Math.max(...column.map((member) => member.width));
    let nodeTop = verticalMargin + (contentHeight - columnHeight(column)) / 2;
    for (const member of column) {
      positions.set(member.node.id, { x: columnLeft + (columnWidth - member.width) / 2, y: nodeTop });
      nodeTop += member.height + horizontalGap;
    }
    columnLeft += columnWidth + verticalGap;
  }
  return positions;
}

function subFlowChildrenByParent<NodeData extends Record<string, unknown>>(
  subFlowNodes: Array<Node<NodeData>>,
  topologyIDs: Set<string>,
): Map<string, Array<Node<NodeData>>> {
  const childrenByParent = new Map<string, Array<Node<NodeData>>>();
  for (const node of subFlowNodes) {
    const parentID = parentStepID(node);
    if (!parentID || !topologyIDs.has(parentID)) continue;
    childrenByParent.set(parentID, [...(childrenByParent.get(parentID) ?? []), node]);
  }
  return childrenByParent;
}

/** The height a Step reserves under itself for its SubFlow nodes, gaps included. */
function subFlowStackHeight<NodeData extends Record<string, unknown>>(children: Array<Node<NodeData>> | undefined): number {
  if (!children || children.length === 0) return 0;
  return children.reduce((height, child) => height + horizontalGap + nodeHeight(child), 0);
}

function placeSubFlowNodesBelow<NodeData extends Record<string, unknown>>(
  childrenByParent: Map<string, Array<Node<NodeData>>>,
  topologyNodes: Array<Node<NodeData>>,
  positions: Map<string, { x: number; y: number }>,
): void {
  const topologyByID = new Map(topologyNodes.map((node) => [node.id, node]));
  for (const [parentID, children] of childrenByParent) {
    const parent = topologyByID.get(parentID);
    const parentPosition = positions.get(parentID);
    if (!parent || !parentPosition) continue;
    let nodeTop = parentPosition.y + nodeHeight(parent) + horizontalGap;
    for (const child of children) {
      positions.set(child.id, { x: parentPosition.x + (nodeWidth(parent) - nodeWidth(child)) / 2, y: nodeTop });
      nodeTop += nodeHeight(child) + horizontalGap;
    }
  }
}

function placeSubFlowNodes<NodeData extends Record<string, unknown>>(
  subFlowNodes: Array<Node<NodeData>>,
  topologyNodes: Array<Node<NodeData>>,
  positions: Map<string, { x: number; y: number }>,
): void {
  const topologyByID = new Map(topologyNodes.map((node) => [node.id, node]));
  const childrenByParent = new Map<string, Array<Node<NodeData>>>();
  for (const node of subFlowNodes) {
    const parentID = parentStepID(node);
    if (!parentID || !positions.has(parentID)) continue;
    childrenByParent.set(parentID, [...(childrenByParent.get(parentID) ?? []), node]);
  }
  for (const [parentID, children] of childrenByParent) {
    const parent = topologyByID.get(parentID);
    const parentPosition = positions.get(parentID);
    if (!parent || !parentPosition) continue;
    const stackHeight = children.reduce((height, child) => height + nodeHeight(child), 0)
      + horizontalGap * Math.max(0, children.length - 1);
    let nodeTop = parentPosition.y + Math.max(0, (nodeHeight(parent) - stackHeight) / 2);
    const nodeLeft = parentPosition.x + nodeWidth(parent) + horizontalGap;
    for (const child of children) {
      positions.set(child.id, { x: nodeLeft, y: nodeTop });
      nodeTop += nodeHeight(child) + horizontalGap;
    }
  }
}

function isSubFlowNode<NodeData extends Record<string, unknown>>(node: Node<NodeData>): boolean {
  return nodeKind(node) === 'subflow';
}

function parentStepID<NodeData extends Record<string, unknown>>(node: Node<NodeData>): string {
  const model = node.data.model;
  if (!model || typeof model !== 'object') return '';
  const value = (model as { parentStepId?: unknown }).parentStepId;
  return typeof value === 'string' ? value : '';
}

function nodeKind<NodeData extends Record<string, unknown>>(node: Node<NodeData>): string {
  const model = node.data.model;
  if (!model || typeof model !== 'object') return '';
  const value = (model as { kind?: unknown }).kind;
  return typeof value === 'string' ? value : '';
}

export function defaultGraphZoom(contentWidth: number, viewportWidth: number): number {
  if (contentWidth <= 0 || viewportWidth <= 0) return 1;
  const availableWidth = Math.max(0, viewportWidth - horizontalMargin * 2);
  return Math.max(minimumGraphZoom, Math.min(1, availableWidth / contentWidth));
}

/**
 * Returns the zoom that fits the whole graph into a box of both sizes, at most 1. It may be
 * below minimumGraphZoom; callers clamp it and treat a lower value as "does not fit".
 */
export function fittingGraphZoom(
  bounds: { height: number; width: number },
  viewport: { height: number; width: number },
): number {
  if (bounds.width <= 0 || bounds.height <= 0 || viewport.width <= 0 || viewport.height <= 0) return 1;
  return Math.min(1, viewport.width / bounds.width, viewport.height / bounds.height);
}

export function graphBounds<NodeData extends Record<string, unknown>>(
  nodes: Array<Node<NodeData>>,
): { height: number; width: number } {
  return {
    height: Math.max(0, ...nodes.map((node) => node.position.y + nodeHeight(node))) + verticalMargin,
    width: Math.max(0, ...nodes.map((node) => node.position.x + nodeWidth(node))) + horizontalMargin,
  };
}
