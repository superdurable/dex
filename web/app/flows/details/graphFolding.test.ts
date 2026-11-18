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
  noGroupOpenIntents,
  roundFrameBoxes,
  roundGroupNodeID,
  stepsInExecutionOrder,
  withGroupOpenChoice,
  withStepExecutionOpened,
  type FoldedGraphItem,
  type GroupOpenIntents,
  type StepGraphFoldingOptions,
} from './graphFolding';

const defaultOptions: StepGraphFoldingOptions = { openIntents: noGroupOpenIntents, selectedStepExecutionID: '' };

type Graph = { nodes: StepGraphNode[]; edges: StepGraphEdge[] };

/** Applies Show (true) or Hide (false) on groups by key, in order, as a reader would. */
function chooseGroups(graph: Graph, choices: Array<[string, boolean]>, selectedStepExecutionID = ''): GroupOpenIntents {
  let openIntents = noGroupOpenIntents;
  for (const [key, isOpen] of choices) {
    const { rounds } = foldStepGraph(graph.nodes, graph.edges, { openIntents, selectedStepExecutionID });
    const group = rounds.find((round) => round.key === key);
    if (!group) throw new Error(`no group ${key}`);
    openIntents = withGroupOpenChoice(openIntents, group, isOpen);
  }
  return openIntents;
}

function foldWithChoices(graph: Graph, choices: Array<[string, boolean]>, selectedStepExecutionID = '') {
  const openIntents = chooseGroups(graph, choices, selectedStepExecutionID);
  return foldStepGraph(graph.nodes, graph.edges, { openIntents, selectedStepExecutionID });
}

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

type ChainStep = [string, StepGraphNode['status']?];

/** A chain of Steps named by type; each moves to the next one. */
function chain(stepTypes: ChainStep[]): { nodes: StepGraphNode[]; edges: StepGraphEdge[] } {
  const nodes: StepGraphNode[] = [startNode];
  const edges: StepGraphEdge[] = [];
  const counts = new Map<string, number>();
  let previous = '__start__';
  stepTypes.forEach(([stepType, status], index) => {
    const count = (counts.get(stepType) ?? 0) + 1;
    counts.set(stepType, count);
    const id = `${stepType}-${count}`;
    nodes.push(stepNode(id, stepType, index + 2, status ?? 'Completed', previous));
    edges.push({ id: `${previous}->${id}`, source: previous, target: id });
    previous = id;
  });
  return { nodes, edges };
}

function repeat(count: number, stepTypes: ChainStep[]): ChainStep[] {
  return Array.from({ length: count }, () => stepTypes).flat();
}

function shown(items: FoldedGraphItem[]): string[] {
  return items.map((item) => {
    if (item.display === 'group') return `[${item.round.title}]`;
    if (item.display === 'more') return `… ${item.hiddenCount} more ${item.unit}`;
    return item.repeatCount > 1 ? `${item.id} ×${item.repeatCount}` : item.id;
  });
}

const turn: ChainStep[] = [['AwaitUserMessage'], ['RunModel'], ['CallTool'], ['CallTool'], ['RunModel']];

// RunModel runs first and most often; AwaitUserMessage starts each turn.
const agentSession = chain([
  ['StartAgentSession'],
  ['RunModel'],
  ...repeat(3, turn),
  ['AwaitUserMessage', 'Waiting'],
]);

describe('Round anchor', () => {
  it('anchors on the repeated Step type with the fewest executions, so each agent turn is a Round', () => {
    const folded = foldStepGraph(agentSession.nodes, agentSession.edges, defaultOptions);
    expect(shown(folded.items)).toEqual(['__start__', 'StartAgentSession-1', 'RunModel-1', '[Rounds 1–3]', 'AwaitUserMessage-4']);
    const [range, latest] = folded.rounds;
    expect(range).toMatchObject({
      kind: 'range',
      roundCount: 3,
      stepCount: 15,
      sequence: ['AwaitUserMessage', 'RunModel', 'CallTool ×2', 'RunModel'],
      isOpen: false,
    });
    expect(latest).toMatchObject({ title: 'Round 4', isLatest: true, isLive: true, isOpen: true, canHide: false });
    expect(folded.edges.map((edge) => `${edge.source}->${edge.target}`)).toEqual([
      '__start__->StartAgentSession-1',
      'StartAgentSession-1->RunModel-1',
      `RunModel-1->${roundGroupNodeID('rounds:AwaitUserMessage-1')}`,
      `${roundGroupNodeID('rounds:AwaitUserMessage-1')}->AwaitUserMessage-4`,
    ]);
  });

  it('breaks a tie of execution counts toward the earliest Step type', () => {
    const graph = chain(repeat(2, [['A'], ['B']]));
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    expect(folded.rounds.map((round) => round.title)).toEqual(['Round 1', 'Round 2']);
    expect(folded.rounds[0].sequence).toEqual(['A', 'B']);
  });

  it('folds nothing when no Step type repeats', () => {
    const graph = chain([['A'], ['B'], ['C', 'Waiting']]);
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    expect(folded.rounds).toEqual([]);
    expect(shown(folded.items)).toEqual(['__start__', 'A-1', 'B-1', 'C-1']);
    expect(folded.edges).toEqual(graph.edges);
  });

  it('orders Steps by their first recorded event', () => {
    const later = stepNode('B-1', 'B', 9);
    const earlier = stepNode('A-1', 'A', 3);
    const planned: StepGraphNode = { id: '__planned:9:0', label: 'C', kind: 'step', status: 'Pending', stepType: 'C', isPlanned: true };
    expect(stepsInExecutionOrder([startNode, planned, later, earlier]).map((node) => node.id)).toEqual(['A-1', 'B-1', '__planned:9:0']);
  });
});

describe('Round ranges', () => {
  it('folds a Checkpoint-every-10 loop into one range of identical Rounds', () => {
    const graph = chain([
      ['SyncBatch'],
      ...repeat(30, [['Checkpoint'], ...repeat(9, [['SyncBatch']])]),
      ['Checkpoint'],
    ]);
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    expect(shown(folded.items)).toEqual(['__start__', 'SyncBatch-1', '[Rounds 1–30]', 'Checkpoint-31']);
    expect(folded.rounds[0]).toMatchObject({ stepCount: 300, sequence: ['Checkpoint', 'SyncBatch ×9'], roundRangeLabel: '' });
  });

  it('titles a loop of one Step type by the Step type and count', () => {
    const graph = chain([...repeat(900, [['SyncBatch']]), ['SyncBatch', 'Waiting']]);
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    expect(shown(folded.items)).toEqual(['__start__', '[SyncBatch ×900]', 'SyncBatch-901']);
    expect(folded.rounds[0]).toMatchObject({ roundRangeLabel: 'Rounds 1–900', sequence: [], hasSingleStepRounds: true });
  });

  it('keeps a failed Round open and splits the range around it', () => {
    const graph = chain([
      ...repeat(4, [['Poll'], ['Check']]),
      ['Poll'], ['Check', 'Failed'],
      ...repeat(3, [['Poll'], ['Check']]),
      ['Poll'], ['Check', 'Waiting'],
    ]);
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    expect(folded.rounds.map((round) => [round.title, round.isOpen, round.canHide])).toEqual([
      ['Rounds 1–4', false, true],
      ['Round 5', true, true],
      ['Rounds 6–8', false, true],
      ['Round 9', true, false],
    ]);
    expect(shown(folded.items)).toEqual(['__start__', '[Rounds 1–4]', 'Poll-5', 'Check-5', '[Rounds 6–8]', 'Poll-9', 'Check-9']);
  });

  it('never folds a live Round and lets a hidden failed Round fold', () => {
    const graph = chain([
      ...repeat(2, [['Poll'], ['Check']]),
      ['Poll'], ['Check'], ['Approve', 'Waiting'],
      ['Poll'], ['Check', 'Failed'],
      ['Poll'], ['Check'],
    ]);
    const folded = foldWithChoices(graph, [['round:Poll-3', false], ['round:Poll-4', false]]);
    expect(folded.rounds.map((round) => [round.title, round.isLive, round.isOpen])).toEqual([
      ['Rounds 1–2', false, false],
      ['Round 3', true, true],
      ['Round 4', false, false],
      ['Round 5', false, true],
    ]);
  });

  it('shows the Rounds of an opened range folded, with the first, selected and last ones', () => {
    const graph = chain(repeat(13, [['Poll'], ['Check']]));
    const folded = foldWithChoices(graph, [['rounds:Poll-1', true]], 'Check-7');
    expect(shown(folded.items)).toEqual([
      '__start__',
      '[Round 1]', '[Round 2]', '[Round 3]',
      '… 3 more Rounds',
      '[Round 7]',
      '… 3 more Rounds',
      '[Round 11]', '[Round 12]',
      'Poll-13', 'Check-13',
    ]);
    expect(folded.groupKeysByStepExecutionID.get('Check-7')).toEqual(['rounds:Poll-1', 'round:Poll-7']);
  });

  it('shows the Steps of an opened one-Step-type range with "… N more" between them', () => {
    const graph = chain([...repeat(20, [['SyncBatch']]), ['SyncBatch', 'Waiting']]);
    const folded = foldWithChoices(graph, [['rounds:SyncBatch-1', true]]);
    expect(shown(folded.items)).toEqual([
      '__start__', 'SyncBatch-1', 'SyncBatch-2', 'SyncBatch-3', '… 15 more Steps', 'SyncBatch-19', 'SyncBatch-20', 'SyncBatch-21',
    ]);
  });

  it('shows consecutive executions of one Step type inside an open Round as one ×N node', () => {
    const graph = chain([
      ['Plan'], ['CallTool'], ['CallTool'], ['Reply'],
      ['Plan'], ['Reply'],
    ]);
    const folded = foldWithChoices(graph, [['round:Plan-1', true]], 'CallTool-1');
    expect(shown(folded.items)).toEqual(['__start__', 'Plan-1', 'CallTool-1 ×2', 'Reply-1', 'Plan-2', 'Reply-2']);
    expect(folded.rounds[0].sequence).toEqual(['Plan', 'CallTool ×2', 'Reply']);
  });

  it('folds repeated Steps before the first Round into one ×N node, except a live one', () => {
    const graph = chain([
      ...repeat(9, [['SyncBatch']]),
      ...repeat(3, [['Checkpoint'], ...repeat(9, [['SyncBatch']])]),
      ['Checkpoint'],
    ]);
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    expect(shown(folded.items)).toEqual(['__start__', 'SyncBatch-9 ×9', '[Rounds 1–3]', 'Checkpoint-4']);

    const live = chain([['Sync'], ['Sync', 'Waiting'], ['Sync'], ['Check'], ['Sync'], ['Check']]);
    const liveFolded = foldStepGraph(live.nodes, live.edges, defaultOptions);
    expect(shown(liveFolded.items).slice(0, 4)).toEqual(['__start__', 'Sync-1', 'Sync-2', 'Sync-3']);
  });

  it('hides SubFlow nodes of a Step inside a folded group', () => {
    const graph = chain([...repeat(3, [['Poll'], ['Check']])]);
    const subFlow: StepGraphNode = { id: '__subflow:Check-1:0', label: 'child', kind: 'subflow', status: 'Completed', parentStepId: 'Check-1' };
    const folded = foldStepGraph(
      [...graph.nodes, subFlow],
      [...graph.edges, { id: 'Check-1->__subflow:Check-1:0', source: 'Check-1', target: subFlow.id }],
      defaultOptions,
    );
    expect(folded.items.some((item) => item.id === subFlow.id)).toBe(false);
    expect(folded.edges.some((edge) => edge.target === subFlow.id)).toBe(false);
  });
});

describe('Folding as events arrive', () => {
  it('keeps an opened Round and the selected Step in view when the Round joins a range', () => {
    const before = chain([['A'], ['B'], ['B'], ['A'], ['B'], ['B']]);
    const openIntents = withStepExecutionOpened(chooseGroups(before, [['round:A-1', true]]), 'B-1');
    const after = chain([['A'], ['B'], ['B'], ['A'], ['B'], ['B'], ['A']]);
    const folded = foldStepGraph(after.nodes, after.edges, { openIntents, selectedStepExecutionID: 'B-1' });
    expect(folded.rounds.map((round) => [round.key, round.isOpen])).toEqual([
      ['rounds:A-1', true],
      ['round:A-1', true],
      ['round:A-2', false],
      ['round:A-3', true],
    ]);
    expect(shown(folded.items)).toEqual(['__start__', 'A-1', 'B-1 ×2', '[Round 2]', 'A-3']);
  });

  it('keeps the Round anchor while it still repeats', () => {
    const before = chain([['A'], ['B'], ['A'], ['B'], ['A']]);
    expect(foldStepGraph(before.nodes, before.edges, defaultOptions).anchorStepType).toBe('B');
    const after = chain([['A'], ['B'], ['A'], ['B'], ['A'], ['B']]);
    expect(foldStepGraph(after.nodes, after.edges, defaultOptions).anchorStepType).toBe('A');
    const kept = foldStepGraph(after.nodes, after.edges, { ...defaultOptions, preferredAnchorStepType: 'B' });
    expect(kept.anchorStepType).toBe('B');
    expect(kept.rounds.map((round) => round.title)).toEqual(['Rounds 1–2', 'Round 3']);
  });

  it('never anchors Rounds on planned Steps and still shows them', () => {
    const graph = chain([['A'], ['B'], ['B'], ['A'], ['B'], ['A'], ['B']]);
    const planned = [0, 1].map((index): StepGraphNode => ({
      id: `__planned:99:${index}`, label: 'C', kind: 'step', status: 'Pending', stepType: 'C', isPlanned: true, fromStepExecutionId: 'B-4',
    }));
    const folded = foldStepGraph([...graph.nodes, ...planned], graph.edges, defaultOptions);
    expect(folded.anchorStepType).toBe('A');
    expect(folded.items.map((item) => item.id)).toEqual(expect.arrayContaining(['__planned:99:0', '__planned:99:1']));
  });

  it('folds a range and its Rounds again with Hide', () => {
    const graph = chain([...repeat(4, [['Poll'], ['Check']])]);
    const folded = foldWithChoices(graph, [['rounds:Poll-1', true], ['round:Poll-2', true], ['rounds:Poll-1', false], ['rounds:Poll-1', true]]);
    expect(folded.rounds.filter((round) => round.parentKey).map((round) => round.isOpen)).toEqual([false, false, false]);
  });
});

describe('Round frames', () => {
  it('surrounds each opened group, leaving room for the frames inside it', () => {
    const items: FoldedGraphItem[] = [
      { display: 'node', id: 'A', node: startNode, repeatCount: 1, frameKeys: ['rounds:1'] },
      { display: 'node', id: 'B', node: startNode, repeatCount: 1, frameKeys: ['rounds:1', 'round:2'] },
      { display: 'node', id: 'C', node: startNode, repeatCount: 1, frameKeys: [] },
    ];
    const boxes = new Map([
      ['A', { x: 100, y: 100, width: 300, height: 128 }],
      ['B', { x: 500, y: 100, width: 300, height: 128 }],
      ['C', { x: 0, y: 0, width: 300, height: 128 }],
    ]);
    expect(roundFrameBoxes(items, boxes)).toEqual([
      { roundKey: 'rounds:1', x: 86, y: 20, width: 742, height: 236 },
      { roundKey: 'round:2', x: 486, y: 60, width: 328, height: 182 },
    ]);
  });
});
