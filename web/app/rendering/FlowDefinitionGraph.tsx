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
  BaseEdge,
  Controls,
  EdgeLabelRenderer,
  Handle,
  MiniMap,
  Position,
  ReactFlow,
  getSmoothStepPath,
  type EdgeProps,
  type EdgeTypes,
  type NodeProps,
  type NodeTypes,
  type ReactFlowInstance,
} from '@xyflow/react';
import type { FlowDefinitionGraph, FlowDefinitionNode, SourceSpan } from '@/lib/types';
import {
  buildDefinitionScene,
  type DefinitionEdgeData,
  type DefinitionLayer,
  type DefinitionNodeData,
  type DefinitionVisibility,
} from './definitionLayout';

const layerLabels: Array<[DefinitionLayer | 'diagnostics', string]> = [
  ['control', 'Control flow'],
  ['waits', 'WaitFor'],
  ['handlers', 'RPC / timeout'],
  ['channels', 'Channels'],
  ['attributes', 'Attributes'],
  ['streams', 'Streams'],
  ['subflows', 'SubFlows'],
  ['diagnostics', 'Diagnostics'],
];

const defaultVisibility: DefinitionVisibility & { diagnostics: boolean } = {
  control: true,
  waits: true,
  handlers: true,
  attributes: true,
  channels: true,
  streams: false,
  subflows: true,
  diagnostics: true,
};

const nodeTypes: NodeTypes = {
  definitionAttributes: AttributesNode,
  definitionChannel: ChannelNode,
  definitionDecision: DecisionNode,
  definitionDispatch: DispatchNode,
  definitionFlow: FlowNode,
  definitionRPC: RPCNode,
  definitionStep: StepNode,
  definitionStream: StreamNode,
  definitionSubFlow: SubFlowNode,
  definitionUnknown: UnknownNode,
  definitionWait: WaitNode,
};

const edgeTypes: EdgeTypes = { definitionEdge: DefinitionEdge };

export function FlowDefinitionGraphView({
  displayName,
  graph,
}: {
  displayName: string;
  graph: FlowDefinitionGraph;
}) {
  const [visibility, setVisibility] = useState(defaultVisibility);
  const [selectedNodeID, setSelectedNodeID] = useState('');
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance | null>(null);
  const scene = useMemo(
    () => buildDefinitionScene(graph, visibility),
    [graph, visibility],
  );
  const selectedNode = scene.nodes.find((node) => node.id === selectedNodeID);

  useEffect(() => {
    if (!flowInstance || scene.nodes.length === 0) return;
    void flowInstance.fitView({ duration: 220, maxZoom: 1, minZoom: 0.7, padding: 0.1 });
  }, [flowInstance, scene]);

  useEffect(() => {
    if (selectedNodeID && !scene.nodes.some((node) => node.id === selectedNodeID)) {
      setSelectedNodeID('');
    }
  }, [scene.nodes, selectedNodeID]);

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

      <div className="flow-definition-canvas">
        <ReactFlow
          edges={scene.edges}
          edgeTypes={edgeTypes}
          elementsSelectable
          fitView
          fitViewOptions={{ maxZoom: 1, minZoom: 0.7, padding: 0.1 }}
          maxZoom={1.8}
          minZoom={0.12}
          nodes={scene.nodes}
          nodesConnectable={false}
          nodesDraggable={false}
          nodeTypes={nodeTypes}
          onInit={setFlowInstance}
          onNodeClick={(_, node) => setSelectedNodeID(node.id)}
          onPaneClick={() => setSelectedNodeID('')}
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={24} size={1} color="#ddd8ea" />
          <Controls position="top-right" />
          <MiniMap
            maskColor="rgba(247, 244, 252, 0.68)"
            nodeColor={miniMapColor}
            pannable
            position="bottom-right"
            zoomable
            nodeStrokeWidth={2}
          />
        </ReactFlow>
      </div>

      {selectedNode && selectedNode.data.kind !== 'flow' && (
        <div className="flow-definition-selection card">
          <div>
            <span>{selectionKind(selectedNode.data)}</span>
            <b>{selectionName(selectedNode.data)}</b>
          </div>
          <code>{selectedNode.id}</code>
          {selectedNode.data.sourceTitle && <span>{selectedNode.data.sourceTitle}</span>}
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

function FlowNode({ data }: NodeProps) {
  const definition = data as DefinitionNodeData;
  return (
    <div className="definition-flow-frame" title={definition.sourceTitle}>
      <strong>{definition.displayName || 'Flow'}</strong>
    </div>
  );
}

function StepNode({ data }: NodeProps) {
  const definition = (data as DefinitionNodeData).definition!;
  return (
    <div className="definition-step-frame" title={sourceTitle(definition)}>
      <Handle id="step-target" position={Position.Top} type="target" />
      <div className="definition-step-title">
        <strong>{definition.name}</strong>
        {definition.start && <span>START</span>}
      </div>
      <Handle id="step-resource-target" position={Position.Left} type="target" />
      <Handle id="step-resource-source" position={Position.Left} type="source" />
      <Handle id="step-recovery-source" position={Position.Bottom} type="source" />
    </div>
  );
}

function WaitNode({ data }: NodeProps) {
  const definition = (data as DefinitionNodeData).definition!;
  return (
    <div className="definition-wait-shape" title={sourceTitle(definition)}>
      <Handle position={Position.Top} type="target" />
      <div className="definition-shape-content">
        <strong>WaitFor <span>{definition.wait?.type ?? definition.name}</span></strong>
        <div className="definition-shape-rows">
          {definition.wait?.conditions.map((condition, index) => (
            <span key={`${condition.kind}-${condition.label}-${index}`} title={condition.expression || condition.label}>
              {waitIcon(condition.kind)} {condition.label}
            </span>
          ))}
        </div>
      </div>
      <Handle position={Position.Bottom} type="source" />
    </div>
  );
}

function DispatchNode({ data }: NodeProps) {
  const definition = data as DefinitionNodeData;
  return (
    <div className="definition-dispatch-diamond" title={definition.displayName}>
      <Handle position={Position.Top} type="target" />
      <span>?</span>
      <Handle position={Position.Bottom} type="source" />
    </div>
  );
}

function DecisionNode({ data }: NodeProps) {
  const definitionData = data as DefinitionNodeData;
  const definition = definitionData.definition!;
  const details = definition.decision;
  const transitionRows = (definitionData.relatedEdges ?? [])
    .filter((edge) => edge.kind === 'transition')
    .map((edge) => ({
      id: edge.id,
      label: definitionData.nameByID?.[edge.to] ?? shortID(edge.to),
      multiplicity: edge.multiplicity,
    }));
  return (
    <div
      className={`definition-decision-card decision-${details?.type ?? 'unknown'}`}
      title={sourceTitle(definition)}
    >
      <Handle position={Position.Top} type="target" />
      <strong>{details?.type ?? definition.name}</strong>
      <div className="definition-shape-rows">
        {transitionRows.map((row) => (
          <span key={row.id}>🧩 {row.label}{row.multiplicity ? ` ${row.multiplicity}` : ''}</span>
        ))}
        {details?.checkedChannels?.map((channelID) => (
          <span key={channelID}>📨 {definitionData.nameByID?.[channelID] ?? shortID(channelID)}</span>
        ))}
        {details?.cancellations?.map((cancellation) => (
          <span className="definition-cancellation" key={`${cancellation.scope}:${cancellation.stepId}`}>
            🚫 {definitionData.nameByID?.[cancellation.stepId] ?? shortID(cancellation.stepId)}
            {cancellation.scope === 'siblings' ? ' · siblings' : ''}
          </span>
        ))}
      </div>
      <Handle position={Position.Bottom} type="source" />
    </div>
  );
}

function ChannelNode({ data }: NodeProps) {
  const definition = (data as DefinitionNodeData).definition!;
  return (
    <div className="definition-channel-pipe" title={sourceTitle(definition)}>
      <Handle position={Position.Left} type="target" />
      <strong>{definition.name}</strong>
      <Handle position={Position.Right} type="source" />
    </div>
  );
}

function AttributesNode({ data }: NodeProps) {
  const definitions = (data as DefinitionNodeData).definitions ?? [];
  return (
    <div className="definition-attributes-box">
      <Handle position={Position.Left} type="target" />
      <Handle position={Position.Right} type="source" />
      <strong>Attributes</strong>
      {definitions.map((definition) => (
        <span key={definition.id} title={sourceTitle(definition)}>
          - {definition.name}:{definition.resource?.valueType ?? 'unknown'}
          {definition.resource?.map ? ' map' : ''}
        </span>
      ))}
    </div>
  );
}

function RPCNode({ data }: NodeProps) {
  const definition = (data as DefinitionNodeData).definition!;
  return (
    <div className="definition-rpc-hexagon" title={sourceTitle(definition)}>
      <Handle position={Position.Left} type="target" />
      <strong>{definition.name}</strong>
      <Handle position={Position.Right} type="source" />
    </div>
  );
}

function SubFlowNode({ data }: NodeProps) {
  const definition = (data as DefinitionNodeData).definition!;
  return (
    <div className="definition-subflow-frame" title={sourceTitle(definition)}>
      <Handle position={Position.Left} type="target" />
      <strong>{definition.name}</strong>
    </div>
  );
}

function StreamNode({ data }: NodeProps) {
  const definition = (data as DefinitionNodeData).definition!;
  return (
    <div className="definition-stream-node" title={sourceTitle(definition)}>
      <Handle position={Position.Left} type="target" />
      <strong>{definition.name}</strong>
      <Handle position={Position.Right} type="source" />
    </div>
  );
}

function UnknownNode({ data }: NodeProps) {
  const definition = (data as DefinitionNodeData).definition!;
  return (
    <div className="definition-unknown-node" title={sourceTitle(definition)}>
      <Handle position={Position.Left} type="target" />
      <strong>Unknown</strong>
      <span>{definition.name}</span>
    </div>
  );
}

function DefinitionEdge({
  id,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  style,
  label,
  data,
}: EdgeProps) {
  const [path, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
    borderRadius: 18,
    offset: edgeLaneOffset(id),
  });
  return (
    <>
      <BaseEdge id={id} markerEnd={markerEnd} path={path} style={style} />
      {label && (
        <EdgeLabelRenderer>
          <span
            className="definition-edge-label nodrag nopan"
            style={{ transform: `translate(-50%, -50%) translate(${labelX}px,${labelY}px)` }}
            title={(data as DefinitionEdgeData | undefined)?.title}
          >
            {String(label)}
          </span>
        </EdgeLabelRenderer>
      )}
    </>
  );
}

function edgeLaneOffset(id: string): number {
  let hash = 0;
  for (const character of id) hash = (hash * 31 + character.charCodeAt(0)) % 4;
  return 18 + hash * 7;
}

function waitIcon(kind: string): string {
  if (kind === 'channel') return '📨';
  if (kind === 'timer') return '⏱️';
  if (kind === 'subflow') return '↳';
  return '❓';
}

function sourceTitle(definition: FlowDefinitionNode): string {
  return definition.span ? sourceLocation(definition.span) : definition.name;
}

function sourceLocation(span: SourceSpan): string {
  return `lines ${span.startLine}:${span.startColumn}–${span.endLine}:${span.endColumn}`;
}

function shortID(id: string): string {
  return id.split(':').at(-1) ?? id;
}

function selectionKind(data: DefinitionNodeData): string {
  if (data.kind === 'attributes') return 'Attributes';
  if (data.kind === 'rpc' && data.definition?.kind === 'timeout_handler') return 'Timeout handler';
  return data.kind.replaceAll('_', ' ');
}

function selectionName(data: DefinitionNodeData): string {
  if (data.kind === 'attributes') return `${data.definitions?.length ?? 0} definitions`;
  return data.definition?.name ?? data.displayName ?? data.kind;
}

function miniMapColor(node: { data?: unknown }): string {
  const data = node.data as DefinitionNodeData | undefined;
  if (data?.kind === 'step') return '#dbeafe';
  if (data?.kind === 'wait') return '#fef3c7';
  if (data?.kind === 'decision') return '#dcfce7';
  if (data?.kind === 'flow') return '#f3effb';
  return '#e2e8f0';
}
