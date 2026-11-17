// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import type { StepGraphEdge, StepGraphNode } from '@/lib/types';
import {
  foldStepGraph,
  roundFrameBoxes,
  roundGroupNodeID,
  stepsInExecutionOrder,
  type FoldedGraphItem,
  type StepGraphFoldingOptions,
} from './graphFolding';

const defaultOptions: StepGraphFoldingOptions = { openRounds: new Map(), selectedStepExecutionID: '' };

function stepNode(
  id: string,
  stepType: string,
  eventId: number,
  status: StepGraphNode['status'] = 'Completed',
  fromStepExecutionId = '__start__',
): StepGraphNode {
  const eventTime = new Date(Date.parse('2026-10-24T17:00:00Z') + eventId * 1000).toISOString();
  return {
    id,
    label: stepType,
    kind: 'step',
    status,
    stepType,
    fromStepExecutionId,
    execute: status === 'Completed' || status === 'Failed'
      ? { eventId, eventTime, type: status === 'Failed' ? 'StepExecuteFailed' : 'StepExecuteCompleted', payload: {} }
      : undefined,
    waitFor: status === 'Waiting'
      ? { eventId, eventTime, type: 'StepWaitForCompleted', payload: {} }
      : undefined,
  };
}

const startNode: StepGraphNode = { id: '__start__', label: 'Flow start', kind: 'source', status: 'Source' };

/** A chain: each Step moves to the next one. */
function chain(steps: Array<[string, string, StepGraphNode['status']?]>): { nodes: StepGraphNode[]; edges: StepGraphEdge[] } {
  const nodes: StepGraphNode[] = [startNode];
  const edges: StepGraphEdge[] = [];
  let previous = '__start__';
  steps.forEach(([id, stepType, status], index) => {
    nodes.push(stepNode(id, stepType, index + 2, status ?? 'Completed', previous));
    edges.push({ id: `${previous}->${id}`, source: previous, target: id });
    previous = id;
  });
  return { nodes, edges };
}

function loop(stepType: string, count: number, statusOf: (index: number) => StepGraphNode['status'] = () => 'Completed') {
  return chain(Array.from({ length: count }, (_, index) => [`${stepType}-${index + 1}`, stepType, statusOf(index)]));
}

function itemIDs(items: FoldedGraphItem[]): string[] {
  return items.map((item) => item.id);
}

const agentSession = chain([
  ['StartAgentSession-1', 'StartAgentSession'],
  ['RunModel-1', 'RunModel'],
  ['AwaitUserMessage-1', 'AwaitUserMessage'],
  ['RunModel-2', 'RunModel'],
  ['ResumeTurn-1', 'ResumeTurn'],
  ['CallTool-1', 'CallTool'],
  ['CallTool-2', 'CallTool'],
  ['ResumeTurn-2', 'ResumeTurn'],
  ['RunModel-3', 'RunModel'],
  ['AwaitUserMessage-2', 'AwaitUserMessage', 'Waiting'],
]);

describe('Round grouping', () => {
  it('starts a Round at each return to the earliest repeated Step type and folds all but the latest', () => {
    const folded = foldStepGraph(agentSession.nodes, agentSession.edges, defaultOptions);
    expect(folded.rounds.map((round) => [round.title, round.stepCount, round.isOpen])).toEqual([
      ['Round 1', 2, false],
      ['Round 2', 5, false],
      ['Round 3', 2, true],
    ]);
    expect(folded.rounds[1].sequence).toEqual(['RunModel', 'ResumeTurn', 'CallTool ×2', 'ResumeTurn']);
    expect(folded.rounds[1].duration).toBe('4s');
    expect(itemIDs(folded.items)).toEqual([
      '__start__',
      'StartAgentSession-1',
      roundGroupNodeID('RunModel-1'),
      roundGroupNodeID('RunModel-2'),
      'RunModel-3',
      'AwaitUserMessage-2',
    ]);
    expect(folded.edges.map((edge) => `${edge.source}->${edge.target}`)).toEqual([
      '__start__->StartAgentSession-1',
      `StartAgentSession-1->${roundGroupNodeID('RunModel-1')}`,
      `${roundGroupNodeID('RunModel-1')}->${roundGroupNodeID('RunModel-2')}`,
      `${roundGroupNodeID('RunModel-2')}->RunModel-3`,
      'RunModel-3->AwaitUserMessage-2',
    ]);
  });

  it('lists at most four Step sequence entries before an ellipsis', () => {
    const longRound = chain([
      ['A-1', 'A'], ['B-1', 'B'], ['C-1', 'C'], ['D-1', 'D'], ['E-1', 'E'], ['A-2', 'A'],
    ]);
    const folded = foldStepGraph(longRound.nodes, longRound.edges, defaultOptions);
    expect(folded.rounds[0].sequence).toEqual(['A', 'B', 'C', 'D', '…']);
  });

  it('merges consecutive single-Step Rounds of one Step type into one group', () => {
    const syncLoop = loop('SyncBatch', 10, (index) => (index === 9 ? 'Waiting' : 'Completed'));
    const folded = foldStepGraph(syncLoop.nodes, syncLoop.edges, defaultOptions);
    expect(folded.rounds).toHaveLength(2);
    const [merged, latest] = folded.rounds;
    expect(merged).toMatchObject({
      title: 'SyncBatch ×9',
      roundRangeLabel: 'Rounds 1–9',
      repeatedStepType: 'SyncBatch',
      stepCount: 9,
      completedCount: 9,
      isOpen: false,
      canHide: true,
      sequence: [],
    });
    expect(latest).toMatchObject({ title: 'Round 10', isLatest: true, isLive: true, isOpen: true, canHide: false });
    expect(itemIDs(folded.items)).toEqual(['__start__', roundGroupNodeID('SyncBatch-1'), 'SyncBatch-10']);
  });

  it('folds nothing when no Step type repeats', () => {
    const graph = chain([['A-1', 'A'], ['B-1', 'B'], ['C-1', 'C', 'Waiting']]);
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    expect(folded.rounds).toEqual([]);
    expect(itemIDs(folded.items)).toEqual(['__start__', 'A-1', 'B-1', 'C-1']);
    expect(folded.edges).toEqual(graph.edges);
  });

  it('shows consecutive executions of one Step type inside an open Round as one ×N node', () => {
    const folded = foldStepGraph(agentSession.nodes, agentSession.edges, {
      openRounds: new Map([['RunModel-2', true]]),
      selectedStepExecutionID: '',
    });
    const toolNode = folded.items.find((item) => item.display === 'node' && item.node.stepType === 'CallTool');
    expect(toolNode).toMatchObject({ id: 'CallTool-2', repeatCount: 2, roundKey: 'RunModel-2' });
    expect(folded.edges.map((edge) => `${edge.source}->${edge.target}`)).toContain('ResumeTurn-1->CallTool-2');
    expect(folded.edges.map((edge) => `${edge.source}->${edge.target}`)).toContain('CallTool-2->ResumeTurn-2');
  });

  it('shows the selected execution for a ×N node', () => {
    const folded = foldStepGraph(agentSession.nodes, agentSession.edges, {
      openRounds: new Map([['RunModel-2', true]]),
      selectedStepExecutionID: 'CallTool-1',
    });
    const toolNode = folded.items.find((item) => item.display === 'node' && item.node.stepType === 'CallTool');
    expect(toolNode).toMatchObject({ id: 'CallTool-1', repeatCount: 2 });
  });

  it('orders Steps by their first recorded event', () => {
    const later = stepNode('B-1', 'B', 9);
    const earlier = stepNode('A-1', 'A', 3);
    const planned: StepGraphNode = { id: '__planned:9:0', label: 'C', kind: 'step', status: 'Pending', stepType: 'C', isPlanned: true };
    expect(stepsInExecutionOrder([startNode, planned, later, earlier]).map((node) => node.id)).toEqual(['A-1', 'B-1', '__planned:9:0']);
  });
});

describe('Round default open state', () => {
  const failedAgentSession = chain([
    ['RunModel-1', 'RunModel'],
    ['CallTool-1', 'CallTool', 'Failed'],
    ['RunModel-2', 'RunModel'],
    ['CallTool-2', 'CallTool'],
    ['RunModel-3', 'RunModel'],
  ]);

  it('opens a Round with a failed Step, which can still be hidden', () => {
    const folded = foldStepGraph(failedAgentSession.nodes, failedAgentSession.edges, defaultOptions);
    expect(folded.rounds.map((round) => [round.title, round.failedCount, round.isOpen, round.canHide])).toEqual([
      ['Round 1', 1, true, true],
      ['Round 2', 0, false, true],
      ['Round 3', 0, true, false],
    ]);
    const hidden = foldStepGraph(failedAgentSession.nodes, failedAgentSession.edges, {
      openRounds: new Map([['RunModel-1', false]]),
      selectedStepExecutionID: '',
    });
    expect(hidden.rounds[0].isOpen).toBe(false);
  });

  it('never folds a Round with a waiting, running or pending Step', () => {
    const graph = chain([
      ['RunModel-1', 'RunModel'],
      ['RunModel-2', 'RunModel'],
      ['Approve-1', 'Approve', 'Waiting'],
      ['RunModel-3', 'RunModel'],
    ]);
    const folded = foldStepGraph(graph.nodes, graph.edges, {
      openRounds: new Map([['RunModel-2', false]]),
      selectedStepExecutionID: '',
    });
    expect(folded.rounds.map((round) => [round.title, round.isLive, round.isOpen, round.canHide])).toEqual([
      ['Round 1', false, false, true],
      ['Round 2', true, true, false],
      ['Round 3', false, true, false],
    ]);
    expect(folded.items.some((item) => item.id === 'Approve-1')).toBe(true);
  });

  it('keeps the latest Round apart from merged single-Step Rounds', () => {
    const syncLoop = loop('SyncBatch', 5);
    const folded = foldStepGraph(syncLoop.nodes, syncLoop.edges, defaultOptions);
    expect(folded.rounds.map((round) => [round.title, round.isOpen])).toEqual([['SyncBatch ×4', false], ['Round 5', true]]);
  });

  it('shows the first three, failed, selected and last two executions of a long opened group', () => {
    const syncLoop = loop('SyncBatch', 21, (index) => (index === 9 ? 'Failed' : 'Completed'));
    const folded = foldStepGraph(syncLoop.nodes, syncLoop.edges, { openRounds: new Map(), selectedStepExecutionID: 'SyncBatch-15' });
    expect(folded.rounds[0]).toMatchObject({ title: 'SyncBatch ×20', failedCount: 1, isOpen: true });
    const shown = folded.items.map((item) => (item.display === 'more' ? `… ${item.hiddenCount} more` : item.id));
    expect(shown).toEqual([
      '__start__',
      'SyncBatch-1', 'SyncBatch-2', 'SyncBatch-3',
      '… 6 more',
      'SyncBatch-10',
      '… 4 more',
      'SyncBatch-15',
      '… 3 more',
      'SyncBatch-19', 'SyncBatch-20',
      'SyncBatch-21',
    ]);
    const edges = folded.edges.map((edge) => `${edge.source}->${edge.target}`);
    expect(edges).toContain('SyncBatch-3->__round-more:SyncBatch-1:SyncBatch-4');
    expect(edges).toContain('__round-more:SyncBatch-1:SyncBatch-4->SyncBatch-10');
  });

  it('hides SubFlow nodes of a Step inside a folded Round', () => {
    const graph = loop('Poll', 3);
    const subFlow: StepGraphNode = { id: '__subflow:Poll-1:0', label: 'child', kind: 'subflow', status: 'Completed', parentStepId: 'Poll-1' };
    const folded = foldStepGraph(
      [...graph.nodes, subFlow],
      [...graph.edges, { id: 'Poll-1->__subflow:Poll-1:0', source: 'Poll-1', target: subFlow.id }],
      defaultOptions,
    );
    expect(folded.items.some((item) => item.id === subFlow.id)).toBe(false);
    expect(folded.edges.some((edge) => edge.target === subFlow.id)).toBe(false);
  });
});

describe('Round frames', () => {
  it('surrounds the nodes of each opened Round with room for its heading', () => {
    const folded = foldStepGraph(agentSession.nodes, agentSession.edges, defaultOptions);
    const boxes = new Map([
      ['RunModel-3', { x: 100, y: 50, width: 300, height: 128 }],
      ['AwaitUserMessage-2', { x: 464, y: 40, width: 300, height: 148 }],
      ['StartAgentSession-1', { x: 0, y: 0, width: 300, height: 128 }],
    ]);
    expect(roundFrameBoxes(folded.items, boxes)).toEqual([
      { roundKey: 'RunModel-3', x: 86, y: 0, width: 692, height: 202 },
    ]);
  });
});
