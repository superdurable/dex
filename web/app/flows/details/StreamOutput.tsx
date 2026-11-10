// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import { ArrowDown, CircleAlert, CircleStop, Radio } from 'lucide-react';
import { formatDate } from '@/lib/format';
import type { TimezonePreference } from '@/lib/format';
import type { StreamMessage } from '@/lib/types';
import { CopyButton } from '../../components/CopyButton';
import { decodedValue, isDexValue } from '../../components/StructuredValue';

/** What the Streams tab is doing with the chosen Stream. */
export type StreamListeningStatus = 'listening' | 'stopped' | 'error';

/** How one Stream message reads on screen. */
export type StreamEntryContent =
  | { kind: 'text'; text: string }
  | { kind: 'progress'; percent: number | null; label: string; phase: string; value: Record<string, unknown> }
  | { kind: 'structured'; value: unknown };

type StreamOutputView = 'transcript' | 'entries';

const LATEST_SCROLL_TOLERANCE_PIXELS = 24;
const RELATIVE_TIME_TICK_MILLISECONDS = 15_000;

/** Classifies one Stream message value as text, a progress update, or a structured value. */
export function streamEntryContentOf(value: unknown): StreamEntryContent {
  const decoded = isDexValue(value) ? decodedValue(value) : value;
  if (typeof decoded === 'string') return { kind: 'text', text: decoded };
  if (typeof decoded === 'number' || typeof decoded === 'boolean') return { kind: 'text', text: String(decoded) };
  if (decoded && typeof decoded === 'object' && !Array.isArray(decoded)) {
    const record = decoded as Record<string, unknown>;
    const percent = progressPercentOf(record);
    const label = firstText(record.message, record.status, record.label);
    const phase = firstText(record.phase, record.stage, record.step);
    if (percent !== null || (phase && 'sequence' in record)) {
      return { kind: 'progress', percent, label, phase, value: record };
    }
  }
  return { kind: 'structured', value: decoded };
}

/** A 0 to 100 percent from a percent or progress field; values up to 1 are fractions. */
function progressPercentOf(record: Record<string, unknown>): number | null {
  const raw = typeof record.percent === 'number' ? record.percent : typeof record.progress === 'number' ? record.progress : null;
  if (raw === null || !Number.isFinite(raw) || raw < 0) return null;
  const percent = raw <= 1 ? raw * 100 : raw;
  return Math.min(percent, 100);
}

function firstText(...candidates: unknown[]): string {
  for (const candidate of candidates) {
    if (typeof candidate === 'string' && candidate.trim()) return candidate;
  }
  return '';
}

/** Whether a scroll box is at, or within a small tolerance of, its newest content. */
export function isScrolledToLatest(metrics: { scrollTop: number; scrollHeight: number; clientHeight: number }): boolean {
  return metrics.scrollHeight - metrics.scrollTop - metrics.clientHeight <= LATEST_SCROLL_TOLERANCE_PIXELS;
}

/** A short relative time such as "just now", "12s ago", or "3m ago". */
export function formatRelativeTime(createdTime: string | null, nowMilliseconds: number): string {
  if (!createdTime) return '';
  const elapsedSeconds = Math.round((nowMilliseconds - Date.parse(createdTime)) / 1000);
  if (!Number.isFinite(elapsedSeconds)) return '';
  if (elapsedSeconds < 5) return 'just now';
  if (elapsedSeconds < 60) return `${elapsedSeconds}s ago`;
  if (elapsedSeconds < 3600) return `${Math.floor(elapsedSeconds / 60)}m ago`;
  if (elapsedSeconds < 86_400) return `${Math.floor(elapsedSeconds / 3600)}h ago`;
  return `${Math.floor(elapsedSeconds / 86_400)}d ago`;
}

function copyTextOf(content: StreamEntryContent): string {
  if (content.kind === 'text') return content.text;
  return JSON.stringify(content.value, null, 2) ?? '';
}

/** The listening state with an icon; a live Stream pulses. */
export function StreamStatusBadge({ status }: { status: StreamListeningStatus }) {
  const Icon = status === 'listening' ? Radio : status === 'error' ? CircleAlert : CircleStop;
  const label = status === 'listening' ? 'Live' : status === 'error' ? 'Error' : 'Stopped';
  return (
    <span className={`stream-status stream-status--${status}`} aria-live="polite">
      {status === 'listening' && <span aria-hidden="true" className="stream-status-pulse" />}
      <Icon aria-hidden="true" size={14} />
      {label}
    </span>
  );
}

/** The messages read from one Stream, as a transcript for text or as entries. */
export function StreamOutput({
  messages,
  timezone,
}: {
  messages: StreamMessage[];
  timezone: TimezonePreference;
}) {
  const contents = messages.map((message) => streamEntryContentOf(message.value));
  const isAllText = contents.every((content) => content.kind === 'text');
  const [pickedView, setPickedView] = useState<StreamOutputView | null>(null);
  const view: StreamOutputView = isAllText ? (pickedView ?? 'transcript') : 'entries';
  const nowMilliseconds = useTickingNow(messages.length > 0);
  const scrollBoxRef = useRef<HTMLDivElement>(null);
  const [isFollowingLatest, setIsFollowingLatest] = useState(true);
  const [seenMessageCount, setSeenMessageCount] = useState(0);

  useLayoutEffect(() => {
    const scrollBox = scrollBoxRef.current;
    if (!scrollBox || !isFollowingLatest) return;
    scrollBox.scrollTop = scrollBox.scrollHeight;
    setSeenMessageCount(messages.length);
  }, [isFollowingLatest, messages.length, view]);

  const jumpToLatest = () => {
    const scrollBox = scrollBoxRef.current;
    if (scrollBox) scrollBox.scrollTop = scrollBox.scrollHeight;
    setIsFollowingLatest(true);
  };

  const latestContent = contents.at(-1);
  const unseenMessageCount = Math.max(messages.length - seenMessageCount, 0);
  const transcriptChunks = contents.map((content) => (content.kind === 'text' ? content.text : ''));
  const transcript = transcriptChunks.join('');

  return (
    <div className="stream-output">
      {latestContent?.kind === 'progress' && (
        <div className="stream-progress-focus">
          <StreamProgress content={latestContent} />
        </div>
      )}
      {isAllText && (
        <div aria-label="Stream view" className="stream-view-switch" role="group">
          {(['transcript', 'entries'] as const).map((option) => (
            <button
              aria-pressed={view === option}
              key={option}
              onClick={() => setPickedView(option)}
              type="button"
            >
              {option === 'transcript' ? 'Transcript' : 'Entries'}
            </button>
          ))}
          {view === 'transcript' && <CopyButton label="Copy transcript" value={transcript} />}
        </div>
      )}
      <div
        className={`stream-scroll stream-scroll--${view}`}
        data-testid="stream-scroll"
        onScroll={(event) => setIsFollowingLatest(isScrolledToLatest(event.currentTarget))}
        ref={scrollBoxRef}
      >
        {view === 'transcript' ? (
          <pre aria-live="polite" className="stream-transcript">
            {messages.map((message, index) => (
              <span
                key={message.resumeToken}
                title={`#${index + 1} · ${formatDate(message.createdTime, timezone)}`}
              >
                {transcriptChunks[index]}
              </span>
            ))}
            <span aria-hidden="true" className="stream-transcript-caret" />
          </pre>
        ) : (
          <ol aria-live="polite" className="stream-entries">
            {messages.map((message, index) => (
              <StreamEntry
                content={contents[index]}
                key={message.resumeToken}
                message={message}
                nowMilliseconds={nowMilliseconds}
                sequenceNumber={index + 1}
                timezone={timezone}
              />
            ))}
          </ol>
        )}
      </div>
      {!isFollowingLatest && (
        <button className="stream-jump-latest" onClick={jumpToLatest} type="button">
          <ArrowDown aria-hidden="true" size={14} />
          Jump to latest{unseenMessageCount > 0 ? ` · ${unseenMessageCount} new` : ''}
        </button>
      )}
    </div>
  );
}

function StreamEntry({
  content,
  message,
  nowMilliseconds,
  sequenceNumber,
  timezone,
}: {
  content: StreamEntryContent;
  message: StreamMessage;
  nowMilliseconds: number;
  sequenceNumber: number;
  timezone: TimezonePreference;
}) {
  const absoluteTime = formatDate(message.createdTime, timezone);
  return (
    <li className={`stream-entry stream-entry--${content.kind}`}>
      <div className="stream-entry-meta">
        <span className="stream-entry-sequence">#{sequenceNumber}</span>
        {message.createdTime && (
          <time dateTime={message.createdTime} title={absoluteTime}>
            {formatRelativeTime(message.createdTime, nowMilliseconds) || absoluteTime}
          </time>
        )}
        {message.source && <code>{message.source}</code>}
        <CopyButton label={`Copy message ${sequenceNumber}`} value={copyTextOf(content)} />
      </div>
      {content.kind === 'text' && <p className="stream-entry-text">{content.text}</p>}
      {content.kind === 'progress' && <StreamProgress content={content} />}
      {content.kind === 'structured' && <StreamStructuredValue value={content.value} />}
    </li>
  );
}

function StreamProgress({ content }: { content: Extract<StreamEntryContent, { kind: 'progress' }> }) {
  const roundedPercent = content.percent === null ? null : Math.round(content.percent);
  return (
    <div className="stream-progress">
      <div className="stream-progress-label">
        <span>{content.label || content.phase || 'Progress'}</span>
        {roundedPercent !== null && <strong>{roundedPercent}%</strong>}
      </div>
      <div
        aria-label={content.label || content.phase || 'Progress'}
        aria-valuemax={100}
        aria-valuemin={0}
        aria-valuenow={roundedPercent ?? undefined}
        className={`stream-progress-track${roundedPercent === null ? ' is-indeterminate' : ''}`}
        role="progressbar"
      >
        <span style={roundedPercent === null ? undefined : { width: `${roundedPercent}%` }} />
      </div>
      {content.label && content.phase && <span className="stream-progress-phase">{content.phase}</span>}
    </div>
  );
}

function StreamStructuredValue({ value }: { value: unknown }) {
  const json = JSON.stringify(value, null, 2) ?? String(value);
  const fieldCount = value && typeof value === 'object' ? Object.keys(value).length : 0;
  const summary = Array.isArray(value)
    ? `List · ${value.length} ${value.length === 1 ? 'item' : 'items'}`
    : `Object · ${fieldCount} ${fieldCount === 1 ? 'field' : 'fields'}`;
  return (
    <details className="stream-json" open>
      <summary>{summary}</summary>
      <pre>{json}</pre>
    </details>
  );
}

function useTickingNow(isTicking: boolean): number {
  const [nowMilliseconds, setNowMilliseconds] = useState(() => Date.now());
  useEffect(() => {
    if (!isTicking) return;
    setNowMilliseconds(Date.now());
    const timer = window.setInterval(() => setNowMilliseconds(Date.now()), RELATIVE_TIME_TICK_MILLISECONDS);
    return () => window.clearInterval(timer);
  }, [isTicking]);
  return nowMilliseconds;
}
