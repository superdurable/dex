// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { describe, expect, it } from 'vitest';
import { buildStepGraph, stepGraphSelection } from './graph';
import type { FlowHistoryEvent } from './types';

function event(
  eventId: number,
  type: FlowHistoryEvent['type'],
  context: Record<string, unknown> = {},
  payload: Record<string, unknown> = {},
): FlowHistoryEvent {
  return { eventId, eventTime: null, type, payload: { ...payload, context } };
}

describe('step graph', () => {
  it('selects adjacent Step executions and their connecting edges', () => {
    const selectedEvent = event(2, 'StepExecuteCompleted', {
      stepExecutionId: 'Root-1',
      fromStepExecutionId: 'Previous-1',
      stepType: 'Root',
    }, {
      output: { stepDecision: { nextSteps: [{
        stepType: 'Left',
        fromStepExecutionIdInternalOnly: 'Root-1',
      }, {
        stepType: 'Right',
        fromStepExecutionIdInternalOnly: 'Root-1',
      }] } },
    });
    const graph = buildStepGraph([
      event(1, 'StepExecuteCompleted', {
        stepExecutionId: 'Previous-1',
        fromStepExecutionId: '__start__',
        stepType: 'Previous',
      }),
      selectedEvent,
      event(3, 'StepExecuteCompleted', {
        stepExecutionId: 'Left-1',
        fromStepExecutionId: 'Root-1',
        stepType: 'Left',
      }),
      event(4, 'StepExecuteCompleted', {
        stepExecutionId: 'Right-1',
        fromStepExecutionId: 'Root-1',
        stepType: 'Right',
      }),
    ]);

    const selection = stepGraphSelection(graph.nodes, graph.edges, selectedEvent);

    expect(selection.selectedStepExecutionID).toBe('Root-1');
    expect([...selection.previousStepExecutionIDs]).toEqual(['Previous-1']);
    expect([...selection.nextStepExecutionIDs]).toEqual([
      'Left-1',
      'Right-1',
    ]);
    expect([...selection.incomingEdgeIDs]).toEqual(['Previous-1->Root-1']);
    expect([...selection.outgoingEdgeIDs]).toEqual([
      'Root-1->Left-1',
      'Root-1->Right-1',
    ]);
  });

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

  it('does not add Time Travel lineage to the business topology', () => {
    const graph = buildStepGraph([
      event(1, 'FlowStartedOrContinued'),
      event(7, 'TimeTravelFork', {}, { previousRunId: 'source-run-id' }),
    ]);

    expect(graph.nodes.find((node) => node.id === '__start__')).toMatchObject({
      label: 'Flow start',
      previousRunId: '',
    });
  });

  it('shows Continue-As-New lineage at the business topology source', () => {
    const graph = buildStepGraph([
      event(1, 'FlowStartedOrContinued', {}, {
        continuedStart: { previousRunId: 'continued-run-id' },
      }),
    ]);

    expect(graph.nodes.find((node) => node.id === '__start__')).toMatchObject({
      previousRunId: 'continued-run-id',
    });
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

  it('does not create topology edges for close decisions', () => {
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
        output: { stepDecision: { closeDecision: { closeDecisionType: 3 } } },
      }),
      event(4, 'FlowClosed'),
    ]);

    expect(graph.nodes.map((node) => node.id)).not.toContain('__end__');
    expect(graph.edges).toEqual([
      {
        id: '__start__->trial-1',
        source: '__start__',
        target: 'trial-1',
      },
      {
        id: '__start__->cancel-1',
        source: '__start__',
        target: 'cancel-1',
      },
    ]);
  });

  it('retains a step method that was still pending when the flow closed', () => {
    const graph = buildStepGraph([
      event(1, 'StepWaitForCompleted', {
        stepExecutionId: 'charge-1',
        fromStepExecutionId: '__start__',
        stepType: 'charge',
      }),
      event(2, 'StepExecutePending', {
        stepExecutionId: 'charge-1',
        fromStepExecutionId: '__start__',
        stepType: 'charge',
      }, {
        phase: 2,
      }),
      event(3, 'FlowClosed'),
    ]);

    const node = graph.nodes.find((candidate) => candidate.id === 'charge-1');
    expect(node?.status).toBe('Pending');
    expect(node?.waitFor?.type).toBe('StepWaitForCompleted');
    expect(node?.pendingExecute?.payload.phase).toBe(2);
    expect(node?.pendingExecute?.type).toBe('StepExecutePending');
  });

  it('shows a planned branch canceled before a Step event was recorded', () => {
    const graph = buildStepGraph([
      event(1, 'StepExecuteCompleted', {
        stepExecutionId: 'Root-1',
        fromStepExecutionId: '__start__',
        stepType: 'Root',
      }, {
        output: { stepDecision: { nextSteps: [{
          stepType: 'Winner',
          fromStepExecutionIdInternalOnly: 'Root-1',
        }, {
          stepType: 'Loser',
          fromStepExecutionIdInternalOnly: 'Root-1',
          stepOptions: { skipWaitFor: true },
        }] } },
      }),
      event(2, 'StepExecuteCompleted', {
        stepExecutionId: 'Winner-1',
        fromStepExecutionId: 'Root-1',
        stepType: 'Winner',
      }, {
        output: { stepDecision: { cancelStepTypes: ['Loser'] } },
      }),
      event(3, 'FlowClosed'),
    ]);

    expect(graph.nodes.filter((node) => node.stepType === 'Winner')).toHaveLength(1);
    const loser = graph.nodes.find((node) => node.stepType === 'Loser');
    expect(loser).toMatchObject({
      status: 'Canceled',
      fromStepExecutionId: 'Root-1',
      isPlanned: true,
    });
    expect(graph.edges).toContainEqual({
      id: `Root-1->${loser?.id}`,
      source: 'Root-1',
      target: loser?.id,
    });
  });

  it('excludes next Steps created by the canceling decision from its snapshot', () => {
    const graph = buildStepGraph([
      event(1, 'StepExecuteCompleted', {
        stepExecutionId: 'Root-1',
        fromStepExecutionId: '__start__',
        stepType: 'Root',
      }, {
        output: { stepDecision: { nextSteps: [{
          stepType: 'Worker',
          fromStepExecutionIdInternalOnly: 'Root-1',
        }, {
          stepType: 'Worker',
          fromStepExecutionIdInternalOnly: 'Root-1',
        }, {
          stepType: 'Winner',
          fromStepExecutionIdInternalOnly: 'Root-1',
        }] } },
      }),
      event(2, 'StepExecuteCompleted', {
        stepExecutionId: 'Worker-1',
        fromStepExecutionId: 'Root-1',
        stepType: 'Worker',
      }),
      event(3, 'StepExecuteCompleted', {
        stepExecutionId: 'Winner-1',
        fromStepExecutionId: 'Root-1',
        stepType: 'Winner',
      }, {
        output: { stepDecision: {
          cancelStepTypes: ['Worker'],
          nextSteps: [{
            stepType: 'Worker',
            fromStepExecutionIdInternalOnly: 'Winner-1',
          }],
        } },
      }),
    ]);

    const workers = graph.nodes.filter((node) => node.stepType === 'Worker');
    expect(workers).toHaveLength(3);
    expect(workers.find((node) => node.id === 'Worker-1')?.status).toBe('Completed');
    expect(workers.find((node) => node.isPlanned
      && node.fromStepExecutionId === 'Root-1')?.status).toBe('Canceled');
    expect(workers.find((node) => node.fromStepExecutionId === 'Winner-1')?.status).toBe('Pending');
  });

  it('limits sibling cancellation to the canceling Step lineage', () => {
    const graph = buildStepGraph([
      event(1, 'StepExecuteCompleted', {
        stepExecutionId: 'Root-1',
        fromStepExecutionId: '__start__',
        stepType: 'Root',
      }, {
        output: { stepDecision: { nextSteps: [{
          stepType: 'ParentA',
          fromStepExecutionIdInternalOnly: 'Root-1',
        }, {
          stepType: 'ParentB',
          fromStepExecutionIdInternalOnly: 'Root-1',
        }] } },
      }),
      event(2, 'StepExecuteCompleted', {
        stepExecutionId: 'ParentA-1',
        fromStepExecutionId: 'Root-1',
        stepType: 'ParentA',
      }, {
        output: { stepDecision: { nextSteps: [{
          stepType: 'Worker',
          fromStepExecutionIdInternalOnly: 'ParentA-1',
        }, {
          stepType: 'Winner',
          fromStepExecutionIdInternalOnly: 'ParentA-1',
        }] } },
      }),
      event(3, 'StepExecuteCompleted', {
        stepExecutionId: 'ParentB-1',
        fromStepExecutionId: 'Root-1',
        stepType: 'ParentB',
      }, {
        output: { stepDecision: { nextSteps: [{
          stepType: 'Worker',
          fromStepExecutionIdInternalOnly: 'ParentB-1',
        }] } },
      }),
      event(4, 'StepExecuteCompleted', {
        stepExecutionId: 'Winner-1',
        fromStepExecutionId: 'ParentA-1',
        stepType: 'Winner',
      }, {
        output: { stepDecision: { cancelSiblingStepTypes: ['Worker'] } },
      }),
    ]);

    const workers = graph.nodes.filter((node) => node.stepType === 'Worker');
    expect(workers.find((node) => node.fromStepExecutionId === 'ParentA-1')?.status).toBe('Canceled');
    expect(workers.find((node) => node.fromStepExecutionId === 'ParentB-1')?.status).toBe('Pending');
  });

  it('keeps cancellation ahead of a stale active-state overlay', () => {
    const graph = buildStepGraph([
      event(1, 'StepWaitForCompleted', {
        stepExecutionId: 'Worker-1',
        fromStepExecutionId: 'Root-1',
        stepType: 'Worker',
      }),
      event(2, 'StepExecuteCompleted', {
        stepExecutionId: 'Winner-1',
        fromStepExecutionId: 'Root-1',
        stepType: 'Winner',
      }, {
        output: { stepDecision: { cancelStepTypes: ['Worker'] } },
      }),
    ], [{
      stepExecutionId: 'Worker-1',
      fromStepExecutionId: 'Root-1',
      stepType: 'Worker',
      phase: 'Active',
      stepExecutionLocals: [],
      timers: [],
    }]);

    const worker = graph.nodes.find((node) => node.id === 'Worker-1');
    expect(worker).toMatchObject({
      status: 'Canceled',
      active: undefined,
    });
    expect(worker?.isPlanned).toBeUndefined();
  });

  it('creates linked SubFlow leaf nodes with deterministic identity', () => {
    const graph = buildStepGraph([
      event(1, 'StepWaitForCompleted', {
        stepExecutionId: 'Parent-1',
        fromStepExecutionId: '__start__',
        stepType: 'Parent',
      }, {
        output: { waitForCondition: { subFlowConditions: [{
          options: { reusePolicy: 1 },
        }] } },
      }),
      event(2, 'StepExecuteCompleted', {
        stepExecutionId: 'Parent-1',
        fromStepExecutionId: '__start__',
        stepType: 'Parent',
      }, {
        input: { conditionResults: { subFlowResults: [{
          flowStatus: 2,
        }] } },
      }),
    ], [], 'parent');

    const subFlow = graph.nodes.find((node) => node.kind === 'subflow');
    expect(subFlow).toMatchObject({
      parentStepId: 'Parent-1',
      flowId: 'SubFlow:parent-Parent-1-0',
      subFlowStatus: 'COMPLETED',
      reusePolicy: 'Attach',
    });
    expect(graph.edges).toContainEqual({
      id: 'Parent-1->__subflow:Parent-1:0',
      source: 'Parent-1',
      target: '__subflow:Parent-1:0',
    });
  });

  it('uses terminal SubFlow results restored in active continue-as-new state', () => {
    const graph = buildStepGraph([], [{
      stepExecutionId: 'Parent-1',
      fromStepExecutionId: '__start__',
      stepType: 'Parent',
      phase: 'Waiting',
      stepExecutionLocals: [],
      timers: [],
      waitingCondition: { subFlowConditions: [{
        conditionId: 'child',
      }] },
      completedConditions: { completedSubFlowResults: { 0: {
        flowStatus: 3,
      } } },
    }], 'parent');

    expect(graph.nodes.find((node) => node.kind === 'subflow')).toMatchObject({
      status: 'Failed',
      subFlowStatus: 'FAILED',
      flowId: 'SubFlow:parent-Parent-1-0',
    });
  });

  it('hides the timeout handler Step unless the timeout policy is Handler', () => {
    const timeoutHandler = {
      stepExecutionId: 'sys:timeout_handler-1',
      fromStepExecutionId: '__start__',
      stepType: 'sys:timeout_handler',
      phase: 'Waiting' as const,
      stepExecutionLocals: [],
      timers: [],
    };

    const failGraph = buildStepGraph([
      event(1, 'FlowStartedOrContinued', {}, {
        flowTimeout: '60s',
        flowTimeoutPolicy: 1,
        initialStart: { startStepType: 'charge' },
      }),
    ], [timeoutHandler]);
    expect(failGraph.nodes.find((node) => node.stepType === 'sys:timeout_handler')).toBeUndefined();

    const cancelGraph = buildStepGraph([
      event(1, 'FlowStartedOrContinued', {}, {
        flowTimeout: '60s',
        flowTimeoutPolicy: 2,
        initialStart: { startStepType: 'charge' },
      }),
    ], [timeoutHandler]);
    expect(cancelGraph.nodes.find((node) => node.stepType === 'sys:timeout_handler')).toBeUndefined();

    const handlerGraph = buildStepGraph([
      event(1, 'FlowStartedOrContinued', {}, {
        flowTimeout: '60s',
        flowTimeoutPolicy: 3,
        initialStart: { startStepType: 'charge' },
      }),
    ], [timeoutHandler]);
    expect(handlerGraph.nodes.find((node) => node.stepType === 'sys:timeout_handler')).toMatchObject({
      id: 'sys:timeout_handler-1',
      status: 'Waiting',
    });
  });
});
