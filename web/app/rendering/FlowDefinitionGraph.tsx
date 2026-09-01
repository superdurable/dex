// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { useEffect, useMemo, useState } from 'react';
import {
  Background,
  Controls,
  Handle,
  MarkerType,
  MiniMap,
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
  FlowDefinitionNode,
  SourceSpan,
} from '@/lib/types';
import { layoutGraph } from '../flows/details/graphLayout';

type DefinitionLayer =
  | 'control'
  | 'waits'
  | 'handlers'
  | 'attributes'
  | 'channels'
  | 'streams'
  | 'subflows'
  | 'diagnostics';

interface DefinitionNodeData extends Record<string, unknown> {
  definition: FlowDefinitionNode;
  model: { kind: 'definition' };
}

const layerLabels: Array<[DefinitionLayer, string]> = [
  ['control', 'Control flow'],
  ['waits', 'Waits'],
  ['handlers', 'RPC / timeout'],
  ['attributes', 'Attributes'],
  ['channels', 'Channels'],
  ['streams', 'Streams'],
  ['subflows', 'SubFlows'],
  ['diagnostics', 'Diagnostics'],
];

const defaultVisibility: Record<DefinitionLayer, boolean> = {
  control: true,
  waits: true,
  handlers: true,
  attributes: false,
  channels: false,
  streams: false,
  subflows: true,
  diagnostics: true,
};

const nodeTypes: NodeTypes = { definition: DefinitionNode };

export function FlowDefinitionGraphView({
  displayName,
  graph,
}: {
  displayName: string;
  graph: FlowDefinitionGraph;
}) {
  const [visibility, setVisibility] = useState(defaultVisibility);
  const [selectedNodeID, setSelectedNodeID] = useState('');
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance<Node<DefinitionNodeData>, Edge> | null>(null);
  const visibleDefinitions = useMemo(
    () => graph.nodes.filter((node) => visibility[layerForNode(node)]),
    [graph.nodes, visibility],
  );
  const visibleNodeIDs = useMemo(
    () => new Set(visibleDefinitions.map((node) => node.id)),
    [visibleDefinitions],
  );
  const edges = useMemo<Edge[]>(() => graph.edges
    .filter((edge) => visibleNodeIDs.has(edge.from) && visibleNodeIDs.has(edge.to))
    .map((edge) => {
      const color = edgeColor(edge.kind);
      return {
        id: edge.id,
        source: edge.from,
        target: edge.to,
        label: edgeLabel(edge),
        type: 'smoothstep',
        markerEnd: { type: MarkerType.ArrowClosed, width: 18, height: 18, color },
        style: {
          stroke: color,
          strokeDasharray: edgeDash(edge.kind),
          strokeWidth: edge.kind === 'failure_transition' ? 2.5 : 1.7,
        },
        labelStyle: { fill: '#46534a', fontSize: 11, fontWeight: 650 },
      };
    }), [graph.edges, visibleNodeIDs]);
  const nodes = useMemo(() => layoutGraph(visibleDefinitions.map((definition) => {
    const dimensions = nodeDimensions(definition.kind);
    return {
      id: definition.id,
      type: 'definition',
      position: { x: 0, y: 0 },
      className: [
        'flow-definition-node',
        `flow-definition-node--${definition.kind}`,
        selectedNodeID === definition.id ? 'is-selected' : '',
      ].filter(Boolean).join(' '),
      width: dimensions.width,
      height: dimensions.height,
      style: dimensions,
      data: { definition, model: { kind: 'definition' as const } },
    };
  }), edges), [edges, selectedNodeID, visibleDefinitions]);
  const selectedNode = graph.nodes.find((node) => node.id === selectedNodeID);

  useEffect(() => {
    if (!flowInstance || nodes.length === 0) return;
    void flowInstance.fitView({ duration: 180, maxZoom: 1.15, padding: 0.12 });
  }, [flowInstance, nodes]);

  useEffect(() => {
    if (selectedNodeID && !visibleNodeIDs.has(selectedNodeID)) setSelectedNodeID('');
  }, [selectedNodeID, visibleNodeIDs]);

  return (
    <section className="flow-definition-view">
      <div className="flow-definition-toolbar">
        <div>
          <p className="eyebrow">Definition graph</p>
          <div className="flow-definition-title">
            <h2>{graph.flow.name || displayName}</h2>
            <span className={graph.valid ? 'is-valid' : 'is-invalid'}>
              {graph.valid ? 'Valid' : 'Needs attention'}
            </span>
          </div>
          <p>{graph.source.language} · {graph.source.path}</p>
        </div>
        <div className="flow-definition-legend" aria-label="Graph visibility">
          {layerLabels.map(([layer, label]) => (
            <button
              aria-pressed={visibility[layer]}
              className={visibility[layer] ? 'is-visible' : ''}
              key={layer}
              onClick={() => setVisibility((current) => ({ ...current, [layer]: !current[layer] }))}
              type="button"
            >
              <i className={`definition-legend-swatch definition-legend-swatch--${layer}`} />
              {label}
            </button>
          ))}
        </div>
      </div>

      {nodes.length === 0 ? (
        <div className="card empty-state"><h3>All graph elements are hidden</h3></div>
      ) : (
        <div className="flow-definition-canvas">
          <ReactFlow
            edges={edges}
            elementsSelectable
            fitView
            maxZoom={2}
            minZoom={0.2}
            nodes={nodes}
            nodesConnectable={false}
            nodesDraggable={false}
            nodeTypes={nodeTypes}
            onInit={setFlowInstance}
            onNodeClick={(_, node) => setSelectedNodeID(node.id)}
          >
            <Background gap={22} size={1} color="#dfe7e5" />
            <Controls position="top-right" />
            <MiniMap pannable position="bottom-right" zoomable nodeStrokeWidth={2} />
          </ReactFlow>
        </div>
      )}

      {selectedNode && (
        <div className="flow-definition-selection card">
          <div>
            <span>{kindLabel(selectedNode.kind)}</span>
            <b>{selectedNode.name}</b>
          </div>
          <code>{selectedNode.id}</code>
          {selectedNode.span && <span>{sourceLocation(selectedNode.span)}</span>}
        </div>
      )}

      {visibility.diagnostics && graph.diagnostics.length > 0 && (
        <div className="flow-definition-diagnostics">
          {graph.diagnostics.map((diagnostic, index) => (
            <div className={`flow-definition-diagnostic is-${diagnostic.severity}`} key={`${diagnostic.code}-${index}`}>
              <b>{diagnostic.code}</b>
              <span>{diagnostic.message}</span>
              {diagnostic.span && <code>{sourceLocation(diagnostic.span)}</code>}
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

function DefinitionNode({ data }: NodeProps<Node<DefinitionNodeData>>) {
  const definition = data.definition;
  return (
    <div className="flow-definition-node-body" title={definition.span ? sourceLocation(definition.span) : definition.name}>
      <Handle position={Position.Top} type="target" />
      <span>{kindLabel(definition.kind)}</span>
      <b>{definition.name}</b>
      {definition.phase && <small>{definition.phase.replaceAll('_', ' ')}</small>}
      {definition.start && <em>START</em>}
      <Handle position={Position.Bottom} type="source" />
    </div>
  );
}

function layerForNode(node: FlowDefinitionNode): DefinitionLayer {
  switch (node.kind) {
    case 'wait':
    case 'timer':
      return 'waits';
    case 'rpc':
    case 'timeout_handler':
      return 'handlers';
    case 'attribute':
      return 'attributes';
    case 'channel':
      return 'channels';
    case 'stream':
      return 'streams';
    case 'subflow':
      return 'subflows';
    default:
      return 'control';
  }
}

function nodeDimensions(kind: string): { height: number; width: number } {
  if (kind === 'step') return { height: 92, width: 230 };
  if (kind === 'decision') return { height: 86, width: 160 };
  if (kind === 'attribute' || kind === 'channel' || kind === 'stream') {
    return { height: 72, width: 190 };
  }
  return { height: 76, width: 190 };
}

function kindLabel(kind: string): string {
  const labels: Record<string, string> = {
    attribute: 'Attribute',
    channel: 'Channel',
    decision: 'Decision',
    rpc: 'RPC',
    start: 'Flow start',
    step: 'Step',
    stream: 'Stream',
    subflow: 'SubFlow',
    terminal: 'Terminal',
    timeout_handler: 'Timeout handler',
    timer: 'Timer',
    unknown: 'Unknown',
    wait: 'WaitFor',
  };
  return labels[kind] ?? kind.replaceAll('_', ' ');
}

function edgeLabel(edge: FlowDefinitionEdge): string {
  const recoveryOption = edge.metadata?.skipWaitFor === true ? 'skip WaitFor' : '';
  return [edge.condition || edge.label, edge.multiplicity, recoveryOption]
    .filter(Boolean)
    .join(' · ');
}

function edgeColor(kind: string): string {
  if (kind === 'failure_transition') return '#b63b3b';
  if (kind === 'cancel') return '#7c3aed';
  if (kind.startsWith('resource_')) return '#2e8b57';
  if (kind === 'wait' || kind === 'wait_condition' || kind === 'subflow') return '#c27a18';
  return '#64748b';
}

function edgeDash(kind: string): string | undefined {
  if (kind === 'failure_transition') return '8 5';
  if (kind === 'cancel') return '3 5';
  if (kind.startsWith('resource_') || kind === 'subflow') return '5 5';
  return undefined;
}

function sourceLocation(span: SourceSpan): string {
  return `lines ${span.startLine}:${span.startColumn}–${span.endLine}:${span.endColumn}`;
}
