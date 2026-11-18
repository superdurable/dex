// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { buildStepGraph } from '@/lib/graph';
import type { FlowHistoryEvent, StepGraphEdge, StepGraphNode } from '@/lib/types';
import {
  foldStepGraph,
  noGroupOpenIntents,
  roundFrameBoxes,
  roundGroupNodeID,
  stepsInExecutionOrder,
  withGroupOpenChoice,
  withStepExecutionOpened,
  withStepRunOpenChoice,
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
  lineage?: { fromEventId: number; nextStepTypes: string[] },
): StepGraphNode {
  const eventTime = new Date(Date.parse('2026-10-24T17:00:00Z') + eventId * 1000).toISOString();
  const context = lineage ? { context: { fromEventId: String(lineage.fromEventId) } } : {};
  const output = lineage ? { output: { stepDecision: { nextSteps: lineage.nextStepTypes.map((next) => ({ stepType: next })) } } } : {};
  return {
    id,
    label: stepType,
    kind: 'step',
    status,
    stepType,
    fromStepExecutionId,
    execute: status === 'Completed' || status === 'Failed'
      ? { eventId, eventTime, type: status === 'Failed' ? 'StepExecuteFailed' : 'StepExecuteCompleted', payload: { ...context, ...output } }
      : undefined,
    waitFor: status === 'Waiting'
      ? { eventId, eventTime, type: 'StepWaitForCompleted', payload: context }
      : undefined,
  };
}

const startNode: StepGraphNode = { id: '__start__', label: 'Flow start', kind: 'source', status: 'Source' };

type ChainStep = [string, StepGraphNode['status']?];

/** A chain of Steps named by type; each one's decision moves to the next one. */
function chain(stepTypes: ChainStep[]): { nodes: StepGraphNode[]; edges: StepGraphEdge[] } {
  const nodes: StepGraphNode[] = [startNode];
  const edges: StepGraphEdge[] = [];
  const counts = new Map<string, number>();
  let previous = '__start__';
  stepTypes.forEach(([stepType, status], index) => {
    const count = (counts.get(stepType) ?? 0) + 1;
    counts.set(stepType, count);
    const id = `${stepType}-${count}`;
    const nextStepType = stepTypes[index + 1]?.[0];
    const lineage = { fromEventId: index === 0 ? 1 : index + 1, nextStepTypes: nextStepType ? [nextStepType] : [] };
    nodes.push(stepNode(id, stepType, index + 2, status ?? 'Completed', previous, lineage));
    edges.push({ id: `${previous}->${id}`, source: previous, target: id });
    previous = id;
  });
  return { nodes, edges };
}

function repeat<Step extends ChainStep | DecidedStep>(count: number, stepTypes: Step[]): Step[] {
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

// Step types of a real long agent run in execution order, "Step" left off; "!" marks a failed tool call.
const longAgentRunSequence = `
  ReceiveRequest AgentDecision CheckIncidents AgentDecision GetSupportHistory AgentDecision CheckIdentity AgentDecision
  GetPayment AgentDecision GetSubscription AgentDecision GetPayment AgentDecision GetUsage AgentDecision CheckIncidents
  AgentDecision CheckIncidents AgentDecision CheckIncidents AgentDecision GetUsage AgentDecision CheckIncidents
  AgentDecision GetUsage AgentDecision GetPayment AgentDecision CheckIncidents AgentDecision GetSubscription AgentDecision
  CheckIdentity! AgentDecision CheckIncidents AgentDecision GetUsage AgentDecision GetSupportHistory AgentDecision
  GetPayment AgentDecision CheckIdentity AgentDecision GetUsage AgentDecision GetPayment AgentDecision CheckIdentity
  AgentDecision CheckIncidents AgentDecision GetPayment AgentDecision GetSupportHistory AgentDecision GetUsage
  AgentDecision GetSupportHistory AgentDecision CheckIdentity AgentDecision GetUsage AgentDecision GetUsage AgentDecision
  GetPayment AgentDecision GetSupportHistory AgentDecision CheckIdentity AgentDecision CheckIdentity AgentDecision
  GetPayment AgentDecision GetSupportHistory AgentDecision GetSubscription AgentDecision GetSupportHistory AgentDecision
  GetSubscription AgentDecision GetSupportHistory AgentDecision GetPayment! AgentDecision GetUsage AgentDecision
  CheckIncidents AgentDecision GetUsage AgentDecision GetPayment AgentDecision GetPayment AgentDecision GetSubscription
  AgentDecision GetSupportHistory AgentDecision GetSubscription AgentDecision CheckIdentity AgentDecision GetUsage
  AgentDecision CheckIdentity AgentDecision GetUsage AgentDecision GetUsage AgentDecision GetUsage AgentDecision GetUsage
  AgentDecision GetSupportHistory AgentDecision CheckIncidents AgentDecision GetUsage AgentDecision GetPayment
  AgentDecision GetSupportHistory AgentDecision GetUsage AgentDecision CheckIdentity AgentDecision GetSubscription
  AgentDecision GetSupportHistory AgentDecision GetPayment AgentDecision GetSupportHistory AgentDecision CheckIdentity!
  AgentDecision GetSupportHistory AgentDecision CheckIdentity AgentDecision GetPayment AgentDecision CheckIdentity
  AgentDecision GetSubscription AgentDecision GetSupportHistory AgentDecision CheckIncidents AgentDecision GetUsage
  AgentDecision GetSupportHistory AgentDecision CheckIncidents AgentDecision CheckIdentity AgentDecision GetSupportHistory
  AgentDecision GetSubscription AgentDecision GetSupportHistory AgentDecision GetPayment AgentDecision GetUsage
  AgentDecision GetUsage AgentDecision CheckIncidents AgentDecision GetSubscription AgentDecision CheckIncidents
  AgentDecision GetUsage AgentDecision GetSubscription AgentDecision GetUsage AgentDecision GetPayment AgentDecision
  CheckIncidents AgentDecision GetUsage AgentDecision CheckIncidents! AgentDecision GetSupportHistory AgentDecision
  GetSupportHistory AgentDecision GetUsage AgentDecision GetSupportHistory AgentDecision GetUsage AgentDecision GetUsage
  AgentDecision GetUsage AgentDecision CheckIncidents AgentDecision GetSupportHistory AgentDecision GetPayment
  AgentDecision GetSubscription AgentDecision CheckIncidents AgentDecision GetPayment AgentDecision CheckIdentity
  AgentDecision GetSupportHistory AgentDecision CheckIdentity AgentDecision GetSupportHistory! AgentDecision GetPayment
  AgentDecision GetSupportHistory AgentDecision GetPayment AgentDecision GetSupportHistory AgentDecision GetUsage
  AgentDecision CheckIdentity AgentDecision GetPayment AgentDecision CheckIdentity AgentDecision GetSubscription
  AgentDecision CheckIdentity AgentDecision CheckIncidents AgentDecision Guardrail AuditIntent IssueRefund
  PrepareCustomerMessage SendCustomerMessage CloseCase`;

const longAgentRunSteps: ChainStep[] = longAgentRunSequence.trim().split(/\s+/).map((name) => (
  name.endsWith('!') ? [`${name.slice(0, -1)}Step`, 'Failed'] : [`${name}Step`]
));
const longAgentRun = chain(longAgentRunSteps);

describe('Round anchor', () => {
  it('anchors on the Step type whose Rounds are most uniform, so each agent turn is a Round', () => {
    const folded = foldStepGraph(agentSession.nodes, agentSession.edges, defaultOptions);
    expect(folded.anchorStepType).toBe('AwaitUserMessage');
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

  it('anchors a long agent run on its decision Step, though some tools run fewer times', () => {
    const executionCounts = new Map<string, number>();
    for (const [stepType] of longAgentRunSteps) executionCounts.set(stepType, (executionCounts.get(stepType) ?? 0) + 1);
    expect(Object.fromEntries(executionCounts)).toMatchObject({
      AgentDecisionStep: 127, GetSubscriptionStep: 13, CheckIdentityStep: 19, GetPaymentStep: 21,
      GetUsageStep: 28, GetSupportHistoryStep: 26, CheckIncidentsStep: 19,
    });
    expect(longAgentRunSteps.filter(([, status]) => status === 'Failed')).toHaveLength(5);

    const folded = foldStepGraph(longAgentRun.nodes, longAgentRun.edges, defaultOptions);
    expect(folded.anchorStepType).toBe('AgentDecisionStep');
    const finished = folded.rounds.filter((round) => !round.parentKey && !round.isLatest);
    expect(finished.reduce((total, round) => total + round.roundCount, 0)).toBe(126);
    for (const round of finished) {
      expect(round.stepCount).toBe(round.roundCount * 2);
      expect(round.sequence).toEqual(['AgentDecisionStep', expect.stringMatching(/^(?!AgentDecisionStep)\w+Step$/)]);
    }
    expect(finished.filter((round) => round.failedCount > 0).map((round) => [round.roundCount, round.isOpen]))
      .toEqual(Array.from({ length: 5 }, () => [1, true]));
    expect(folded.rounds.at(-1)).toMatchObject({ isLatest: true, stepCount: 7, sequence: expect.arrayContaining(['AgentDecisionStep']) });
  });

  it('prefers a uniform anchor over a less frequent one with uneven Rounds', () => {
    // Retry's finished Rounds are 2 and 5 Steps long; Fetch's are mostly 1.
    const graph = chain([
      ['Fetch'], ['Retry'], ['Fetch'], ['Retry'],
      ...repeat(4, [['Fetch']]), ['Retry'],
      ...repeat(5, [['Fetch']]),
    ]);
    expect(foldStepGraph(graph.nodes, graph.edges, defaultOptions).anchorStepType).toBe('Fetch');
  });

  it('breaks a tie of uniformity toward fewer executions, then the earliest Step type', () => {
    const cron = buildStepGraph(cronScheduleHistory(5, () => 'wait-first'));
    expect(foldStepGraph(cron.nodes, cron.edges, defaultOptions).anchorStepType).toBe('runCronSchedule');
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

type CronSiblingOrder = 'run-first' | 'wait-first' | 'run-after-next-decision';

/** Events of one Step execution, shaped as the history API sends them. */
function cronStepEvent(
  eventId: number,
  type: FlowHistoryEvent['type'],
  stepExecutionId: string,
  fromStepExecutionId: string,
  fromEventId: number,
  nextStepTypes?: string[],
): FlowHistoryEvent {
  const stepType = stepExecutionId.replace(/-\d+$/, '');
  return {
    eventId,
    eventTime: new Date(Date.parse('2026-11-17T00:00:00Z') + eventId * 1000).toISOString(),
    type,
    payload: {
      context: { stepExecutionId, stepType, fromStepExecutionId, fromEventId: String(fromEventId) },
      ...(nextStepTypes ? { output: { stepDecision: { nextSteps: nextStepTypes.map((next) => ({ stepType: next })) } } } : {}),
    },
  };
}

/** A Cron schedule run: each wait decides GoToMany(run, next wait), whose first events race. */
function cronScheduleHistory(roundCount: number, siblingOrderOf: (round: number) => CronSiblingOrder): FlowHistoryEvent[] {
  const events: FlowHistoryEvent[] = [{ eventId: 1, eventTime: null, type: 'FlowStartedOrContinued', payload: {} }];
  const add = (...args: Parameters<typeof cronStepEvent> extends [number, ...infer Rest] ? Rest : never) => {
    const event = cronStepEvent(events.length + 1, ...args);
    events.push(event);
    return event.eventId;
  };
  const startDecision = add('StepExecuteCompleted', 'startCronSchedule-1', '__start__', 1, ['waitForCronSchedule']);
  add('StepWaitForCompleted', 'waitForCronSchedule-1', 'startCronSchedule-1', startDecision);
  let waitDecision = add('StepExecuteCompleted', 'waitForCronSchedule-1', 'startCronSchedule-1', startDecision, [
    'runCronSchedule', 'waitForCronSchedule',
  ]);
  for (let round = 1; round <= roundCount; round += 1) {
    const deciding = `waitForCronSchedule-${round}`;
    const nextWait = `waitForCronSchedule-${round + 1}`;
    const run = `runCronSchedule-${round}`;
    const isLast = round === roundCount;
    const decideNextWait = () => (isLast ? 0 : add('StepExecuteCompleted', nextWait, deciding, waitDecision, [
      'runCronSchedule', 'waitForCronSchedule',
    ]));
    const runOnce = () => add('StepExecuteCompleted', run, deciding, waitDecision, []);
    const order = siblingOrderOf(round);
    let nextDecision = 0;
    if (order === 'run-first') runOnce();
    add('StepWaitForCompleted', nextWait, deciding, waitDecision);
    if (order === 'wait-first') runOnce();
    nextDecision = decideNextWait();
    if (order === 'run-after-next-decision') runOnce();
    waitDecision = nextDecision;
  }
  return events;
}

const cronSiblingOrders: CronSiblingOrder[] = ['run-first', 'wait-first', 'wait-first', 'run-after-next-decision', 'run-first'];

describe('Spawn order', () => {
  it('folds a Cron schedule into one range however its spawned Steps race', () => {
    const graph = buildStepGraph(cronScheduleHistory(40, (round) => cronSiblingOrders[round % cronSiblingOrders.length]));
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    expect(folded.anchorStepType).toBe('runCronSchedule');
    expect(folded.rounds.map((round) => round.title)).toEqual(['Rounds 1–39', 'Round 40']);
    expect(folded.rounds[0]).toMatchObject({ stepCount: 78, sequence: ['runCronSchedule', 'waitForCronSchedule'] });
    expect(folded.rounds[1]).toMatchObject({ isLatest: true, isOpen: true, stepCount: 2 });
    expect(folded.items.filter((item) => item.display === 'group')).toHaveLength(1);
    expect(shown(folded.items)).toEqual([
      '__start__', 'startCronSchedule-1', 'waitForCronSchedule-1', '[Rounds 1–39]', 'runCronSchedule-40', 'waitForCronSchedule-41',
    ]);
  });

  it('keeps a delayed sibling in its decision when the deciding event is outside the loaded window', () => {
    const history = cronScheduleHistory(2, (round) => (round === 1 ? 'run-after-next-decision' : 'wait-first'));
    const firstDecision = history.find((event) => event.type === 'StepExecuteCompleted'
      && (event.payload.context as Record<string, unknown>).stepExecutionId === 'waitForCronSchedule-1');
    const window = history.filter((event) => event.eventId > (firstDecision?.eventId ?? 0));
    const graph = buildStepGraph(window, [], '', { hasEarlierEvents: true });
    expect(stepsInExecutionOrder(graph.nodes, window).map((node) => node.id)).toEqual([
      'waitForCronSchedule-2', 'runCronSchedule-1', 'runCronSchedule-2', 'waitForCronSchedule-3',
    ]);
    const folded = foldStepGraph(graph.nodes, graph.edges, {
      ...defaultOptions, preferredAnchorStepType: 'runCronSchedule', historyEvents: window,
    });
    expect(folded.rounds.map((round) => [round.key, round.isLatest])).toEqual([
      ['round:runCronSchedule-1', false],
      ['round:runCronSchedule-2', true],
    ]);
  });

  it('gives each execution of a repeated Step type its own place in the decision', () => {
    const events = [
      cronStepEvent(2, 'StepExecuteCompleted', 'Plan-1', '__start__', 1, ['A', 'B', 'A']),
      cronStepEvent(3, 'StepExecuteCompleted', 'A-2', 'Plan-1', 2, []),
      cronStepEvent(4, 'StepExecuteCompleted', 'A-1', 'Plan-1', 2, []),
      cronStepEvent(5, 'StepExecuteCompleted', 'B-1', 'Plan-1', 2, []),
    ];
    const graph = buildStepGraph(events);
    expect(stepsInExecutionOrder(graph.nodes, events).map((node) => node.id)).toEqual(['Plan-1', 'A-1', 'B-1', 'A-2']);
  });

  it('orders the Steps an RPC started by the RPC decision', () => {
    const rpc: FlowHistoryEvent = {
      eventId: 2,
      eventTime: null,
      type: 'RpcExecutionCompleted',
      payload: { rpcName: 'startJobs', stepDecision: { nextSteps: [{ stepType: 'Fetch' }, { stepType: 'Notify' }] } },
    };
    const events = [
      rpc,
      cronStepEvent(3, 'StepExecuteCompleted', 'Notify-1', '__rpc/startJobs', 2, []),
      cronStepEvent(4, 'StepExecuteCompleted', 'Fetch-1', '__rpc/startJobs', 2, []),
    ];
    const executed = buildStepGraph(events).nodes.filter((node) => !node.isPlanned);
    expect(stepsInExecutionOrder(executed, events).map((node) => node.id)).toEqual(['Fetch-1', 'Notify-1']);
  });

  it('orders Steps without events in one total order, whatever order they arrive in', () => {
    const eventless = ['Task2-1', '__planned:10:0', 'Task02-1', '__planned:9:0'].map((id): StepGraphNode => ({
      id, label: 'Task', kind: 'step', status: 'Pending', stepType: 'Task', isPlanned: id.startsWith('__planned'),
    }));
    const forward = stepsInExecutionOrder(eventless).map((node) => node.id);
    expect(stepsInExecutionOrder([...eventless].reverse()).map((node) => node.id)).toEqual(forward);
    expect(new Set(forward).size).toBe(4);
    expect(forward.indexOf('__planned:9:0')).toBeLessThan(forward.indexOf('__planned:10:0'));
  });

  it('orders by first event when the deciding event is outside the loaded window', () => {
    const history = cronScheduleHistory(3, () => 'wait-first');
    // Drop the first wait's decision, so the Steps it started fall back to their first events.
    const firstDecision = history.find((event) => event.type === 'StepExecuteCompleted'
      && (event.payload.context as Record<string, unknown>).stepExecutionId === 'waitForCronSchedule-1');
    const window = history.filter((event) => event.eventId > (firstDecision?.eventId ?? 0));
    const graph = buildStepGraph(window, [], '', { hasEarlierEvents: true });
    expect(stepsInExecutionOrder(graph.nodes).map((node) => node.id)).toEqual([
      'waitForCronSchedule-2', 'runCronSchedule-1',
      'runCronSchedule-2', 'waitForCronSchedule-3',
      'runCronSchedule-3', 'waitForCronSchedule-4',
    ]);
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
    expect(folded.anchorStepType).toBe('Checkpoint');
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

/** The anchor after each Step arrives, each fold preferring the one before, as Live updates do. */
function growingAnchors(steps: ChainStep[]): string[] {
  let preferredAnchorStepType = '';
  return steps.map((_, index) => {
    const graph = chain(steps.slice(0, index + 1));
    const { anchorStepType } = foldStepGraph(graph.nodes, graph.edges, { ...defaultOptions, preferredAnchorStepType });
    if (anchorStepType) preferredAnchorStepType = anchorStepType;
    return anchorStepType;
  });
}

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
    // Selecting B-1 also opened its ×2 run.
    expect(shown(folded.items)).toEqual(['__start__', 'A-1', 'B-1', 'B-2', '[Round 2]', 'A-3']);
    expect(folded.stepRuns.map((stepRun) => [stepRun.key, stepRun.isOpen])).toEqual([['run:B-1', true]]);
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

  it('moves a kept anchor to a more uniform one as a Checkpoint loop grows, then keeps it', () => {
    const steps: ChainStep[] = [['SyncBatch'], ...repeat(20, [['Checkpoint'], ...repeat(19, [['SyncBatch']])]), ['Checkpoint']];
    const anchors = growingAnchors(steps);
    // SyncBatch repeats first; the second Checkpoint finishes a 20-Step Round, more uniform than 18 of 19.
    expect(anchors.slice(0, 3)).toEqual(['', '', 'SyncBatch']);
    const switchedAt = anchors.indexOf('Checkpoint');
    expect(steps.slice(0, switchedAt + 1).filter(([stepType]) => stepType === 'Checkpoint')).toHaveLength(2);
    expect(anchors.slice(switchedAt).every((anchor) => anchor === 'Checkpoint')).toBe(true);
  });

  it('keeps the agent decision anchor at every point of a long agent run', () => {
    const anchors = growingAnchors(longAgentRunSteps);
    const firstAnchored = anchors.findIndex(Boolean);
    expect(anchors.slice(firstAnchored).every((anchor) => anchor === 'AgentDecisionStep')).toBe(true);
  });

  it('keeps a preferred anchor when another Step type is as uniform', () => {
    const graph = chain(repeat(4, [['A'], ['B']]));
    expect(foldStepGraph(graph.nodes, graph.edges, defaultOptions).anchorStepType).toBe('A');
    const kept = foldStepGraph(graph.nodes, graph.edges, { ...defaultOptions, preferredAnchorStepType: 'B' });
    expect(kept.anchorStepType).toBe('B');
    // B's finished Rounds are 1, 1, 2, 2 Steps long; A's are 4, 2, 2.
    const lessUniform = chain([['A'], ['B'], ['B'], ['B'], ['A'], ['B'], ['A'], ['B'], ['A']]);
    const replaced = foldStepGraph(lessUniform.nodes, lessUniform.edges, { ...defaultOptions, preferredAnchorStepType: 'B' });
    expect(replaced.anchorStepType).toBe('A');
  });

  it('never anchors Rounds on planned Steps and still shows them', () => {
    const graph = chain([['A'], ['B'], ['B'], ['A'], ['B'], ['A'], ['B']]);
    const planned = [0, 1].map((index): StepGraphNode => ({
      id: `__planned:99:${index}`, label: 'C', kind: 'step', status: 'Pending', stepType: 'C', isPlanned: true, fromStepExecutionId: 'B-4',
    }));
    const folded = foldStepGraph([...graph.nodes, ...planned], graph.edges, defaultOptions);
    // B's finished Rounds are 1, 2, 2 Steps long; A's are 3, 2.
    expect(folded.anchorStepType).toBe('B');
    expect(folded.items.map((item) => item.id)).toEqual(expect.arrayContaining(['__planned:99:0', '__planned:99:1']));
  });

  it('folds a range and its Rounds again with Hide', () => {
    const graph = chain([...repeat(4, [['Poll'], ['Check']])]);
    const folded = foldWithChoices(graph, [['rounds:Poll-1', true], ['round:Poll-2', true], ['rounds:Poll-1', false], ['rounds:Poll-1', true]]);
    expect(folded.rounds.filter((round) => round.parentKey).map((round) => round.isOpen)).toEqual([false, false, false]);
  });
});

const gracefulComplete = { closeDecision: { closeDecisionType: 2 } };
const goTo = (stepType: string, cancelStepTypes?: string[]) => ({ nextSteps: [{ stepType }], ...(cancelStepTypes ? { cancelStepTypes } : {}) });
type DecidedStep = [stepType: string, stepDecision: unknown | 'failed'];

/** A chain of settled Step executions from history, each returning its Step decision or failing. */
function decidedChainGraph(steps: DecidedStep[]): Graph {
  const events: FlowHistoryEvent[] = [];
  const counts = new Map<string, number>();
  let fromStepExecutionId = '__start__';
  let fromEventId = 1;
  steps.forEach(([stepType, stepDecision], index) => {
    const count = (counts.get(stepType) ?? 0) + 1;
    counts.set(stepType, count);
    const stepExecutionId = `${stepType}-${count}`;
    const eventId = index + 2;
    const isFailed = stepDecision === 'failed';
    events.push({
      eventId,
      eventTime: new Date(Date.parse('2026-11-17T00:00:00Z') + eventId * 1000).toISOString(),
      type: isFailed ? 'StepExecuteFailed' : 'StepExecuteCompleted',
      payload: {
        context: { stepExecutionId, stepType, fromStepExecutionId, fromEventId: String(fromEventId) },
        ...(isFailed ? {} : { output: { stepDecision } }),
      },
    });
    fromStepExecutionId = stepExecutionId;
    fromEventId = eventId;
  });
  return buildStepGraph(events);
}

/** A Checkpoint Round: Checkpoint, then nine SyncBatch executions, the last deciding `last`. */
function checkpointRound(last: unknown): DecidedStep[] {
  return [
    ['Checkpoint', goTo('SyncBatch')],
    ...Array.from({ length: 8 }, (): DecidedStep => ['SyncBatch', goTo('SyncBatch')]),
    ['SyncBatch', last],
  ];
}

describe('×N runs by Step decision', () => {
  it('folds only executions that return the same Step decision', () => {
    const graph = decidedChainGraph([...repeat(3, checkpointRound(goTo('Checkpoint'))), ...checkpointRound(gracefulComplete)]);
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    // Uniform Rounds still merge into one range.
    expect(folded.rounds.map((round) => round.title)).toEqual(['Rounds 1–3', 'Round 4']);
    expect(folded.rounds[0]).toMatchObject({ sequence: ['Checkpoint', 'SyncBatch ×9'], stepCount: 30 });
    expect(folded.rounds[1].sequence).toEqual(['Checkpoint', 'SyncBatch ×8', 'SyncBatch']);
    expect(shown(folded.items)).toEqual(['__start__', '[Rounds 1–3]', 'Checkpoint-4', 'SyncBatch-35 ×8', 'SyncBatch-36']);
    expect(folded.stepRuns).toEqual([expect.objectContaining({
      key: 'run:SyncBatch-28', stepType: 'SyncBatch', stepCount: 8, decisionLabel: 'GoTo', isOpen: false, parentKey: 'round:Checkpoint-4',
    })]);
  });

  it('keeps one ×9 node per Round when every execution decides GoTo', () => {
    const graph = decidedChainGraph([...repeat(2, checkpointRound(goTo('Checkpoint'))), ['Checkpoint', gracefulComplete]]);
    const folded = foldWithChoices(graph, [['rounds:Checkpoint-1', true], ['round:Checkpoint-1', true]]);
    expect(shown(folded.items)).toContain('SyncBatch-9 ×9');
  });

  it('counts a cancellation as part of the decision', () => {
    const graph = decidedChainGraph([
      ['SyncBatch', goTo('SyncBatch')],
      ['SyncBatch', goTo('SyncBatch')],
      ['SyncBatch', goTo('SyncBatch', ['Poll'])],
      ['SyncBatch', goTo('Checkpoint')],
      ['Checkpoint', goTo('Checkpoint')],
      ['Checkpoint', gracefulComplete],
    ]);
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    expect(shown(folded.items).slice(0, 4)).toEqual(['__start__', 'SyncBatch-2 ×2', 'SyncBatch-3', 'SyncBatch-4']);
  });

  it('never merges Rounds that decide differently', () => {
    const pollRound = (checkDecision: unknown): DecidedStep[] => [['Poll', goTo('Check')], ['Check', checkDecision]];
    const graph = decidedChainGraph([
      ...repeat(2, pollRound(goTo('Poll'))),
      ...pollRound(goTo('Poll', ['Retry'])),
      ...repeat(2, pollRound(goTo('Poll'))),
      ...pollRound(gracefulComplete),
    ]);
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    expect(folded.rounds.map((round) => round.title)).toEqual(['Rounds 1–2', 'Round 3', 'Rounds 4–5', 'Round 6']);
  });

  it('keeps a failed execution in the run of the executions around it', () => {
    const graph = decidedChainGraph([
      ['SyncBatch', goTo('SyncBatch')],
      ['SyncBatch', 'failed'],
      ['SyncBatch', goTo('Checkpoint')],
      ['Checkpoint', goTo('Checkpoint')],
      ['Checkpoint', gracefulComplete],
    ]);
    const folded = foldStepGraph(graph.nodes, graph.edges, defaultOptions);
    expect(shown(folded.items).slice(0, 2)).toEqual(['__start__', 'SyncBatch-2 ×3']);
    expect(folded.stepRuns[0]).toMatchObject({ failedCount: 1, decisionLabel: 'GoTo' });
  });
});

describe('Opened ×N runs', () => {
  const tenSyncBatches = decidedChainGraph([
    ...Array.from({ length: 6 }, (): DecidedStep => ['SyncBatch', goTo('SyncBatch')]),
    ['SyncBatch', 'failed'],
    ...Array.from({ length: 3 }, (): DecidedStep => ['SyncBatch', goTo('SyncBatch')]),
    ['Checkpoint', goTo('Checkpoint')],
    ['Checkpoint', gracefulComplete],
  ]);

  it('shows the first, failed, selected and last executions inside a frame, and folds again with Hide', () => {
    const fold = (openIntents: GroupOpenIntents) => foldStepGraph(tenSyncBatches.nodes, tenSyncBatches.edges, {
      openIntents, selectedStepExecutionID: 'SyncBatch-5',
    });
    const [stepRun] = fold(noGroupOpenIntents).stepRuns;
    expect(stepRun).toMatchObject({ key: 'run:SyncBatch-1', stepCount: 10, isOpen: false });
    const opened = fold(withStepRunOpenChoice(noGroupOpenIntents, stepRun, true));
    expect(shown(opened.items).slice(0, 11)).toEqual([
      '__start__',
      'SyncBatch-1', 'SyncBatch-2', 'SyncBatch-3',
      '… 1 more Steps',
      'SyncBatch-5',
      '… 1 more Steps',
      'SyncBatch-7',
      '… 1 more Steps',
      'SyncBatch-9', 'SyncBatch-10',
    ]);
    expect(opened.items.find((item) => item.id === 'SyncBatch-7')?.frameKeys).toEqual(['run:SyncBatch-1']);
    expect(opened.edges.map((edge) => `${edge.source}->${edge.target}`)).toContain('SyncBatch-3->__run-more:run:SyncBatch-1:SyncBatch-4');

    const hidden = fold(withStepRunOpenChoice(withStepRunOpenChoice(noGroupOpenIntents, stepRun, true), stepRun, false));
    expect(shown(hidden.items).slice(0, 2)).toEqual(['__start__', 'SyncBatch-5 ×10']);
  });

  it('stays open as new executions join the run', () => {
    const before = decidedChainGraph([
      ['SyncBatch', goTo('SyncBatch')], ['SyncBatch', goTo('SyncBatch')], ['SyncBatch', goTo('Checkpoint')],
      ['Checkpoint', goTo('Checkpoint')], ['Checkpoint', gracefulComplete],
    ]);
    const [stepRun] = foldStepGraph(before.nodes, before.edges, defaultOptions).stepRuns;
    const openIntents = withStepRunOpenChoice(noGroupOpenIntents, stepRun, true);
    const after = decidedChainGraph([
      ...Array.from({ length: 4 }, (): DecidedStep => ['SyncBatch', goTo('SyncBatch')]), ['SyncBatch', goTo('Checkpoint')],
      ['Checkpoint', goTo('Checkpoint')], ['Checkpoint', gracefulComplete],
    ]);
    const folded = foldStepGraph(after.nodes, after.edges, { openIntents, selectedStepExecutionID: '' });
    expect(folded.stepRuns[0]).toMatchObject({ key: 'run:SyncBatch-1', stepCount: 5, isOpen: true });
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
