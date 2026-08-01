'use client';

import { useMemo } from 'react';
import { Link } from 'react-router-dom';
import {
  Background,
  Controls,
  MarkerType,
  MiniMap,
  ReactFlow,
  type Edge,
  type Node,
} from '@xyflow/react';
import { buildStepGraph } from '@/lib/graph';
import type { FlowHistoryEvent, FlowState, StepGraphNode } from '@/lib/types';
import { layoutGraph } from './graphLayout';

interface StepNodeData extends Record<string, unknown> {
  label: React.ReactNode;
  model: StepGraphNode;
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
  const graph = useMemo(
    () => buildStepGraph(events, state?.activeStepExecutions ?? []),
    [events, state],
  );
  const edges = useMemo<Edge[]>(() => graph.edges.map((edge) => ({
    ...edge,
    type: 'smoothstep',
    markerEnd: { type: MarkerType.ArrowClosed, width: 18, height: 18, color: '#78909c' },
    style: { stroke: '#78909c', strokeWidth: 1.5 },
  })), [graph.edges]);
  const nodes = useMemo(() => {
    const raw: Array<Node<StepNodeData>> = graph.nodes.map((node) => ({
      id: node.id,
      position: { x: 0, y: 0 },
      className: `step-flow-node node-${node.kind} node-${node.status.toLowerCase()}`,
      data: {
        model: node,
        label: (
          <div className="graph-node-label">
            <span>{node.kind === 'source' ? 'Source' : node.kind === 'terminal' ? 'Terminal' : node.status}</span>
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
            {node.kind === 'step' && <code>{node.id}</code>}
            {node.transient && <em>Transient</em>}
          </div>
        ),
      },
    }));
    return layoutGraph(raw, edges);
  }, [edges, graph.nodes]);

  if (!nodes.length) return <div className="card empty-state"><h3>No step topology loaded</h3></div>;
  return (
    <div className="graph-view">
      <div className="view-toolbar">
        <div>
          <p className="eyebrow">Business topology</p>
          <h2>Step graph</h2>
        </div>
        <div className="graph-legend">
          {['Active', 'Waiting', 'Completed', 'Failed'].map((status) => (
            <span key={status}><i className={`legend-${status.toLowerCase()}`} />{status}</span>
          ))}
        </div>
      </div>
      <div className="graph-canvas">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          fitView
          minZoom={0.15}
          maxZoom={2}
          onNodeClick={(_, node) => {
            const model = (node.data as StepNodeData).model;
            onSelectEvent(model.execute ?? model.waitFor ?? null);
          }}
          nodesDraggable
        >
          <Background gap={22} size={1} color="#dfe7e5" />
          <Controls position="top-right" />
          <MiniMap pannable zoomable nodeStrokeWidth={2} />
        </ReactFlow>
      </div>
      {selectedEvent && (
        <div className="graph-selection">
          Selected {selectedEvent.type} #{selectedEvent.eventId}
        </div>
      )}
    </div>
  );
}
