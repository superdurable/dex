// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { formatDuration } from '@/lib/format';
import type { FlowHistoryEvent, StepGraphEdge, StepGraphNode } from '@/lib/types';

/** An opened single-Step group longer than this shows only its first, failed, selected and last executions. */
const maximumUnelidedGroupSize = 6;
const shownFirstExecutions = 3;
const shownLastExecutions = 2;
/** A folded Round lists at most this many Step sequence entries before an ellipsis. */
const maximumSequenceEntries = 4;

/** A Round, or consecutive single-Step Rounds of one Step type, shown as one unit. */
export interface StepRoundGroup {
  /** The first Round's starting Step execution ID; it stays stable while events arrive. */
  key: string;
  firstRoundNumber: number;
  lastRoundNumber: number;
  /** "Round N", or "SyncBatch ×900" for merged single-Step Rounds. */
  title: string;
  /** "Rounds 1–900" for merged single-Step Rounds; empty otherwise. */
  roundRangeLabel: string;
  /** The Step type of merged single-Step Rounds; empty otherwise. */
  repeatedStepType: string;
  stepExecutionIDs: string[];
  stepCount: number;
  completedCount: number;
  failedCount: number;
  /** Step types in execution order, consecutive repeats as "CallTool ×2"; empty for merged single-Step Rounds. */
  sequence: string[];
  duration: string;
  isLatest: boolean;
  /** Holds a waiting, running or pending Step, so it is never folded. */
  isLive: boolean;
  isOpen: boolean;
  /** Only Rounds that are neither the latest nor live can be hidden again. */
  canHide: boolean;
}

/** A node the folded graph draws: an original node, a folded Round, or the gap of an elided Round. */
export type FoldedGraphItem =
  | { display: 'node'; id: string; node: StepGraphNode; repeatCount: number; roundKey: string | null }
  | { display: 'group'; id: string; round: StepRoundGroup }
  | { display: 'more'; id: string; hiddenCount: number; roundKey: string };

export interface FoldedStepGraph {
  items: FoldedGraphItem[];
  edges: StepGraphEdge[];
  /** Every Round group in execution order; empty when no Step type repeats. */
  rounds: StepRoundGroup[];
  /** The Round group key of each Step execution inside a Round. */
  roundKeyByStepExecutionID: Map<string, string>;
}

export interface StepGraphFoldingOptions {
  /** Round groups a reader opened (true) or hid (false); others use the default open state. */
  openRounds: ReadonlyMap<string, boolean>;
  selectedStepExecutionID: string;
}

interface RoundDraft {
  number: number;
  steps: StepGraphNode[];
}

/** Folds repeated Rounds; each starts when the run returns to the earliest repeated Step type. */
export function foldStepGraph(
  nodes: StepGraphNode[],
  edges: StepGraphEdge[],
  { openRounds, selectedStepExecutionID }: StepGraphFoldingOptions,
): FoldedStepGraph {
  const steps = stepsInExecutionOrder(nodes);
  const drafts = roundDrafts(steps);
  if (drafts.length === 0) return unfoldedStepGraph(nodes, edges);
  const groups = mergeSingleStepRounds(drafts).map((members, index, all) => (
    roundGroup(members, index === all.length - 1, openRounds)
  ));
  const displayIDByNodeID = new Map<string, string>();
  const itemByID = new Map<string, FoldedGraphItem>();
  const roundKeyByStepExecutionID = new Map<string, string>();
  const stepByID = new Map(steps.map((step) => [step.id, step]));
  for (const group of groups) {
    for (const id of group.stepExecutionIDs) roundKeyByStepExecutionID.set(id, group.key);
    const groupSteps = group.stepExecutionIDs.map((id) => stepByID.get(id)).filter(isDefined);
    if (!group.isOpen) addFoldedGroup(group, displayIDByNodeID, itemByID);
    else if (group.repeatedStepType) addOpenRepeatedGroup(group, groupSteps, selectedStepExecutionID, displayIDByNodeID, itemByID);
    else addOpenRound(group, groupSteps, selectedStepExecutionID, displayIDByNodeID, itemByID);
  }
  for (const node of nodes) {
    if (displayIDByNodeID.has(node.id)) continue;
    if (node.kind === 'subflow') {
      // A SubFlow node shows only while its parent Step shows as itself.
      const parentID = node.parentStepId ?? '';
      if ((displayIDByNodeID.get(parentID) ?? parentID) !== parentID) continue;
    }
    displayIDByNodeID.set(node.id, node.id);
    itemByID.set(node.id, { display: 'node', id: node.id, node, repeatCount: 1, roundKey: null });
  }
  const items: FoldedGraphItem[] = [];
  const emitted = new Set<string>();
  for (const node of nodes) {
    const displayID = displayIDByNodeID.get(node.id);
    if (!displayID || emitted.has(displayID)) continue;
    emitted.add(displayID);
    const item = itemByID.get(displayID);
    if (item) items.push(item);
  }
  // Every shown item maps from some node, so this only guards against a missed one.
  for (const item of itemByID.values()) {
    if (!emitted.has(item.id)) items.push(item);
  }
  return { items, edges: foldedEdges(edges, displayIDByNodeID), rounds: groups, roundKeyByStepExecutionID };
}

/** The graph as it is, one node per Step execution, for when Folded is off. */
export function unfoldedStepGraph(nodes: StepGraphNode[], edges: StepGraphEdge[]): FoldedStepGraph {
  return {
    items: nodes.map((node) => ({ display: 'node', id: node.id, node, repeatCount: 1, roundKey: null })),
    edges,
    rounds: [],
    roundKeyByStepExecutionID: new Map(),
  };
}

/** Step executions ordered by their first recorded event; those without one keep their order at the end. */
export function stepsInExecutionOrder(nodes: StepGraphNode[]): StepGraphNode[] {
  return nodes
    .map((node, index) => ({ node, index, order: firstEventID(node) }))
    .filter(({ node }) => node.kind === 'step')
    .sort((left, right) => left.order - right.order || left.index - right.index)
    .map(({ node }) => node);
}

function firstEventID(node: StepGraphNode): number {
  const ids = stepEvents(node).map((event) => event.eventId);
  return ids.length > 0 ? Math.min(...ids) : Number.POSITIVE_INFINITY;
}

function stepEvents(node: StepGraphNode): FlowHistoryEvent[] {
  return [node.waitFor, node.execute, node.pendingWaitFor, node.pendingExecute].filter(isDefined);
}

function stepTypeOf(node: StepGraphNode): string {
  return node.stepType || node.label;
}

/** Splits Steps into Rounds at the earliest repeated Step type; Steps before its first execution stay outside. */
function roundDrafts(steps: StepGraphNode[]): RoundDraft[] {
  const counts = new Map<string, number>();
  for (const step of steps) counts.set(stepTypeOf(step), (counts.get(stepTypeOf(step)) ?? 0) + 1);
  const anchor = steps.find((step) => (counts.get(stepTypeOf(step)) ?? 0) > 1);
  if (!anchor) return [];
  const anchorType = stepTypeOf(anchor);
  const drafts: RoundDraft[] = [];
  for (const step of steps) {
    if (stepTypeOf(step) === anchorType) drafts.push({ number: drafts.length + 1, steps: [step] });
    else drafts.at(-1)?.steps.push(step);
  }
  return drafts;
}

export function isLiveStep(node: StepGraphNode): boolean {
  return Boolean(node.isPlanned || node.active)
    || node.status === 'Active' || node.status === 'Waiting' || node.status === 'Pending';
}

/** Groups Rounds; consecutive single-Step Rounds of one Step type merge unless one is the latest or live. */
function mergeSingleStepRounds(drafts: RoundDraft[]): RoundDraft[][] {
  const groups: RoundDraft[][] = [];
  const isMergeable = (draft: RoundDraft, index: number) => (
    draft.steps.length === 1 && index < drafts.length - 1 && !isLiveStep(draft.steps[0])
  );
  drafts.forEach((draft, index) => {
    const previous = groups.at(-1);
    const previousDraft = previous?.at(-1);
    if (previous && previousDraft && isMergeable(draft, index) && isMergeable(previousDraft, index - 1)
      && stepTypeOf(previousDraft.steps[0]) === stepTypeOf(draft.steps[0])) {
      previous.push(draft);
      return;
    }
    groups.push([draft]);
  });
  return groups;
}

function roundGroup(members: RoundDraft[], isLatest: boolean, openRounds: ReadonlyMap<string, boolean>): StepRoundGroup {
  const steps = members.flatMap((draft) => draft.steps);
  const first = members[0];
  const last = members.at(-1) ?? first;
  const key = first.steps[0].id;
  const isMerged = members.length > 1;
  const repeatedStepType = isMerged ? stepTypeOf(first.steps[0]) : '';
  const failedCount = steps.filter((step) => step.status === 'Failed').length;
  const isLive = steps.some(isLiveStep);
  const isOpen = isLive || isLatest || (openRounds.get(key) ?? failedCount > 0);
  return {
    key,
    firstRoundNumber: first.number,
    lastRoundNumber: last.number,
    title: isMerged ? `${repeatedStepType} ×${members.length.toLocaleString('en-US')}` : `Round ${first.number}`,
    roundRangeLabel: isMerged ? `Rounds ${first.number}–${last.number}` : '',
    repeatedStepType,
    stepExecutionIDs: steps.map((step) => step.id),
    stepCount: steps.length,
    completedCount: steps.filter((step) => step.status === 'Completed').length,
    failedCount,
    sequence: isMerged ? [] : stepSequence(steps),
    duration: stepsDuration(steps),
    isLatest,
    isLive,
    isOpen,
    canHide: !isLive && !isLatest,
  };
}

function stepSequence(steps: StepGraphNode[]): string[] {
  const entries = consecutiveRuns(steps, () => true)
    .map((run) => (run.length > 1 ? `${stepTypeOf(run[0])} ×${run.length}` : stepTypeOf(run[0])));
  return entries.length > maximumSequenceEntries ? [...entries.slice(0, maximumSequenceEntries), '…'] : entries;
}

function stepsDuration(steps: StepGraphNode[]): string {
  const times = steps.flatMap(stepEvents)
    .map((event) => event.eventTime)
    .filter((time): time is string => Boolean(time))
    .sort((left, right) => Date.parse(left) - Date.parse(right));
  return times.length > 0 ? formatDuration(times[0], times.at(-1) ?? times[0]) : '';
}

/** Splits Steps into runs of one Step type; a Step that may not join a run starts its own. */
function consecutiveRuns(steps: StepGraphNode[], canJoin: (step: StepGraphNode) => boolean): StepGraphNode[][] {
  const runs: StepGraphNode[][] = [];
  for (const step of steps) {
    const run = runs.at(-1);
    const previous = run?.at(-1);
    if (run && previous && stepTypeOf(previous) === stepTypeOf(step) && canJoin(previous) && canJoin(step)) run.push(step);
    else runs.push([step]);
  }
  return runs;
}

function addFoldedGroup(
  group: StepRoundGroup,
  displayIDByNodeID: Map<string, string>,
  itemByID: Map<string, FoldedGraphItem>,
): void {
  const id = roundGroupNodeID(group.key);
  for (const stepID of group.stepExecutionIDs) displayIDByNodeID.set(stepID, id);
  itemByID.set(id, { display: 'group', id, round: group });
}

/** Opened merged single-Step Rounds; a long one shows its first, failed, selected and last executions. */
function addOpenRepeatedGroup(
  group: StepRoundGroup,
  steps: StepGraphNode[],
  selectedStepExecutionID: string,
  displayIDByNodeID: Map<string, string>,
  itemByID: Map<string, FoldedGraphItem>,
): void {
  const isElided = steps.length > maximumUnelidedGroupSize;
  const isShown = (step: StepGraphNode, index: number) => !isElided
    || index < shownFirstExecutions || index >= steps.length - shownLastExecutions
    || step.status === 'Failed' || step.id === selectedStepExecutionID;
  let gap: StepGraphNode[] = [];
  const closeGap = () => {
    if (gap.length === 0) return;
    const id = `__round-more:${group.key}:${gap[0].id}`;
    for (const hidden of gap) displayIDByNodeID.set(hidden.id, id);
    itemByID.set(id, { display: 'more', id, hiddenCount: gap.length, roundKey: group.key });
    gap = [];
  };
  steps.forEach((step, index) => {
    if (!isShown(step, index)) {
      gap.push(step);
      return;
    }
    closeGap();
    displayIDByNodeID.set(step.id, step.id);
    itemByID.set(step.id, { display: 'node', id: step.id, node: step, repeatCount: 1, roundKey: group.key });
  });
  closeGap();
}

/** An opened Round; consecutive settled executions of one Step type show as one node with a ×N badge. */
function addOpenRound(
  group: StepRoundGroup,
  steps: StepGraphNode[],
  selectedStepExecutionID: string,
  displayIDByNodeID: Map<string, string>,
  itemByID: Map<string, FoldedGraphItem>,
): void {
  for (const run of consecutiveRuns(steps, (step) => !isLiveStep(step))) {
    const shown = run.find((step) => step.id === selectedStepExecutionID)
      ?? run.findLast((step) => step.status === 'Failed')
      ?? run.at(-1)
      ?? run[0];
    for (const step of run) displayIDByNodeID.set(step.id, shown.id);
    itemByID.set(shown.id, { display: 'node', id: shown.id, node: shown, repeatCount: run.length, roundKey: group.key });
  }
}

/** Points each edge at the nodes that show its ends, without duplicates or edges inside one node. */
function foldedEdges(edges: StepGraphEdge[], displayIDByNodeID: Map<string, string>): StepGraphEdge[] {
  const folded = new Map<string, StepGraphEdge>();
  for (const edge of edges) {
    const source = displayIDByNodeID.get(edge.source);
    const target = displayIDByNodeID.get(edge.target);
    if (!source || !target || source === target) continue;
    const id = source === edge.source && target === edge.target ? edge.id : `${source}->${target}`;
    if (!folded.has(id)) folded.set(id, { id, source, target });
  }
  return [...folded.values()];
}

export function roundGroupNodeID(key: string): string {
  return `__round:${key}`;
}

function isDefined<Value>(value: Value | undefined): value is Value {
  return value !== undefined;
}

/** The box a Round frame draws around its nodes, with room for its heading above them. */
export interface RoundFrameBox {
  roundKey: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

export const roundFramePadding = { side: 14, top: 40, bottom: 14 };

/** Measures one frame per opened Round from the laid-out boxes of its nodes. */
export function roundFrameBoxes(
  items: FoldedGraphItem[],
  boxes: ReadonlyMap<string, { x: number; y: number; width: number; height: number }>,
): RoundFrameBox[] {
  const membersByRound = new Map<string, Array<{ x: number; y: number; width: number; height: number }>>();
  for (const item of items) {
    const roundKey = item.display === 'group' ? null : item.roundKey;
    const box = boxes.get(item.id);
    if (!roundKey || !box) continue;
    membersByRound.set(roundKey, [...(membersByRound.get(roundKey) ?? []), box]);
  }
  return [...membersByRound.entries()].map(([roundKey, members]) => {
    const left = Math.min(...members.map((box) => box.x));
    const top = Math.min(...members.map((box) => box.y));
    const right = Math.max(...members.map((box) => box.x + box.width));
    const bottom = Math.max(...members.map((box) => box.y + box.height));
    return {
      roundKey,
      x: left - roundFramePadding.side,
      y: top - roundFramePadding.top,
      width: right - left + roundFramePadding.side * 2,
      height: bottom - top + roundFramePadding.top + roundFramePadding.bottom,
    };
  });
}
