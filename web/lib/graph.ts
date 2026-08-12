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

export const START_NODE_ID = '__start__';
export const END_NODE_ID = '__end__';

const stepEventTypes = new Set([
  'StepWaitForCompleted',
  'StepWaitForFailed',
  'StepWaitForPending',
  'StepExecuteCompleted',
  'StepExecuteFailed',
  'StepExecutePending',
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
): { nodes: StepGraphNode[]; edges: StepGraphEdge[] } {
  const nodes = new Map<string, StepGraphNode>();
  const closingStepExecutionIDs = new Set<string>();
  nodes.set(START_NODE_ID, {
    id: START_NODE_ID,
    label: 'Flow start',
    kind: 'source',
    status: 'Source',
    previousRunId: previousRunID(events),
  });

  for (const event of events) {
    if (!stepEventTypes.has(event.type)) continue;
    const info = stepContext(event);
    const id = stringField(info.stepExecutionId);
    if (!id) continue;
    const existing = nodes.get(id);
    const failed = event.type.endsWith('Failed');
    const pending = event.type.endsWith('Pending');
    const waitFor = event.type.startsWith('StepWaitFor') && !pending ? event : existing?.waitFor;
    const execute = event.type.startsWith('StepExecute') && !pending ? event : existing?.execute;
    if (event.type === 'StepExecuteCompleted' && hasCloseDecision(event)) {
      closingStepExecutionIDs.add(id);
    }
    nodes.set(id, {
      id,
      label: stringField(info.stepType) || id,
      kind: 'step',
      status: pending ? 'Pending' : failed ? 'Failed' : execute ? 'Completed' : 'Waiting',
      stepType: stringField(info.stepType),
      fromStepExecutionId: stringField(info.fromStepExecutionId) || START_NODE_ID,
      waitFor,
      execute,
      pendingWaitFor: event.type === 'StepWaitForPending' ? event : existing?.pendingWaitFor,
      pendingExecute: event.type === 'StepExecutePending' ? event : existing?.pendingExecute,
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
      pendingWaitFor: existing?.pendingWaitFor,
      pendingExecute: existing?.pendingExecute,
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
    const requestedSource = node.fromStepExecutionId || START_NODE_ID;
    const source = nodes.has(requestedSource) ? requestedSource : START_NODE_ID;
    edges.push({ id: `${source}->${node.id}`, source, target: node.id });
  }

  if (closed) {
    for (const stepExecutionID of closingStepExecutionIDs) {
      edges.push({
        id: `${stepExecutionID}->${END_NODE_ID}`,
        source: stepExecutionID,
        target: END_NODE_ID,
      });
    }
  }

  return { nodes: [...nodes.values()], edges };
}

function hasCloseDecision(event: FlowHistoryEvent): boolean {
  const output = event.payload.output;
  if (!output || typeof output !== 'object') return false;
  const stepDecision = (output as Record<string, unknown>).stepDecision;
  if (!stepDecision || typeof stepDecision !== 'object') return false;
  const closeDecision = (stepDecision as Record<string, unknown>).closeDecision;
  return Boolean(closeDecision && typeof closeDecision === 'object');
}
