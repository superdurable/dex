// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

'use client';

import { useEffect, useId, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Background,
  ControlButton,
  Controls,
  Handle,
  MarkerType,
  Position,
  ReactFlow,
  type Edge,
  type Node,
  type NodeProps,
  type NodeTypes,
  type ReactFlowInstance,
} from '@xyflow/react';
import {
  ArrowRight,
  Ban,
  CheckCheck,
  ChevronDown,
  ChevronRight,
  CircleCheck,
  CircleDashed,
  CircleDot,
  CircleSlash2,
  CircleX,
  Clock,
  Flag,
  FoldHorizontal,
  Hourglass,
  Inbox,
  Map as MapIcon,
  Maximize,
  Minus,
  OctagonX,
  Play,
  Repeat2,
  X,
  type LucideIcon,
} from 'lucide-react';
import { buildStepGraph, stepGraphSelection } from '@/lib/graph';
import type { FlowHistoryEvent, FlowState, StepGraphNode } from '@/lib/types';
import {
  foldStepGraph,
  noGroupOpenIntents,
  withGroupOpenChoice,
  withStepExecutionOpened,
  type GroupOpenIntents,
  roundFrameBoxes,
  roundFrameNodeID,
  unfoldedStepGraph,
  type FoldedGraphItem,
  type FoldedStepGraph,
  type StepRoundGroup,
} from './graphFolding';
import {
  executeDecisionSummary,
  type ExecuteDecisionKind,
  type ExecuteDecisionSummary,
} from './executeDecisionSummary';
import {
  defaultGraphZoom,
  fittingGraphZoom,
  graphBounds,
  layoutGraph,
  minimumGraphZoom,
  type GraphDirection,
} from './graphLayout';
import { GraphOverview } from './GraphOverview';
import { graphViewStorageKeys, useStoredGraphFlag } from './graphViewPreferences';

/** What a node draws: a Step execution or source, a folded group, an elision gap, or a frame. */
type GraphNodeDisplay = 'node' | 'group' | 'more' | 'frame';

interface StepNodeData extends Record<string, unknown> {
  label: React.ReactNode;
  display: GraphNodeDisplay;
  /** The Step execution, source or SubFlow node; absent for Round groups, gaps and frames. */
  model?: StepGraphNode;
  /** The Round group a folded node, gap or frame stands for. */
  round?: StepRoundGroup;
  /** The opened groups this node is drawn inside, outermost first. */
  frameKeys?: string[];
  hasSubFlowChildren?: boolean;
  direction: GraphDirection;
}

const foldedGroupDimensions = { width: 280, withSequenceHeight: 136, height: 112 };
const elisionGapDimensions = { width: 140, height: 44 };
/** The canvas keeps this much room above the highest Round frame heading. */
const frameTopClearance = 12;
const viewportTransitionMilliseconds = 280;

type MethodStatus = 'Started' | 'Waiting' | 'Running' | 'Pending' | 'Completed' | 'Failed' | 'Canceled' | 'Not started';

const statusIcons: Record<string, LucideIcon> = {
  Active: Play,
  Canceled: Ban,
  Completed: CircleCheck,
  Failed: CircleX,
  'Not started': CircleDashed,
  Pending: Clock,
  Running: Play,
  Source: CircleDot,
  Started: Play,
  Waiting: Hourglass,
};

/**
 * Step node heights. Each WaitFor condition kind beyond the first adds one spaced line;
 * each Channel line beyond the first adds one unspaced line.
 */
const stepNodeHeight = { executeOnly: 128, withWaitFor: 148, perExtraConditionLine: 18, perExtraChannelLine: 14 };

/** More Channel names collapse into a "+N more" line so the node keeps a bounded height. */
const maxVisibleChannelNames = 2;

/** Below this zoom a whole left-to-right graph is too small to read, so it frames the selected Step instead. */
const readableFitZoom = 0.6;
/** A framed graph zooms out a little, so the selected Step shows its neighbors. */
const framedGraphZoom = 0.8;
const maximumGraphZoom = 2;
/** The zoom-out limit of the controls, lowered further when only that fits the whole graph. */
const controlMinimumZoom = 0.2;
/** Fit View leaves a margin of 5% on each side. */
const fitPadding = 0.9;

/** Whether a node lies wholly inside the canvas at the current viewport. */
function isNodeInView(
  viewport: { x: number; y: number; zoom: number },
  canvas: { width: number; height: number },
  position: { x: number; y: number },
  size: { width: number; height: number },
): boolean {
  const left = position.x * viewport.zoom + viewport.x;
  const top = position.y * viewport.zoom + viewport.y;
  return left >= 0 && top >= 0
    && left + size.width * viewport.zoom <= canvas.width
    && top + size.height * viewport.zoom <= canvas.height;
}

const graphEdgeColor = 'var(--mark)';
const connectedStepColor = 'var(--ink-dim)';
const waitingEdgeColor = 'var(--tone-wait)';
const introLeaveMilliseconds = 220;
/** The mini map panel's width plus its margin from the canvas edge. */
const miniMapReservedWidth = 200;
/** Space left of an opened group's frame after the view pans to it. */
const revealMargin = 24;

function prefersReducedMotion(): boolean {
  return typeof window.matchMedia === 'function' && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
}

function viewportTransitionDuration(): number {
  return prefersReducedMotion() ? 0 : viewportTransitionMilliseconds;
}

function waitingCondition(node: StepGraphNode): Record<string, unknown> {
  const output = node.waitFor?.payload.output;
  if (output && typeof output === 'object') {
    const condition = (output as Record<string, unknown>).waitForCondition;
    if (condition && typeof condition === 'object') return condition as Record<string, unknown>;
  }
  return node.active?.waitingCondition ?? {};
}

function conditionCount(condition: Record<string, unknown>, key: string): number {
  const values = condition[key];
  return Array.isArray(values) ? values.length : 0;
}

function channelNames(condition: Record<string, unknown>): string[] {
  const conditions = condition.channelConditions;
  if (!Array.isArray(conditions)) return [];
  return conditions.flatMap((entry) => {
    if (!entry || typeof entry !== 'object') return [];
    const name = (entry as Record<string, unknown>).channelName;
    return typeof name === 'string' && name ? [name] : [];
  });
}

/** Splits Channel names into the lines the node shows and the count behind "+N more". */
function visibleChannelNames(names: string[]): { visible: string[]; hiddenCount: number } {
  if (names.length <= maxVisibleChannelNames) return { visible: names, hiddenCount: 0 };
  const visible = names.slice(0, maxVisibleChannelNames);
  return { visible, hiddenCount: names.length - visible.length };
}

/** Counts the lines the Channel condition shows: names, "+N more", or one count summary. */
function channelLineCount(names: string[]): number {
  if (names.length === 0) return 1;
  const { visible, hiddenCount } = visibleChannelNames(names);
  return visible.length + (hiddenCount > 0 ? 1 : 0);
}

function StatusIcon({ status, size = 14 }: { status: string; size?: number }) {
  const Icon = statusIcons[status] ?? CircleDashed;
  return <Icon aria-hidden="true" size={size} strokeWidth={2.25} />;
}

/** Whether a Step node shows its WaitFor method, which decides the node height. */
function showsWaitFor(node: StepGraphNode): boolean {
  return Boolean(
    node.waitFor || node.pendingWaitFor
      || (!skipsWaitFor(node) && (node.active || node.isPlanned) && !node.execute),
  );
}

/** Counts the condition kinds a WaitFor method lists and the Channel lines among them. */
function conditionLineCounts(node: StepGraphNode): { kinds: number; channelLines: number } {
  const condition = waitingCondition(node);
  const kinds = ['timerConditions', 'channelConditions', 'subFlowConditions']
    .filter((key) => conditionCount(condition, key) > 0).length;
  const channelLines = conditionCount(condition, 'channelConditions') > 0
    ? channelLineCount([...new Set(channelNames(condition))])
    : 0;
  return { kinds, channelLines };
}

function skipsWaitFor(node: StepGraphNode): boolean {
  const options = node.active?.movement?.stepOptions ?? node.movement?.stepOptions;
  return Boolean(options && typeof options === 'object' && (options as Record<string, unknown>).skipWaitFor === true);
}

function ConditionIcon({ type }: { type: 'timer' | 'channel' | 'subflow' }) {
  if (type === 'timer') {
    return (
      <svg aria-hidden="true" className="graph-condition-icon" viewBox="0 0 16 16">
        <circle cx="8" cy="8" r="5.5" />
        <path d="M8 4.8v3.5l2.4 1.4" />
      </svg>
    );
  }
  if (type === 'channel') return (
    <svg aria-hidden="true" className="graph-condition-icon" viewBox="0 0 16 16">
      <circle cx="4" cy="4" r="1.7" />
      <circle cx="12" cy="8" r="1.7" />
      <circle cx="4" cy="12" r="1.7" />
      <path d="M5.7 4.7 10.3 7M5.7 11.3 10.3 9" />
    </svg>
  );
  return (
    <svg aria-hidden="true" className="graph-condition-icon" viewBox="0 0 16 16">
      <rect x="2" y="3" width="5" height="4" rx="1" />
      <rect x="9" y="9" width="5" height="4" rx="1" />
      <path d="M7 5h2.5a2 2 0 0 1 2 2v2" />
    </svg>
  );
}

function waitForStatus(node: StepGraphNode): MethodStatus {
  if (node.waitFor?.type === 'StepWaitForFailed') return 'Failed';
  if (node.pendingWaitFor) return 'Pending';
  if (node.active?.phase === 'Waiting') return 'Waiting';
  if (node.waitFor) return 'Started';
  if (node.active) return 'Running';
  if (node.status === 'Canceled') return 'Canceled';
  return 'Not started';
}

function executeStatus(node: StepGraphNode): MethodStatus {
  if (node.execute?.type === 'StepExecuteFailed') return 'Failed';
  if (node.pendingExecute) return 'Pending';
  if (node.execute) return 'Completed';
  if (node.active?.phase === 'Active' && node.waitFor) return 'Running';
  if (node.status === 'Canceled') return 'Canceled';
  return 'Not started';
}

const executeDecisionIcons: Record<ExecuteDecisionKind, LucideIcon> = {
  'next-steps': ArrowRight,
  'dead-end': CircleSlash2,
  'graceful-complete': Flag,
  'force-complete': CheckCheck,
  'force-fail': OctagonX,
  'complete-when-channels-empty': Inbox,
  cancel: Ban,
};

function MethodSection({
  name,
  status,
  event,
  decision,
  children,
  onSelect,
  isSelected = false,
}: {
  name: string;
  status: MethodStatus;
  event?: FlowHistoryEvent;
  /** The Step decision a completed Execute returned; it replaces the status line. */
  decision?: ExecuteDecisionSummary;
  children?: React.ReactNode;
  onSelect: (event: FlowHistoryEvent) => void;
  isSelected?: boolean;
}) {
  const decisionClass = decision ? ` graph-method-decision graph-method-decision-${decision.kind}` : '';
  const decisionDescriptionID = useId();
  return (
    <button
      aria-describedby={decision ? decisionDescriptionID : undefined}
      aria-pressed={event ? isSelected : undefined}
      className={`graph-method graph-method-${name.toLowerCase()} graph-method-${status.toLowerCase().replace(' ', '-')}${decisionClass}${isSelected ? ' is-selected' : ''} nodrag nopan`}
      disabled={!event}
      type="button"
      onClick={(clickEvent) => {
        clickEvent.stopPropagation();
        if (event) onSelect(event);
      }}
    >
      <span>{name}</span>
      {decision
        ? <ExecuteDecisionLine decision={decision} />
        : <strong><StatusIcon status={status} size={13} />{status}</strong>}
      {/* Hidden text stays out of the button name but still serves as its description. */}
      {decision && <span hidden id={decisionDescriptionID}>{decision.fullText.split('\n').join('; ')}</span>}
      {children}
    </button>
  );
}

function ExecuteDecisionLine({ decision }: { decision: ExecuteDecisionSummary }) {
  const Icon = executeDecisionIcons[decision.kind];
  return (
    <strong className="graph-execute-decision" title={decision.fullText}>
      <Icon aria-hidden="true" size={13} strokeWidth={2.25} />
      {decision.kind === 'next-steps' && <span className="sr-only">Next Steps </span>}
      <span className="graph-execute-decision-text">{decision.label}</span>
      {decision.hiddenCount > 0 && <span className="graph-execute-decision-more">+{decision.hiddenCount}</span>}
    </strong>
  );
}

export function StepNodeLabel({
  node,
  onSelect,
  selectedEventId = null,
  repeatCount = 1,
}: {
  node: StepGraphNode;
  onSelect: (event: FlowHistoryEvent) => void;
  /** The selected event; the method that recorded it shows as selected. */
  selectedEventId?: number | null;
  /** Consecutive executions of this Step type the node stands for; above 1 it shows a ×N badge. */
  repeatCount?: number;
}) {
  const condition = waitingCondition(node);
  const timerCount = conditionCount(condition, 'timerConditions');
  const channelCount = conditionCount(condition, 'channelConditions');
  const subFlowCount = conditionCount(condition, 'subFlowConditions');
  const channels = [...new Set(channelNames(condition))];
  const channelSummary = channels.length > 0 ? channels.join(', ') : `${channelCount} channel${channelCount === 1 ? '' : 's'}`;
  const shownChannels = visibleChannelNames(channels);
  const showWaitFor = showsWaitFor(node);
  const executeMethodStatus = executeStatus(node);
  const decision = executeMethodStatus === 'Completed' ? executeDecisionSummary(node.execute) : undefined;
  return (
    <div className={`graph-step-label${showWaitFor ? '' : ' graph-step-label--execute-only'}`}>
      <div className="graph-step-heading">
        <span className={`graph-step-status status-tone-${node.status.toLowerCase()}`}>
          <StatusIcon status={node.status} size={15} />
        </span>
        <b>{node.label}</b>
        {repeatCount > 1 && (
          <span className="graph-step-repeat" title={`${repeatCount} consecutive Step executions of ${node.label}`}>
            ×{repeatCount}
          </span>
        )}
        <span className="graph-step-meta">
          <span className={`graph-step-status-text status-tone-${node.status.toLowerCase()}`}>{node.status}</span>
          <code title={node.isPlanned
            ? node.status === 'Canceled'
              ? 'Canceled before a Step event was recorded'
              : 'No Step event has been recorded'
            : undefined}
          >
            {node.isPlanned ? 'Execution ID unavailable' : node.id}
          </code>
        </span>
      </div>
      <div className="graph-methods">
        {showWaitFor && (
          <MethodSection
            name="WaitFor"
            status={waitForStatus(node)}
            event={node.waitFor ?? node.pendingWaitFor}
            isSelected={selectedEventId !== null && (node.waitFor ?? node.pendingWaitFor)?.eventId === selectedEventId}
            onSelect={onSelect}
          >
            {(timerCount > 0 || channelCount > 0 || subFlowCount > 0) && (
              <small className="graph-conditions">
                {timerCount > 0 && (
                  <i title={`${timerCount} timer condition${timerCount === 1 ? '' : 's'}`}>
                    <ConditionIcon type="timer" />
                    {timerCount} timer{timerCount === 1 ? '' : 's'}
                  </i>
                )}
                {channelCount > 0 && (
                  <i title={channelSummary}>
                    <ConditionIcon type="channel" />
                    <span className="graph-condition-names">
                      {channels.length > 0 ? shownChannels.visible.map((channel) => <span key={channel}>{channel}</span>) : channelSummary}
                      {shownChannels.hiddenCount > 0 && (
                        <span className="graph-condition-more">+{shownChannels.hiddenCount} more</span>
                      )}
                    </span>
                  </i>
                )}
                {subFlowCount > 0 && (
                  <i title={`${subFlowCount} SubFlow condition${subFlowCount === 1 ? '' : 's'}`}>
                    <ConditionIcon type="subflow" />
                    {subFlowCount} SubFlow{subFlowCount === 1 ? '' : 's'}
                  </i>
                )}
              </small>
            )}
          </MethodSection>
        )}
        <MethodSection
          name="Execute"
          status={executeMethodStatus}
          decision={decision}
          event={node.execute ?? node.pendingExecute}
          isSelected={selectedEventId !== null && (node.execute ?? node.pendingExecute)?.eventId === selectedEventId}
          onSelect={onSelect}
        />
      </div>
    </div>
  );
}

function SubFlowNodeLabel({ flow }: { flow: StepGraphNode }) {
  return (
    <Link
      className="graph-sub-flow-link nodrag nopan"
      to={`/v1/flows/${encodeURIComponent(flow.flowId ?? '')}`}
      aria-label={`Open SubFlow ${flow.flowId ?? ''}`}
    >
      <span><ConditionIcon type="subflow" />SubFlow · {flow.subFlowStatus}</span>
      <code title={flow.flowId}>{flow.flowId}</code>
      {flow.reusePolicy && <small>{flow.reusePolicy}</small>}
    </Link>
  );
}

/** An opened range of multi-Step Rounds shows Rounds; anything else shows Steps. */
function showsRounds(round: StepRoundGroup): boolean {
  return round.kind === 'range' && !round.hasSingleStepRounds;
}

function stepCountLabel(count: number): string {
  return `${count.toLocaleString('en-US')} Step${count === 1 ? '' : 's'}`;
}

/** A folded Round: its title, Step count, status bar, Step sequence and duration. */
export function RoundGroupLabel({ round, onOpen }: { round: StepRoundGroup; onOpen: (key: string) => void }) {
  const completedShare = round.stepCount > 0 ? (round.completedCount / round.stepCount) * 100 : 0;
  const failedShare = round.stepCount > 0 ? (round.failedCount / round.stepCount) * 100 : 0;
  return (
    <div className={`graph-round-group${round.failedCount > 0 ? ' has-failed' : ''}`}>
      <div className="graph-round-heading">
        <Repeat2 aria-hidden="true" className="graph-round-icon" size={15} strokeWidth={2.25} />
        <b title={round.title}>{round.title}</b>
        <span className="graph-round-count">{stepCountLabel(round.stepCount)}</span>
      </div>
      <div
        aria-label={`${round.completedCount} completed, ${round.failedCount} failed`}
        className="graph-round-bar"
        role="img"
      >
        <span className="graph-round-bar-completed" style={{ width: `${completedShare}%` }} />
        <span className="graph-round-bar-failed" style={{ width: `${failedShare}%` }} />
      </div>
      {round.sequence.length > 0 && (
        <p className="graph-round-sequence" title={round.sequence.join(' → ')}>{round.sequence.join(' → ')}</p>
      )}
      <div className="graph-round-facts">
        {round.roundRangeLabel && <span>{round.roundRangeLabel}</span>}
        {round.duration && <span>{round.duration}</span>}
        {round.failedCount > 0 && <span className="graph-round-failed">{round.failedCount.toLocaleString('en-US')} failed</span>}
        <button
          className="graph-round-action nodrag nopan"
          onClick={(clickEvent) => {
            clickEvent.stopPropagation();
            onOpen(round.key);
          }}
          type="button"
        >
          <ChevronRight aria-hidden="true" size={13} strokeWidth={2.5} />
          {showsRounds(round) ? 'Show Rounds' : 'Show Steps'}
        </button>
      </div>
    </div>
  );
}

/** The heading of an opened Round's frame, with Hide Steps unless it is the latest or a live Round. */
export function RoundFrameLabel({ round, onHide }: { round: StepRoundGroup; onHide: (key: string) => void }) {
  const facts = [round.roundRangeLabel, stepCountLabel(round.stepCount), round.duration].filter(Boolean).join(' · ');
  return (
    <div className="graph-round-frame-heading">
      <Repeat2 aria-hidden="true" className="graph-round-icon" size={14} strokeWidth={2.25} />
      <b>{round.title}</b>
      <span>{facts}</span>
      {round.canHide && (
        <button
          className="graph-round-action nodrag nopan"
          onClick={(clickEvent) => {
            clickEvent.stopPropagation();
            onHide(round.key);
          }}
          type="button"
        >
          <ChevronDown aria-hidden="true" size={13} strokeWidth={2.5} />
          {showsRounds(round) ? 'Hide Rounds' : 'Hide Steps'}
        </button>
      )}
    </div>
  );
}

/** Where edges meet a node: topology edges follow the direction, and SubFlow edges cross it. */
function handlePositions(direction: GraphDirection) {
  return direction === 'lr'
    ? { topologyIn: Position.Left, topologyOut: Position.Right, subFlowOut: Position.Bottom, subFlowIn: Position.Top }
    : { topologyIn: Position.Top, topologyOut: Position.Bottom, subFlowOut: Position.Right, subFlowIn: Position.Left };
}

/** The node kind that decides its handles; Round groups and gaps connect like Steps. */
function handleKind(data: StepNodeData): StepGraphNode['kind'] | 'frame' {
  if (data.display === 'frame') return 'frame';
  if (data.display !== 'node' || !data.model) return 'step';
  return data.model.kind;
}

function GraphNode({ data }: NodeProps<Node<StepNodeData>>) {
  const kind = handleKind(data);
  const positions = handlePositions(data.direction);
  if (kind === 'frame') return <div className="graph-node-root">{data.label}</div>;
  return (
    <div className="graph-node-root">
      {kind === 'step' && (
        <Handle id="topology" position={positions.topologyIn} type="target" />
      )}
      <div className="graph-node-body">{data.label}</div>
      {(kind === 'source' || kind === 'step') && (
        <Handle id="topology" position={positions.topologyOut} type="source" />
      )}
      {kind === 'step' && data.hasSubFlowChildren === true && (
        <Handle
          className="graph-subflow-handle"
          id="subflow"
          position={positions.subFlowOut}
          type="source"
        />
      )}
      {kind === 'subflow' && (
        <Handle
          className="graph-subflow-handle"
          id="subflow"
          position={positions.subFlowIn}
          type="target"
        />
      )}
    </div>
  );
}

const graphNodeTypes: NodeTypes = { graph: GraphNode };

export function graphNodeDimensions(node: StepGraphNode): { height: number; width: number } {
  if (node.kind !== 'step') return { height: 90, width: 220 };
  if (!showsWaitFor(node)) return { height: stepNodeHeight.executeOnly, width: 300 };
  const { kinds, channelLines } = conditionLineCounts(node);
  const extraKindLines = Math.max(0, kinds - 1);
  const extraChannelLines = Math.max(0, channelLines - 1);
  return {
    height: stepNodeHeight.withWaitFor
      + extraKindLines * stepNodeHeight.perExtraConditionLine
      + extraChannelLines * stepNodeHeight.perExtraChannelLine,
    width: 300,
  };
}

function foldedItemDimensions(item: FoldedGraphItem): { height: number; width: number } {
  if (item.display === 'node') return graphNodeDimensions(item.node);
  if (item.display === 'more') return elisionGapDimensions;
  return {
    width: foldedGroupDimensions.width,
    height: item.round.sequence.length > 0 ? foldedGroupDimensions.withSequenceHeight : foldedGroupDimensions.height,
  };
}

function nodeHandles(
  kind: StepGraphNode['kind'],
  dimensions: { height: number; width: number },
  hasSubFlowChildren: boolean,
  direction: GraphDirection,
): Node['handles'] {
  const box = { x: 0, y: 0, width: dimensions.width, height: dimensions.height };
  const positions = handlePositions(direction);
  if (kind === 'subflow') {
    return [{ ...box, id: 'subflow', type: 'target', position: positions.subFlowIn }];
  }
  if (kind === 'source') {
    return [{ ...box, id: 'topology', type: 'source', position: positions.topologyOut }];
  }
  const handles: NonNullable<Node['handles']> = [
    { ...box, id: 'topology', type: 'target', position: positions.topologyIn },
    { ...box, id: 'topology', type: 'source', position: positions.topologyOut },
  ];
  if (hasSubFlowChildren) {
    handles.push({ ...box, id: 'subflow', type: 'source', position: positions.subFlowOut });
  }
  return handles;
}

/** The same-zoom viewport that brings a box into view, top left first; null when already shown. */
function viewportShowingBox(
  viewport: { x: number; y: number; zoom: number },
  canvas: { width: number; height: number },
  box: { x: number; y: number; width: number; height: number },
): { x: number; y: number; zoom: number } | null {
  const axisOffset = (offset: number, start: number, length: number, canvasLength: number) => {
    const shownStart = start * viewport.zoom + offset;
    const shownEnd = shownStart + length * viewport.zoom;
    if (length * viewport.zoom > canvasLength - revealMargin * 2 || shownStart < revealMargin) {
      return revealMargin - start * viewport.zoom;
    }
    if (shownEnd > canvasLength - revealMargin) return canvasLength - revealMargin - (start + length) * viewport.zoom;
    return offset;
  };
  const x = axisOffset(viewport.x, box.x, box.width, canvas.width);
  const y = axisOffset(viewport.y, box.y, box.height, canvas.height);
  return x === viewport.x && y === viewport.y ? null : { x, y, zoom: viewport.zoom };
}

/** The box of the latest Round and the node just before it, or null when nothing is folded. */
export interface NewestFoldedPart {
  /** The latest Round's frame with the node just before it. */
  x: number;
  width: number;
  /** Left and right edges of each top-level node and outer frame, in graph units. */
  units: Array<{ left: number; right: number }>;
}

function newestFoldedBox(nodes: Array<Node<StepNodeData>>, folded: FoldedStepGraph): NewestFoldedPart | null {
  const latest = folded.rounds.find((round) => round.isLatest);
  const frame = latest && nodes.find((node) => node.id === roundFrameNodeID(latest.key));
  if (!frame) return null;
  const widthOf = (node: Node<StepNodeData>) => Number(node.width ?? 0);
  const units = nodes
    .filter((node) => (node.data.display === 'frame' ? !node.data.round?.parentKey : !node.data.frameKeys?.length))
    .map((node) => ({ left: node.position.x, right: node.position.x + widthOf(node) }));
  const previous = units
    .filter((unit) => unit.right <= frame.position.x)
    .reduce<{ left: number } | undefined>((nearest, unit) => (!nearest || unit.left > nearest.left ? unit : nearest), undefined);
  const left = previous ? previous.left : frame.position.x;
  return { x: left, width: frame.position.x + widthOf(frame) - left, units };
}

/** Keeps the newest part's right edge in view, then as many whole earlier nodes as fit. */
export function newestFoldedViewportX(newest: NewestFoldedPart, zoom: number, canvasWidth: number): number {
  const right = newest.x + newest.width;
  const availableWidth = (canvasWidth - revealMargin * 2) / zoom;
  if (newest.width > availableWidth) return canvasWidth - revealMargin - right * zoom;
  const candidates = newest.units.map((unit) => unit.left).filter((left) => left <= newest.x).sort((a, b) => a - b);
  for (const candidate of candidates) {
    // A node that crosses the candidate edge would be cut, so the edge moves to its left.
    const left = Math.min(candidate, ...newest.units.filter((unit) => unit.right > candidate && unit.left < right).map((unit) => unit.left));
    if (right - left <= availableWidth) return revealMargin - left * zoom;
  }
  return revealMargin - newest.x * zoom;
}

/** Adds a frame behind each opened Round and moves the graph so every frame heading fits. */
function withRoundFrames(
  nodes: Array<Node<StepNodeData>>,
  folded: FoldedStepGraph,
  direction: GraphDirection,
  onHide: (key: string) => void,
): Array<Node<StepNodeData>> {
  if (folded.rounds.length === 0) return nodes;
  const boxes = new Map(nodes.map((node) => [node.id, {
    x: node.position.x,
    y: node.position.y,
    width: typeof node.style?.width === 'number' ? node.style.width : node.width ?? 0,
    height: typeof node.style?.height === 'number' ? node.style.height : node.height ?? 0,
  }]));
  const frames = roundFrameBoxes(folded.items, boxes);
  if (frames.length === 0) return nodes;
  const shiftX = Math.max(0, frameTopClearance - Math.min(...frames.map((frame) => frame.x)));
  const shiftY = Math.max(0, frameTopClearance - Math.min(...frames.map((frame) => frame.y)));
  const roundByKey = new Map(folded.rounds.map((round) => [round.key, round]));
  // Larger frames first, so a range's frame draws behind the Round frames inside it.
  const outerFramesFirst = [...frames].sort((left, right) => right.width * right.height - left.width * left.height);
  const frameNodes = outerFramesFirst.flatMap((frame): Array<Node<StepNodeData>> => {
    const round = roundByKey.get(frame.roundKey);
    if (!round) return [];
    const size = { width: frame.width, height: frame.height };
    return [{
      id: roundFrameNodeID(round.key),
      type: 'graph',
      position: { x: frame.x + shiftX, y: frame.y + shiftY },
      className: `graph-round-frame${round.failedCount > 0 ? ' graph-round-frame-failed' : ''}`,
      ...size,
      // Frames only draw behind their Steps; the canvas keeps panning through them.
      style: { ...size, pointerEvents: 'none' },
      zIndex: -1,
      focusable: false,
      selectable: false,
      handles: [],
      data: { direction, display: 'frame', round, label: <RoundFrameLabel round={round} onHide={onHide} /> },
    }];
  });
  const shifted = shiftX === 0 && shiftY === 0
    ? nodes
    : nodes.map((node) => ({ ...node, position: { x: node.position.x + shiftX, y: node.position.y + shiftY } }));
  return [...frameNodes, ...shifted];
}

export function ExecutionGraph({
  flowId,
  events,
  hasEarlierEvents = false,
  state,
  selectedEvent,
  onSelectEvent,
  direction = 'tb',
}: {
  /** Top to bottom by default; left to right in the bounded box above a run's event panel. */
  direction?: GraphDirection;
  flowId: string;
  events: FlowHistoryEvent[];
  /** True when the events omit history before the newest window, so missing sources are earlier events. */
  hasEarlierEvents?: boolean;
  state: FlowState | null;
  selectedEvent: FlowHistoryEvent | null;
  onSelectEvent: (event: FlowHistoryEvent | null) => void;
}) {
  const graphCanvas = useRef<HTMLDivElement>(null);
  const [canvasSize, setCanvasSize] = useState({ width: 0, height: 0 });
  const canvasWidth = canvasSize.width;
  const isLeftToRight = direction === 'lr';
  const [isMiniMapHidden, setIsMiniMapHidden] = useStoredGraphFlag(graphViewStorageKeys.isMiniMapHidden, false);
  const [isFolded, setIsFolded] = useStoredGraphFlag(graphViewStorageKeys.isFolded, false);
  const [isFoldedIntroDismissed, setIsFoldedIntroDismissed] = useStoredGraphFlag(
    graphViewStorageKeys.isFoldedIntroDismissed,
    false,
  );
  const [isFoldedIntroLeaving, setIsFoldedIntroLeaving] = useState(false);
  const [openIntents, setOpenIntents] = useState<GroupOpenIntents>(noGroupOpenIntents);
  // The Round anchor stays while it still repeats, so new events never regroup the whole run.
  const preferredAnchorStepType = useRef({ flowId, stepType: '' });
  if (preferredAnchorStepType.current.flowId !== flowId) preferredAnchorStepType.current = { flowId, stepType: '' };
  const [flowInstance, setFlowInstance] = useState<ReactFlowInstance<Node<StepNodeData>, Edge> | null>(null);
  const graph = useMemo(
    () => buildStepGraph(events, state?.activeStepExecutions ?? [], flowId, { hasEarlierEvents }),
    [events, flowId, hasEarlierEvents, state],
  );
  const selectedStepExecutionID = useMemo(
    () => stepGraphSelection(graph.nodes, graph.edges, selectedEvent).selectedStepExecutionID,
    [graph.edges, graph.nodes, selectedEvent],
  );
  const folded = useMemo<FoldedStepGraph>(
    () => (isFolded
      ? foldStepGraph(graph.nodes, graph.edges, {
        openIntents,
        selectedStepExecutionID,
        preferredAnchorStepType: preferredAnchorStepType.current.stepType,
        historyEvents: events,
      })
      : unfoldedStepGraph(graph.nodes, graph.edges)),
    [events, graph.edges, graph.nodes, isFolded, openIntents, selectedStepExecutionID],
  );
  useEffect(() => {
    if (folded.anchorStepType) preferredAnchorStepType.current = { flowId, stepType: folded.anchorStepType };
  }, [flowId, folded.anchorStepType]);
  const shownStepNodes = useMemo(
    () => folded.items.flatMap((item) => (item.display === 'node' ? [item.node] : [])),
    [folded.items],
  );
  const selection = useMemo(
    () => stepGraphSelection(shownStepNodes, folded.edges, selectedEvent),
    [folded.edges, selectedEvent, shownStepNodes],
  );
  const shownNodeByID = useMemo(
    () => new Map(shownStepNodes.map((node) => [node.id, node])),
    [shownStepNodes],
  );
  const subFlowParentIDs = useMemo(
    () => new Set(
      shownStepNodes
        .filter((node) => node.kind === 'subflow')
        .map((node) => node.parentStepId),
    ),
    [shownStepNodes],
  );

  // Each newly selected event opens the groups that hold its Step; Hide can fold them again.
  const selectedEventId = selectedEvent?.eventId ?? null;
  useEffect(() => {
    if (!isFolded || selectedEventId === null || !selectedStepExecutionID) return;
    setOpenIntents((current) => withStepExecutionOpened(current, selectedStepExecutionID));
  }, [isFolded, selectedEventId, selectedStepExecutionID]);

  const revealedRoundKey = useRef('');
  const setRoundOpen = (key: string, isOpen: boolean) => {
    const group = folded.rounds.find((round) => round.key === key);
    if (!group) return;
    if (isOpen) revealedRoundKey.current = key;
    setOpenIntents((current) => withGroupOpenChoice(current, group, isOpen));
  };

  const edges = useMemo<Edge[]>(() => folded.edges.map((edge) => {
    const isIncoming = selection.incomingEdgeIDs.has(edge.id);
    const isOutgoing = selection.outgoingEdgeIDs.has(edge.id);
    const target = shownNodeByID.get(edge.target);
    const isSubFlow = target?.kind === 'subflow';
    const isWaitingTarget = target?.kind === 'step' && (target.status === 'Waiting' || target.status === 'Pending');
    const color = isIncoming || isOutgoing ? connectedStepColor : isWaitingTarget ? waitingEdgeColor : graphEdgeColor;
    return {
      ...edge,
      className: [
        isSubFlow ? 'graph-edge-subflow' : isIncoming ? 'graph-edge-incoming' : isOutgoing ? 'graph-edge-outgoing' : '',
        isWaitingTarget ? 'graph-edge-waiting' : '',
      ].filter(Boolean).join(' ') || undefined,
      type: 'smoothstep',
      sourceHandle: isSubFlow ? 'subflow' : 'topology',
      targetHandle: isSubFlow ? 'subflow' : 'topology',
      markerEnd: { type: MarkerType.ArrowClosed, width: 12, height: 12, color },
      style: {
        stroke: color,
        strokeWidth: isIncoming || isOutgoing ? 2.25 : 1.25,
        strokeDasharray: isSubFlow || isWaitingTarget ? '5 4' : undefined,
      },
      zIndex: isIncoming || isOutgoing ? 2 : 0,
    };
  }), [folded.edges, selection, shownNodeByID]);
  const nodes = useMemo(() => {
    const raw: Array<Node<StepNodeData>> = folded.items.map((item) => {
      const dimensions = foldedItemDimensions(item);
      if (item.display !== 'node') {
        return {
          id: item.id,
          type: 'graph',
          position: { x: 0, y: 0 },
          className: item.display === 'group'
            ? `step-flow-node node-round-group${item.round.failedCount > 0 ? ' node-round-group-failed' : ''}`
            : 'step-flow-node node-round-more',
          width: dimensions.width,
          height: dimensions.height,
          style: dimensions,
          handles: nodeHandles('step', dimensions, false, direction),
          data: {
            direction,
            display: item.display,
            frameKeys: item.frameKeys,
            round: item.display === 'group' ? item.round : undefined,
            label: item.display === 'group' ? (
              <RoundGroupLabel round={item.round} onOpen={(key) => setRoundOpen(key, true)} />
            ) : (
              <span className="graph-round-more">
                … {item.hiddenCount.toLocaleString('en-US')} more{item.unit === 'Rounds' ? ' Rounds' : ''}
              </span>
            ),
          },
        };
      }
      const { node } = item;
      return {
        id: node.id,
        type: 'graph',
        position: { x: 0, y: 0 },
        className: [
          'step-flow-node',
          `node-${node.kind}`,
          `node-${node.status.toLowerCase()}`,
          item.repeatCount > 1 ? 'node-repeated' : '',
          node.id === selection.selectedStepExecutionID ? 'graph-node-current' : '',
          selection.previousStepExecutionIDs.has(node.id) ? 'graph-node-previous' : '',
          selection.nextStepExecutionIDs.has(node.id) ? 'graph-node-next' : '',
        ].filter(Boolean).join(' '),
        width: dimensions.width,
        height: dimensions.height,
        style: dimensions,
        handles: nodeHandles(node.kind, dimensions, subFlowParentIDs.has(node.id), direction),
        data: {
          direction,
          display: 'node',
          frameKeys: item.frameKeys,
          model: node,
          hasSubFlowChildren: subFlowParentIDs.has(node.id),
          label: node.kind === 'step' ? (
            <StepNodeLabel
              node={node}
              onSelect={onSelectEvent}
              repeatCount={item.repeatCount}
              selectedEventId={selectedEvent?.eventId ?? null}
            />
          ) : node.kind === 'subflow' ? (
            <SubFlowNodeLabel flow={node} />
          ) : (
            <div className="graph-node-label">
              <span className="graph-node-kind">
                <StatusIcon status={node.kind === 'source' ? 'Source' : node.status} size={13} />
                {node.kind === 'source' ? 'Source' : node.status}
              </span>
              <b>{node.label}</b>
            </div>
          ),
        },
      };
    });
    const laidOut = layoutGraph(raw, edges, direction);
    return withRoundFrames(laidOut, folded, direction, (key) => setRoundOpen(key, false));
  }, [direction, edges, folded, onSelectEvent, selectedEvent?.eventId, selection, subFlowParentIDs]);
  const bounds = useMemo(() => graphBounds(nodes), [nodes]);
  // Left to right fits the whole graph while readable; otherwise it fits the height and frames the selection.
  // The whole graph ends left of the mini map, so it never covers the latest Step.
  const shownCanvasWidth = isLeftToRight && !isMiniMapHidden
    ? Math.max(canvasWidth / 2, canvasWidth - miniMapReservedWidth)
    : canvasWidth;
  const fitZoom = fittingGraphZoom(bounds, { width: shownCanvasWidth, height: canvasSize.height });
  const isWholeGraphShown = isLeftToRight && fitZoom >= readableFitZoom;
  const heightFitZoom = fittingGraphZoom({ width: 1, height: bounds.height }, { width: 1, height: canvasSize.height });
  const initialZoom = !isLeftToRight
    ? defaultGraphZoom(bounds.width, canvasWidth)
    : isWholeGraphShown ? fitZoom : Math.max(minimumGraphZoom, Math.min(framedGraphZoom, heightFitZoom));
  useEffect(() => {
    const canvas = graphCanvas.current;
    if (!canvas) return undefined;
    const updateSize = () => setCanvasSize((current) => (
      current.width === canvas.clientWidth && current.height === canvas.clientHeight
        ? current
        : { width: canvas.clientWidth, height: canvas.clientHeight }
    ));
    updateSize();
    const observer = new ResizeObserver(updateSize);
    observer.observe(canvas);
    return () => observer.disconnect();
  }, []);

  // The first viewport is set once; later events, layouts and resizes keep a reader's zoom and pan.
  const hasAppliedInitialViewport = useRef(false);
  const isViewportTransitionWanted = useRef(false);
  const newestFramedViewport = useRef<{ x: number; y: number; zoom: number } | null>(null);
  useEffect(() => {
    if (hasAppliedInitialViewport.current || !flowInstance || canvasWidth <= 0) return;
    hasAppliedInitialViewport.current = true;
    // A folded graph too wide to fit opens on its newest part instead of its left edge.
    const newest = isFolded && isLeftToRight && !isWholeGraphShown ? newestFoldedBox(nodes, folded) : null;
    const x = newest
      ? newestFoldedViewportX(newest, initialZoom, shownCanvasWidth)
      : Math.max(0, (shownCanvasWidth - bounds.width * initialZoom) / 2);
    const viewport = {
      x,
      y: isLeftToRight ? Math.max(0, (canvasSize.height - bounds.height * initialZoom) / 2) : 0,
      zoom: initialZoom,
    };
    newestFramedViewport.current = newest ? viewport : null;
    void flowInstance.setViewport(viewport, isViewportTransitionWanted.current ? { duration: viewportTransitionDuration() } : undefined);
  }, [bounds.height, bounds.width, canvasSize.height, canvasWidth, flowInstance, folded, initialZoom, isFolded, isLeftToRight, isWholeGraphShown, nodes, shownCanvasWidth]);

  const dismissFoldedIntro = () => {
    if (isFoldedIntroDismissed) return;
    setIsFoldedIntroDismissed(true);
    setIsFoldedIntroLeaving(true);
  };
  useEffect(() => {
    if (!isFoldedIntroLeaving) return undefined;
    const timer = window.setTimeout(() => setIsFoldedIntroLeaving(false), introLeaveMilliseconds);
    return () => window.clearTimeout(timer);
  }, [isFoldedIntroLeaving]);
  // Folding lays the graph out anew, so the viewport is set again.
  const toggleFolded = () => {
    hasAppliedInitialViewport.current = false;
    framedEventId.current = null;
    isViewportTransitionWanted.current = true;
    setIsFolded(!isFolded);
    dismissFoldedIntro();
  };

  // Frames the selected Step on first show, and later only when a newly selected Step is out of view.
  const framedEventId = useRef<number | null>(null);
  useEffect(() => {
    if (!flowInstance || !selectedEvent || !hasAppliedInitialViewport.current) return undefined;
    if (framedEventId.current === selectedEvent.eventId) return undefined;
    const element = graphCanvas.current?.querySelector<HTMLElement>('.graph-method.is-selected')
      ?.closest<HTMLElement>('.react-flow__node');
    const node = element?.dataset.id ? flowInstance.getNode(element.dataset.id) : undefined;
    if (!node) return undefined;
    const isFirstFrame = framedEventId.current === null;
    framedEventId.current = selectedEvent.eventId;
    const width = node.measured?.width ?? node.width ?? 0;
    const height = node.measured?.height ?? node.height ?? 0;
    // The newest-part framing already shows the latest Steps, so it moves only for a Step out of view.
    const shouldCenter = isFirstFrame && !newestFramedViewport.current
      ? !isWholeGraphShown
      : !isNodeInView(newestFramedViewport.current ?? flowInstance.getViewport(), canvasSize, node.position, { width, height });
    newestFramedViewport.current = null;
    if (!shouldCenter) return undefined;
    // Pan the graph, not the page, so the bar and the run line stay in view.
    // A frame later, so the initial viewport above has been set first.
    const frame = window.requestAnimationFrame(() => {
      void flowInstance.setCenter(node.position.x + width / 2, node.position.y + height / 2, {
        zoom: flowInstance.getZoom(),
        duration: isViewportTransitionWanted.current ? viewportTransitionDuration() : undefined,
      });
    });
    return () => window.cancelAnimationFrame(frame);
  }, [canvasSize, flowInstance, isWholeGraphShown, nodes, selectedEvent]);

  // After Show Steps or Show Rounds, pans so the opened group's frame is in view; zoom stays.
  useEffect(() => {
    const key = revealedRoundKey.current;
    if (!key || !flowInstance) return;
    revealedRoundKey.current = '';
    const frame = nodes.find((node) => node.id === roundFrameNodeID(key));
    if (!frame) return;
    const viewport = flowInstance.getViewport();
    const box = { ...frame.position, width: Number(frame.width ?? 0), height: Number(frame.height ?? 0) };
    const shown = viewportShowingBox(viewport, canvasSize, box);
    if (shown) void flowInstance.setViewport(shown, { duration: viewportTransitionDuration() });
  }, [canvasSize, flowInstance, nodes]);

  const wholeGraphZoom = fittingGraphZoom(bounds, { width: canvasWidth * fitPadding, height: canvasSize.height * fitPadding });
  /** Fits the whole graph into the box, however small, since Fit View must show everything. */
  const fitWholeGraph = () => {
    if (!flowInstance || canvasWidth <= 0) return;
    const zoom = Math.min(maximumGraphZoom, wholeGraphZoom);
    void flowInstance.setViewport({
      x: (canvasWidth - bounds.width * zoom) / 2,
      y: (canvasSize.height - bounds.height * zoom) / 2,
      zoom,
    }, { duration: 200 });
  };
  // Zooming out can always reach the whole graph.
  const lowestZoom = Math.min(controlMinimumZoom, wholeGraphZoom);

  const isFoldedIntroShown = !isFoldedIntroDismissed || isFoldedIntroLeaving;
  const centerOnMiniMapPoint = (position: { x: number; y: number }) => {
    if (!flowInstance) return;
    void flowInstance.setCenter(position.x, position.y, {
      zoom: flowInstance.getZoom(),
      duration: viewportTransitionDuration(),
    });
  };

  if (!nodes.length) return <div className="card empty-state"><h3>No execution topology loaded</h3></div>;
  return (
    <div className={`graph-view${isLeftToRight ? ' graph-view--lr' : ''}${isFolded ? ' graph-view--folded' : ''}`}>
      <div className="view-toolbar graph-toolbar">
        <h2 className="sr-only">Execution graph</h2>
        <div className="graph-fold-controls">
          <button
            aria-pressed={isFolded}
            className="graph-fold-toggle"
            onClick={toggleFolded}
            title="Fold repeated Rounds of Steps"
            type="button"
          >
            <FoldHorizontal aria-hidden="true" size={14} strokeWidth={2.25} />
            Folded
          </button>
          {isFolded && folded.rounds.length === 0 && (
            <span className="graph-fold-note">No Step type repeats, so nothing folds.</span>
          )}
          {isFoldedIntroShown && (
            <span className={`graph-fold-intro${isFoldedIntroLeaving ? ' is-leaving' : ''}`} role="note">
              <span><b>Folded</b> groups repeated Rounds of Steps into one node. Turn it on for long runs.</span>
              <button
                aria-label="Dismiss"
                className="graph-fold-intro-dismiss"
                disabled={isFoldedIntroLeaving}
                onClick={dismissFoldedIntro}
                title="Dismiss"
                type="button"
              >
                <X aria-hidden="true" size={13} strokeWidth={2.5} />
              </button>
            </span>
          )}
        </div>
        <div className="graph-legend">
          <span><i className="legend-source" />Source</span>
          <span><i className="legend-active" />Active / Pending</span>
          <span><i className="legend-waiting" />Waiting</span>
          <span><i className="legend-failed" />Failed</span>
          <span><i className="legend-canceled" />Canceled</span>
          <span><i className="legend-subflow" />SubFlow</span>
          {isFolded && folded.rounds.length > 0 && <span><i className="legend-folded" />Folded Round</span>}
        </div>
      </div>
      <div
        className="graph-canvas"
        ref={graphCanvas}
        style={isLeftToRight
          ? undefined
          : { height: `min(${Math.max(650, bounds.height * initialZoom)}px, max(480px, calc(100vh - 220px)))` }}
      >
        <ReactFlow
          proOptions={{ hideAttribution: true }}
          nodes={nodes}
          edges={edges}
          nodeTypes={graphNodeTypes}
          minZoom={isLeftToRight ? lowestZoom : minimumGraphZoom}
          maxZoom={maximumGraphZoom}
          elementsSelectable={false}
          nodesConnectable={false}
          nodesDraggable={false}
          preventScrolling={false}
          zoomOnScroll={false}
          // In the bounded box a wheel scrolls the page; dragging pans the graph.
          panOnScroll={!isLeftToRight}
          onInit={setFlowInstance}
          onNodeClick={(_, node) => {
            const data = node.data as StepNodeData;
            if (data.display === 'group' && data.round) {
              setRoundOpen(data.round.key, true);
              return;
            }
            const { model } = data;
            if (data.display !== 'node' || !model || model.kind === 'subflow') return;
            onSelectEvent(model.execute ?? model.pendingExecute ?? model.waitFor ?? model.pendingWaitFor ?? null);
          }}
        >
          <Background gap={22} size={1} color="var(--line-faint)" />
          {/* Nodes are never draggable or selectable, so the lock has nothing to toggle. */}
          <Controls position="top-right" showFitView={false} showInteractive={false}>
            <ControlButton aria-label="Fit view" className="react-flow__controls-fitview" onClick={fitWholeGraph} title="Fit view">
              <Maximize aria-hidden="true" size={12} strokeWidth={2.5} />
            </ControlButton>
          </Controls>
          {isMiniMapHidden ? (
            <button
              aria-label="Show mini map"
              className="graph-minimap-show nodrag nopan"
              onClick={() => setIsMiniMapHidden(false)}
              title="Show mini map"
              type="button"
            >
              <MapIcon aria-hidden="true" size={15} strokeWidth={2} />
            </button>
          ) : (
            <div className="graph-minimap-shell nodrag nopan">
              <div className="graph-minimap-heading">
                <span>Mini map</span>
                <button
                  aria-label="Hide mini map"
                  className="graph-minimap-hide"
                  onClick={() => setIsMiniMapHidden(true)}
                  title="Hide mini map"
                  type="button"
                >
                  <Minus aria-hidden="true" size={13} strokeWidth={2.5} />
                </button>
              </div>
              <GraphOverview bounds={bounds} nodes={nodes} onCenter={centerOnMiniMapPoint} />
            </div>
          )}
        </ReactFlow>
      </div>
    </div>
  );
}
