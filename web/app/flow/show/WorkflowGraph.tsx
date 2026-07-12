'use client';

import { useEffect, useMemo, useState, useCallback, useRef } from 'react';
import {
  ReactFlow,
  Background,
  Controls,
  MiniMap,
  MarkerType,
  Position,
  useNodesState,
  useEdgesState,
  type Node,
  type Edge,
  type NodeMouseHandler,
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import dagre from 'dagre';
import StepNode from './StepNode';
import EventCard from '../../components/EventCard';
import StackTracePre from '../../components/StackTracePre';
import WaitForConditionPanel from '../../components/WaitForConditionPanel';
import type { ActiveStepExecutionLive, HistoryEvent } from '../../api/_grpc/mappers';
import { buildGraph, type StepNodeData } from './buildGraph';
import { SectionSelectionContext, type Section, type SectionSelection } from './SectionSelectionContext';

const NODE_WIDTH = 240;
const NODE_HEIGHT = 130;
const DEFAULT_DETAIL_WIDTH = 380;
const MIN_DETAIL_WIDTH = 280;
const MAX_DETAIL_WIDTH = 720;

const nodeTypes = { stepNode: StepNode };

function applyLayout(nodes: Node[], edges: Edge[]): Node[] {
  const g = new dagre.graphlib.Graph();
  g.setDefaultEdgeLabel(() => ({}));
  g.setGraph({ rankdir: 'TB', nodesep: 40, ranksep: 60 });
  for (const n of nodes) g.setNode(n.id, { width: NODE_WIDTH, height: NODE_HEIGHT });
  for (const e of edges) g.setEdge(e.source, e.target);
  dagre.layout(g);
  return nodes.map((n) => {
    const p = g.node(n.id);
    return {
      ...n,
      position: { x: p.x - NODE_WIDTH / 2, y: p.y - NODE_HEIGHT / 2 },
      sourcePosition: Position.Bottom,
      targetPosition: Position.Top,
    };
  });
}

interface WorkflowGraphProps {
  events: HistoryEvent[];
  // Live snapshot of every active step from RunsService.GetRun. Used to
  // overlay an in-flight INVOKING_EXECUTE step (e.g. long Sleep inside
  // Execute) as a "Running" node BEFORE its StepExecuteCompleted history
  // event lands. Empty/undefined is fine — graph then shows only
  // history-derived nodes (the previous behavior).
  activeStepExecutions?: Record<string, ActiveStepExecutionLive>;
}

// Map the "section" filter to the history event types that belong to it.
// The side panel filters selectedEvents through this so the user sees
// exactly the chunk of timeline they asked for.
function eventTypesForSection(section: Section): Set<string> | null {
  if (section === 'wait') {
    return new Set(['StepWaitForCompleted', 'StepsUnblocked']);
  }
  if (section === 'execute') {
    return new Set(['StepExecuteCompleted', 'StepsUnblocked']);
  }
  return null; // overview: no filter
}

export default function WorkflowGraph({ events, activeStepExecutions }: WorkflowGraphProps) {
  const graph = useMemo(
    () => buildGraph(events, activeStepExecutions),
    [events, activeStepExecutions],
  );

  const initialNodes = useMemo<Node[]>(
    () =>
      graph.nodes.map((d) => ({
        id: d.stepExeId,
        type: 'stepNode',
        position: { x: 0, y: 0 },
        data: d as unknown as Record<string, unknown>,
      })),
    [graph],
  );

  const initialEdges = useMemo<Edge[]>(
    () =>
      graph.edges.map((e) => ({
        id: e.id,
        source: e.source,
        target: e.target,
        type: 'smoothstep',
        animated: false,
        style: { stroke: '#2563eb', strokeWidth: 1.5 },
        markerEnd: { type: MarkerType.ArrowClosed, width: 12, height: 12, color: '#2563eb' },
      })),
    [graph],
  );

  const [nodes, setNodes, onNodesChange] = useNodesState<Node>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<Edge>([]);
  // Single source of truth for both node selection and which sub-area
  // (WaitFor / Execute / overview) the side panel should show.
  const [selection, setSelection] = useState<SectionSelection | null>(null);
  const [detailWidth, setDetailWidth] = useState(DEFAULT_DETAIL_WIDTH);
  const resizeRef = useRef<{ startX: number; startWidth: number } | null>(null);

  const beginDetailResize = useCallback(
    (event: React.PointerEvent<HTMLDivElement>) => {
      event.preventDefault();
      resizeRef.current = { startX: event.clientX, startWidth: detailWidth };
      event.currentTarget.setPointerCapture(event.pointerId);
    },
    [detailWidth],
  );

  const moveDetailResize = useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    if (!resizeRef.current) return;
    const delta = resizeRef.current.startX - event.clientX;
    const nextWidth = Math.min(
      MAX_DETAIL_WIDTH,
      Math.max(MIN_DETAIL_WIDTH, resizeRef.current.startWidth + delta),
    );
    setDetailWidth(nextWidth);
  }, []);

  const endDetailResize = useCallback((event: React.PointerEvent<HTMLDivElement>) => {
    if (!resizeRef.current) return;
    resizeRef.current = null;
    event.currentTarget.releasePointerCapture(event.pointerId);
  }, []);

  useEffect(() => {
    setNodes(applyLayout(initialNodes, initialEdges));
    setEdges(initialEdges);
  }, [initialNodes, initialEdges, setNodes, setEdges]);

  const onNodeClick = useCallback<NodeMouseHandler>(
    (_, node) => {
      // Click on the node body (outside the WaitFor / Execute strips) shows
      // the overview (section=null). Inner strips set their own section
      // before the event bubbles up — they call setSelection then
      // stopPropagation, so this handler only fires for the overview case.
      setSelection({ stepExeId: node.id, section: null });
    },
    [],
  );

  const selectionContextValue = useMemo(
    () => ({ selection, setSelection }),
    [selection],
  );

  const selectedNode = useMemo<StepNodeData | null>(() => {
    if (!selection) return null;
    return graph.nodes.find((n) => n.stepExeId === selection.stepExeId) ?? null;
  }, [graph.nodes, selection]);

  const selectedEvents = useMemo<HistoryEvent[]>(() => {
    if (!selection || selection.stepExeId.startsWith('__')) return [];
    const filterTypes = eventTypesForSection(selection.section);
    return events.filter((e) => {
      const data = e.payload.data as Record<string, unknown> | undefined;
      if (!data || data.step_exe_id !== selection.stepExeId) return false;
      if (!filterTypes) return true;
      return filterTypes.has(e.payload.type);
    });
  }, [events, selection]);

  return (
    <SectionSelectionContext.Provider value={selectionContextValue}>
      <div
        data-testid="workflow-graph"
        className="h-[600px] border border-gray-200 rounded-lg flex overflow-hidden"
      >
        <div
          className={`flex-1 min-h-0 h-full ${selectedNode ? 'border-r border-gray-200' : ''}`}
        >
          {graph.nodes.length === 0 ? (
            <div className="h-full flex items-center justify-center text-sm text-gray-500">
              No step events available for graph visualization.
            </div>
          ) : (
            <ReactFlow
              nodes={nodes}
              edges={edges}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onNodeClick={onNodeClick}
              nodeTypes={nodeTypes}
              fitView
              minZoom={0.2}
              maxZoom={2}
              className="h-full w-full"
              style={{ width: '100%', height: '100%' }}
              proOptions={{ hideAttribution: true }}
            >
              <Controls position="top-right" showInteractive={false} />
              <MiniMap
                position="top-left"
                pannable
                zoomable
                nodeStrokeWidth={2}
                style={{ width: 120, height: 80 }}
              />
              <Background />
            </ReactFlow>
          )}
        </div>

        {selectedNode && (
          <>
            <div
              data-testid="detail-panel-resize-handle"
              role="separator"
              aria-orientation="vertical"
              aria-label="Resize detail panel"
              className="w-1.5 shrink-0 cursor-col-resize bg-gray-100 hover:bg-blue-300 active:bg-blue-400 touch-none"
              onPointerDown={beginDetailResize}
              onPointerMove={moveDetailResize}
              onPointerUp={endDetailResize}
              onPointerCancel={endDetailResize}
            />
            <div
              data-testid="step-node-detail"
              style={{ width: detailWidth }}
              className="shrink-0 min-w-0 overflow-auto bg-white"
            >
              <div className="sticky top-0 bg-white border-b border-gray-200 px-4 py-3 flex justify-between items-start">
              <div>
                <div className="text-xs uppercase tracking-wide text-gray-500">
                  {selectedNode.isVirtual
                    ? 'Virtual'
                    : selection?.section === 'wait'
                      ? 'Step → WaitFor'
                      : selection?.section === 'execute'
                        ? 'Step → Execute'
                        : 'Step execution'}
                </div>
                <div className="font-mono text-xs break-all text-gray-900 mt-0.5">
                  {selectedNode.stepExeId}
                </div>
                {!selectedNode.isVirtual && (
                  <div className="text-xs text-gray-500 mt-0.5">{selectedNode.stepId}</div>
                )}
              </div>
              <button
                type="button"
                onClick={() => setSelection(null)}
                aria-label="Close detail panel"
                className="text-gray-500 hover:text-gray-800 text-lg leading-none"
              >
                ×
              </button>
            </div>
            <div className="p-3 space-y-3">
              {selectedNode.isVirtual ? (
                <pre className="text-xs bg-gray-50 border border-gray-200 rounded p-2 overflow-x-auto">
                  {JSON.stringify(selectedNode.meta ?? {}, null, 2)}
                </pre>
              ) : (
                <>
                  {/* Live wait condition tree shown for the overview AND the
                      WaitFor section. Hidden when the user is focused on
                      Execute since it's not the relevant context there. */}
                  {selection?.section !== 'execute' &&
                    (selectedNode.waitFor ||
                      (selectedNode.conditionResults && selectedNode.conditionResults.length > 0)) && (
                      <div className="border border-gray-200 rounded p-2 bg-gray-50">
                        <div className="text-xs font-medium uppercase tracking-wide text-gray-500 mb-1">
                          {selectedNode.status === 'Waiting' ? 'Currently waiting on' : 'Wait condition (last)'}
                        </div>
                        <WaitForConditionPanel
                          tree={selectedNode.waitFor ?? null}
                          results={selectedNode.conditionResults}
                        />
                      </div>
                    )}
                  {/* Condition results from the resume path: only when the
                      user picked Execute, since that's where they live on
                      StepExecuteCompleted. The events list below also shows
                      them, but surfacing the panel here makes the
                      "what fired before Execute" obvious at a glance. */}
                  {selection?.section === 'execute' &&
                    selectedNode.conditionResults &&
                    selectedNode.conditionResults.length > 0 && (
                      <div className="border border-gray-200 rounded p-2 bg-gray-50">
                        <div className="text-xs font-medium uppercase tracking-wide text-gray-500 mb-1">
                          Condition results (what fired)
                        </div>
                        <WaitForConditionPanel results={selectedNode.conditionResults} />
                      </div>
                    )}
                  {selectedNode.retryState && (
                    <div
                      className="border border-amber-200 rounded p-2 bg-amber-50"
                      data-testid="step-retry-panel"
                    >
                      <div className="text-xs font-medium uppercase tracking-wide text-amber-800 mb-1">
                        Retry state
                      </div>
                      <div className="text-xs text-amber-900">
                        Attempt {selectedNode.retryState.currentAttempts}
                      </div>
                      {selectedNode.retryState.lastError != null && (
                        <div className="text-xs text-amber-900 mt-1 break-all">
                          {selectedNode.retryState.lastError}
                        </div>
                      )}
                      {selectedNode.retryState.lastErrorStackTrace != null && (
                        <div className="mt-2">
                          <div className="text-xs font-medium uppercase tracking-wide text-amber-800 mb-1">
                            Stack trace
                          </div>
                          <StackTracePre
                            text={selectedNode.retryState.lastErrorStackTrace}
                            className="text-amber-900"
                          />
                        </div>
                      )}
                    </div>
                  )}
                  {selectedEvents.length === 0 ? (
                    <p className="text-sm text-gray-500">
                      {selection?.section
                        ? `No ${selection.section === 'wait' ? 'WaitFor' : 'Execute'} events linked to this step.`
                        : 'No events linked to this step.'}
                    </p>
                  ) : (
                    <ol className="relative">
                      {selectedEvents.map((e) => (
                        <EventCard key={e.id} event={e} />
                      ))}
                    </ol>
                  )}
                </>
              )}
            </div>
            </div>
          </>
        )}
      </div>
    </SectionSelectionContext.Provider>
  );
}
