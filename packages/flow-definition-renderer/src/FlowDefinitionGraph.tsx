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
  type Edge,
  type EdgeProps,
  type EdgeTypes,
  type NodeProps,
  type NodeTypes,
  type ReactFlowInstance,
} from '@xyflow/react';
import type { FlowDefinitionGraph, FlowDefinitionNode, SourceSpan } from './types';
import {
  buildDefinitionScene,
  filterDefinitionEdgesForSelection,
  isResourceRelation,
  type DefinitionEdgeData,
  type DefinitionLayer,
  type DefinitionNodeData,
  type DefinitionVisibility,
} from './definitionLayout';

const layerLabels: Array<[DefinitionLayer | 'diagnostics', string]> = [
  ['control', 'Control flow'],
  ['waits', 'WaitFor'],
  ['rpcs', 'RPC'],
  ['channels', 'Channels'],
  ['attributes', 'Attributes'],
  ['streams', 'Streams'],
  ['subflows', 'SubFlows'],
  ['diagnostics', 'Diagnostics'],
];

const defaultVisibility: DefinitionVisibility & { diagnostics: boolean } = {
  control: true,
  waits: true,
  rpcs: true,
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
  definitionTimeout: TimeoutNode,
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
  const [selectedEdgeID, setSelectedEdgeID] = useState('');
  const [isMiniMapExpanded, setIsMiniMapExpanded] = useState(false);
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance | null>(null);
  const scene = useMemo(
    () => buildDefinitionScene(graph, visibility),
    [graph, visibility],
  );
  const selectedNode = scene.nodes.find((node) => node.id === selectedNodeID);
  const visibleEdges = useMemo(
    () => filterDefinitionEdgesForSelection(scene.edges, graph.nodes, selectedNodeID),
    [graph.nodes, scene.edges, selectedNodeID],
  );
  const selectedEdge = visibleEdges.find((edge) => edge.id === selectedEdgeID);
  const renderedEdges = useMemo(
    () => visibleEdges.map((edge) => ({ ...edge, selected: edge.id === selectedEdgeID })),
    [selectedEdgeID, visibleEdges],
  );

  useEffect(() => {
    if (!flowInstance || scene.nodes.length === 0) return;
    void flowInstance.fitView({ duration: 220, maxZoom: 1, minZoom: 0.12, padding: 0.1 });
  }, [flowInstance, scene]);

  useEffect(() => {
    if (selectedNodeID && !scene.nodes.some((node) => node.id === selectedNodeID)) {
      setSelectedNodeID('');
    }
  }, [scene.nodes, selectedNodeID]);

  useEffect(() => {
    if (selectedEdgeID && !visibleEdges.some((edge) => edge.id === selectedEdgeID)) {
      setSelectedEdgeID('');
    }
  }, [selectedEdgeID, visibleEdges]);

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
          edges={renderedEdges}
          edgeTypes={edgeTypes}
          elementsSelectable
          fitView
          fitViewOptions={{ maxZoom: 1, minZoom: 0.12, padding: 0.1 }}
          maxZoom={1.8}
          minZoom={0.12}
          nodes={scene.nodes}
          nodesConnectable={false}
          nodesDraggable={false}
          nodeTypes={nodeTypes}
          onInit={setFlowInstance}
          onEdgeClick={(_, edge) => {
            setSelectedEdgeID(edge.id);
            if (!isResourceRelation((edge.data as DefinitionEdgeData | undefined)?.kind)) {
              setSelectedNodeID('');
            }
          }}
          onNodeClick={(_, node) => {
            setSelectedNodeID(node.id);
            setSelectedEdgeID('');
          }}
          onPaneClick={() => {
            setSelectedNodeID('');
            setSelectedEdgeID('');
          }}
          proOptions={{ hideAttribution: true }}
        >
          <Background gap={24} size={1} color="#ddd8ea" />
          <Controls
            aria-label="Graph viewport controls"
            fitViewOptions={{ maxZoom: 1, minZoom: 0.12, padding: 0.1 }}
            position="top-right"
            showInteractive={false}
          />
          <div className={`graph-minimap-shell${isMiniMapExpanded ? ' expanded' : ''}`}>
            {isMiniMapExpanded && (
              <MiniMap
                ariaLabel="Flow rendering Mini Map"
                className="graph-minimap"
                maskColor="rgba(247, 244, 252, 0.68)"
                nodeColor={miniMapColor}
                pannable
                position="top-left"
                zoomable
                nodeStrokeWidth={2}
              />
            )}
            <button
              aria-expanded={isMiniMapExpanded}
              aria-label={isMiniMapExpanded ? 'Hide Mini Map' : 'Show Mini Map'}
              className="graph-minimap-toggle nodrag nopan"
              onClick={() => setIsMiniMapExpanded((expanded) => !expanded)}
              title={isMiniMapExpanded ? 'Hide Mini Map' : 'Show Mini Map'}
              type="button"
            >
              {isMiniMapExpanded ? 'Hide Mini Map' : 'Show Mini Map'}
            </button>
          </div>
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

      {selectedEdge && (
        <div className="flow-definition-selection flow-definition-edge-selection card">
          <div>
            <span>{edgeKindLabel(selectedEdge)}</span>
            <b>{selectedEdge.data?.displayLabel || 'Selected relation'}</b>
          </div>
          <code>{sceneNodeName(scene.nodes, selectedEdge.source)} → {sceneNodeName(scene.nodes, selectedEdge.target)}</code>
          {selectedEdge.data?.title && <span>{selectedEdge.data.title}</span>}
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
    <div aria-label={definition.sourceTitle} className="definition-flow-frame">
      <strong>{definition.displayName || 'Flow'}</strong>
    </div>
  );
}

function StepNode({ data }: NodeProps) {
  const definition = (data as DefinitionNodeData).definition!;
  return (
    <div aria-label={sourceTitle(definition)} className="definition-step-frame">
      <Handle id="step-target" position={Position.Top} type="target" />
      <Handle id="step-control-outer-target" position={Position.Right} style={{ top: 22 }} type="target" />
      <Handle id="step-control-outer-source" position={Position.Right} style={{ top: 'calc(100% - 22px)' }} type="source" />
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
    <div aria-label={sourceTitle(definition)} className="definition-wait-shape">
      <Handle position={Position.Top} type="target" />
      <div className="definition-shape-content">
        <strong>WaitFor <span>{definition.wait?.type ?? definition.name}</span></strong>
        <div className="definition-shape-rows">
          {definition.wait?.conditions.map((condition, index) => (
            <span key={`${condition.kind}-${condition.label}-${index}`}>
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
    <div aria-label={definition.displayName} className="definition-dispatch-diamond">
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
      aria-label={sourceTitle(definition)}
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
    <div aria-label={sourceTitle(definition)} className="definition-channel-pipe">
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
        <span key={definition.id}>
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
    <div aria-label={sourceTitle(definition)} className="definition-rpc-hexagon">
      <Handle position={Position.Left} type="target" />
      <strong>{definition.name}</strong>
      <Handle position={Position.Right} type="source" />
    </div>
  );
}

function TimeoutNode({ data }: NodeProps) {
  const definitionData = data as DefinitionNodeData;
  const definition = definitionData.definition!;
  const decisions = definitionData.definitions ?? [];
  return (
    <div aria-label={sourceTitle(definition)} className="definition-timeout-handler">
      <Handle position={Position.Left} type="target" />
      <span className="definition-timeout-kicker">⏱ TIMEOUT HANDLER</span>
      <strong>handleTimeout</strong>
      <div className="definition-timeout-decisions">
        {decisions.map((decision) => (
          <span key={decision.id}>
            {decision.decision?.type ?? decision.name}
            {timeoutMovementLabels(decision, definitionData).map((label) => ` → 🧩 ${label}`)}
          </span>
        ))}
      </div>
      <Handle position={Position.Right} type="source" />
    </div>
  );
}

function SubFlowNode({ data }: NodeProps) {
  const definition = (data as DefinitionNodeData).definition!;
  return (
    <div aria-label={sourceTitle(definition)} className="definition-subflow-frame">
      <Handle position={Position.Left} type="target" />
      <strong>{definition.name}</strong>
    </div>
  );
}

function StreamNode({ data }: NodeProps) {
  const definition = (data as DefinitionNodeData).definition!;
  return (
    <div aria-label={sourceTitle(definition)} className="definition-stream-node">
      <Handle position={Position.Left} type="target" />
      <strong>{definition.name}</strong>
      <Handle position={Position.Right} type="source" />
    </div>
  );
}

function UnknownNode({ data }: NodeProps) {
  const definition = (data as DefinitionNodeData).definition!;
  return (
    <div aria-label={sourceTitle(definition)} className="definition-unknown-node">
      <Handle position={Position.Left} type="target" />
      <strong>Unknown</strong>
      <span>{definition.name}</span>
    </div>
  );
}

function DefinitionEdge({
  data,
  id,
  selected,
  sourceX,
  sourceY,
  targetX,
  targetY,
  sourcePosition,
  targetPosition,
  markerEnd,
  style,
}: EdgeProps) {
  const edgeData = data as DefinitionEdgeData | undefined;
  const [path, labelX, labelY] = getSmoothStepPath({
    sourceX,
    sourceY,
    sourcePosition,
    targetX,
    targetY,
    targetPosition,
    borderRadius: 18,
    offset: (edgeData?.route === 'outer-right' ? 72 : 28) + edgeLaneOffset(id),
  });
  const selectedStyle = selected
    ? {
      ...style,
      filter: 'drop-shadow(0 0 4px rgba(37, 99, 235, 0.85))',
      strokeWidth: numericStrokeWidth(style?.strokeWidth) + 3,
    }
    : style;
  return (
    <>
      <BaseEdge id={id} interactionWidth={28} markerEnd={markerEnd} path={path} style={selectedStyle} />
      {selected && edgeData?.displayLabel && (
        <EdgeLabelRenderer>
          <SelectedEdgeLabel label={edgeData.displayLabel} labelX={labelX} labelY={labelY} />
        </EdgeLabelRenderer>
      )}
    </>
  );
}

export function SelectedEdgeLabel({
  label,
  labelX,
  labelY,
}: {
  label: string;
  labelX: number;
  labelY: number;
}) {
  return (
    <span
      className="definition-selected-edge-label nodrag nopan"
      style={{ transform: `translate(-50%, -100%) translate(${labelX}px, ${labelY - 14}px)` }}
    >
      {label}
    </span>
  );
}

function edgeLaneOffset(id: string): number {
  let hash = 0;
  for (const character of id) hash = (hash * 31 + character.charCodeAt(0)) % 4;
  return hash * 9;
}

function numericStrokeWidth(value: string | number | undefined): number {
  if (typeof value === 'number') return value;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : 1.5;
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

function timeoutMovementLabels(
  decision: FlowDefinitionNode,
  data: DefinitionNodeData,
): string[] {
  return (data.relatedEdges ?? [])
    .filter((edge) => edge.kind === 'transition' && edge.from === decision.id)
    .map((edge) => data.nameByID?.[edge.to] ?? shortID(edge.to));
}

function selectionKind(data: DefinitionNodeData): string {
  if (data.kind === 'attributes') return 'Attributes';
  if (data.kind === 'timeout') return 'Timeout handler';
  return data.kind.replaceAll('_', ' ');
}

function selectionName(data: DefinitionNodeData): string {
  if (data.kind === 'attributes') return `${data.definitions?.length ?? 0} definitions`;
  return data.definition?.name ?? data.displayName ?? data.kind;
}

function edgeKindLabel(edge: Edge<DefinitionEdgeData>): string {
  if (edge.data?.kind === 'failure_transition') return 'Recovery edge';
  if (edge.data?.kind === 'transition') return 'Step transition';
  if (edge.data?.kind === 'branch') return 'Conditional branch';
  if (edge.data?.kind === 'phase') return 'Step phase';
  return edge.data?.kind?.replaceAll('_', ' ') ?? 'Relation';
}

function sceneNodeName(
  nodes: Array<{ data: DefinitionNodeData; id: string }>,
  id: string,
): string {
  const node = nodes.find((candidate) => candidate.id === id);
  if (!node) return id;
  if (node.data.kind === 'attributes') return 'Attributes';
  return node.data.definition?.name ?? node.data.displayName ?? id;
}

function miniMapColor(node: { data?: unknown }): string {
  const data = node.data as DefinitionNodeData | undefined;
  if (data?.kind === 'step') return '#dbeafe';
  if (data?.kind === 'wait') return '#fef3c7';
  if (data?.kind === 'decision') return '#dcfce7';
  if (data?.kind === 'timeout') return '#fff1d6';
  if (data?.kind === 'flow') return '#f3effb';
  return '#e2e8f0';
}
