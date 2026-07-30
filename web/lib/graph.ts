import type {
  ActiveStepExecution,
  FlowHistoryEvent,
  StepGraphEdge,
  StepGraphNode,
} from './types';

export const START_NODE_ID = '__start__';
export const END_NODE_ID = '__end__';

const stepEventTypes = new Set([
  'StepWaitForCompleted',
  'StepWaitForFailed',
  'StepExecuteCompleted',
  'StepExecuteFailed',
]);

function execution(event: FlowHistoryEvent): Record<string, unknown> {
  const value = event.payload.execution;
  return value && typeof value === 'object' ? value as Record<string, unknown> : {};
}

function stringField(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

export function buildStepGraph(
  events: FlowHistoryEvent[],
  activeSteps: ActiveStepExecution[] = [],
): { nodes: StepGraphNode[]; edges: StepGraphEdge[] } {
  const nodes = new Map<string, StepGraphNode>();
  nodes.set(START_NODE_ID, {
    id: START_NODE_ID,
    label: 'Flow start',
    kind: 'source',
    status: 'Source',
  });

  for (const event of events) {
    if (!stepEventTypes.has(event.type)) continue;
    const info = execution(event);
    const id = stringField(info.stepExecutionId);
    if (!id) continue;
    const existing = nodes.get(id);
    const failed = event.type.endsWith('Failed');
    const waitFor = event.type.startsWith('StepWaitFor') ? event : existing?.waitFor;
    const execute = event.type.startsWith('StepExecute') ? event : existing?.execute;
    nodes.set(id, {
      id,
      label: stringField(info.stepType) || id,
      kind: 'step',
      status: failed ? 'Failed' : execute ? 'Completed' : 'Waiting',
      stepType: stringField(info.stepType),
      fromStepExecutionId: stringField(info.fromStepExecutionId) || START_NODE_ID,
      waitFor,
      execute,
      transient: info.isTransientStep === true,
    });
  }

  for (const active of activeSteps) {
    const existing = nodes.get(active.stepExecutionId);
    nodes.set(active.stepExecutionId, {
      id: active.stepExecutionId,
      label: active.stepType || active.stepExecutionId,
      kind: 'step',
      status: active.phase === 'Waiting' ? 'Waiting' : 'Active',
      stepType: active.stepType,
      fromStepExecutionId: active.fromStepExecutionId || START_NODE_ID,
      waitFor: existing?.waitFor,
      execute: existing?.execute,
      active,
      transient: existing?.transient,
    });
  }

  const closed = events.findLast((event) => event.type === 'FlowClosed');
  if (closed) {
    nodes.set(END_NODE_ID, {
      id: END_NODE_ID,
      label: 'Flow closed',
      kind: 'terminal',
      status: 'Terminal',
    });
  }

  for (const node of [...nodes.values()]) {
    const source = node.fromStepExecutionId;
    if (!source?.startsWith('__rpc/')) continue;
    if (!nodes.has(source)) {
      nodes.set(source, {
        id: source,
        label: `RPC · ${source.slice('__rpc/'.length)}`,
        kind: 'source',
        status: 'Source',
      });
    }
  }

  const edges: StepGraphEdge[] = [];
  for (const node of nodes.values()) {
    if (node.kind !== 'step') continue;
    const source = node.fromStepExecutionId || START_NODE_ID;
    edges.push({ id: `${source}->${node.id}`, source, target: node.id });
  }

  if (closed) {
    const stepIdsWithChildren = new Set(edges.map((edge) => edge.source));
    for (const node of nodes.values()) {
      if (node.kind === 'step' && !stepIdsWithChildren.has(node.id)) {
        edges.push({ id: `${node.id}->${END_NODE_ID}`, source: node.id, target: END_NODE_ID });
      }
    }
  }

  return { nodes: [...nodes.values()], edges };
}
