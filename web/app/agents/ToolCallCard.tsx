// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ReactNode } from 'react';
import {
  Ban,
  ChevronRight,
  CircleAlert,
  CircleCheck,
  CircleDashed,
  CircleMinus,
  Clock,
  FilePenLine,
  FileText,
  Globe,
  Hourglass,
  LoaderCircle,
  MessageSquare,
  Search,
  ShieldAlert,
  SquareTerminal,
  TriangleAlert,
  Wrench,
  type LucideIcon,
} from 'lucide-react';
import { formatElapsedDuration } from '@/lib/timeline';
import { SESSION_COPY } from './copy';
import type { ToolCallState, ToolCallView } from './sessionModel';
import './session.css';

/** How loudly a call's state reads: colour is spent on what needs a person. */
type ToolCallTone = 'attention' | 'danger' | 'active' | 'waiting' | 'success' | 'quiet';

const STATE_LOOK: Record<ToolCallState, { tone: ToolCallTone; icon: LucideIcon; isLabelShown: boolean }> = {
  awaiting_approval: { tone: 'attention', icon: ShieldAlert, isLabelShown: true },
  awaiting_answer: { tone: 'attention', icon: MessageSquare, isLabelShown: true },
  failed: { tone: 'danger', icon: CircleAlert, isLabelShown: true },
  outcome_unknown: { tone: 'danger', icon: TriangleAlert, isLabelShown: true },
  recording: { tone: 'danger', icon: TriangleAlert, isLabelShown: true },
  running: { tone: 'active', icon: LoaderCircle, isLabelShown: true },
  retrying: { tone: 'active', icon: LoaderCircle, isLabelShown: true },
  queued: { tone: 'waiting', icon: Clock, isLabelShown: true },
  write_queued: { tone: 'waiting', icon: Clock, isLabelShown: true },
  waiting: { tone: 'waiting', icon: Hourglass, isLabelShown: true },
  deciding: { tone: 'waiting', icon: Clock, isLabelShown: true },
  done: { tone: 'success', icon: CircleCheck, isLabelShown: false },
  returned: { tone: 'success', icon: CircleCheck, isLabelShown: false },
  rejected: { tone: 'quiet', icon: Ban, isLabelShown: true },
  skipped: { tone: 'quiet', icon: CircleMinus, isLabelShown: true },
  cancelled: { tone: 'quiet', icon: CircleMinus, isLabelShown: true },
  no_result: { tone: 'quiet', icon: CircleDashed, isLabelShown: true },
};

/** States whose row opens by itself, so the decision or the failure is in view. */
const OPEN_STATES = new Set<ToolCallState>(['awaiting_approval', 'awaiting_answer', 'failed', 'outcome_unknown', 'recording']);

/** One tool call as a compact row that opens to its input and output; `children` holds a waiting call's controls. */
export function ToolCallCard({
  call,
  children,
  onTellAgent,
  now = Date.now(),
}: {
  call: ToolCallView;
  children?: ReactNode;
  /** Shown on an outcome-unknown call; omit it where no steer control exists. */
  onTellAgent?: (call: ToolCallView) => void;
  now?: number;
}) {
  const look = STATE_LOOK[call.state];
  const StatusIcon = look.icon;
  const toolKind = toolKindOf(call.toolName);
  const ToolIcon = TOOL_KIND_ICON[toolKind];
  const stateLabel = SESSION_COPY.state[call.state];
  const summary = toolArgumentSummary(call.argumentsText);
  const detail = toolCallDetail(call, now);
  const isDetailInline = detail !== '' && call.state !== 'done' && call.state !== 'no_result';
  const argumentsText = prettyJSON(call.argumentsText);
  const isErrorResult = ['failed', 'outcome_unknown', 'skipped', 'recording'].includes(call.state);
  const resultText = isErrorResult ? call.error : call.output;
  const effect = call.effect === null ? null : SESSION_COPY.effect[call.effect] ?? call.effect;
  return (
    <article
      aria-label={`${call.toolName} ${stateLabel}`}
      className="atc"
      data-source={call.source}
      data-state={call.state}
      data-testid={`tool-call-${call.toolCallId}`}
      data-tone={look.tone}
    >
      <details className="atc-disclosure" open={OPEN_STATES.has(call.state)}>
        <summary className="atc-row">
          <span className="atc-icon" aria-hidden="true" data-kind={toolKind}><ToolIcon size={15} strokeWidth={2} /></span>
          <span className="atc-main">
            <span className="atc-line">
              <span className="atc-name">{call.toolName}</span>
              {summary && <span className="atc-summary">{summary}</span>}
            </span>
            {isDetailInline && <span className="atc-detail">{detail}</span>}
          </span>
          <span className="atc-status" data-tone={look.tone} title={stateLabel}>
            <StatusIcon aria-hidden="true" size={15} strokeWidth={2.2} />
            {look.isLabelShown && <span>{stateLabel}</span>}
          </span>
          <ChevronRight aria-hidden="true" className="atc-chevron" size={15} strokeWidth={2} />
        </summary>
        <div className="atc-body">
          {!isDetailInline && detail !== '' && <p className="atc-note">{detail}</p>}
          {argumentsText !== '' && (
            <div className="atc-block">
              <span className="atc-blockhead">{SESSION_COPY.arguments}</span>
              <pre aria-label={SESSION_COPY.arguments}>{argumentsText}</pre>
            </div>
          )}
          {resultText && (
            <div className="atc-block" data-kind={isErrorResult ? 'error' : 'output'}>
              <span className="atc-blockhead">{isErrorResult ? SESSION_COPY.error : SESSION_COPY.output}</span>
              <pre>{prettyJSON(resultText)}</pre>
            </div>
          )}
          <p className="atc-meta">
            {effect && <span className="atc-effect">{effect}</span>}
            <span className="atc-id" title={SESSION_COPY.toolCallId}>{call.toolCallId}</span>
            {call.stepExecutionId && <span className="atc-id" title={SESSION_COPY.writtenBy}>{call.stepExecutionId}</span>}
          </p>
        </div>
      </details>
      {children}
      {call.state === 'outcome_unknown' && onTellAgent && (
        <div className="agc-buttons">
          <button className="agc-secondary" onClick={() => onTellAgent(call)} type="button">
            {SESSION_COPY.tellAgent}
          </button>
        </div>
      )}
    </article>
  );
}

/** The sentence under a call's name, which says what the state means for the operator. */
export function toolCallDetail(call: ToolCallView, now: number): string {
  const approved = call.requiresApproval === true && !['awaiting_approval', 'rejected', 'cancelled'].includes(call.state)
    ? `${SESSION_COPY.approvedNote} `
    : '';
  switch (call.state) {
    case 'queued': {
      if (!call.stepType) return `${approved}${SESSION_COPY.startingDetail}`;
      const since = call.since ? Date.parse(call.since) : Number.NaN;
      const waited = Number.isFinite(since) ? formatElapsedDuration(now - since) : '';
      const attempt = call.attempt !== undefined && call.attempt > 1
        ? ` ${SESSION_COPY.attempt(call.attempt, call.maxAttempts)}`
        : '';
      return `${approved}${SESSION_COPY.queuedDetail(call.pendingReason, waited, call.attempt)}${attempt}`;
    }
    case 'running':
      return `${approved}${SESSION_COPY.runningDetail} ${SESSION_COPY.attempt(call.attempt, call.maxAttempts)}`.trim();
    case 'retrying': {
      const at = call.since ? new Date(call.since).toLocaleTimeString() : '';
      return `${SESSION_COPY.retryingDetail(at)} ${SESSION_COPY.attempt(call.attempt, call.maxAttempts)}`.trim();
    }
    case 'recording': return SESSION_COPY.recordingDetail(call.stepType ?? '');
    case 'deciding': return SESSION_COPY.decidingDetail;
    case 'waiting': return SESSION_COPY.waitingDetail;
    case 'outcome_unknown': return SESSION_COPY.outcomeUnknownDetail;
    case 'skipped':
      return call.skippedAfterWrite ? SESSION_COPY.skippedAfterWriteDetail : SESSION_COPY.skippedDetail;
    case 'write_queued': return SESSION_COPY.writeQueuedDetail;
    case 'cancelled': return SESSION_COPY.cancelledDetail(call.cancellationReason);
    case 'rejected': return SESSION_COPY.rejectedDetail(call.rejectionMessage);
    case 'no_result': return SESSION_COPY.noResultDetail;
    case 'done': return approved.trim();
    default: return '';
  }
}

/** Argument names whose value says what a call is about, most telling first. */
const SUMMARY_KEYS = ['command', 'cmd', 'pattern', 'query', 'file_path', 'path', 'url', 'question', 'message', 'description', 'prompt', 'text', 'name'];
const SUMMARY_MAX_LENGTH = 160;

/** One line that says what a call works on, such as its command or file, from its JSON arguments. */
export function toolArgumentSummary(argumentsText: string): string {
  const text = argumentsText.trim();
  if (text === '') return '';
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return clip(text.replace(/\s+/g, ' '));
  }
  if (typeof parsed !== 'object' || parsed === null || Array.isArray(parsed)) {
    return clip(typeof parsed === 'string' ? parsed : JSON.stringify(parsed));
  }
  const fields = parsed as Record<string, unknown>;
  for (const key of SUMMARY_KEYS) {
    const value = fields[key];
    if (typeof value === 'string' && value.trim() !== '') return clip(shortenPath(value.trim()));
  }
  const scalars = Object.entries(fields)
    .filter(([, value]) => ['string', 'number', 'boolean'].includes(typeof value))
    .map(([key, value]) => `${key}: ${String(value)}`);
  return clip(scalars.join(', ').replace(/\s+/g, ' '));
}

/** Keeps the last two segments of a long absolute path, where the file's own name is. */
function shortenPath(value: string): string {
  if (!value.startsWith('/') || /\s/.test(value)) return value.replace(/\s+/g, ' ');
  const segments = value.split('/').filter(Boolean);
  return segments.length > 3 ? `…/${segments.slice(-2).join('/')}` : value;
}

function clip(value: string): string {
  return value.length > SUMMARY_MAX_LENGTH ? `${value.slice(0, SUMMARY_MAX_LENGTH - 1)}…` : value;
}

type ToolKind = 'terminal' | 'write' | 'read' | 'search' | 'web' | 'ask' | 'other';

const TOOL_KIND_ICON: Record<ToolKind, LucideIcon> = {
  terminal: SquareTerminal,
  write: FilePenLine,
  read: FileText,
  search: Search,
  web: Globe,
  ask: MessageSquare,
  other: Wrench,
};

/** The kind of work a tool name suggests, which picks its glyph and tint. */
function toolKindOf(toolName: string): ToolKind {
  const name = toolName.toLowerCase();
  if (/bash|shell|exec|command|terminal/.test(name)) return 'terminal';
  if (/write|edit|patch/.test(name)) return 'write';
  if (/read|view|cat\b|open/.test(name)) return 'read';
  if (/glob|grep|search|find|list|\bls\b/.test(name)) return 'search';
  if (/web|fetch|http|url|browse/.test(name)) return 'web';
  if (/ask|question/.test(name)) return 'ask';
  return 'other';
}

function prettyJSON(text: string): string {
  if (text.trim() === '') return '';
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}
