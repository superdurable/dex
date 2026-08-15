// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type {
  ActiveStepExecution,
  FlowHistoryEvent,
  StepGraphEdge,
  StepGraphNode,
} from './types';
import { subFlowReusePolicyLabel, subFlowStatusName } from './semantic';
import { generatedSubFlowID } from './subflows';

export const START_NODE_ID = '__start__';

const stepEventTypes = new Set([
  'StepWaitForCompleted',
  'StepWaitForFailed',
  'StepWaitForPending',
  'StepExecuteCompleted',
  'StepExecuteFailed',
  'StepExecutePending',
]);

const cancellableStatuses = new Set<StepGraphNode['status']>([
  'Active',
  'Waiting',
  'Pending',
]);

function stepContext(event: FlowHistoryEvent): Record<string, unknown> {
  const value = event.payload.context;
  return value && typeof value === 'object' ? value as Record<string, unknown> : {};
}

function stringField(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function previousRunID(events: FlowHistoryEvent[]): string {
  const started = events.find((event) => event.type === 'FlowStartedOrContinued');
  const continued = started?.payload.continuedStart;
  if (!continued || typeof continued !== 'object') return '';
  return stringField((continued as Record<string, unknown>).previousRunId);
}

export function buildStepGraph(
  events: FlowHistoryEvent[],
  activeSteps: ActiveStepExecution[] = [],
  parentFlowID = '',
): { nodes: StepGraphNode[]; edges: StepGraphEdge[] } {
  const nodes = new Map<string, StepGraphNode>();
  const plannedNodesByLineage = new Map<string, string[]>();
  nodes.set(START_NODE_ID, {
    id: START_NODE_ID,
    label: 'Flow start',
    kind: 'source',
    status: 'Source',
    previousRunId: previousRunID(events),
  });

  for (const event of events) {
    if (stepEventTypes.has(event.type)) {
      addStepEvent(nodes, plannedNodesByLineage, event);
    }
    const decision = stepDecision(event);
    if (!decision) continue;
    cancelMatchingNodes(nodes, decision, stringField(stepContext(event).fromStepExecutionId));
    addPlannedNodes(nodes, plannedNodesByLineage, event, decision);
  }

  for (const active of activeSteps) {
    const existing = nodes.get(active.stepExecutionId)
      ?? takePlannedNode(
        nodes,
        plannedNodesByLineage,
        active.fromStepExecutionId || START_NODE_ID,
        active.stepType,
      );
    if (existing?.isPlanned) nodes.delete(existing.id);
    const isCanceled = existing?.status === 'Canceled';
    nodes.set(active.stepExecutionId, {
      id: active.stepExecutionId,
      label: active.stepType || active.stepExecutionId,
      kind: 'step',
      status: isCanceled ? 'Canceled' : active.phase === 'Waiting' ? 'Waiting' : 'Active',
      stepType: active.stepType,
      fromStepExecutionId: active.fromStepExecutionId || START_NODE_ID,
      movement: existing?.movement ?? active.movement,
      waitFor: existing?.waitFor,
      execute: existing?.execute,
      pendingWaitFor: existing?.pendingWaitFor,
      pendingExecute: existing?.pendingExecute,
      active: isCanceled ? undefined : active,
    });
  }

  for (const stepNode of [...nodes.values()]) {
    if (stepNode.kind !== 'step') continue;
    addSubFlowNodes(nodes, stepNode, parentFlowID);
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
    if (node.kind === 'subflow') {
      edges.push({
        id: `${node.parentStepId}->${node.id}`,
        source: node.parentStepId ?? START_NODE_ID,
        target: node.id,
      });
      continue;
    }
    if (node.kind !== 'step') continue;
    const requestedSource = node.fromStepExecutionId || START_NODE_ID;
    const source = nodes.has(requestedSource) ? requestedSource : START_NODE_ID;
    edges.push({ id: `${source}->${node.id}`, source, target: node.id });
  }

  return { nodes: [...nodes.values()], edges };
}

export function stepGraphSelection(
  nodes: StepGraphNode[],
  edges: StepGraphEdge[],
  selectedEvent: FlowHistoryEvent | null,
): {
  selectedStepExecutionID: string;
  previousStepExecutionIDs: Set<string>;
  nextStepExecutionIDs: Set<string>;
  incomingEdgeIDs: Set<string>;
  outgoingEdgeIDs: Set<string>;
} {
  const selectedStepExecutionID = selectedEvent
    ? stringField(stepContext(selectedEvent).stepExecutionId)
    : '';
  const stepExecutionIDs = new Set(
    nodes.filter((node) => node.kind === 'step' && !node.isPlanned).map((node) => node.id),
  );
  const previousStepExecutionIDs = new Set<string>();
  const nextStepExecutionIDs = new Set<string>();
  const incomingEdgeIDs = new Set<string>();
  const outgoingEdgeIDs = new Set<string>();
  if (!stepExecutionIDs.has(selectedStepExecutionID)) {
    return {
      selectedStepExecutionID: '',
      previousStepExecutionIDs,
      nextStepExecutionIDs,
      incomingEdgeIDs,
      outgoingEdgeIDs,
    };
  }

  for (const edge of edges) {
    if (edge.target === selectedStepExecutionID && stepExecutionIDs.has(edge.source)) {
      previousStepExecutionIDs.add(edge.source);
      incomingEdgeIDs.add(edge.id);
    }
    if (edge.source === selectedStepExecutionID && stepExecutionIDs.has(edge.target)) {
      nextStepExecutionIDs.add(edge.target);
      outgoingEdgeIDs.add(edge.id);
    }
  }
  return {
    selectedStepExecutionID,
    previousStepExecutionIDs,
    nextStepExecutionIDs,
    incomingEdgeIDs,
    outgoingEdgeIDs,
  };
}

function addStepEvent(
  nodes: Map<string, StepGraphNode>,
  plannedNodesByLineage: Map<string, string[]>,
  event: FlowHistoryEvent,
): void {
  const info = stepContext(event);
  const id = stringField(info.stepExecutionId);
  if (!id) return;
  const stepType = stringField(info.stepType);
  const fromStepExecutionID = stringField(info.fromStepExecutionId) || START_NODE_ID;
  const existing = nodes.get(id)
    ?? takePlannedNode(nodes, plannedNodesByLineage, fromStepExecutionID, stepType);
  if (existing?.isPlanned) nodes.delete(existing.id);
  const failed = event.type.endsWith('Failed');
  const pending = event.type.endsWith('Pending');
  const waitFor = event.type.startsWith('StepWaitFor') && !pending ? event : existing?.waitFor;
  const execute = event.type.startsWith('StepExecute') && !pending ? event : existing?.execute;
  const status = existing?.status === 'Canceled'
    ? 'Canceled'
    : pending ? 'Pending' : failed ? 'Failed' : execute ? 'Completed' : 'Waiting';
  nodes.set(id, {
    id,
    label: stepType || id,
    kind: 'step',
    status,
    stepType,
    fromStepExecutionId: fromStepExecutionID,
    movement: existing?.movement,
    waitFor,
    execute,
    pendingWaitFor: event.type === 'StepWaitForPending' ? event : existing?.pendingWaitFor,
    pendingExecute: event.type === 'StepExecutePending' ? event : existing?.pendingExecute,
  });
}

function addPlannedNodes(
  nodes: Map<string, StepGraphNode>,
  plannedNodesByLineage: Map<string, string[]>,
  event: FlowHistoryEvent,
  decision: Record<string, unknown>,
): void {
  const producerID = stringField(stepContext(event).stepExecutionId);
  const movements = Array.isArray(decision.nextSteps) ? decision.nextSteps : [];
  movements.forEach((rawMovement, index) => {
    if (!rawMovement || typeof rawMovement !== 'object') return;
    const movement = rawMovement as Record<string, unknown>;
    const stepType = stringField(movement.stepType);
    if (!stepType) return;
    const fromStepExecutionID = stringField(movement.fromStepExecutionIdInternalOnly)
      || stringField(movement.fromStepExecutionId)
      || producerID
      || START_NODE_ID;
    const id = `__planned:${event.eventId}:${index}`;
    nodes.set(id, {
      id,
      label: stepType,
      kind: 'step',
      status: 'Pending',
      stepType,
      fromStepExecutionId: fromStepExecutionID,
      movement,
      isPlanned: true,
    });
    const key = lineageKey(fromStepExecutionID, stepType);
    plannedNodesByLineage.set(key, [...(plannedNodesByLineage.get(key) ?? []), id]);
  });
}

function cancelMatchingNodes(
  nodes: Map<string, StepGraphNode>,
  decision: Record<string, unknown>,
  producerFromStepExecutionID: string,
): void {
  const globalStepTypes = stringSet(decision.cancelStepTypes);
  const siblingStepTypes = stringSet(decision.cancelSiblingStepTypes);
  if (globalStepTypes.size === 0 && siblingStepTypes.size === 0) return;
  for (const [id, node] of nodes) {
    if (node.kind !== 'step' || !cancellableStatuses.has(node.status)) continue;
    const isGlobalMatch = globalStepTypes.has(node.stepType ?? '');
    const isSiblingMatch = siblingStepTypes.has(node.stepType ?? '')
      && node.fromStepExecutionId === producerFromStepExecutionID;
    if (isGlobalMatch || isSiblingMatch) {
      nodes.set(id, { ...node, status: 'Canceled', active: undefined });
    }
  }
}

function takePlannedNode(
  nodes: Map<string, StepGraphNode>,
  plannedNodesByLineage: Map<string, string[]>,
  fromStepExecutionID: string,
  stepType: string,
): StepGraphNode | undefined {
  const key = lineageKey(fromStepExecutionID, stepType);
  const ids = plannedNodesByLineage.get(key) ?? [];
  const index = ids.findIndex((id) => {
    const node = nodes.get(id);
    return node?.isPlanned === true && node.status !== 'Canceled';
  });
  if (index < 0) return undefined;
  const [id] = ids.splice(index, 1);
  if (ids.length === 0) plannedNodesByLineage.delete(key);
  return nodes.get(id);
}

function lineageKey(fromStepExecutionID: string, stepType: string): string {
  return `${fromStepExecutionID}\u0000${stepType}`;
}

function stringSet(value: unknown): Set<string> {
  if (!Array.isArray(value)) return new Set();
  return new Set(value.filter((entry): entry is string => typeof entry === 'string'));
}

function stepDecision(event: FlowHistoryEvent): Record<string, unknown> | undefined {
  const container = event.type === 'StepExecuteCompleted'
    ? event.payload.output
    : event.type === 'RpcExecutionCompleted' ? event.payload : undefined;
  if (!container || typeof container !== 'object') return undefined;
  const decision = (container as Record<string, unknown>).stepDecision;
  return decision && typeof decision === 'object'
    ? decision as Record<string, unknown>
    : undefined;
}

function addSubFlowNodes(
  nodes: Map<string, StepGraphNode>,
  stepNode: StepGraphNode,
  parentFlowID: string,
): void {
  const waitOutput = stepNode.waitFor?.payload.output;
  const output = waitOutput && typeof waitOutput === 'object'
    ? waitOutput as Record<string, unknown> : {};
  const waitingCondition = output.waitForCondition && typeof output.waitForCondition === 'object'
    ? output.waitForCondition as Record<string, unknown>
    : stepNode.active?.waitingCondition ?? {};
  const conditions = Array.isArray(waitingCondition.subFlowConditions)
    ? waitingCondition.subFlowConditions : [];
  const executeInput = stepNode.execute?.payload.input;
  const input = executeInput && typeof executeInput === 'object'
    ? executeInput as Record<string, unknown> : {};
  const conditionResults = input.conditionResults && typeof input.conditionResults === 'object'
    ? input.conditionResults as Record<string, unknown> : {};
  const results = Array.isArray(conditionResults.subFlowResults)
    ? conditionResults.subFlowResults : [];
  const completedConditions = stepNode.active?.completedConditions ?? {};
  const activeResults = completedConditions.completedSubFlowResults
    && typeof completedConditions.completedSubFlowResults === 'object'
    ? completedConditions.completedSubFlowResults as Record<string, unknown>
    : {};
  conditions.forEach((rawCondition, index) => {
    if (!rawCondition || typeof rawCondition !== 'object') return;
    const condition = rawCondition as Record<string, unknown>;
    const result = results[index] && typeof results[index] === 'object'
      ? results[index] as Record<string, unknown>
      : activeResults[String(index)] && typeof activeResults[String(index)] === 'object'
        ? activeResults[String(index)] as Record<string, unknown>
        : {};
    const flowId = generatedSubFlowID(parentFlowID, stepNode.id, index);
    if (!flowId) return;
    const status = subFlowStatusName(result.flowStatus ?? 1);
    const options = condition.options && typeof condition.options === 'object'
      ? condition.options as Record<string, unknown> : {};
    const id = `__subflow:${stepNode.id}:${index}`;
    nodes.set(id, {
      id,
      label: flowId,
      kind: 'subflow',
      status: ['FAILED', 'CANCELED', 'TIMEOUT', 'TERMINATED'].includes(status)
        ? 'Failed'
        : status === 'RUNNING' ? 'Waiting' : 'Completed',
      parentStepId: stepNode.id,
      flowId,
      subFlowStatus: status,
      reusePolicy: subFlowReusePolicyLabel(options.reusePolicy),
    });
  });
}
