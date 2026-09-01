// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import dagre from 'dagre';
import { MarkerType, type Edge, type Node } from '@xyflow/react';
import type {
  FlowDefinitionEdge,
  FlowDefinitionGraph,
  FlowDefinitionNode,
  SourceSpan,
} from '@/lib/types';

export type DefinitionLayer =
  | 'control'
  | 'waits'
  | 'handlers'
  | 'attributes'
  | 'channels'
  | 'streams'
  | 'subflows';

export type DefinitionVisibility = Record<DefinitionLayer, boolean>;

export interface DefinitionNodeData extends Record<string, unknown> {
  kind: 'flow' | 'step' | 'wait' | 'dispatch' | 'decision' | 'channel' | 'attributes' | 'rpc' | 'subflow' | 'stream' | 'unknown';
  definition?: FlowDefinitionNode;
  definitions?: FlowDefinitionNode[];
  displayName?: string;
  relatedEdges?: FlowDefinitionEdge[];
  nameByID?: Record<string, string>;
  sourceTitle?: string;
}

export interface DefinitionEdgeData extends Record<string, unknown> {
  title?: string;
}

export interface DefinitionScene {
  edges: Array<Edge<DefinitionEdgeData>>;
  nodes: Array<Node<DefinitionNodeData>>;
}

interface Dimensions {
  height: number;
  width: number;
}

interface StepLayout {
  children: Array<Node<DefinitionNodeData>>;
  dimensions: Dimensions;
}

const flowID = 'definition:flow';
const attributeGroupID = 'definition:attributes';
const flowOrigin = { x: 150, y: 42 };
const flowHeaderHeight = 72;
const resourceRailWidth = 300;
const stepGap = 28;
const branchGap = 22;
const cardWidth = 260;
const dispatchSize = 58;

export function buildDefinitionScene(
  graph: FlowDefinitionGraph,
  visibility: DefinitionVisibility,
): DefinitionScene {
  const definitionsByID = new Map(graph.nodes.map((node) => [node.id, node]));
  const steps = visibility.control
    ? graph.nodes.filter((node) => node.kind === 'step').sort(byNameThenID)
    : [];
  const stepLayouts = new Map(steps.map((step) => [
    step.id,
    layoutStep(graph, step, visibility),
  ]));
  const stepPositions = layoutStepTopology(graph, steps, stepLayouts, definitionsByID);
  const topologyBounds = boundsForSteps(steps, stepLayouts, stepPositions);
  const resourceNodes = layoutResources(graph, visibility);
  const handlerNodes = layoutHandlers(graph, visibility);
  const sideRailHeight = Math.max(
    flowHeaderHeight + 50,
    ...resourceNodes.map((node) => node.position.y + numericStyle(node, 'height')),
    ...handlerNodes.map((node) => node.position.y + numericStyle(node, 'height')),
  );
  const centralLeft = resourceRailWidth + 56;
  const stepsTop = flowHeaderHeight + 36;
  const stepNodes = steps.map((step) => {
    const layout = stepLayouts.get(step.id)!;
    const position = stepPositions.get(step.id) ?? { x: 0, y: 0 };
    return {
      id: step.id,
      type: 'definitionStep',
      parentId: flowID,
      position: { x: centralLeft + position.x, y: stepsTop + position.y },
      style: layout.dimensions,
      data: {
        kind: 'step' as const,
        definition: step,
        sourceTitle: sourceTitle(step.span),
      },
    } satisfies Node<DefinitionNodeData>;
  });
  const childNodes = steps.flatMap((step) => stepLayouts.get(step.id)?.children ?? []);
  const subFlowNodes = layoutSubFlows(
    graph,
    visibility,
    definitionsByID,
    stepNodes,
    centralLeft + topologyBounds.width + 86,
  );
  const unknownNodes = layoutUnknownNodes(graph, visibility, centralLeft + topologyBounds.width + 86, subFlowNodes);
  const rightEdge = Math.max(
    centralLeft + topologyBounds.width,
    ...subFlowNodes.map((node) => node.position.x + numericStyle(node, 'width')),
    ...unknownNodes.map((node) => node.position.x + numericStyle(node, 'width')),
  );
  const bottomEdge = Math.max(
    stepsTop + topologyBounds.height,
    sideRailHeight,
    ...subFlowNodes.map((node) => node.position.y + numericStyle(node, 'height')),
    ...unknownNodes.map((node) => node.position.y + numericStyle(node, 'height')),
  );
  const flowDimensions = {
    width: Math.max(860, rightEdge + 64),
    height: Math.max(540, bottomEdge + 60),
  };
  const flowNode: Node<DefinitionNodeData> = {
    id: flowID,
    type: 'definitionFlow',
    position: flowOrigin,
    selectable: false,
    style: flowDimensions,
    data: {
      kind: 'flow',
      displayName: graph.flow.name,
      sourceTitle: sourceTitle(graph.flow.span),
    },
  };
  const nodes = [
    flowNode,
    ...resourceNodes,
    ...handlerNodes,
    ...stepNodes,
    ...childNodes,
    ...subFlowNodes,
    ...unknownNodes,
  ];
  const visibleIDs = new Set(nodes.map((node) => node.id));
  const endpointMap = new Map<string, string>();
  for (const attribute of graph.nodes.filter((node) => node.kind === 'attribute')) {
    endpointMap.set(attribute.id, attributeGroupID);
  }
  for (const definition of graph.nodes.filter((node) => node.kind === 'decision')) {
    const parent = definition.parentId ? definitionsByID.get(definition.parentId) : undefined;
    if (parent?.kind === 'rpc' || parent?.kind === 'timeout_handler') {
      endpointMap.set(definition.id, parent.id);
    }
  }
  const edges = graph.edges
    .filter((edge) => edge.kind !== 'cancel')
    .map((edge) => definitionEdge(edge, endpointMap, definitionsByID))
    .filter((edge) => visibleIDs.has(edge.source) && visibleIDs.has(edge.target));
  edges.push(...internalStepEdges(graph, stepLayouts, visibleIDs));
  return { nodes, edges };
}

function layoutStep(
  graph: FlowDefinitionGraph,
  step: FlowDefinitionNode,
  visibility: DefinitionVisibility,
): StepLayout {
  const waitDefinitions = visibility.waits
    ? graph.nodes.filter((node) => node.kind === 'wait' && node.parentId === step.id).sort(bySpanThenID)
    : [];
  const decisionDefinitions = graph.nodes
    .filter((node) => node.kind === 'decision' && node.parentId === step.id && node.phase !== 'rpc' && node.phase !== 'timeout')
    .sort(bySpanThenID);
  const unknownDefinitions = visibility.control
    ? graph.nodes.filter((node) => node.kind === 'unknown' && node.parentId === step.id).sort(bySpanThenID)
    : [];
  const waitSection = sectionDimensions(waitDefinitions, waitDimensions);
  const decisionSection = sectionDimensions(
    decisionDefinitions,
    (definition) => decisionDimensions(definition, graph.edges),
  );
  const unknownSection = { height: unknownDefinitions.length * 82, width: 224 };
  const contentWidth = Math.max(268, waitSection.width, decisionSection.width, unknownSection.width);
  const width = contentWidth + stepGap * 2;
  let cursorTop = 48;
  const children: Array<Node<DefinitionNodeData>> = [];
  if (waitDefinitions.length > 0) {
    const placed = placeSection(step.id, waitDefinitions, cursorTop, contentWidth, 'wait');
    children.push(...placed.nodes);
    cursorTop += placed.height + 24;
  }
  if (decisionDefinitions.length > 0) {
    const placed = placeSection(step.id, decisionDefinitions, cursorTop, contentWidth, 'decision', graph.edges, graph.nodes);
    children.push(...placed.nodes);
    cursorTop += placed.height + 24;
  }
  for (const unknown of unknownDefinitions) {
    children.push({
      id: unknown.id,
      type: 'definitionUnknown',
      parentId: step.id,
      position: { x: (contentWidth - 224) / 2 + stepGap, y: cursorTop },
      style: { height: 64, width: 224 },
      data: { kind: 'unknown', definition: unknown, sourceTitle: sourceTitle(unknown.span) },
    });
    cursorTop += 82;
  }
  return {
    children,
    dimensions: { height: Math.max(142, cursorTop), width },
  };
}

function sectionDimensions(
  definitions: FlowDefinitionNode[],
  dimensionsFor: (definition: FlowDefinitionNode) => Dimensions,
): Dimensions {
  if (definitions.length === 0) return { height: 0, width: 0 };
  const columns = Math.min(3, definitions.length);
  const rows = Math.ceil(definitions.length / columns);
  const rowHeights = Array.from({ length: rows }, (_, row) => Math.max(
    ...definitions
      .slice(row * columns, row * columns + columns)
      .map((definition) => dimensionsFor(definition).height),
  ));
  return {
    width: columns * cardWidth + (columns - 1) * branchGap,
    height: rowHeights.reduce((total, height) => total + height, 0)
      + (rows - 1) * branchGap
      + (definitions.length > 1 ? dispatchSize + branchGap : 0),
  };
}

function placeSection(
  parentID: string,
  definitions: FlowDefinitionNode[],
  top: number,
  contentWidth: number,
  kind: 'wait' | 'decision',
  graphEdges: FlowDefinitionEdge[] = [],
  graphDefinitions: FlowDefinitionNode[] = [],
): { height: number; nodes: Array<Node<DefinitionNodeData>> } {
  const dimensionsFor = kind === 'wait'
    ? waitDimensions
    : (definition: FlowDefinitionNode) => decisionDimensions(definition, graphEdges);
  const measured = sectionDimensions(definitions, dimensionsFor);
  const nodes: Array<Node<DefinitionNodeData>> = [];
  let gridTop = top;
  if (definitions.length > 1) {
    const dispatchID = `${kind}-scene-dispatch:${parentID}`;
    nodes.push({
      id: dispatchID,
      type: 'definitionDispatch',
      parentId: parentID,
      position: { x: (contentWidth - dispatchSize) / 2 + stepGap, y: top },
      style: { height: dispatchSize, width: dispatchSize },
      data: { kind: 'dispatch', displayName: kind === 'wait' ? 'WaitFor' : 'Decision' },
    });
    gridTop += dispatchSize + branchGap;
  }
  const columns = Math.min(3, definitions.length);
  const rows = Math.ceil(definitions.length / columns);
  const rowHeights = Array.from({ length: rows }, (_, row) => Math.max(
    ...definitions.slice(row * columns, row * columns + columns).map((definition) => dimensionsFor(definition).height),
  ));
  let rowTop = gridTop;
  for (let index = 0; index < definitions.length; index += 1) {
    const definition = definitions[index];
    const row = Math.floor(index / columns);
    const column = index % columns;
    const dimensions = dimensionsFor(definition);
    const rowCount = Math.min(columns, definitions.length - row * columns);
    const rowWidth = rowCount * cardWidth + (rowCount - 1) * branchGap;
    const left = (contentWidth - rowWidth) / 2 + column * (cardWidth + branchGap) + stepGap;
    const relatedEdges = graphEdges.filter((edge) => edge.from === definition.id);
    const nameByID = Object.fromEntries(graphDefinitions.map((item) => [item.id, item.name]));
    nodes.push({
      id: definition.id,
      type: kind === 'wait' ? 'definitionWait' : 'definitionDecision',
      parentId: parentID,
      position: { x: left, y: rowTop },
      style: dimensions,
      data: {
        kind,
        definition,
        relatedEdges,
        nameByID,
        sourceTitle: sourceTitle(definition.span),
      },
    });
    if (column === rowCount - 1) rowTop += rowHeights[row] + branchGap;
  }
  return { height: measured.height, nodes };
}

function waitDimensions(definition: FlowDefinitionNode): Dimensions {
  const rows = definition.wait?.conditions.length ?? 0;
  return { height: Math.max(96, 62 + rows * 25), width: cardWidth };
}

function decisionDimensions(
  definition: FlowDefinitionNode,
  graphEdges: FlowDefinitionEdge[] = [],
): Dimensions {
  const relatedRows = graphEdges.filter((edge) => edge.from === definition.id && edge.kind === 'transition').length
    + (definition.decision?.checkedChannels?.length ?? 0)
    + (definition.decision?.cancellations?.length ?? 0);
  return { height: Math.max(94, 66 + relatedRows * 25), width: cardWidth };
}

function layoutStepTopology(
  graph: FlowDefinitionGraph,
  steps: FlowDefinitionNode[],
  stepLayouts: Map<string, StepLayout>,
  definitionsByID: Map<string, FlowDefinitionNode>,
): Map<string, { x: number; y: number }> {
  if (steps.length === 0) return new Map();
  const dagreGraph = new dagre.graphlib.Graph();
  dagreGraph.setDefaultEdgeLabel(() => ({}));
  dagreGraph.setGraph({ rankdir: 'TB', ranksep: 104, nodesep: 72, marginx: 0, marginy: 0 });
  const stepIDs = new Set(steps.map((step) => step.id));
  for (const step of steps) {
    const dimensions = stepLayouts.get(step.id)!.dimensions;
    dagreGraph.setNode(step.id, dimensions);
  }
  for (const edge of graph.edges) {
    if (edge.kind !== 'transition' && edge.kind !== 'failure_transition') continue;
    const sourceDefinition = definitionsByID.get(edge.from);
    const source = sourceDefinition?.parentId ?? edge.from;
    if (stepIDs.has(source) && stepIDs.has(edge.to)) dagreGraph.setEdge(source, edge.to);
  }
  dagre.layout(dagreGraph);
  const raw = steps.map((step) => {
    const positioned = dagreGraph.node(step.id);
    const dimensions = stepLayouts.get(step.id)!.dimensions;
    return {
      id: step.id,
      x: positioned.x - dimensions.width / 2,
      y: positioned.y - dimensions.height / 2,
    };
  });
  const minimumX = Math.min(...raw.map((position) => position.x));
  const minimumY = Math.min(...raw.map((position) => position.y));
  return new Map(raw.map((position) => [position.id, {
    x: position.x - minimumX,
    y: position.y - minimumY,
  }]));
}

function boundsForSteps(
  steps: FlowDefinitionNode[],
  layouts: Map<string, StepLayout>,
  positions: Map<string, { x: number; y: number }>,
): Dimensions {
  return {
    width: Math.max(360, ...steps.map((step) => {
      const position = positions.get(step.id) ?? { x: 0, y: 0 };
      return position.x + layouts.get(step.id)!.dimensions.width;
    })),
    height: Math.max(360, ...steps.map((step) => {
      const position = positions.get(step.id) ?? { x: 0, y: 0 };
      return position.y + layouts.get(step.id)!.dimensions.height;
    })),
  };
}

function layoutResources(
  graph: FlowDefinitionGraph,
  visibility: DefinitionVisibility,
): Array<Node<DefinitionNodeData>> {
  const nodes: Array<Node<DefinitionNodeData>> = [];
  let top = flowHeaderHeight + 34;
  if (visibility.channels) {
    for (const channel of graph.nodes.filter((node) => node.kind === 'channel').sort(byNameThenID)) {
      nodes.push({
        id: channel.id,
        type: 'definitionChannel',
        parentId: flowID,
        position: { x: 124, y: top },
        style: { height: 56, width: 224 },
        data: { kind: 'channel', definition: channel, sourceTitle: sourceTitle(channel.span) },
      });
      top += 72;
    }
  }
  const attributes = visibility.attributes
    ? graph.nodes.filter((node) => node.kind === 'attribute').sort(byNameThenID)
    : [];
  if (attributes.length > 0) {
    top += 12;
    nodes.push({
      id: attributeGroupID,
      type: 'definitionAttributes',
      parentId: flowID,
      position: { x: 116, y: top },
      style: { height: 52 + attributes.length * 27, width: 232 },
      data: { kind: 'attributes', definitions: attributes },
    });
    top += 68 + attributes.length * 27;
  }
  if (visibility.streams) {
    for (const stream of graph.nodes.filter((node) => node.kind === 'stream').sort(byNameThenID)) {
      nodes.push({
        id: stream.id,
        type: 'definitionStream',
        parentId: flowID,
        position: { x: 124, y: top },
        style: { height: 58, width: 224 },
        data: { kind: 'stream', definition: stream, sourceTitle: sourceTitle(stream.span) },
      });
      top += 74;
    }
  }
  return nodes;
}

function layoutHandlers(
  graph: FlowDefinitionGraph,
  visibility: DefinitionVisibility,
): Array<Node<DefinitionNodeData>> {
  if (!visibility.handlers) return [];
  return graph.nodes
    .filter((node) => node.kind === 'rpc' || node.kind === 'timeout_handler')
    .sort(byNameThenID)
    .map((handler, index) => ({
      id: handler.id,
      type: 'definitionRPC',
      parentId: flowID,
      position: { x: -108, y: flowHeaderHeight + 36 + index * 88 },
      style: { height: 62, width: 220 },
      data: { kind: 'rpc' as const, definition: handler, sourceTitle: sourceTitle(handler.span) },
    }));
}

function layoutSubFlows(
  graph: FlowDefinitionGraph,
  visibility: DefinitionVisibility,
  definitionsByID: Map<string, FlowDefinitionNode>,
  stepNodes: Array<Node<DefinitionNodeData>>,
  left: number,
): Array<Node<DefinitionNodeData>> {
  if (!visibility.subflows) return [];
  const stepPositions = new Map(stepNodes.map((node) => [node.id, node.position]));
  const nextTopByStep = new Map<string, number>();
  return graph.nodes.filter((node) => node.kind === 'subflow').sort(byNameThenID).map((subflow, index) => {
    const relation = graph.edges.find((edge) => edge.kind === 'subflow' && edge.to === subflow.id);
    const wait = relation ? definitionsByID.get(relation.from) : undefined;
    const stepID = wait?.parentId ?? '';
    const stepTop = stepPositions.get(stepID)?.y ?? flowHeaderHeight + 40 + index * 94;
    const top = nextTopByStep.get(stepID) ?? stepTop;
    nextTopByStep.set(stepID, top + 92);
    return {
      id: subflow.id,
      type: 'definitionSubFlow',
      parentId: flowID,
      position: { x: left, y: top },
      style: { height: 68, width: 224 },
      data: { kind: 'subflow' as const, definition: subflow, sourceTitle: sourceTitle(subflow.span) },
    };
  });
}

function layoutUnknownNodes(
  graph: FlowDefinitionGraph,
  visibility: DefinitionVisibility,
  left: number,
  subflows: Array<Node<DefinitionNodeData>>,
): Array<Node<DefinitionNodeData>> {
  if (!visibility.control) return [];
  const top = Math.max(flowHeaderHeight + 44, ...subflows.map((node) => node.position.y + numericStyle(node, 'height') + 24));
  return graph.nodes.filter((node) => node.kind === 'unknown' && !node.parentId).sort(byNameThenID).map((unknown, index) => ({
    id: unknown.id,
    type: 'definitionUnknown',
    parentId: flowID,
    position: { x: left, y: top + index * 90 },
    style: { height: 64, width: 224 },
    data: { kind: 'unknown' as const, definition: unknown, sourceTitle: sourceTitle(unknown.span) },
  }));
}

function definitionEdge(
  definition: FlowDefinitionEdge,
  endpointMap: Map<string, string>,
  definitionsByID: Map<string, FlowDefinitionNode>,
): Edge<DefinitionEdgeData> {
  const source = endpointMap.get(definition.from) ?? definition.from;
  const target = endpointMap.get(definition.to) ?? definition.to;
  const sourceDefinition = definitionsByID.get(definition.from);
  const targetDefinition = definitionsByID.get(definition.to);
  const color = edgeColor(definition.kind);
  const label = edgeLabel(definition);
  return {
    id: definition.id,
    source,
    sourceHandle: sourceHandle(definition.kind, sourceDefinition),
    target,
    targetHandle: targetHandle(definition.kind, targetDefinition),
    label,
    type: 'definitionEdge',
    markerEnd: { type: MarkerType.ArrowClosed, width: 17, height: 17, color },
    style: {
      stroke: color,
      strokeDasharray: isDashed(definition.kind) ? '6 5' : undefined,
      strokeWidth: definition.kind === 'failure_transition' ? 2.8 : definition.kind === 'transition' ? 2 : 1.6,
    },
    data: { title: [definition.condition, definition.label, sourceTitle(definition.span)].filter(Boolean).join(' · ') },
  };
}

function sourceHandle(kind: string, definition?: FlowDefinitionNode): string | undefined {
  if (definition?.kind !== 'step') return undefined;
  if (kind === 'failure_transition') return 'step-recovery-source';
  if (kind.startsWith('resource_')) return 'step-resource-source';
  return undefined;
}

function targetHandle(kind: string, definition?: FlowDefinitionNode): string | undefined {
  if (definition?.kind !== 'step') return undefined;
  if (kind === 'transition' || kind === 'failure_transition') return 'step-target';
  if (kind.startsWith('resource_')) return 'step-resource-target';
  return undefined;
}

function internalStepEdges(
  graph: FlowDefinitionGraph,
  layouts: Map<string, StepLayout>,
  visibleIDs: Set<string>,
): Array<Edge<DefinitionEdgeData>> {
  const edges: Array<Edge<DefinitionEdgeData>> = [];
  for (const [stepID, layout] of layouts) {
    const waits = layout.children.filter((node) => node.data.kind === 'wait');
    const decisions = layout.children.filter((node) => node.data.kind === 'decision');
    const waitDispatch = layout.children.find((node) => node.id === `wait-scene-dispatch:${stepID}`);
    const decisionDispatch = layout.children.find((node) => node.id === `decision-scene-dispatch:${stepID}`);
    if (waitDispatch) {
      for (const wait of waits) edges.push(branchEdge(waitDispatch.id, wait));
    }
    if (decisionDispatch) {
      for (const decision of decisions) edges.push(branchEdge(decisionDispatch.id, decision));
    }
    const phaseSources = waits.length > 0 ? waits : [];
    const phaseTarget = decisionDispatch?.id ?? decisions[0]?.id;
    if (phaseTarget && visibleIDs.has(phaseTarget)) {
      for (const source of phaseSources) {
        edges.push({
          id: `phase:${source.id}:${phaseTarget}`,
          source: source.id,
          target: phaseTarget,
          type: 'definitionEdge',
          markerEnd: { type: MarkerType.ArrowClosed, width: 13, height: 13, color: '#94a3b8' },
          style: { stroke: '#94a3b8', strokeWidth: 1.35 },
          data: { title: 'WaitFor completes before Execute' },
        });
      }
    }
  }
  return edges.filter((edge) => visibleIDs.has(edge.source) && visibleIDs.has(edge.target));
}

function branchEdge(source: string, target: Node<DefinitionNodeData>): Edge<DefinitionEdgeData> {
  const condition = target.data.definition?.condition || 'otherwise';
  return {
    id: `branch:${source}:${target.id}`,
    source,
    target: target.id,
    label: truncateCondition(condition),
    type: 'definitionEdge',
    markerEnd: { type: MarkerType.ArrowClosed, width: 13, height: 13, color: '#64748b' },
    style: { stroke: '#64748b', strokeWidth: 1.35 },
    data: { title: [condition, sourceTitle(target.data.definition?.span)].filter(Boolean).join(' · ') },
  };
}

function edgeLabel(edge: FlowDefinitionEdge): string {
  if (edge.kind === 'failure_transition') {
    return edge.metadata?.skipWaitFor === true ? 'Execute failure · skip WaitFor' : 'Execute failure';
  }
  if (edge.kind === 'transition') return edge.multiplicity ?? '';
  if (edge.kind === 'subflow') return 'start';
  return '';
}

function edgeColor(kind: string): string {
  if (kind === 'failure_transition') return '#c43b62';
  if (kind === 'transition') return '#334155';
  if (kind === 'subflow') return '#8058c7';
  if (kind === 'wait_condition') return '#b7791f';
  return '#4d7b69';
}

function isDashed(kind: string): boolean {
  return kind.startsWith('resource_') || kind === 'wait_condition' || kind === 'subflow';
}

function truncateCondition(condition: string): string {
  return condition.length <= 36 ? condition : `${condition.slice(0, 35)}…`;
}

function sourceTitle(span?: SourceSpan): string {
  return span ? `lines ${span.startLine}:${span.startColumn}–${span.endLine}:${span.endColumn}` : '';
}

function numericStyle(node: Node<DefinitionNodeData>, key: 'height' | 'width'): number {
  const value = node.style?.[key];
  return typeof value === 'number' ? value : 0;
}

function byNameThenID(left: FlowDefinitionNode, right: FlowDefinitionNode): number {
  return left.name.localeCompare(right.name) || left.id.localeCompare(right.id);
}

function bySpanThenID(left: FlowDefinitionNode, right: FlowDefinitionNode): number {
  return (left.span?.startLine ?? 0) - (right.span?.startLine ?? 0)
    || (left.span?.startColumn ?? 0) - (right.span?.startColumn ?? 0)
    || left.id.localeCompare(right.id);
}
