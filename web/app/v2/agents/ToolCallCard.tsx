// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { ReactNode } from 'react';
import { formatElapsedDuration } from '@/lib/timeline';
import { SESSION_COPY } from './copy';
import type { ToolCallView } from './sessionModel';
import './session.css';

/** One tool call as a compact card; `children` holds a waiting call's controls. */
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
  const effect = call.effect === null
    ? SESSION_COPY.effectUnknown
    : SESSION_COPY.effect[call.effect] ?? call.effect;
  const detail = toolCallDetail(call, now);
  const argumentsText = prettyJSON(call.argumentsText);
  const result = call.state === 'failed' || call.state === 'outcome_unknown' || call.state === 'skipped' || call.state === 'recording'
    ? { label: SESSION_COPY.error, text: call.error }
    : { label: SESSION_COPY.output, text: call.output };
  return (
    <article
      aria-label={`${call.toolName} ${SESSION_COPY.state[call.state]}`}
      className="atc"
      data-source={call.source}
      data-state={call.state}
      data-testid={`tool-call-${call.toolCallId}`}
    >
      <header className="atc-head">
        <span className="atc-name t-mono">{call.toolName}</span>
        <span className="atc-effect" data-unknown={call.effect === null ? 'true' : undefined}>{effect}</span>
        <span className="atc-state" data-state={call.state}>{SESSION_COPY.state[call.state]}</span>
      </header>
      {argumentsText !== '' && (
        <pre className="atc-args t-mono" aria-label={SESSION_COPY.arguments}>{argumentsText}</pre>
      )}
      {detail && <p className="atc-detail">{detail}</p>}
      {result.text && (
        <details className="atc-result">
          <summary>{result.label}</summary>
          <pre className="t-mono">{prettyJSON(result.text)}</pre>
        </details>
      )}
      {children}
      {call.state === 'outcome_unknown' && onTellAgent && (
        <div className="agc-buttons">
          <button className="v2-ghost" onClick={() => onTellAgent(call)} type="button">
            {SESSION_COPY.tellAgent}
          </button>
        </div>
      )}
      <footer className="atc-foot t-mono">
        {call.toolCallId}
        {call.stepExecutionId ? ` · ${call.stepExecutionId}` : ''}
      </footer>
    </article>
  );
}

/** The sentence under a card's header, which says what the state means for the operator. */
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
    case 'skipped': return SESSION_COPY.skippedDetail;
    case 'cancelled': return SESSION_COPY.cancelledDetail(call.cancellationReason);
    case 'rejected': return SESSION_COPY.rejectedDetail(call.rejectionMessage);
    case 'returned': return SESSION_COPY.fromMessages;
    case 'no_result': return `${SESSION_COPY.fromMessages} ${SESSION_COPY.noResultDetail}`;
    case 'done': return approved.trim();
    default: return '';
  }
}

function prettyJSON(text: string): string {
  if (text.trim() === '') return '';
  try {
    return JSON.stringify(JSON.parse(text), null, 2);
  } catch {
    return text;
  }
}
