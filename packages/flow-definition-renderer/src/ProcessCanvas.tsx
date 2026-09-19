// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useMemo, useState } from 'react';
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
  type NodeTypes,
  type ReactFlowInstance,
} from '@xyflow/react';
import type {
  FlowDefinitionEdge,
  FlowDefinitionGraph,
  FlowDefinitionGroup,
  FlowDefinitionNode,
} from './types';

type ProcessCanvasDirection = 'tb' | 'lr';
type ProcessCanvasDetail = 'collapsed' | 'expanded';

interface ProcessStepData extends Record<string, unknown> {
  definition: FlowDefinitionNode;
  detail: ProcessCanvasDetail;
  direction: ProcessCanvasDirection;
  waitDescriptions: string[];
  destinationNames: string[];
}

interface ProcessGroupData extends Record<string, unknown> {
  group: FlowDefinitionGroup;
  hue: number;
}

export interface ProcessCanvasScene {
  nodes: Array<Node<ProcessStepData | ProcessGroupData>>;
  edges: Edge[];
}

const processNodeTypes: NodeTypes = {
  processStep: ProcessStepNode,
  processGroup: ProcessGroupNode,
};

export function ProcessCanvasView({ graph }: { graph: FlowDefinitionGraph }) {
  const [detail, setDetail] = useState<ProcessCanvasDetail>('collapsed');
  const [direction, setDirection] = useState<ProcessCanvasDirection>('tb');
  const [selectedID, setSelectedID] = useState('');
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance | null>(null);
  const scene = useMemo(
    () => buildProcessCanvasScene(graph, detail, direction),
    [detail, direction, graph],
  );
  const selectedStep = graph.nodes.find((node) => node.id === selectedID && node.kind === 'step');
  const selectedGroup = graph.groups?.find((group) => `group:${group.id}` === selectedID);

  useEffect(() => {
    if (!flowInstance || scene.nodes.length === 0) return;
    void flowInstance.fitView({ duration: 180, maxZoom: 1.15, minZoom: 0.16, padding: 0.08 });
  }, [detail, direction, flowInstance, scene.nodes.length]);

  return (
    <section className="process-canvas-view">
      <div className="process-canvas-toolbar">
        <div>
          <p className="eyebrow">Process Canvas · FDG 2.0</p>
          <h2>{graph.flow.name}</h2>
          <p>{graph.source.language} · {graph.source.path}</p>
        </div>
        <div className="process-canvas-options">
          <div className="process-segmented" aria-label="Process detail" role="group">
            {(['collapsed', 'expanded'] as const).map((option) => (
              <button
                aria-pressed={detail === option}
                key={option}
                onClick={() => setDetail(option)}
                type="button"
              >
                {option === 'collapsed' ? 'Collapsed' : 'Expanded'}
              </button>
            ))}
          </div>
          <div className="process-segmented" aria-label="Process direction" role="group">
            {(['tb', 'lr'] as const).map((option) => (
              <button
                aria-pressed={direction === option}
                key={option}
                onClick={() => setDirection(option)}
                type="button"
              >
                {option === 'tb' ? 'Top-down' : 'Left-right'}
              </button>
            ))}
          </div>
        </div>
      </div>
      <div className="process-canvas-stage">
        <ReactFlow
          edges={scene.edges}
          elementsSelectable
          fitView
          fitViewOptions={{ maxZoom: 1.15, minZoom: 0.16, padding: 0.08 }}
          maxZoom={1.8}
          minZoom={0.16}
          nodes={scene.nodes}
          nodesConnectable={false}
          nodesDraggable={false}
          nodeTypes={processNodeTypes}
          onInit={setFlowInstance}
          onNodeClick={(_, node) => setSelectedID(node.id)}
          onPaneClick={() => setSelectedID('')}
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={24} size={1} color="#dce4dc" />
          <Controls position="top-right" showInteractive={false} />
        </ReactFlow>
      </div>
      {(selectedStep || selectedGroup) && (
        <div className="process-canvas-selection">
          {selectedStep && <ProcessStepDetails graph={graph} step={selectedStep} />}
          {selectedGroup && (
            <>
              <span>Group</span>
              <strong>{selectedGroup.label}</strong>
              <p>{selectedGroup.stepIds.length} registered Steps</p>
            </>
          )}
        </div>
      )}
      {graph.diagnostics.length > 0 && (
        <div className="flow-definition-diagnostics">
          {graph.diagnostics.map((diagnostic, index) => (
            <div className={`flow-definition-diagnostic is-${diagnostic.severity}`} key={`${diagnostic.code}-${index}`}>
              <b>{diagnostic.code}</b>
              <span>{diagnostic.message}</span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

export function buildProcessCanvasScene(
  graph: FlowDefinitionGraph,
  detail: ProcessCanvasDetail = 'collapsed',
  direction: ProcessCanvasDirection = 'tb',
): ProcessCanvasScene {
  const definitions = Array.isArray(graph.nodes) ? graph.nodes : [];
  const definitionsByID = new Map(definitions.map((definition) => [definition.id, definition]));
  const steps = definitions.filter((definition) => definition.kind === 'step');
  const stepsByID = new Map(steps.map((step) => [step.id, step]));
  const assigned = new Set<string>();
  const protocolGroups = Array.isArray(graph.groups) ? graph.groups : [];
  const groups = protocolGroups.map((group) => {
    const stepIds: string[] = [];
    if (Array.isArray(group.stepIds)) {
      for (const stepID of group.stepIds) {
        if (typeof stepID !== 'string' || !stepsByID.has(stepID) || assigned.has(stepID)) continue;
        assigned.add(stepID);
        stepIds.push(stepID);
      }
    }
    return {
      id: safeText(group.id),
      label: safeText(group.label) || safeText(group.id),
      stepIds,
    };
  }).filter((group) => group.id !== '' && group.stepIds.length > 0);
  const ungrouped = steps.filter((step) => !assigned.has(step.id)).map((step) => step.id);
  if (ungrouped.length > 0) {
    groups.push({ id: 'ungrouped', label: 'Ungrouped', stepIds: ungrouped });
  }

  const nodes: Array<Node<ProcessStepData | ProcessGroupData>> = [];
  let groupCursor = 36;
  groups.forEach((group, groupIndex) => {
    const stepWidth = detail === 'expanded' ? 284 : 226;
    const stepHeight = detail === 'expanded' ? 172 : 92;
    const acrossCount = Math.min(3, Math.max(1, group.stepIds.length));
    const alongCount = Math.ceil(group.stepIds.length / acrossCount);
    const bandWidth = direction === 'tb'
      ? acrossCount * stepWidth + (acrossCount - 1) * 28 + 72
      : alongCount * stepWidth + (alongCount - 1) * 38 + 72;
    const bandHeight = direction === 'tb'
      ? alongCount * stepHeight + (alongCount - 1) * 34 + 92
      : acrossCount * stepHeight + (acrossCount - 1) * 24 + 92;
    const bandX = direction === 'tb' ? 48 : groupCursor;
    const bandY = direction === 'tb' ? groupCursor : 48;
    nodes.push({
      id: `group:${group.id}`,
      type: 'processGroup',
      position: { x: bandX, y: bandY },
      style: { width: bandWidth, height: bandHeight, zIndex: 0 },
      data: { group, hue: groupIndex % 6 },
      selectable: true,
      zIndex: 0,
    });
    group.stepIds.forEach((stepID, stepIndex) => {
      const row = Math.floor(stepIndex / acrossCount);
      const column = stepIndex % acrossCount;
      const localX = direction === 'tb'
        ? 36 + column * (stepWidth + 28)
        : 36 + row * (stepWidth + 38);
      const localY = direction === 'tb'
        ? 56 + row * (stepHeight + 34)
        : 56 + column * (stepHeight + 24);
      const position = { x: bandX + localX, y: bandY + localY, width: stepWidth, height: stepHeight };
      const step = stepsByID.get(stepID)!;
      nodes.push({
        id: stepID,
        type: 'processStep',
        position: { x: position.x, y: position.y },
        style: { width: stepWidth, height: stepHeight, zIndex: 2 },
        data: {
          definition: step,
          detail,
          direction,
          waitDescriptions: stepWaitDescriptions(step, definitions),
          destinationNames: stepDestinations(step, graph.edges, definitionsByID),
        },
        zIndex: 2,
      });
    });
    groupCursor += (direction === 'tb' ? bandHeight : bandWidth) + 34;
  });

  const edges = controlTopologyEdges(graph.edges, definitionsByID, stepsByID).map((edge) => ({
    id: edge.id,
    source: edge.from,
    target: edge.to,
    sourceHandle: direction === 'tb' ? 'vertical-source' : 'horizontal-source',
    targetHandle: direction === 'tb' ? 'vertical-target' : 'horizontal-target',
    type: 'smoothstep',
    markerEnd: {
      type: MarkerType.ArrowClosed,
      color: edge.kind === 'failure_transition' ? '#b84a4a' : '#4d7661',
      height: 17,
      width: 17,
    },
    label: edge.count > 1 ? `${edge.count} paths` : undefined,
    style: {
      stroke: edge.kind === 'failure_transition' ? '#b84a4a' : '#4d7661',
      strokeDasharray: edge.kind === 'failure_transition' ? '7 5' : undefined,
      strokeWidth: edge.kind === 'failure_transition' ? 2.4 : 2,
    },
    zIndex: 1,
  }));
  return { nodes, edges };
}

function controlTopologyEdges(
  edges: FlowDefinitionEdge[],
  definitionsByID: Map<string, FlowDefinitionNode>,
  stepsByID: Map<string, FlowDefinitionNode>,
): Array<{ id: string; from: string; to: string; kind: string; count: number }> {
  const merged = new Map<string, { id: string; from: string; to: string; kind: string; count: number }>();
  for (const edge of Array.isArray(edges) ? edges : []) {
    if (edge.kind !== 'transition' && edge.kind !== 'failure_transition') continue;
    const from = stepOwnerID(edge.from, definitionsByID);
    const to = stepOwnerID(edge.to, definitionsByID);
    if (!stepsByID.has(from) || !stepsByID.has(to)) continue;
    const key = `${from}:${to}:${edge.kind}`;
    const existing = merged.get(key);
    if (existing) existing.count += 1;
    else merged.set(key, { id: edge.id || key, from, to, kind: edge.kind, count: 1 });
  }
  return [...merged.values()];
}

function stepOwnerID(id: string, definitionsByID: Map<string, FlowDefinitionNode>): string {
  let current = definitionsByID.get(id);
  const visited = new Set<string>();
  while (current && !visited.has(current.id)) {
    if (current.kind === 'step') return current.id;
    visited.add(current.id);
    current = current.parentId ? definitionsByID.get(current.parentId) : undefined;
  }
  return id;
}

function stepWaitDescriptions(step: FlowDefinitionNode, definitions: FlowDefinitionNode[]): string[] {
  return definitions
    .filter((definition) => definition.kind === 'wait' && definition.parentId === step.id)
    .flatMap((definition) => {
      const conditions = Array.isArray(definition.wait?.conditions) ? definition.wait.conditions : [];
      if (conditions.length === 0) return ['Unresolved wait condition'];
      return conditions.map((condition) => `${condition.kind}: ${safeText(condition.label)}`);
    });
}

function stepDestinations(
  step: FlowDefinitionNode,
  edges: FlowDefinitionEdge[],
  definitionsByID: Map<string, FlowDefinitionNode>,
): string[] {
  const names = new Set<string>();
  for (const edge of Array.isArray(edges) ? edges : []) {
    if (edge.kind !== 'transition' && edge.kind !== 'failure_transition') continue;
    if (stepOwnerID(edge.from, definitionsByID) !== step.id) continue;
    const target = definitionsByID.get(stepOwnerID(edge.to, definitionsByID));
    if (target) names.add(safeText(target.name));
  }
  return [...names];
}

function ProcessStepNode({ data }: NodeProps) {
  const step = data as ProcessStepData;
  const direction = step.direction;
  return (
    <div className="process-step-card">
      <Handle id="vertical-target" position={Position.Top} type="target" />
      <Handle id="horizontal-target" position={Position.Left} type="target" />
      <div className="process-step-heading">
        <span>{step.definition.start ? 'Start' : 'Step'}</span>
        <strong>{safeText(step.definition.name)}</strong>
      </div>
      <code>{safeText(step.definition.id).replace(/^step:/, '')}</code>
      {step.detail === 'expanded' && (
        <div className="process-step-sections">
          <span><b>Waits for</b>{step.waitDescriptions[0] ?? 'Nothing'}</span>
          <span><b>Then goes to</b>{step.destinationNames.join(', ') || 'Flow completion'}</span>
        </div>
      )}
      <Handle id="vertical-source" position={Position.Bottom} type="source" />
      <Handle id="horizontal-source" position={Position.Right} type="source" />
      <i aria-hidden="true" data-direction={direction} />
    </div>
  );
}

function ProcessGroupNode({ data }: NodeProps) {
  const group = data as ProcessGroupData;
  return (
    <div className="process-group-band" data-hue={group.hue}>
      <span>{safeText(group.group.label)}</span>
    </div>
  );
}

function ProcessStepDetails({ graph, step }: { graph: FlowDefinitionGraph; step: FlowDefinitionNode }) {
  const waits = stepWaitDescriptions(step, graph.nodes);
  const destinations = stepDestinations(step, graph.edges, new Map(graph.nodes.map((node) => [node.id, node])));
  return (
    <>
      <span>Selected Step</span>
      <strong>{safeText(step.name)}</strong>
      <code>{safeText(step.id)}</code>
      <p>{waits.length > 0 ? `Waits for ${waits.join(', ')}` : 'No WaitFor phase'}</p>
      <p>{destinations.length > 0 ? `Can proceed to ${destinations.join(', ')}` : 'No outgoing Step transition'}</p>
    </>
  );
}

function safeText(value: unknown): string {
  return typeof value === 'string' ? value : '';
}
