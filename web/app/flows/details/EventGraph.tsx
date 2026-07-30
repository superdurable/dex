'use client';

import { useMemo } from 'react';
import {
  Background,
  Controls,
  MarkerType,
  MiniMap,
  ReactFlow,
  type Edge,
  type Node,
} from '@xyflow/react';
import type { FlowHistoryEvent } from '@/lib/types';
import { layoutGraph } from './graphLayout';

interface EventNodeData extends Record<string, unknown> {
  label: React.ReactNode;
  event: FlowHistoryEvent;
}

export function EventGraph({
  events,
  selectedEvent,
  onSelectEvent,
}: {
  events: FlowHistoryEvent[];
  selectedEvent: FlowHistoryEvent | null;
  onSelectEvent: (event: FlowHistoryEvent) => void;
}) {
  const edges = useMemo<Edge[]>(() => semanticEventEdges(events), [events]);
  const nodes = useMemo(() => {
    const raw: Array<Node<EventNodeData>> = events.map((event) => ({
      id: String(event.eventId),
      position: { x: 0, y: 0 },
      className: `event-flow-node ${selectedEvent?.eventId === event.eventId ? 'selected' : ''}`,
      data: {
        event,
        label: (
          <div className="graph-node-label">
            <span>Event {event.eventId}</span>
            <b>{event.type}</b>
            <code>{event.eventTime || 'No timestamp'}</code>
          </div>
        ),
      },
    }));
    return layoutGraph(raw, edges, 'LR');
  }, [edges, events, selectedEvent]);

  if (!events.length) return <div className="card empty-state"><h3>No semantic events loaded</h3></div>;
  return (
    <div className="graph-view">
      <div className="view-toolbar">
        <div>
          <p className="eyebrow">Semantic sequence</p>
          <h2>Event graph</h2>
          <p>Each node is a Dex event, never a workflow task, activity, or marker.</p>
        </div>
      </div>
      <div className="graph-canvas">
        <ReactFlow
          nodes={nodes}
          edges={edges}
          fitView
          minZoom={0.1}
          maxZoom={2}
          onNodeClick={(_, node) => onSelectEvent((node.data as EventNodeData).event)}
        >
          <Background gap={22} size={1} color="#dfe7e5" />
          <Controls position="top-right" />
          <MiniMap pannable zoomable />
        </ReactFlow>
      </div>
    </div>
  );
}

function semanticEventEdges(events: FlowHistoryEvent[]): Edge[] {
  const lastByStep = new Map<string, FlowHistoryEvent>();
  const lastRpcByName = new Map<string, FlowHistoryEvent>();
  const result: Edge[] = [];
  for (const [index, event] of events.entries()) {
    let source = index > 0 ? events[index - 1] : undefined;
    const execution = event.payload.execution as Record<string, unknown> | undefined;
    const stepExecutionId = typeof execution?.stepExecutionId === 'string'
      ? execution.stepExecutionId
      : '';
    const fromStepExecutionId = typeof execution?.fromStepExecutionId === 'string'
      ? execution.fromStepExecutionId
      : '';
    if (stepExecutionId && lastByStep.has(stepExecutionId)) {
      source = lastByStep.get(stepExecutionId);
    } else if (fromStepExecutionId.startsWith('__rpc/')) {
      source = lastRpcByName.get(fromStepExecutionId.slice('__rpc/'.length)) ?? source;
    } else if (fromStepExecutionId && lastByStep.has(fromStepExecutionId)) {
      source = lastByStep.get(fromStepExecutionId);
    }
    if (source) {
      result.push({
        id: `${source.eventId}->${event.eventId}`,
        source: String(source.eventId),
        target: String(event.eventId),
        type: 'smoothstep',
        markerEnd: { type: MarkerType.ArrowClosed, color: '#9aa8a5' },
        style: { stroke: '#9aa8a5' },
      });
    }
    if (stepExecutionId) lastByStep.set(stepExecutionId, event);
    if (event.type === 'RpcExecutionCompleted' && typeof event.payload.rpcName === 'string') {
      lastRpcByName.set(event.payload.rpcName, event);
    }
  }
  return result;
}
