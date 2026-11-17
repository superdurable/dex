// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { formatDuration } from '@/lib/format';
import type { FlowHistoryEvent, StepGraphEdge, StepGraphNode } from '@/lib/types';

/** An opened range longer than this shows only its first, selected and last Rounds. */
const maximumUnelidedRangeSize = 6;
const shownFirstRounds = 3;
const shownLastRounds = 2;
/** A folded group lists at most this many Step sequence entries before an ellipsis. */
const maximumSequenceEntries = 4;

/** A Round, or a range of consecutive Rounds with the same Step sequence, shown as one unit. */
export interface StepRoundGroup {
  /** "round:" or "rounds:" and the first Round's starting Step execution ID; stable while events arrive. */
  key: string;
  kind: 'round' | 'range';
  firstRoundNumber: number;
  lastRoundNumber: number;
  roundCount: number;
  /** "Round N", "Rounds 1–100", or "SyncBatch ×900" for a range of one Step type. */
  title: string;
  /** "Rounds 1–900" when the title names the Step type; empty otherwise. */
  roundRangeLabel: string;
  /** Each Round holds one Step execution, so opening the range shows Steps, not Rounds. */
  hasSingleStepRounds: boolean;
  stepExecutionIDs: string[];
  stepCount: number;
  completedCount: number;
  failedCount: number;
  /** Step types in execution order, consecutive repeats as "CallTool ×2"; empty when the title names them. */
  sequence: string[];
  duration: string;
  isLatest: boolean;
  /** Holds a waiting, running or pending Step, so it is never folded. */
  isLive: boolean;
  isOpen: boolean;
  /** The latest Round and live Rounds cannot be hidden. */
  canHide: boolean;
}

/** A node the folded graph draws: an original node, a folded group, or a gap of an opened range. */
export type FoldedGraphItem =
  | { display: 'node'; id: string; node: StepGraphNode; repeatCount: number; frameKeys: string[] }
  | { display: 'group'; id: string; round: StepRoundGroup; frameKeys: string[] }
  | { display: 'more'; id: string; hiddenCount: number; unit: 'Steps' | 'Rounds'; frameKeys: string[] };

export interface FoldedStepGraph {
  items: FoldedGraphItem[];
  edges: StepGraphEdge[];
  /** Every Round and range in execution order, with each range's Rounds after it; empty when nothing repeats. */
  rounds: StepRoundGroup[];
  /** The group keys that hold each Step execution, outermost first. */
  groupKeysByStepExecutionID: Map<string, string[]>;
}

export interface StepGraphFoldingOptions {
  /** Groups a reader opened (true) or hid (false); others use the default open state. */
  openRounds: ReadonlyMap<string, boolean>;
  selectedStepExecutionID: string;
}

interface RoundDraft {
  number: number;
  key: string;
  steps: StepGraphNode[];
  signature: string;
  isLive: boolean;
  hasFailed: boolean;
  isLatest: boolean;
}

interface FoldingContext {
  options: StepGraphFoldingOptions;
  displayIDByNodeID: Map<string, string>;
  itemByID: Map<string, FoldedGraphItem>;
  groupKeysByStepExecutionID: Map<string, string[]>;
  rounds: StepRoundGroup[];
}

/** Folds repeated Rounds; each starts at the repeated Step type with the fewest executions. */
export function foldStepGraph(
  nodes: StepGraphNode[],
  edges: StepGraphEdge[],
  options: StepGraphFoldingOptions,
): FoldedStepGraph {
  const steps = stepsInExecutionOrder(nodes);
  const drafts = roundDrafts(steps);
  if (drafts.length === 0) return unfoldedStepGraph(nodes, edges);
  const context: FoldingContext = {
    options,
    displayIDByNodeID: new Map(),
    itemByID: new Map(),
    groupKeysByStepExecutionID: new Map(),
    rounds: [],
  };
  // Steps before the first Round fold their repeats as inside a Round.
  addStepRuns(context, steps.slice(0, steps.indexOf(drafts[0].steps[0])), []);
  for (const unit of mergeIdenticalRounds(drafts)) {
    if (unit.length === 1) addRound(context, unit[0], []);
    else addRange(context, unit);
  }
  const { displayIDByNodeID, itemByID } = context;
  for (const node of nodes) {
    if (displayIDByNodeID.has(node.id)) continue;
    if (node.kind === 'subflow') {
      // A SubFlow node shows only while its parent Step shows as itself.
      const parentID = node.parentStepId ?? '';
      if ((displayIDByNodeID.get(parentID) ?? parentID) !== parentID) continue;
    }
    displayIDByNodeID.set(node.id, node.id);
    itemByID.set(node.id, { display: 'node', id: node.id, node, repeatCount: 1, frameKeys: [] });
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
  return {
    items,
    edges: foldedEdges(edges, displayIDByNodeID),
    rounds: context.rounds,
    groupKeysByStepExecutionID: context.groupKeysByStepExecutionID,
  };
}

/** The graph as it is, one node per Step execution, for when Folded is off. */
export function unfoldedStepGraph(nodes: StepGraphNode[], edges: StepGraphEdge[]): FoldedStepGraph {
  return {
    items: nodes.map((node) => ({ display: 'node', id: node.id, node, repeatCount: 1, frameKeys: [] })),
    edges,
    rounds: [],
    groupKeysByStepExecutionID: new Map(),
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

export function isLiveStep(node: StepGraphNode): boolean {
  return Boolean(node.isPlanned || node.active)
    || node.status === 'Active' || node.status === 'Waiting' || node.status === 'Pending';
}

/** Anchors Rounds on the repeated Step type with the fewest executions, ties to the earliest. */
function roundDrafts(steps: StepGraphNode[]): RoundDraft[] {
  const counts = new Map<string, number>();
  for (const step of steps) counts.set(stepTypeOf(step), (counts.get(stepTypeOf(step)) ?? 0) + 1);
  let anchorType = '';
  for (const step of steps) {
    const count = counts.get(stepTypeOf(step)) ?? 0;
    if (count > 1 && (!anchorType || count < (counts.get(anchorType) ?? 0))) anchorType = stepTypeOf(step);
  }
  if (!anchorType) return [];
  const groups: StepGraphNode[][] = [];
  for (const step of steps) {
    if (stepTypeOf(step) === anchorType) groups.push([step]);
    else groups.at(-1)?.push(step);
  }
  return groups.map((roundSteps, index) => ({
    number: index + 1,
    key: `round:${roundSteps[0].id}`,
    steps: roundSteps,
    signature: stepSequence(roundSteps).join(' → '),
    isLive: roundSteps.some(isLiveStep),
    hasFailed: roundSteps.some((step) => step.status === 'Failed'),
    isLatest: index === groups.length - 1,
  }));
}

/** Groups consecutive settled Rounds with the same Step sequence; open-by-default Rounds stand alone. */
function mergeIdenticalRounds(drafts: RoundDraft[]): RoundDraft[][] {
  const units: RoundDraft[][] = [];
  const isMergeable = (draft: RoundDraft) => !draft.isLive && !draft.hasFailed && !draft.isLatest;
  for (const draft of drafts) {
    const previous = units.at(-1);
    const previousDraft = previous?.at(-1);
    if (previous && previousDraft && isMergeable(draft) && isMergeable(previousDraft)
      && previousDraft.signature === draft.signature) {
      previous.push(draft);
    } else {
      units.push([draft]);
    }
  }
  return units;
}

function addRound(context: FoldingContext, draft: RoundDraft, outerKeys: string[]): void {
  const group = roundGroup(draft, context.options.openRounds);
  context.rounds.push(group);
  const groupKeys = [...outerKeys, group.key];
  for (const step of draft.steps) context.groupKeysByStepExecutionID.set(step.id, groupKeys);
  if (!group.isOpen) {
    addGroupNode(context, group, draft.steps, outerKeys);
    return;
  }
  addStepRuns(context, draft.steps, groupKeys);
}

/** Shows consecutive settled executions of one Step type as one node with a ×N badge. */
function addStepRuns(context: FoldingContext, steps: StepGraphNode[], frameKeys: string[]): void {
  for (const run of consecutiveRuns(steps, (step) => !isLiveStep(step))) {
    const shown = run.find((step) => step.id === context.options.selectedStepExecutionID)
      ?? run.findLast((step) => step.status === 'Failed')
      ?? run.at(-1)
      ?? run[0];
    for (const step of run) context.displayIDByNodeID.set(step.id, shown.id);
    context.itemByID.set(shown.id, { display: 'node', id: shown.id, node: shown, repeatCount: run.length, frameKeys });
  }
}

/** A range of Rounds; opened, it shows its first, selected and last Rounds, each folded. */
function addRange(context: FoldingContext, drafts: RoundDraft[]): void {
  const group = rangeGroup(drafts, context.options.openRounds);
  context.rounds.push(group);
  const steps = drafts.flatMap((draft) => draft.steps);
  for (const draft of drafts) {
    const groupKeys = draft.steps.length > 1 ? [group.key, draft.key] : [group.key];
    for (const step of draft.steps) context.groupKeysByStepExecutionID.set(step.id, groupKeys);
  }
  if (!group.isOpen) {
    addGroupNode(context, group, steps, []);
    return;
  }
  const frameKeys = [group.key];
  const selectedID = context.options.selectedStepExecutionID;
  const isElided = drafts.length > maximumUnelidedRangeSize;
  const isShown = (draft: RoundDraft, index: number) => !isElided
    || index < shownFirstRounds || index >= drafts.length - shownLastRounds
    || draft.steps.some((step) => step.id === selectedID);
  let gap: RoundDraft[] = [];
  const closeGap = () => {
    if (gap.length === 0) return;
    const id = `__round-more:${group.key}:${gap[0].key}`;
    for (const hidden of gap.flatMap((draft) => draft.steps)) context.displayIDByNodeID.set(hidden.id, id);
    context.itemByID.set(id, {
      display: 'more',
      id,
      hiddenCount: gap.length,
      unit: group.hasSingleStepRounds ? 'Steps' : 'Rounds',
      frameKeys,
    });
    gap = [];
  };
  drafts.forEach((draft, index) => {
    if (!isShown(draft, index)) {
      gap.push(draft);
      return;
    }
    closeGap();
    if (draft.steps.length === 1) {
      const [step] = draft.steps;
      context.displayIDByNodeID.set(step.id, step.id);
      context.itemByID.set(step.id, { display: 'node', id: step.id, node: step, repeatCount: 1, frameKeys });
      return;
    }
    addRound(context, draft, frameKeys);
  });
  closeGap();
}

function addGroupNode(context: FoldingContext, group: StepRoundGroup, steps: StepGraphNode[], frameKeys: string[]): void {
  const id = roundGroupNodeID(group.key);
  for (const step of steps) context.displayIDByNodeID.set(step.id, id);
  context.itemByID.set(id, { display: 'group', id, round: group, frameKeys });
}

function roundGroup(draft: RoundDraft, openRounds: ReadonlyMap<string, boolean>): StepRoundGroup {
  const isOpen = draft.isLive || draft.isLatest || (openRounds.get(draft.key) ?? draft.hasFailed);
  return {
    ...stepTotals(draft.steps),
    key: draft.key,
    kind: 'round',
    firstRoundNumber: draft.number,
    lastRoundNumber: draft.number,
    roundCount: 1,
    title: `Round ${draft.number}`,
    roundRangeLabel: '',
    hasSingleStepRounds: draft.steps.length === 1,
    sequence: truncatedSequence(stepSequence(draft.steps)),
    isLatest: draft.isLatest,
    isLive: draft.isLive,
    isOpen,
    canHide: !draft.isLive && !draft.isLatest,
  };
}

function rangeGroup(drafts: RoundDraft[], openRounds: ReadonlyMap<string, boolean>): StepRoundGroup {
  const first = drafts[0];
  const last = drafts.at(-1) ?? first;
  const steps = drafts.flatMap((draft) => draft.steps);
  const key = `rounds:${first.steps[0].id}`;
  const isOneStepType = new Set(first.steps.map(stepTypeOf)).size === 1;
  const rangeLabel = `Rounds ${first.number}–${last.number}`;
  return {
    ...stepTotals(steps),
    key,
    kind: 'range',
    firstRoundNumber: first.number,
    lastRoundNumber: last.number,
    roundCount: drafts.length,
    title: isOneStepType ? `${stepTypeOf(first.steps[0])} ×${steps.length.toLocaleString('en-US')}` : rangeLabel,
    roundRangeLabel: isOneStepType ? rangeLabel : '',
    hasSingleStepRounds: first.steps.length === 1,
    sequence: isOneStepType ? [] : truncatedSequence(stepSequence(first.steps)),
    isLatest: false,
    isLive: false,
    isOpen: openRounds.get(key) ?? false,
    canHide: true,
  };
}

function stepTotals(steps: StepGraphNode[]) {
  return {
    stepExecutionIDs: steps.map((step) => step.id),
    stepCount: steps.length,
    completedCount: steps.filter((step) => step.status === 'Completed').length,
    failedCount: steps.filter((step) => step.status === 'Failed').length,
    duration: stepsDuration(steps),
  };
}

function stepSequence(steps: StepGraphNode[]): string[] {
  return consecutiveRuns(steps, () => true)
    .map((run) => (run.length > 1 ? `${stepTypeOf(run[0])} ×${run.length}` : stepTypeOf(run[0])));
}

function truncatedSequence(entries: string[]): string[] {
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
  return `__group:${key}`;
}

export function roundFrameNodeID(key: string): string {
  return `__frame:${key}`;
}

function isDefined<Value>(value: Value | undefined): value is Value {
  return value !== undefined;
}

/** The box a frame draws around an opened group's nodes, with room for its heading above them. */
export interface RoundFrameBox {
  roundKey: string;
  x: number;
  y: number;
  width: number;
  height: number;
}

export const roundFramePadding = { side: 14, top: 40, bottom: 14 };

interface Box { x: number; y: number; width: number; height: number }

/** Measures one frame per opened group; an outer frame also leaves room for the frames inside it. */
export function roundFrameBoxes(items: FoldedGraphItem[], boxes: ReadonlyMap<string, Box>): RoundFrameBox[] {
  const membersByKey = new Map<string, Box[]>();
  for (const item of items) {
    const box = boxes.get(item.id);
    if (!box) continue;
    item.frameKeys.forEach((key, depth) => {
      const innerFrames = item.frameKeys.length - depth - 1;
      const grown = {
        x: box.x - roundFramePadding.side * innerFrames,
        y: box.y - roundFramePadding.top * innerFrames,
        width: box.width + roundFramePadding.side * 2 * innerFrames,
        height: box.height + (roundFramePadding.top + roundFramePadding.bottom) * innerFrames,
      };
      membersByKey.set(key, [...(membersByKey.get(key) ?? []), grown]);
    });
  }
  return [...membersByKey.entries()].map(([roundKey, members]) => {
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
