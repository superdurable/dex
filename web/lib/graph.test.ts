// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { describe, expect, it } from 'vitest';
import { buildStepGraph, END_NODE_ID } from './graph';
import type { FlowHistoryEvent } from './types';

function event(
  eventId: number,
  type: FlowHistoryEvent['type'],
  execution: Record<string, unknown> = {},
  payload: Record<string, unknown> = {},
): FlowHistoryEvent {
  return { eventId, eventTime: null, type, payload: { ...payload, execution } };
}

describe('step graph', () => {
  it('uses the same lineage model for semantic SYNC and ASYNC events', () => {
    const events = [
      event(1, 'StepExecuteCompleted', {
        stepExecutionId: 'A-1',
        fromStepExecutionId: '__start__',
        stepType: 'A',
        durability: 1,
      }),
      event(2, 'StepExecuteCompleted', {
        stepExecutionId: 'B-1',
        fromStepExecutionId: 'A-1',
        stepType: 'B',
        durability: 2,
      }),
      event(3, 'FlowClosed'),
    ];
    const graph = buildStepGraph(events);
    expect(graph.edges).toContainEqual({
      id: '__start__->A-1',
      source: '__start__',
      target: 'A-1',
    });
    expect(graph.edges.map((edge) => `${edge.source}->${edge.target}`)).toContain('A-1->B-1');
    expect(graph.nodes.find((node) => node.id === 'B-1')?.status).toBe('Completed');
  });

  it('connects lineage from a previous run to the current run start', () => {
    const graph = buildStepGraph([
      event(1, 'StepExecuteCompleted', {
        stepExecutionId: 'S2-1',
        fromStepExecutionId: 'S1-1',
        stepType: 'S2',
      }),
      event(2, 'FlowClosed'),
    ]);
    expect(graph.edges).toContainEqual({
      id: '__start__->S2-1',
      source: '__start__',
      target: 'S2-1',
    });
    expect(graph.nodes.find((node) => node.id === 'S2-1')?.fromStepExecutionId).toBe('S1-1');
  });

  it('creates RPC sources and overlays waiting state', () => {
    const graph = buildStepGraph([], [{
      stepExecutionId: 'Approval-1',
      fromStepExecutionId: '__rpc/approve',
      stepType: 'Approval',
      phase: 'Waiting',
      stepExecutionLocals: [],
      timers: [],
    }]);
    expect(graph.nodes.find((node) => node.id === '__rpc/approve')?.kind).toBe('source');
    expect(graph.edges).toContainEqual({
      id: '__rpc/approve->Approval-1',
      source: '__rpc/approve',
      target: 'Approval-1',
    });
  });

  it('connects only the step with a close decision to the terminal node', () => {
    const graph = buildStepGraph([
      event(1, 'StepWaitForCompleted', {
        stepExecutionId: 'trial-1',
        fromStepExecutionId: 'initialize-1',
        stepType: 'trial',
      }),
      event(2, 'StepWaitForCompleted', {
        stepExecutionId: 'cancel-1',
        fromStepExecutionId: 'initialize-1',
        stepType: 'cancel',
      }),
      event(3, 'StepExecuteCompleted', {
        stepExecutionId: 'cancel-1',
        fromStepExecutionId: 'initialize-1',
        stepType: 'cancel',
      }, {
        response: { stepDecision: { closeDecision: { closeDecisionType: 3 } } },
      }),
      event(4, 'FlowClosed'),
    ]);

    expect(graph.edges.filter((edge) => edge.target === END_NODE_ID)).toEqual([{
      id: 'cancel-1->__end__',
      source: 'cancel-1',
      target: '__end__',
    }]);
  });
});
