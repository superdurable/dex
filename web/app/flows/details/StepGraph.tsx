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

type MethodStatus = 'Started' | 'Waiting' | 'Running' | 'Completed' | 'Failed' | 'Pending' | 'Not used';

function waitingCondition(node: StepGraphNode): Record<string, unknown> {
  const response = node.waitFor?.payload.response;
  if (response && typeof response === 'object') {
    const condition = (response as Record<string, unknown>).waitingCondition;
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

function ConditionIcon({ type }: { type: 'timer' | 'channel' }) {
  if (type === 'timer') {
    return (
      <svg aria-hidden="true" className="graph-condition-icon" viewBox="0 0 16 16">
        <circle cx="8" cy="8" r="5.5" />
        <path d="M8 4.8v3.5l2.4 1.4" />
      </svg>
    );
  }
  return (
    <svg aria-hidden="true" className="graph-condition-icon" viewBox="0 0 16 16">
      <circle cx="4" cy="4" r="1.7" />
      <circle cx="12" cy="8" r="1.7" />
      <circle cx="4" cy="12" r="1.7" />
      <path d="M5.7 4.7 10.3 7M5.7 11.3 10.3 9" />
    </svg>
  );
}

function waitForStatus(node: StepGraphNode): MethodStatus {
  if (node.waitFor?.type === 'StepWaitForFailed') return 'Failed';
  if (node.active?.phase === 'Waiting') return 'Waiting';
  if (node.waitFor) return 'Started';
  if (node.transient) return 'Not used';
  if (node.active) return 'Running';
  return 'Pending';
}

function executeStatus(node: StepGraphNode): MethodStatus {
  if (node.execute?.type === 'StepExecuteFailed') return 'Failed';
  if (node.execute) return 'Completed';
  if (node.active?.phase === 'Active' && node.waitFor) return 'Running';
  return 'Pending';
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
  const channels = [...new Set(channelNames(condition))];
  const channelSummary = channels.length > 0 ? channels.join(', ') : `${channelCount} channel${channelCount === 1 ? '' : 's'}`;
  return (
    <div className="graph-step-label">
      <div className="graph-step-heading">
        <span>{node.status}</span>
        <b>{node.label}</b>
        <code>{node.id}</code>
      </div>
      <div className="graph-methods">
        <MethodSection
          name="WaitFor"
          status={waitForStatus(node)}
          event={node.waitFor}
          onSelect={onSelect}
        >
          {(timerCount > 0 || channelCount > 0) && (
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
            </small>
          )}
        </MethodSection>
        <MethodSection
          name="Execute"
          status={executeStatus(node)}
          event={node.execute}
          onSelect={onSelect}
        />
      </div>
    </div>
  );
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
      style: { width: node.kind === 'step' ? 340 : 220 },
      data: {
        model: node,
        label: node.kind === 'step' ? (
          <StepNodeLabel node={node} onSelect={onSelectEvent} />
        ) : (
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
          </div>
        ),
      },
    }));
    return layoutGraph(raw, edges);
  }, [edges, graph.nodes, onSelectEvent]);

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
