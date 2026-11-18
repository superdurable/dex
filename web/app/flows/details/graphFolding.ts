// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { formatDuration } from '@/lib/format';
import { stepDecision } from '@/lib/graph';
import { decidingEventIdOf } from '@/lib/historyRelations';
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
  /** The opened range this Round shows inside; empty for a top-level group. */
  parentKey: string;
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
  /** The Step type that starts each Round; empty when nothing repeats. */
  anchorStepType: string;
}

/** Open (true) or hide (false) choices per Step execution ID, so they survive regrouping. */
export interface GroupOpenIntents {
  /** Choices that open or hide Rounds. */
  rounds: ReadonlyMap<string, boolean>;
  /** Choices that open or hide ranges of Rounds. */
  ranges: ReadonlyMap<string, boolean>;
}

export const noGroupOpenIntents: GroupOpenIntents = { rounds: new Map(), ranges: new Map() };

/** Records Show or Hide on a group for each of its Step executions. */
export function withGroupOpenChoice(intents: GroupOpenIntents, group: StepRoundGroup, isOpen: boolean): GroupOpenIntents {
  const rounds = new Map(intents.rounds);
  const ranges = new Map(intents.ranges);
  for (const id of group.stepExecutionIDs) {
    // Opening a range leaves its Rounds folded; hiding it folds them too.
    if (group.kind === 'round' || !isOpen) rounds.set(id, isOpen);
    // A Round inside an opened range leaves the range as it is.
    if (!group.parentKey) ranges.set(id, isOpen);
  }
  return { rounds, ranges };
}

/** Opens every group that holds the selected Step execution. */
export function withStepExecutionOpened(intents: GroupOpenIntents, stepExecutionID: string): GroupOpenIntents {
  return {
    rounds: new Map(intents.rounds).set(stepExecutionID, true),
    ranges: new Map(intents.ranges).set(stepExecutionID, true),
  };
}

export interface StepGraphFoldingOptions {
  openIntents: GroupOpenIntents;
  selectedStepExecutionID: string;
  /** The Step type that anchored Rounds before; kept while it still repeats, so groups stay stable. */
  preferredAnchorStepType?: string;
  /** The loaded history; its Step and RPC decisions order the Steps each one started. */
  historyEvents?: FlowHistoryEvent[];
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
  const steps = stepsInExecutionOrder(nodes, options.historyEvents);
  const anchorStepType = roundAnchorStepType(steps, options.preferredAnchorStepType ?? '');
  const drafts = roundDrafts(steps, anchorStepType);
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
    anchorStepType,
  };
}

/** The graph as it is, one node per Step execution, for when Folded is off. */
export function unfoldedStepGraph(nodes: StepGraphNode[], edges: StepGraphEdge[]): FoldedStepGraph {
  return {
    items: nodes.map((node) => ({ display: 'node', id: node.id, node, repeatCount: 1, frameKeys: [] })),
    edges,
    rounds: [],
    groupKeysByStepExecutionID: new Map(),
    anchorStepType: '',
  };
}

interface StepSpawnOrder {
  node: StepGraphNode;
  /** The deciding event that started the Step execution, loaded or not; else its first event. */
  spawnEventID: number;
  /** The index in the deciding event's next Steps; see the constants below for the other cases. */
  nextStepIndex: number;
  firstEventID: number;
}

/** Sorts a Step without a deciding event before the Steps its own first event decides. */
const noDecisionNextStepIndex = -1;
/** Sorts a Step after its siblings when its deciding event is unloaded or lists it no more. */
const unknownNextStepIndex = Number.MAX_SAFE_INTEGER;

/** Step executions in spawn order: deciding event, next-Step index, first event, then ID. */
export function stepsInExecutionOrder(nodes: StepGraphNode[], historyEvents: FlowHistoryEvent[] = []): StepGraphNode[] {
  const steps = nodes.filter((node) => node.kind === 'step');
  const nextStepIndexesByDecision = nextStepIndexesByDecisionEvent([...historyEvents, ...steps.flatMap(stepEvents)]);
  const decidedSteps = new Map<string, StepGraphNode[]>();
  const orders = new Map<StepGraphNode, StepSpawnOrder>();
  for (const node of steps) {
    const firstEvent = firstEventID(node);
    const decisionEventID = stepEvents(node).map(decidingEventIdOf).find(isDefined);
    if (decisionEventID === undefined) {
      orders.set(node, { node, spawnEventID: firstEvent, nextStepIndex: noDecisionNextStepIndex, firstEventID: firstEvent });
      continue;
    }
    orders.set(node, { node, spawnEventID: decisionEventID, nextStepIndex: unknownNextStepIndex, firstEventID: firstEvent });
    appendToList(decidedSteps, decisionStepTypeKey(decisionEventID, stepTypeOf(node)), node);
  }
  // The k-th execution of a Step type takes that type's k-th place in its decision.
  for (const [key, siblings] of decidedSteps) {
    const indexes = nextStepIndexesByDecision.get(key);
    if (!indexes) continue;
    siblings.sort((left, right) => compareStepExecutionIDs(left.id, right.id) || firstEventID(left) - firstEventID(right));
    siblings.forEach((sibling, occurrence) => {
      const order = orders.get(sibling);
      if (order && occurrence < indexes.length) order.nextStepIndex = indexes[occurrence];
    });
  }
  return [...orders.values()]
    .sort((left, right) => compareNumbers(left.spawnEventID, right.spawnEventID)
      || left.nextStepIndex - right.nextStepIndex
      || compareNumbers(left.firstEventID, right.firstEventID)
      || compareStepExecutionIDs(left.node.id, right.node.id))
    .map(({ node }) => node);
}

/** The next-Step indexes of each Step type in each loaded Step or RPC decision. */
function nextStepIndexesByDecisionEvent(events: FlowHistoryEvent[]): Map<string, number[]> {
  const decisionEventIDs = new Set<number>();
  const indexesByKey = new Map<string, number[]>();
  for (const event of events) {
    const decision = stepDecision(event);
    if (!decision || decisionEventIDs.has(event.eventId)) continue;
    decisionEventIDs.add(event.eventId);
    const nextSteps = Array.isArray(decision.nextSteps) ? decision.nextSteps : [];
    nextSteps.forEach((movement, index) => {
      appendToList(indexesByKey, decisionStepTypeKey(event.eventId, nextStepTypeOf(movement)), index);
    });
  }
  return indexesByKey;
}

function appendToList<Value>(listsByKey: Map<string, Value[]>, key: string, value: Value): void {
  const list = listsByKey.get(key);
  if (list) list.push(value);
  else listsByKey.set(key, [value]);
}

function decisionStepTypeKey(decisionEventID: number, stepType: string): string {
  return `${decisionEventID}\u0000${stepType}`;
}

function nextStepTypeOf(movement: unknown): string {
  const stepType = movement && typeof movement === 'object' ? (movement as Record<string, unknown>).stepType : undefined;
  return typeof stepType === 'string' ? stepType : '';
}

/** Compares numbers where two infinities are equal, unlike subtraction, which gives NaN. */
function compareNumbers(left: number, right: number): number {
  return left === right ? 0 : left < right ? -1 : 1;
}

/** Orders "Step-9" before "Step-10"; distinct IDs that compare equal numerically fall back to code points. */
function compareStepExecutionIDs(left: string, right: string): number {
  return left.localeCompare(right, 'en', { numeric: true }) || (left < right ? -1 : left > right ? 1 : 0);
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

/** The least-repeated executed Step type, ties to the earliest; a still-repeating preferred type wins. */
function roundAnchorStepType(steps: StepGraphNode[], preferredStepType: string): string {
  const counts = new Map<string, number>();
  for (const step of steps) {
    if (!step.isPlanned) counts.set(stepTypeOf(step), (counts.get(stepTypeOf(step)) ?? 0) + 1);
  }
  if ((counts.get(preferredStepType) ?? 0) > 1) return preferredStepType;
  let anchorType = '';
  for (const step of steps) {
    const count = step.isPlanned ? 0 : counts.get(stepTypeOf(step)) ?? 0;
    if (count > 1 && (!anchorType || count < (counts.get(anchorType) ?? 0))) anchorType = stepTypeOf(step);
  }
  return anchorType;
}

/** Splits Steps into Rounds at each executed anchor Step; earlier Steps stay outside every Round. */
function roundDrafts(steps: StepGraphNode[], anchorType: string): RoundDraft[] {
  if (!anchorType) return [];
  const groups: StepGraphNode[][] = [];
  for (const step of steps) {
    if (stepTypeOf(step) === anchorType && !step.isPlanned) groups.push([step]);
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
  const group = roundGroup(draft, context.options.openIntents, outerKeys.at(-1) ?? '');
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
  const group = rangeGroup(drafts, context.options.openIntents);
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
    || draft.steps.some((step) => step.id === selectedID || context.options.openIntents.rounds.get(step.id) === true);
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

/** True when any Step execution was opened, false when some were hidden and none opened. */
function chosenOpenState(choices: ReadonlyMap<string, boolean>, steps: StepGraphNode[]): boolean | undefined {
  let state: boolean | undefined;
  for (const step of steps) {
    const choice = choices.get(step.id);
    if (choice === true) return true;
    if (choice === false) state = false;
  }
  return state;
}

function roundGroup(draft: RoundDraft, intents: GroupOpenIntents, parentKey: string): StepRoundGroup {
  const roundChoice = chosenOpenState(intents.rounds, draft.steps);
  // Inside an opened range a Round opens only by choice; on its own a range choice also counts.
  const choice = parentKey ? roundChoice : roundChoice ?? chosenOpenState(intents.ranges, draft.steps);
  const isOpen = draft.isLive || draft.isLatest || (choice ?? (!parentKey && draft.hasFailed));
  return {
    parentKey,
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

function rangeGroup(drafts: RoundDraft[], intents: GroupOpenIntents): StepRoundGroup {
  const first = drafts[0];
  const last = drafts.at(-1) ?? first;
  const steps = drafts.flatMap((draft) => draft.steps);
  const key = `rounds:${first.steps[0].id}`;
  // An opened Round inside keeps its range open, so what the reader opened stays in view.
  const isOpen = chosenOpenState(intents.ranges, steps) === true || chosenOpenState(intents.rounds, steps) === true;
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
    isOpen,
    canHide: true,
    parentKey: '',
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
