// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
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
import { buildStepGraph, stepGraphSelection } from '@/lib/graph';
import type { FlowHistoryEvent, FlowState, StepGraphNode } from '@/lib/types';
import {
  defaultGraphZoom,
  graphBounds,
  layoutGraph,
  minimumGraphZoom,
} from './graphLayout';

interface StepNodeData extends Record<string, unknown> {
  label: React.ReactNode;
  model: StepGraphNode;
  hasSubFlowChildren?: boolean;
}

type MethodStatus = 'Started' | 'Waiting' | 'Running' | 'Pending' | 'Completed' | 'Failed' | 'Canceled' | 'Not started';

const graphEdgeColor = '#78909c';
const previousStepColor = '#3f78a8';
const nextStepColor = '#c16f24';

function waitingCondition(node: StepGraphNode): Record<string, unknown> {
  const output = node.waitFor?.payload.output;
  if (output && typeof output === 'object') {
    const condition = (output as Record<string, unknown>).waitForCondition;
    if (condition && typeof condition === 'object') return condition as Record<string, unknown>;
  }
  return node.active?.waitingCondition ?? {};
}

function conditionCount(condition: Record<string, unknown>, key: string): number {
  const values = condition[key];
  return Array.isArray(values) ? values.length : 0;
}

function channelNames(condition: Record<string, unknown>): string[] {
  const conditions = condition.channelConditions;
  if (!Array.isArray(conditions)) return [];
  return conditions.flatMap((entry) => {
    if (!entry || typeof entry !== 'object') return [];
    const name = (entry as Record<string, unknown>).channelName;
    return typeof name === 'string' && name ? [name] : [];
  });
}

function skipsWaitFor(node: StepGraphNode): boolean {
  const options = node.active?.movement?.stepOptions ?? node.movement?.stepOptions;
  return Boolean(options && typeof options === 'object' && (options as Record<string, unknown>).skipWaitFor === true);
}

function ConditionIcon({ type }: { type: 'timer' | 'channel' | 'subflow' }) {
  if (type === 'timer') {
    return (
      <svg aria-hidden="true" className="graph-condition-icon" viewBox="0 0 16 16">
        <circle cx="8" cy="8" r="5.5" />
        <path d="M8 4.8v3.5l2.4 1.4" />
      </svg>
    );
  }
  if (type === 'channel') return (
    <svg aria-hidden="true" className="graph-condition-icon" viewBox="0 0 16 16">
      <circle cx="4" cy="4" r="1.7" />
      <circle cx="12" cy="8" r="1.7" />
      <circle cx="4" cy="12" r="1.7" />
      <path d="M5.7 4.7 10.3 7M5.7 11.3 10.3 9" />
    </svg>
  );
  return (
    <svg aria-hidden="true" className="graph-condition-icon" viewBox="0 0 16 16">
      <rect x="2" y="3" width="5" height="4" rx="1" />
      <rect x="9" y="9" width="5" height="4" rx="1" />
      <path d="M7 5h2.5a2 2 0 0 1 2 2v2" />
    </svg>
  );
}

function waitForStatus(node: StepGraphNode): MethodStatus {
  if (node.waitFor?.type === 'StepWaitForFailed') return 'Failed';
  if (node.pendingWaitFor) return 'Pending';
  if (node.active?.phase === 'Waiting') return 'Waiting';
  if (node.waitFor) return 'Started';
  if (node.active) return 'Running';
  if (node.status === 'Canceled') return 'Canceled';
  return 'Not started';
}

function executeStatus(node: StepGraphNode): MethodStatus {
  if (node.execute?.type === 'StepExecuteFailed') return 'Failed';
  if (node.pendingExecute) return 'Pending';
  if (node.execute) return 'Completed';
  if (node.active?.phase === 'Active' && node.waitFor) return 'Running';
  if (node.status === 'Canceled') return 'Canceled';
  return 'Not started';
}

function MethodSection({
  name,
  status,
  event,
  children,
  onSelect,
}: {
  name: string;
  status: MethodStatus;
  event?: FlowHistoryEvent;
  children?: React.ReactNode;
  onSelect: (event: FlowHistoryEvent) => void;
}) {
  return (
    <button
      className={`graph-method graph-method-${name.toLowerCase()} graph-method-${status.toLowerCase().replace(' ', '-')} nodrag nopan`}
      disabled={!event}
      type="button"
      onClick={(clickEvent) => {
        clickEvent.stopPropagation();
        if (event) onSelect(event);
      }}
    >
      <span>{name}</span>
      <strong>{status}</strong>
      {children}
    </button>
  );
}

function StepNodeLabel({
  node,
  onSelect,
}: {
  node: StepGraphNode;
  onSelect: (event: FlowHistoryEvent) => void;
}) {
  const condition = waitingCondition(node);
  const timerCount = conditionCount(condition, 'timerConditions');
  const channelCount = conditionCount(condition, 'channelConditions');
  const subFlowCount = conditionCount(condition, 'subFlowConditions');
  const channels = [...new Set(channelNames(condition))];
  const channelSummary = channels.length > 0 ? channels.join(', ') : `${channelCount} channel${channelCount === 1 ? '' : 's'}`;
  const showWaitFor = Boolean(
    node.waitFor || node.pendingWaitFor
      || (!skipsWaitFor(node) && (node.active || node.isPlanned) && !node.execute),
  );
  return (
    <div className={`graph-step-label${showWaitFor ? '' : ' graph-step-label--execute-only'}`}>
      <div className="graph-step-heading">
        <span>{node.status}</span>
        <b>{node.label}</b>
        <code title={node.isPlanned
          ? node.status === 'Canceled'
            ? 'Canceled before a Step event was recorded'
            : 'No Step event has been recorded'
          : undefined}
        >
          {node.isPlanned ? 'Execution ID unavailable' : node.id}
        </code>
      </div>
      <div className="graph-methods">
        {showWaitFor && (
          <MethodSection
            name="WaitFor"
            status={waitForStatus(node)}
            event={node.waitFor ?? node.pendingWaitFor}
            onSelect={onSelect}
          >
            {(timerCount > 0 || channelCount > 0 || subFlowCount > 0) && (
              <small className="graph-conditions">
                {timerCount > 0 && (
                  <i title={`${timerCount} timer condition${timerCount === 1 ? '' : 's'}`}>
                    <ConditionIcon type="timer" />
                    {timerCount} timer{timerCount === 1 ? '' : 's'}
                  </i>
                )}
                {channelCount > 0 && (
                  <i title={channelSummary}>
                    <ConditionIcon type="channel" />
                    <span>{channelSummary}</span>
                  </i>
                )}
                {subFlowCount > 0 && (
                  <i title={`${subFlowCount} SubFlow condition${subFlowCount === 1 ? '' : 's'}`}>
                    <ConditionIcon type="subflow" />
                    {subFlowCount} SubFlow{subFlowCount === 1 ? '' : 's'}
                  </i>
                )}
              </small>
            )}
          </MethodSection>
        )}
        <MethodSection
          name="Execute"
          status={executeStatus(node)}
          event={node.execute ?? node.pendingExecute}
          onSelect={onSelect}
        />
      </div>
    </div>
  );
}

function SubFlowNodeLabel({ flow }: { flow: StepGraphNode }) {
  return (
    <Link
      className="graph-sub-flow-link nodrag nopan"
      to={`/flows/${encodeURIComponent(flow.flowId ?? '')}`}
      aria-label={`Open SubFlow ${flow.flowId ?? ''}`}
    >
      <span><ConditionIcon type="subflow" />SubFlow · {flow.subFlowStatus}</span>
      <code title={flow.flowId}>{flow.flowId}</code>
      {flow.reusePolicy && <small>{flow.reusePolicy}</small>}
    </Link>
  );
}

function GraphNode({ data }: NodeProps<Node<StepNodeData>>) {
  const { model } = data;
  return (
    <div className="graph-node-root">
      {model.kind === 'step' && (
        <Handle id="topology" position={Position.Top} type="target" />
      )}
      <div className="graph-node-body">{data.label}</div>
      {(model.kind === 'source' || model.kind === 'step') && (
        <Handle id="topology" position={Position.Bottom} type="source" />
      )}
      {model.kind === 'step' && data.hasSubFlowChildren === true && (
        <Handle
          className="graph-subflow-handle"
          id="subflow"
          position={Position.Right}
          type="source"
        />
      )}
      {model.kind === 'subflow' && (
        <Handle
          className="graph-subflow-handle"
          id="subflow"
          position={Position.Left}
          type="target"
        />
      )}
    </div>
  );
}

const graphNodeTypes: NodeTypes = { graph: GraphNode };

function graphNodeDimensions(node: StepGraphNode): { height: number; width: number } {
  if (node.kind !== 'step') return { height: 90, width: 220 };
  return {
    height: node.execute && !node.waitFor && !node.pendingWaitFor ? 126 : 158,
    width: 300,
  };
}

function nodeHandles(
  node: StepGraphNode,
  dimensions: { height: number; width: number },
  hasSubFlowChildren: boolean,
): Node['handles'] {
  const box = { x: 0, y: 0, width: dimensions.width, height: dimensions.height };
  if (node.kind === 'subflow') {
    return [{ ...box, id: 'subflow', type: 'target', position: Position.Left }];
  }
  if (node.kind === 'source') {
    return [{ ...box, id: 'topology', type: 'source', position: Position.Bottom }];
  }
  const handles: NonNullable<Node['handles']> = [
    { ...box, id: 'topology', type: 'target', position: Position.Top },
    { ...box, id: 'topology', type: 'source', position: Position.Bottom },
  ];
  if (hasSubFlowChildren) {
    handles.push({ ...box, id: 'subflow', type: 'source', position: Position.Right });
  }
  return handles;
}

export function StepGraph({
  flowId,
  events,
  state,
  selectedEvent,
  onSelectEvent,
}: {
  flowId: string;
  events: FlowHistoryEvent[];
  state: FlowState | null;
  selectedEvent: FlowHistoryEvent | null;
  onSelectEvent: (event: FlowHistoryEvent | null) => void;
}) {
  const graphCanvas = useRef<HTMLDivElement>(null);
  const [canvasWidth, setCanvasWidth] = useState(0);
  const [isMiniMapExpanded, setIsMiniMapExpanded] = useState(false);
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance<Node<StepNodeData>, Edge> | null>(null);
  const graph = useMemo(
    () => buildStepGraph(events, state?.activeStepExecutions ?? [], flowId),
    [events, flowId, state],
  );
  const selection = useMemo(
    () => stepGraphSelection(graph.nodes, graph.edges, selectedEvent),
    [graph.edges, graph.nodes, selectedEvent],
  );
  const nodeByID = useMemo(
    () => new Map(graph.nodes.map((node) => [node.id, node])),
    [graph.nodes],
  );
  const subFlowParentIDs = useMemo(
    () => new Set(
      graph.nodes
        .filter((node) => node.kind === 'subflow')
        .map((node) => node.parentStepId),
    ),
    [graph.nodes],
  );
  const edges = useMemo<Edge[]>(() => graph.edges.map((edge) => {
    const isIncoming = selection.incomingEdgeIDs.has(edge.id);
    const isOutgoing = selection.outgoingEdgeIDs.has(edge.id);
    const isSubFlow = nodeByID.get(edge.target)?.kind === 'subflow';
    const color = isIncoming ? previousStepColor : isOutgoing ? nextStepColor : graphEdgeColor;
    return {
      ...edge,
      className: isSubFlow
        ? 'graph-edge-subflow'
        : isIncoming ? 'graph-edge-incoming' : isOutgoing ? 'graph-edge-outgoing' : undefined,
      type: 'smoothstep',
      sourceHandle: isSubFlow ? 'subflow' : 'topology',
      targetHandle: isSubFlow ? 'subflow' : 'topology',
      markerEnd: { type: MarkerType.ArrowClosed, width: 18, height: 18, color },
      style: {
        stroke: color,
        strokeWidth: isIncoming || isOutgoing ? 3 : 1.5,
        strokeDasharray: isSubFlow ? '6 4' : undefined,
      },
      zIndex: isIncoming || isOutgoing ? 2 : 0,
    };
  }), [graph.edges, nodeByID, selection]);
  const nodes = useMemo(() => {
    const raw: Array<Node<StepNodeData>> = graph.nodes.map((node) => {
      const dimensions = graphNodeDimensions(node);
      return {
        id: node.id,
        type: 'graph',
        position: { x: 0, y: 0 },
        className: [
          'step-flow-node',
          `node-${node.kind}`,
          `node-${node.status.toLowerCase()}`,
          node.id === selection.selectedStepExecutionID ? 'graph-node-current' : '',
          selection.previousStepExecutionIDs.has(node.id) ? 'graph-node-previous' : '',
          selection.nextStepExecutionIDs.has(node.id) ? 'graph-node-next' : '',
        ].filter(Boolean).join(' '),
        width: dimensions.width,
        height: dimensions.height,
        style: dimensions,
        handles: nodeHandles(node, dimensions, subFlowParentIDs.has(node.id)),
        data: {
          model: node,
          hasSubFlowChildren: subFlowParentIDs.has(node.id),
          label: node.kind === 'step' ? (
            <StepNodeLabel node={node} onSelect={onSelectEvent} />
          ) : node.kind === 'subflow' ? (
            <SubFlowNodeLabel flow={node} />
          ) : (
            <div className="graph-node-label">
              <span>{node.kind === 'source' ? 'Source' : node.status}</span>
              {node.previousRunId ? (
                <>
                  <b>
                    <Link
                      className="graph-run-link nodrag nopan"
                      title={node.previousRunId}
                      to={`/flows/${encodeURIComponent(flowId)}/${encodeURIComponent(node.previousRunId)}`}
                    >
                      Continued from previous run
                    </Link>
                  </b>
                  <code title={node.previousRunId}>{node.previousRunId}</code>
                </>
              ) : <b>{node.label}</b>}
            </div>
          ),
        },
      };
    });
    return layoutGraph(raw, edges);
  }, [edges, flowId, graph.nodes, onSelectEvent, selection, subFlowParentIDs]);
  const bounds = useMemo(() => graphBounds(nodes), [nodes]);
  const initialZoom = defaultGraphZoom(bounds.width, canvasWidth);

  useEffect(() => {
    const canvas = graphCanvas.current;
    if (!canvas) return undefined;
    const updateWidth = () => setCanvasWidth(canvas.clientWidth);
    updateWidth();
    const observer = new ResizeObserver(updateWidth);
    observer.observe(canvas);
    return () => observer.disconnect();
  }, []);

  useEffect(() => {
    if (!flowInstance || canvasWidth <= 0) return;
    void flowInstance.setViewport({
      x: Math.max(0, (canvasWidth - bounds.width * initialZoom) / 2),
      y: 0,
      zoom: initialZoom,
    });
  }, [bounds.width, canvasWidth, flowInstance, initialZoom]);

  if (!nodes.length) return <div className="card empty-state"><h3>No step topology loaded</h3></div>;
  return (
    <div className="graph-view">
      <div className="view-toolbar">
        <div>
          <h2>Step graph</h2>
        </div>
        <div className="graph-legend">
          <span><i className="legend-source" />Source</span>
          <span><i className="legend-active" />Active</span>
          <span><i className="legend-waiting" />Waiting</span>
          <span><i className="legend-pending" />Pending</span>
          <span><i className="legend-failed" />Failed</span>
          <span><i className="legend-canceled" />Canceled</span>
          <span><i className="legend-subflow" />SubFlow</span>
        </div>
      </div>
      <div
        className="graph-canvas"
        ref={graphCanvas}
        style={{ height: Math.max(650, bounds.height * initialZoom) }}
      >
        <ReactFlow
          nodes={nodes}
          edges={edges}
          nodeTypes={graphNodeTypes}
          minZoom={minimumGraphZoom}
          maxZoom={2}
          elementsSelectable={false}
          nodesConnectable={false}
          nodesDraggable={false}
          preventScrolling={false}
          zoomOnScroll={false}
          onInit={setFlowInstance}
          onNodeClick={(_, node) => {
            const model = (node.data as StepNodeData).model;
            if (model.kind === 'subflow') return;
            onSelectEvent(model.execute ?? model.pendingExecute ?? model.waitFor ?? model.pendingWaitFor ?? null);
          }}
        >
          <Background gap={22} size={1} color="#dfe7e5" />
          <Controls position="top-right" />
          <div className={`graph-minimap-shell${isMiniMapExpanded ? ' expanded' : ''}`}>
            {isMiniMapExpanded && (
              <MiniMap
                ariaLabel="Step graph Mini Map"
                className="graph-minimap"
                pannable
                position="top-left"
                zoomable
                nodeStrokeWidth={2}
              />
            )}
            <button
              aria-expanded={isMiniMapExpanded}
              aria-label={isMiniMapExpanded ? 'Collapse Mini Map' : 'Expand Mini Map'}
              className="graph-minimap-toggle nodrag nopan"
              onClick={() => setIsMiniMapExpanded((expanded) => !expanded)}
              title={isMiniMapExpanded ? 'Collapse Mini Map' : 'Expand Mini Map'}
              type="button"
            >
              {isMiniMapExpanded ? 'Close ×' : 'Mini Map'}
            </button>
          </div>
        </ReactFlow>
      </div>
      {selectedEvent && (
        <div className="graph-selection">
          Selected {selectedEvent.type}
        </div>
      )}
    </div>
  );
}
