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
} from './types';

export type DefinitionLayer =
  | 'control'
  | 'waits'
  | 'rpcs'
  | 'attributes'
  | 'channels'
  | 'streams'
  | 'subflows';

export type DefinitionVisibility = Record<DefinitionLayer, boolean>;

export interface DefinitionNodeData extends Record<string, unknown> {
  kind: 'flow' | 'step' | 'wait' | 'dispatch' | 'decision' | 'channel' | 'attributes' | 'rpc' | 'timeout' | 'subflow' | 'stream' | 'unknown';
  definition?: FlowDefinitionNode;
  definitions?: FlowDefinitionNode[];
  displayName?: string;
  relatedEdges?: FlowDefinitionEdge[];
  nameByID?: Record<string, string>;
  selectionDetails?: DefinitionSelectionDetail[];
  sourceTitle?: string;
}

export interface DefinitionEdgeData extends Record<string, unknown> {
  definitionSourceID?: string;
  definitionTargetID?: string;
  displayLabel?: string;
  kind?: string;
  route?: 'forward' | 'outer-right';
  sceneSourceID?: string;
  selectionDetails?: DefinitionSelectionDetail[];
  title?: string;
}

export interface DefinitionSelectionDetail {
  label: string;
  sourceTitle?: string;
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

interface DefinitionGroup {
  definitions: FlowDefinitionNode[];
  id: string;
}

const flowID = 'definition:flow';
const attributeGroupID = 'definition:attributes';
const flowOrigin = { x: 150, y: 42 };
const flowHeaderHeight = 72;
const resourceRailWidth = 400;
const stepGap = 32;
const branchGap = 42;
const cardWidth = 288;
const dispatchSize = 58;
const stepTopologyRankSeparation = 168;

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
  const handlerNodes = layoutRPCsAndTimeoutHandlers(graph, visibility);
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
  for (const node of childNodes.filter((child) => child.data.kind === 'decision')) {
    for (const definition of node.data.definitions ?? []) endpointMap.set(definition.id, node.id);
  }
  const edges = mergeGroupedTransitionEdges(graph.edges
    .filter((edge) => edge.kind !== 'cancel')
    .map((edge) => definitionEdge(edge, endpointMap, definitionsByID, stepNodes))
    .filter((edge) => visibleIDs.has(edge.source) && visibleIDs.has(edge.target)));
  edges.push(...internalStepEdges(graph, stepLayouts, visibleIDs));
  return { nodes, edges };
}

export function filterDefinitionEdgesForSelection(
  edges: Array<Edge<DefinitionEdgeData>>,
  definitions: FlowDefinitionNode[],
  selectedNodeID: string,
): Array<Edge<DefinitionEdgeData>> {
  const definitionsByID = new Map(definitions.map((definition) => [definition.id, definition]));
  return edges.filter((edge) => {
    if (!isResourceRelation(edge.data?.kind)) return true;
    if (!selectedNodeID) return false;
    const sourceID = edge.data?.definitionSourceID ?? edge.source;
    const targetID = edge.data?.definitionTargetID ?? edge.target;
    if (selectedNodeID === attributeGroupID) {
      return definitionsByID.get(sourceID)?.kind === 'attribute'
        || definitionsByID.get(targetID)?.kind === 'attribute';
    }
    const selectedDefinition = definitionsByID.get(selectedNodeID);
    if (!selectedDefinition) return false;
    if (selectedDefinition.kind === 'channel' || selectedDefinition.kind === 'stream') {
      return sourceID === selectedNodeID || targetID === selectedNodeID;
    }
    const ownerID = resourceOwnerID(selectedDefinition, definitionsByID);
    if (!ownerID) return false;
    return belongsToOwner(sourceID, ownerID, definitionsByID)
      || belongsToOwner(targetID, ownerID, definitionsByID);
  });
}

export function isResourceRelation(kind: string | undefined): boolean {
  return kind === 'wait_condition' || kind?.startsWith('resource_') === true;
}

function resourceOwnerID(
  definition: FlowDefinitionNode,
  definitionsByID: Map<string, FlowDefinitionNode>,
): string {
  let current: FlowDefinitionNode | undefined = definition;
  while (current) {
    if (current.kind === 'step' || current.kind === 'rpc' || current.kind === 'timeout_handler') {
      return current.id;
    }
    current = current.parentId ? definitionsByID.get(current.parentId) : undefined;
  }
  return '';
}

function belongsToOwner(
  definitionID: string,
  ownerID: string,
  definitionsByID: Map<string, FlowDefinitionNode>,
): boolean {
  let current = definitionsByID.get(definitionID);
  while (current) {
    if (current.id === ownerID) return true;
    current = current.parentId ? definitionsByID.get(current.parentId) : undefined;
  }
  return false;
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
  const nameByID = Object.fromEntries(graph.nodes.map((node) => [node.id, node.name]));
  const waitGroups = waitDefinitions.map(singleDefinitionGroup);
  const decisionGroups = groupDecisionDefinitions(decisionDefinitions, graph.edges);
  const waitSection = sectionDimensions(waitGroups, (group) => waitDimensions(group.definitions[0]));
  const decisionSection = sectionDimensions(
    decisionGroups,
    (group) => decisionDimensions(group, graph.edges, nameByID),
  );
  const unknownSection = { height: unknownDefinitions.length * 82, width: 224 };
  const contentWidth = Math.max(268, waitSection.width, decisionSection.width, unknownSection.width);
  const width = contentWidth + stepGap * 2;
  let cursorTop = stepHeaderHeight(step.name, width);
  const children: Array<Node<DefinitionNodeData>> = [];
  if (waitDefinitions.length > 0) {
    const placed = placeSection(step.id, waitGroups, cursorTop, contentWidth, 'wait');
    children.push(...placed.nodes);
    cursorTop += placed.height + 38;
  }
  if (decisionDefinitions.length > 0) {
    const placed = placeSection(step.id, decisionGroups, cursorTop, contentWidth, 'decision', graph.edges, graph.nodes);
    children.push(...placed.nodes);
    cursorTop += placed.height + 32;
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
    dimensions: { height: Math.max(154, cursorTop), width },
  };
}

function sectionDimensions(
  groups: DefinitionGroup[],
  dimensionsFor: (group: DefinitionGroup) => Dimensions,
): Dimensions {
  if (groups.length === 0) return { height: 0, width: 0 };
  const columns = Math.min(3, groups.length);
  const rows = Math.ceil(groups.length / columns);
  const rowHeights = Array.from({ length: rows }, (_, row) => Math.max(
    ...groups
      .slice(row * columns, row * columns + columns)
      .map((group) => dimensionsFor(group).height),
  ));
  return {
    width: columns * cardWidth + (columns - 1) * branchGap,
    height: rowHeights.reduce((total, height) => total + height, 0)
      + (rows - 1) * branchGap
      + (groups.length > 1 ? dispatchSize + branchGap : 0),
  };
}

function placeSection(
  parentID: string,
  groups: DefinitionGroup[],
  top: number,
  contentWidth: number,
  kind: 'wait' | 'decision',
  graphEdges: FlowDefinitionEdge[] = [],
  graphDefinitions: FlowDefinitionNode[] = [],
): { height: number; nodes: Array<Node<DefinitionNodeData>> } {
  const dimensionsFor = kind === 'wait'
    ? (group: DefinitionGroup) => waitDimensions(group.definitions[0])
    : (group: DefinitionGroup) => decisionDimensions(
      group,
      graphEdges,
      Object.fromEntries(graphDefinitions.map((item) => [item.id, item.name])),
    );
  const measured = sectionDimensions(groups, dimensionsFor);
  const nodes: Array<Node<DefinitionNodeData>> = [];
  let gridTop = top;
  if (groups.length > 1) {
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
  const columns = Math.min(3, groups.length);
  const rows = Math.ceil(groups.length / columns);
  const rowHeights = Array.from({ length: rows }, (_, row) => Math.max(
    ...groups.slice(row * columns, row * columns + columns).map((group) => dimensionsFor(group).height),
  ));
  let rowTop = gridTop;
  for (let index = 0; index < groups.length; index += 1) {
    const group = groups[index];
    const definition = group.definitions[0];
    const row = Math.floor(index / columns);
    const column = index % columns;
    const dimensions = dimensionsFor(group);
    const rowCount = Math.min(columns, groups.length - row * columns);
    const rowWidth = rowCount * cardWidth + (rowCount - 1) * branchGap;
    const left = (contentWidth - rowWidth) / 2 + column * (cardWidth + branchGap) + stepGap;
    const definitionIDs = new Set(group.definitions.map((item) => item.id));
    const relatedEdges = graphEdges.filter((edge) => definitionIDs.has(edge.from));
    const nameByID = Object.fromEntries(graphDefinitions.map((item) => [item.id, item.name]));
    nodes.push({
      id: group.id,
      type: kind === 'wait' ? 'definitionWait' : 'definitionDecision',
      parentId: parentID,
      position: { x: left, y: rowTop },
      style: dimensions,
      data: {
        kind,
        definition,
        definitions: group.definitions,
        relatedEdges,
        nameByID,
        selectionDetails: kind === 'decision' ? decisionSelectionDetails(group.definitions) : undefined,
        sourceTitle: sourceTitle(definition.span),
      },
    });
    if (column === rowCount - 1) rowTop += rowHeights[row] + branchGap;
  }
  return { height: measured.height, nodes };
}

function waitDimensions(definition: FlowDefinitionNode): Dimensions {
  const headerLines = wrappedLineCount(`WaitFor ${definition.wait?.type ?? definition.name}`, 30);
  const conditionLines = (definition.wait?.conditions ?? []).reduce(
    (total, condition) => total + wrappedLineCount(`${waitIconPrefix(condition.kind)} ${condition.label}`, 34),
    0,
  );
  return { height: Math.max(108, 54 + headerLines * 18 + conditionLines * 20), width: cardWidth };
}

function decisionDimensions(
  group: DefinitionGroup,
  graphEdges: FlowDefinitionEdge[] = [],
  nameByID: Record<string, string> = {},
): Dimensions {
  const definition = group.definitions[0];
  const definitionIDs = new Set(group.definitions.map((item) => item.id));
  const headerLines = wrappedLineCount(definition.decision?.type ?? definition.name, 30);
  const transitionLines = uniqueEdgesByTarget(graphEdges
    .filter((edge) => definitionIDs.has(edge.from) && edge.kind === 'transition'))
    .reduce((total, edge) => total + wrappedLineCount(
      `Step ${nameByID[edge.to] ?? shortID(edge.to)} ${edge.multiplicity ?? ''}`,
      34,
    ), 0);
  const channelLines = (definition.decision?.checkedChannels ?? []).reduce(
    (total, channelID) => total + wrappedLineCount(`Channel ${nameByID[channelID] ?? shortID(channelID)}`, 34),
    0,
  );
  const cancellationLines = (definition.decision?.cancellations ?? []).reduce(
    (total, cancellation) => total + wrappedLineCount(
      `Cancel ${nameByID[cancellation.stepId] ?? shortID(cancellation.stepId)} ${cancellation.scope === 'siblings' ? 'siblings' : ''}`,
      31,
    ), 0);
  const rowLines = transitionLines + channelLines + cancellationLines;
  return { height: Math.max(102, 48 + headerLines * 18 + rowLines * 22), width: cardWidth };
}

function singleDefinitionGroup(definition: FlowDefinitionNode): DefinitionGroup {
  return { definitions: [definition], id: definition.id };
}

function groupDecisionDefinitions(
  definitions: FlowDefinitionNode[],
  graphEdges: FlowDefinitionEdge[],
): DefinitionGroup[] {
  const groups: DefinitionGroup[] = [];
  const groupsByTarget = new Map<string, DefinitionGroup>();
  for (const definition of definitions) {
    const key = decisionTargetKey(definition, graphEdges);
    const existing = key ? groupsByTarget.get(key) : undefined;
    if (existing) {
      existing.definitions.push(definition);
      continue;
    }
    const group = singleDefinitionGroup(definition);
    groups.push(group);
    if (key) groupsByTarget.set(key, group);
  }
  return groups;
}

function decisionTargetKey(
  definition: FlowDefinitionNode,
  graphEdges: FlowDefinitionEdge[],
): string {
  const transitions = graphEdges
    .filter((edge) => edge.kind === 'transition' && edge.from === definition.id)
    .map((edge) => `${edge.to}:${edge.multiplicity ?? ''}`)
    .sort();
  if (transitions.length === 0) return '';
  return JSON.stringify({
    cancellations: definition.decision?.cancellations ?? [],
    checkedChannels: [...(definition.decision?.checkedChannels ?? [])].sort(),
    transitions,
    type: definition.decision?.type ?? definition.name,
  });
}

function decisionSelectionDetails(definitions: FlowDefinitionNode[]): DefinitionSelectionDetail[] {
  return definitions.map((definition) => ({
    label: definition.condition || 'otherwise',
    sourceTitle: sourceTitle(definition.span),
  }));
}

function uniqueEdgesByTarget(edges: FlowDefinitionEdge[]): FlowDefinitionEdge[] {
  const seen = new Set<string>();
  return edges.filter((edge) => {
    const key = `${edge.to}:${edge.multiplicity ?? ''}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
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
  dagreGraph.setGraph({
    rankdir: 'TB',
    ranksep: stepTopologyRankSeparation,
    nodesep: 104,
    marginx: 0,
    marginy: 0,
  });
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
  const positions = new Map(raw.map((position) => [position.id, {
    x: position.x - minimumX,
    y: position.y - minimumY,
  }]));
  return placeStartStepFirst(graph.flow.startStepId, steps, stepLayouts, positions);
}

function placeStartStepFirst(
  startStepID: string | undefined,
  steps: FlowDefinitionNode[],
  stepLayouts: Map<string, StepLayout>,
  positions: Map<string, { x: number; y: number }>,
): Map<string, { x: number; y: number }> {
  if (!startStepID) return positions;
  const startPosition = positions.get(startStepID);
  if (!startPosition) return positions;
  const minimumY = Math.min(...[...positions.values()].map((position) => position.y));
  if (startPosition.y === minimumY) return positions;
  const startDimensions = stepLayouts.get(startStepID)!.dimensions;
  const topologyWidth = Math.max(...steps.map((step) => {
    const position = positions.get(step.id)!;
    return position.x + stepLayouts.get(step.id)!.dimensions.width;
  }));
  const verticalOffset = startDimensions.height + stepTopologyRankSeparation;
  const reordered = new Map([...positions].map(([stepID, position]) => [stepID, stepID === startStepID
    ? { x: Math.max(0, (topologyWidth - startDimensions.width) / 2), y: 0 }
    : { x: position.x, y: position.y + verticalOffset }]));
  return reordered;
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
      const height = Math.max(60, 38 + wrappedLineCount(channel.name, 30) * 18);
      nodes.push({
        id: channel.id,
        type: 'definitionChannel',
        parentId: flowID,
        position: { x: 180, y: top },
        style: { height, width: 258 },
        data: { kind: 'channel', definition: channel, sourceTitle: sourceTitle(channel.span) },
      });
      top += height + 18;
    }
  }
  const attributes = visibility.attributes
    ? graph.nodes.filter((node) => node.kind === 'attribute').sort(byNameThenID)
    : [];
  if (attributes.length > 0) {
    top += 12;
    const rowLines = attributes.reduce((total, attribute) => total + wrappedLineCount(
      `- ${attribute.name}:${attribute.resource?.valueType ?? 'unknown'}${attribute.resource?.map ? ' map' : ''}`,
      35,
    ), 0);
    const height = 60 + rowLines * 19 + attributes.length * 4;
    nodes.push({
      id: attributeGroupID,
      type: 'definitionAttributes',
      parentId: flowID,
      position: { x: 168, y: top },
      style: { height, width: 270 },
      data: { kind: 'attributes', definitions: attributes },
    });
    top += height + 20;
  }
  if (visibility.streams) {
    for (const stream of graph.nodes.filter((node) => node.kind === 'stream').sort(byNameThenID)) {
      const height = Math.max(60, 38 + wrappedLineCount(stream.name, 30) * 18);
      nodes.push({
        id: stream.id,
        type: 'definitionStream',
        parentId: flowID,
        position: { x: 180, y: top },
        style: { height, width: 258 },
        data: { kind: 'stream', definition: stream, sourceTitle: sourceTitle(stream.span) },
      });
      top += height + 18;
    }
  }
  return nodes;
}

function layoutRPCsAndTimeoutHandlers(
  graph: FlowDefinitionGraph,
  visibility: DefinitionVisibility,
): Array<Node<DefinitionNodeData>> {
  const nameByID = Object.fromEntries(graph.nodes.map((node) => [node.id, node.name]));
  let top = flowHeaderHeight + 36;
  return graph.nodes
    .filter((node) => node.kind === 'timeout_handler' || (visibility.rpcs && node.kind === 'rpc'))
    .sort(byNameThenID)
    .map((handler) => {
      const decisions = graph.nodes.filter((node) => node.kind === 'decision' && node.parentId === handler.id);
      const relatedEdges = decisions.flatMap((decision) => graph.edges.filter((edge) => edge.from === decision.id));
      const isTimeout = handler.kind === 'timeout_handler';
      const decisionLines = decisions.reduce((total, decision) => {
        const movementLines = relatedEdges
          .filter((edge) => edge.from === decision.id && edge.kind === 'transition')
          .reduce((count, edge) => count + wrappedLineCount(nameByID[edge.to] ?? shortID(edge.to), 24), 0);
        return total + wrappedLineCount(decision.decision?.type ?? decision.name, 24) + movementLines;
      }, 0);
      const height = isTimeout
        ? Math.max(104, 62 + decisionLines * 19)
        : Math.max(68, 42 + wrappedLineCount(handler.name, 28) * 18);
      const node: Node<DefinitionNodeData> = {
        id: handler.id,
        type: isTimeout ? 'definitionTimeout' : 'definitionRPC',
        parentId: flowID,
        position: { x: -132, y: top },
        style: { height, width: 260 },
        data: {
          kind: isTimeout ? 'timeout' : 'rpc',
          definition: handler,
          definitions: decisions,
          relatedEdges,
          nameByID,
          sourceTitle: sourceTitle(handler.span),
        },
      };
      top += height + 30;
      return node;
    });
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
    const height = Math.max(72, 42 + wrappedLineCount(subflow.name, 28) * 19);
    nextTopByStep.set(stepID, top + height + 26);
    return {
      id: subflow.id,
      type: 'definitionSubFlow',
      parentId: flowID,
      position: { x: left, y: top },
      style: { height, width: 238 },
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
  let nextTop = top;
  return graph.nodes.filter((node) => node.kind === 'unknown' && !node.parentId).sort(byNameThenID).map((unknown) => {
    const height = Math.max(68, 44 + wrappedLineCount(unknown.name, 30) * 18);
    const node: Node<DefinitionNodeData> = {
      id: unknown.id,
      type: 'definitionUnknown',
      parentId: flowID,
      position: { x: left, y: nextTop },
      style: { height, width: 238 },
      data: { kind: 'unknown' as const, definition: unknown, sourceTitle: sourceTitle(unknown.span) },
    };
    nextTop += height + 26;
    return node;
  });
}

function definitionEdge(
  definition: FlowDefinitionEdge,
  endpointMap: Map<string, string>,
  definitionsByID: Map<string, FlowDefinitionNode>,
  stepNodes: Array<Node<DefinitionNodeData>>,
): Edge<DefinitionEdgeData> {
  const source = endpointMap.get(definition.from) ?? definition.from;
  const target = endpointMap.get(definition.to) ?? definition.to;
  const sourceDefinition = definitionsByID.get(definition.from);
  const targetDefinition = definitionsByID.get(definition.to);
  const color = edgeColor(definition.kind);
  const label = edgeLabel(definition);
  const isOuterRight = usesOuterRightRoute(definition, sourceDefinition, targetDefinition, stepNodes);
  const routedSource = isOuterRight && sourceDefinition?.kind === 'decision' && sourceDefinition.parentId
    ? sourceDefinition.parentId
    : source;
  return {
    id: definition.id,
    source: routedSource,
    sourceHandle: sourceHandle(definition.kind, sourceDefinition, isOuterRight),
    target,
    targetHandle: targetHandle(definition.kind, targetDefinition, isOuterRight),
    label,
    type: 'definitionEdge',
    interactionWidth: 28,
    markerEnd: { type: MarkerType.ArrowClosed, width: 17, height: 17, color },
    style: {
      stroke: color,
      strokeDasharray: isDashed(definition.kind) ? '6 5' : undefined,
      strokeWidth: definition.kind === 'failure_transition' ? 2.8 : definition.kind === 'transition' ? 2 : 1.6,
    },
    data: {
      definitionSourceID: definition.from,
      definitionTargetID: definition.to,
      displayLabel: definition.condition || label || definition.label,
      kind: definition.kind,
      route: isOuterRight ? 'outer-right' : 'forward',
      sceneSourceID: source,
      selectionDetails: sourceDefinition?.kind === 'decision' && definition.kind === 'transition'
        ? decisionSelectionDetails([sourceDefinition])
        : undefined,
      title: [definition.condition, definition.label, sourceTitle(definition.span)].filter(Boolean).join(' · '),
    },
  };
}

function mergeGroupedTransitionEdges(
  edges: Array<Edge<DefinitionEdgeData>>,
): Array<Edge<DefinitionEdgeData>> {
  const merged: Array<Edge<DefinitionEdgeData>> = [];
  const transitionsByTarget = new Map<string, Edge<DefinitionEdgeData>>();
  for (const edge of edges) {
    if (edge.data?.kind !== 'transition' || !edge.data.sceneSourceID) {
      merged.push(edge);
      continue;
    }
    const key = `${edge.data.sceneSourceID}:${edge.target}:${edge.sourceHandle ?? ''}:${edge.targetHandle ?? ''}`;
    const existing = transitionsByTarget.get(key);
    if (!existing) {
      const copy = { ...edge, data: { ...edge.data } };
      transitionsByTarget.set(key, copy);
      merged.push(copy);
      continue;
    }
    const selectionDetails = [
      ...(existing.data?.selectionDetails ?? []),
      ...(edge.data.selectionDetails ?? []),
    ];
    existing.data = {
      ...existing.data,
      displayLabel: `${selectionDetails.length} conditions`,
      selectionDetails,
      title: `${selectionDetails.length} conditions lead to the same target`,
    };
  }
  return merged;
}

function sourceHandle(
  kind: string,
  definition?: FlowDefinitionNode,
  isOuterRight = false,
): string | undefined {
  if (isOuterRight && (definition?.kind === 'decision' || definition?.kind === 'step')) {
    return 'step-control-outer-source';
  }
  if (definition?.kind !== 'step') return undefined;
  if (kind === 'failure_transition') return 'step-recovery-source';
  if (kind.startsWith('resource_')) return 'step-resource-source';
  return undefined;
}

function targetHandle(
  kind: string,
  definition?: FlowDefinitionNode,
  isOuterRight = false,
): string | undefined {
  if (definition?.kind !== 'step') return undefined;
  if (isOuterRight) return 'step-control-outer-target';
  if (kind === 'transition' || kind === 'failure_transition') return 'step-target';
  if (kind.startsWith('resource_')) return 'step-resource-target';
  return undefined;
}

function usesOuterRightRoute(
  edge: FlowDefinitionEdge,
  sourceDefinition: FlowDefinitionNode | undefined,
  targetDefinition: FlowDefinitionNode | undefined,
  stepNodes: Array<Node<DefinitionNodeData>>,
): boolean {
  if (edge.kind !== 'transition' && edge.kind !== 'failure_transition') return false;
  const sourceStepID = sourceDefinition?.kind === 'decision'
    ? sourceDefinition.parentId
    : sourceDefinition?.kind === 'step' ? sourceDefinition.id : undefined;
  if (!sourceStepID || targetDefinition?.kind !== 'step') return false;
  const positions = new Map(stepNodes.map((node) => [node.id, node.position]));
  const sourcePosition = positions.get(sourceStepID);
  const targetPosition = positions.get(targetDefinition.id);
  if (!sourcePosition || !targetPosition) return false;
  return sourceStepID === targetDefinition.id || targetPosition.y <= sourcePosition.y + 24;
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
          interactionWidth: 28,
          markerEnd: { type: MarkerType.ArrowClosed, width: 13, height: 13, color: '#94a3b8' },
          style: { stroke: '#94a3b8', strokeWidth: 1.35 },
          data: { kind: 'phase', title: 'WaitFor completes before Execute' },
        });
      }
    }
  }
  return edges.filter((edge) => visibleIDs.has(edge.source) && visibleIDs.has(edge.target));
}

function branchEdge(source: string, target: Node<DefinitionNodeData>): Edge<DefinitionEdgeData> {
  const selectionDetails = target.data.selectionDetails?.length
    ? target.data.selectionDetails
    : [{
      label: target.data.definition?.condition || 'otherwise',
      sourceTitle: sourceTitle(target.data.definition?.span),
    }];
  const condition = selectionDetails.length === 1
    ? selectionDetails[0].label
    : `${selectionDetails.length} conditions`;
  return {
    id: `branch:${source}:${target.id}`,
    source,
    target: target.id,
    label: condition,
    type: 'definitionEdge',
    interactionWidth: 28,
    markerEnd: { type: MarkerType.ArrowClosed, width: 13, height: 13, color: '#64748b' },
    style: { stroke: '#64748b', strokeWidth: 1.35 },
    data: {
      displayLabel: condition,
      kind: 'branch',
      selectionDetails,
      title: selectionDetails.length > 1
        ? `${selectionDetails.length} conditions lead to the same target`
        : [condition, sourceTitle(target.data.definition?.span)].filter(Boolean).join(' · '),
    },
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

function stepHeaderHeight(name: string, width: number): number {
  const availableCharacters = Math.max(24, Math.floor((width - 112) / 8));
  return Math.max(52, 28 + wrappedLineCount(name, availableCharacters) * 20);
}

function wrappedLineCount(value: string, charactersPerLine: number): number {
  if (!value) return 1;
  return Math.max(1, Math.ceil(value.length / charactersPerLine));
}

function waitIconPrefix(kind: string): string {
  if (kind === 'channel') return 'channel';
  if (kind === 'timer') return 'timer';
  if (kind === 'subflow') return 'subflow';
  return 'unknown';
}

function shortID(id: string): string {
  return id.split(':').at(-1) ?? id;
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
