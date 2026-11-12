// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createContext, memo, useCallback, useContext, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { ArrowDown, ArrowUp, CircleAlert, CircleStop, Radio } from 'lucide-react';
import { storedValueJSONReplacer } from '@/lib/blobs';
import { formatDate } from '@/lib/format';
import type { TimezonePreference } from '@/lib/format';
import type { StreamMessage } from '@/lib/types';
import { CopyButton } from '../../components/CopyButton';
import { decodedValue, isDexValue, undecodedPayloadOf } from '../../components/StructuredValue';

/** What the Streams tab is doing with the chosen Stream. */
export type StreamListeningStatus = 'listening' | 'stopped' | 'error';

/** One progress update read from a Stream message. */
export interface StreamProgressContent {
  kind: 'progress';
  /** The percent or progress field, or progress / total as a percent; null when the update has no amount. */
  amount: number | null;
  /** Whether an amount up to 1 may be a fraction, as in a declared ProgressUpdate or a phase-and-sequence record. */
  isFractionEligible: boolean;
  /** Whether the amount was computed from progress and total, so it is already a percent. */
  isComputedFromTotal: boolean;
  label: string;
  phase: string;
  value: Record<string, unknown>;
}

/** How one Stream message reads on screen. */
export type StreamEntryContent =
  | { kind: 'text'; text: string }
  | { kind: 'scalar'; text: string }
  | StreamProgressContent
  | { kind: 'undecoded'; encoding: string; byteSize: number }
  | { kind: 'structured'; value: unknown };

/** One Stream message with its content, classified once when it arrives. */
export interface StreamEntryRecord {
  message: StreamMessage;
  content: StreamEntryContent;
  /** The one-based position of the message since listening started, kept when older messages are dropped. */
  sequenceNumber: number;
}

/** The Stream messages kept on screen, in arrival order with the newest last. */
export interface StreamEntriesState {
  records: StreamEntryRecord[];
  droppedMessageCount: number;
  /** Whether any progress amount exceeded 1, so the whole Stream reads amounts as 0 to 100. */
  hasPercentAboveOne: boolean;
}

/** The most Stream messages that stay on screen; older ones are dropped. */
export const MAX_KEPT_STREAM_MESSAGES = 1000;

/** The kept-message limits a reader can choose in the Streams tab. */
export const KEPT_STREAM_MESSAGE_LIMIT_CHOICES = [100, 500, 1000] as const;

const KEPT_STREAM_MESSAGE_LIMIT_STORAGE_KEY = 'dex-web-stream-kept-messages';

/** The reader's remembered kept-message limit, or the maximum when none is stored or storage is unavailable. */
export function readStoredKeptStreamMessageLimit(): number {
  try {
    const stored = Number(window.localStorage.getItem(KEPT_STREAM_MESSAGE_LIMIT_STORAGE_KEY));
    return KEPT_STREAM_MESSAGE_LIMIT_CHOICES.find((choice) => choice === stored) ?? MAX_KEPT_STREAM_MESSAGES;
  } catch {
    return MAX_KEPT_STREAM_MESSAGES;
  }
}

/** Remembers the reader's kept-message limit; a browser without storage keeps it for this page only. */
export function storeKeptStreamMessageLimit(limit: number): void {
  try {
    window.localStorage.setItem(KEPT_STREAM_MESSAGE_LIMIT_STORAGE_KEY, String(limit));
  } catch {
    // Storage can be blocked or full; the choice still applies until the page reloads.
  }
}

/** No Stream messages. */
export const EMPTY_STREAM_ENTRIES: StreamEntriesState = { records: [], droppedMessageCount: 0, hasPercentAboveOne: false };

type StreamOutputView = 'transcript' | 'entries';

const LATEST_SCROLL_TOLERANCE_PIXELS = 24;
const RELATIVE_TIME_TICK_MILLISECONDS = 15_000;
const OPEN_STRUCTURED_ENTRY_COUNT = 3;
const COUNT_ANNOUNCEMENT_INTERVAL_MILLISECONDS = 5_000;

/** Whether a declared Stream value type is the connector ProgressUpdate, whose percent runs from 0 to 1. */
export function isProgressUpdateValueType(valueType: string): boolean {
  return valueType === 'ProgressUpdate' || valueType.endsWith('.ProgressUpdate');
}

/** Classifies one Stream message value as text, a scalar, a progress update, an undecoded payload, or a structured value. */
export function streamEntryContentOf(value: unknown, declaredValueType = ''): StreamEntryContent {
  const isEnvelope = isDexValue(value);
  const undecoded = isEnvelope ? undecodedPayloadOf(value) : null;
  if (undecoded) return { kind: 'undecoded', encoding: undecoded.encoding, byteSize: undecoded.byteSize };
  const decoded = isEnvelope ? decodedValue(value) : value;
  if (typeof decoded === 'string') return { kind: 'text', text: decoded };
  if (typeof decoded === 'number' || typeof decoded === 'boolean') return { kind: 'scalar', text: String(decoded) };
  if (decoded && typeof decoded === 'object' && !Array.isArray(decoded)) {
    const progress = progressContentOf(decoded as Record<string, unknown>, isProgressUpdateValueType(declaredValueType));
    if (progress) return progress;
  }
  return { kind: 'structured', value: decoded };
}

function progressContentOf(record: Record<string, unknown>, isDeclaredProgressUpdate: boolean): StreamProgressContent | null {
  const label = firstText(record.message, record.status, record.label);
  const phase = firstText(record.phase, record.stage, record.step);
  const hasPhaseAndSequence = phase !== '' && 'sequence' in record;
  let amount: number | null = null;
  let isComputedFromTotal = false;
  if (isNonNegativeNumber(record.percent)) {
    amount = record.percent;
  } else if (isNonNegativeNumber(record.progress)) {
    if ('total' in record) {
      // A progress count without a usable total is a plain record, not a progress update.
      if (!isNonNegativeNumber(record.total) || record.total === 0) return null;
      amount = (record.progress / record.total) * 100;
      isComputedFromTotal = true;
    } else {
      amount = record.progress;
    }
  }
  if (amount === null && !hasPhaseAndSequence) return null;
  return {
    kind: 'progress',
    amount,
    isFractionEligible: !isComputedFromTotal && (isDeclaredProgressUpdate || hasPhaseAndSequence),
    isComputedFromTotal,
    label,
    phase,
    value: record,
  };
}

/** A 0 to 100 percent; eligible amounts up to 1 are fractions until the Stream shows an amount above 1. */
export function progressPercentOf(content: StreamProgressContent, hasPercentAboveOne: boolean): number | null {
  if (content.amount === null) return null;
  const isFraction = content.isFractionEligible && !hasPercentAboveOne && content.amount <= 1;
  return Math.min(isFraction ? content.amount * 100 : content.amount, 100);
}

function isNonNegativeNumber(value: unknown): value is number {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0;
}

function firstText(...candidates: unknown[]): string {
  for (const candidate of candidates) {
    if (typeof candidate === 'string' && candidate.trim()) return candidate;
  }
  return '';
}

/** Appends one classified message, dropping the oldest messages beyond the kept limit. */
export function appendStreamEntry(
  state: StreamEntriesState,
  record: StreamEntryRecord,
  maxKeptMessages = MAX_KEPT_STREAM_MESSAGES,
): StreamEntriesState {
  const content = record.content;
  return trimStreamEntries(
    {
      records: [...state.records, record],
      droppedMessageCount: state.droppedMessageCount,
      hasPercentAboveOne: state.hasPercentAboveOne
        || (content.kind === 'progress' && !content.isComputedFromTotal && content.amount !== null && content.amount > 1),
    },
    maxKeptMessages,
  );
}

/** Drops the oldest messages beyond the kept limit and counts them; returns the same state when nothing is dropped. */
export function trimStreamEntries(state: StreamEntriesState, maxKeptMessages: number): StreamEntriesState {
  const overflowCount = Math.max(state.records.length - maxKeptMessages, 0);
  if (overflowCount === 0) return state;
  return {
    ...state,
    records: state.records.slice(overflowCount),
    droppedMessageCount: state.droppedMessageCount + overflowCount,
  };
}

/** Where the Stream output sits in the window, and the part of the window the reader can see. */
export interface StreamOutputBounds {
  top: number;
  bottom: number;
  visibleTop: number;
  visibleBottom: number;
}

/** Whether the end of a transcript, its newest text, is in view or within a small tolerance of it. */
export function isScrolledToLatest(bounds: StreamOutputBounds): boolean {
  return bounds.bottom <= bounds.visibleBottom + LATEST_SCROLL_TOLERANCE_PIXELS;
}

/** Whether the top of the entries, their newest entry, is in view or within a small tolerance of it. */
export function isScrolledToNewestEntry(bounds: StreamOutputBounds): boolean {
  return bounds.top >= bounds.visibleTop - LATEST_SCROLL_TOLERANCE_PIXELS;
}

/** Whether the page keeps the newest transcript text in view: the reader has scrolled down to its end. */
export function isFollowingTranscriptEnd(bounds: StreamOutputBounds): boolean {
  return isScrolledToLatest(bounds) && bounds.top < bounds.visibleTop;
}

// The sticky page header covers the window top; the output's scroll-margin-top states its height.
function streamOutputBoundsOf(output: HTMLElement): StreamOutputBounds {
  const { top, bottom } = output.getBoundingClientRect();
  const style = getComputedStyle(output);
  return {
    top,
    bottom,
    visibleTop: parseFloat(style.scrollMarginTop) || 0,
    visibleBottom: window.innerHeight - (parseFloat(style.scrollMarginBottom) || 0),
  };
}

/**
 * The entry that holds the reader's place: the first one that starts in view, or else the one the window top
 * cuts through. An entry that starts in view keeps its top when a newer entry above it closes its JSON.
 */
interface StreamScrollAnchor {
  resumeToken: string;
  offsetFromVisibleTop: number;
}

function streamScrollAnchorOf(output: HTMLElement): StreamScrollAnchor | null {
  const entryElements = output.querySelectorAll<HTMLElement>('.stream-entry');
  const { visibleTop, visibleBottom } = streamOutputBoundsOf(output);
  const firstEntryIndexWhere = (isPastWindowTop: (rect: DOMRect) => boolean) => {
    // Entries stack in order, so a binary search finds the first one.
    let low = 0;
    let high = entryElements.length - 1;
    let foundIndex = -1;
    while (low <= high) {
      const middle = Math.floor((low + high) / 2);
      if (isPastWindowTop(entryElements[middle].getBoundingClientRect())) {
        foundIndex = middle;
        high = middle - 1;
      } else {
        low = middle + 1;
      }
    }
    return foundIndex;
  };
  const startingInViewIndex = firstEntryIndexWhere((rect) => rect.top >= visibleTop);
  const isStartingInView = startingInViewIndex >= 0
    && entryElements[startingInViewIndex].getBoundingClientRect().top < visibleBottom;
  const anchorIndex = isStartingInView ? startingInViewIndex : firstEntryIndexWhere((rect) => rect.bottom > visibleTop);
  const anchorElement = anchorIndex >= 0 ? entryElements[anchorIndex] : null;
  const resumeToken = anchorElement?.dataset.resumeToken;
  if (!anchorElement || resumeToken === undefined) return null;
  return { resumeToken, offsetFromVisibleTop: anchorElement.getBoundingClientRect().top - visibleTop };
}

/** Scrolls the page so the anchored entry sits where it was; returns false when that entry is no longer on screen. */
function restoreStreamScrollAnchor(output: HTMLElement, anchor: StreamScrollAnchor): boolean {
  const anchorElement = [...output.querySelectorAll<HTMLElement>('.stream-entry')]
    .find((element) => element.dataset.resumeToken === anchor.resumeToken);
  if (!anchorElement) return false;
  const offsetFromVisibleTop = anchorElement.getBoundingClientRect().top - streamOutputBoundsOf(output).visibleTop;
  const scrollDelta = offsetFromVisibleTop - anchor.offsetFromVisibleTop;
  if (scrollDelta !== 0) window.scrollBy(0, scrollDelta);
  return true;
}

/** Scrolls the page to the newest messages: the top of the entries, or the end of a transcript. */
function scrollToLatest(output: HTMLElement, isNewestFirst: boolean) {
  output.scrollIntoView({ block: isNewestFirst ? 'start' : 'end' });
}

/** Scrolls the page to the newest messages only when they are out of view, as after a view switch. */
function showLatestInWindow(output: HTMLElement, isNewestFirst: boolean) {
  const bounds = streamOutputBoundsOf(output);
  const isLatestInView = isNewestFirst ? isScrolledToNewestEntry(bounds) : isScrolledToLatest(bounds);
  if (!isLatestInView) scrollToLatest(output, isNewestFirst);
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
  if (content.kind === 'text' || content.kind === 'scalar') return content.text;
  if (content.kind === 'undecoded') return JSON.stringify({ encoding: content.encoding, byteSize: content.byteSize }, null, 2);
  return JSON.stringify(content.value, storedValueJSONReplacer, 2) ?? '';
}

function transcriptTextOf(records: StreamEntryRecord[]): string {
  return records.map((record) => (record.content.kind === 'text' ? record.content.text : '')).join('');
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

/** A screen reader announcement of the message count, at most once every few seconds. */
export function StreamCountAnnouncement({ messageCount }: { messageCount: number }) {
  const announcedCount = useThrottledValue(messageCount, COUNT_ANNOUNCEMENT_INTERVAL_MILLISECONDS);
  return (
    <span aria-live="polite" className="sr-only">
      {announcedCount > 0 ? `${announcedCount} ${announcedCount === 1 ? 'message' : 'messages'}` : ''}
    </span>
  );
}

/** The messages read from one Stream, as a transcript for text or as entries. */
export function StreamOutput({
  entries,
  status,
  timezone,
}: {
  entries: StreamEntriesState;
  status: StreamListeningStatus;
  timezone: TimezonePreference;
}) {
  const { records, droppedMessageCount, hasPercentAboveOne } = entries;
  const isAllText = records.every((record) => record.content.kind === 'text');
  const [pickedView, setPickedView] = useState<StreamOutputView | null>(null);
  const view: StreamOutputView = isAllText ? (pickedView ?? 'transcript') : 'entries';
  const isNewestFirst = view === 'entries';
  const outputRef = useRef<HTMLDivElement>(null);
  const scrollAnchorRef = useRef<StreamScrollAnchor | null>(null);
  const isFollowingTranscriptEndRef = useRef(false);
  const [isFollowingLatest, setIsFollowingLatest] = useState(true);
  const latestRecord = records.at(-1);
  const latestSequenceNumber = latestRecord?.sequenceNumber ?? 0;
  const [seenSequenceNumber, setSeenSequenceNumber] = useState(0);
  const newestFirstRecords = useMemo(() => [...records].reverse(), [records]);

  // Refs keep the window listeners registered once per mount and never read a stale render.
  const isNewestFirstRef = useRef(isNewestFirst);
  const latestSequenceNumberRef = useRef(latestSequenceNumber);
  const shownViewRef = useRef(view);
  const isViewPickedRef = useRef(false);
  const hasSwitchedToEntriesByItselfRef = useRef(isNewestFirst);

  // The page scrolls, not the output, so the reader's place is read from the window.
  const followScrollPosition = useCallback(() => {
    const output = outputRef.current;
    if (!output) return;
    const bounds = streamOutputBoundsOf(output);
    let isAtLatest: boolean;
    if (isNewestFirstRef.current) {
      isAtLatest = isScrolledToNewestEntry(bounds);
      scrollAnchorRef.current = isAtLatest ? null : streamScrollAnchorOf(output);
    } else {
      isAtLatest = isScrolledToLatest(bounds);
      // Scrolling down to the end starts following; following then lasts while the end stays in view.
      isFollowingTranscriptEndRef.current = isAtLatest
        && (isFollowingTranscriptEndRef.current || isFollowingTranscriptEnd(bounds));
    }
    setIsFollowingLatest(isAtLatest);
    if (isAtLatest) setSeenSequenceNumber(latestSequenceNumberRef.current);
  }, []);

  useEffect(() => {
    window.addEventListener('scroll', followScrollPosition, { passive: true });
    window.addEventListener('resize', followScrollPosition);
    return () => {
      window.removeEventListener('scroll', followScrollPosition);
      window.removeEventListener('resize', followScrollPosition);
    };
  }, [followScrollPosition]);

  useLayoutEffect(() => {
    isNewestFirstRef.current = isNewestFirst;
    latestSequenceNumberRef.current = latestSequenceNumber;
    const output = outputRef.current;
    if (!output) return;
    if (shownViewRef.current !== view) {
      shownViewRef.current = view;
      // Trimming can flip the view back and forth; only a pick or the first switch to entries moves the page.
      const isFirstSwitchToEntries = isNewestFirst && !isViewPickedRef.current && !hasSwitchedToEntriesByItselfRef.current;
      const shouldShowLatest = isViewPickedRef.current || isFirstSwitchToEntries;
      if (isFirstSwitchToEntries) hasSwitchedToEntriesByItselfRef.current = true;
      isViewPickedRef.current = false;
      scrollAnchorRef.current = null;
      isFollowingTranscriptEndRef.current = shouldShowLatest && !isNewestFirst;
      if (shouldShowLatest) showLatestInWindow(output, isNewestFirst);
    } else if (isNewestFirst) {
      const anchor = scrollAnchorRef.current;
      if (anchor) restoreStreamScrollAnchor(output, anchor);
    } else if (isFollowingTranscriptEndRef.current) {
      const bounds = streamOutputBoundsOf(output);
      if (bounds.bottom > bounds.visibleBottom) window.scrollBy(0, bounds.bottom - bounds.visibleBottom);
    }
    followScrollPosition();
  }, [followScrollPosition, isNewestFirst, latestSequenceNumber, records, view]);

  const pickView = (option: StreamOutputView) => {
    isViewPickedRef.current = option !== view;
    setPickedView(option);
  };

  const backToLatest = () => {
    if (outputRef.current) scrollToLatest(outputRef.current, isNewestFirst);
    scrollAnchorRef.current = null;
    isFollowingTranscriptEndRef.current = !isNewestFirst;
    setIsFollowingLatest(true);
    setSeenSequenceNumber(latestSequenceNumber);
  };

  const unseenMessageCount = Math.max(latestSequenceNumber - seenSequenceNumber, 0);
  const droppedNote = droppedMessageCount > 0 && (
    <p className="stream-dropped-note">
      {droppedMessageCount} older {droppedMessageCount === 1 ? 'message' : 'messages'} dropped · showing the newest{' '}
      {records.length}
    </p>
  );

  // A zero-height sticky holder keeps the button on screen while the page scrolls.
  const backToLatestButton = (
    <div className={`stream-back-to-latest-holder stream-back-to-latest-holder--${isNewestFirst ? 'top' : 'bottom'}`}>
      <button
        className={`stream-back-to-latest stream-back-to-latest--${isNewestFirst ? 'top' : 'bottom'}`}
        onClick={backToLatest}
        type="button"
      >
        {isNewestFirst ? <ArrowUp aria-hidden="true" size={14} /> : <ArrowDown aria-hidden="true" size={14} />}
        {unseenMessageCount > 0 ? `${unseenMessageCount} new · ` : ''}Back to latest
      </button>
    </div>
  );

  return (
    <StreamClock isTicking={records.length > 0}>
      <div className="stream-output">
        {latestRecord?.content.kind === 'progress' && (
          <div className="stream-progress-focus">
            <StreamProgress content={latestRecord.content} hasPercentAboveOne={hasPercentAboveOne} />
          </div>
        )}
        {isAllText && (
          <div aria-label="Stream view" className="stream-view-switch" role="group">
            {(['transcript', 'entries'] as const).map((option) => (
              <button
                aria-pressed={view === option}
                key={option}
                onClick={() => pickView(option)}
                type="button"
              >
                {option === 'transcript' ? 'Transcript' : 'Entries'}
              </button>
            ))}
            {view === 'transcript' && <CopyButton label="Copy transcript" value={() => transcriptTextOf(records)} />}
          </div>
        )}
        <div className="stream-scroll-frame">
          {!isFollowingLatest && isNewestFirst && backToLatestButton}
          <div
            className={`stream-scroll stream-scroll--${view}`}
            data-testid="stream-scroll"
            ref={outputRef}
          >
            {view === 'transcript' ? (
              <>
                {droppedNote}
                <pre className="stream-transcript">
                  {records.map((record) => (
                    <StreamTranscriptChunk key={record.message.resumeToken} record={record} timezone={timezone} />
                  ))}
                  {status === 'listening' && <span aria-hidden="true" className="stream-transcript-caret" />}
                </pre>
              </>
            ) : (
              <>
                <ol className="stream-entries">
                  {newestFirstRecords.map((record) => (
                    <StreamEntry
                      hasPercentAboveOne={record.content.kind === 'progress' && hasPercentAboveOne}
                      isRecent={
                        record.content.kind === 'structured'
                        && record.sequenceNumber > latestSequenceNumber - OPEN_STRUCTURED_ENTRY_COUNT
                      }
                      key={record.message.resumeToken}
                      record={record}
                      timezone={timezone}
                    />
                  ))}
                </ol>
                {droppedNote}
              </>
            )}
          </div>
          {!isFollowingLatest && !isNewestFirst && backToLatestButton}
        </div>
      </div>
    </StreamClock>
  );
}

const StreamTranscriptChunk = memo(function StreamTranscriptChunk({
  record,
  timezone,
}: {
  record: StreamEntryRecord;
  timezone: TimezonePreference;
}) {
  return (
    <span title={`#${record.sequenceNumber} · ${formatDate(record.message.createdTime, timezone)}`}>
      {record.content.kind === 'text' ? record.content.text : ''}
    </span>
  );
});

const StreamEntry = memo(function StreamEntry({
  hasPercentAboveOne,
  isRecent,
  record,
  timezone,
}: {
  hasPercentAboveOne: boolean;
  /** Whether a structured value is among the newest few, which open their JSON. */
  isRecent: boolean;
  record: StreamEntryRecord;
  timezone: TimezonePreference;
}) {
  const { content, message, sequenceNumber } = record;
  const absoluteTime = formatDate(message.createdTime, timezone);
  return (
    <li className={`stream-entry stream-entry--${content.kind}`} data-resume-token={message.resumeToken}>
      <div className="stream-entry-meta">
        <span className="stream-entry-sequence">#{sequenceNumber}</span>
        {message.createdTime && <StreamRelativeTime absoluteTime={absoluteTime} createdTime={message.createdTime} />}
        {message.source && <code>{message.source}</code>}
        <CopyButton label={`Copy message ${sequenceNumber}`} value={() => copyTextOf(content)} />
      </div>
      {content.kind === 'text' && <p className="stream-entry-text">{content.text}</p>}
      {content.kind === 'scalar' && <p className="stream-entry-text stream-entry-scalar">{content.text}</p>}
      {content.kind === 'progress' && (
        <>
          <StreamProgress content={content} hasPercentAboveOne={hasPercentAboveOne} />
          <StreamStructuredValue isRecent={false} value={content.value} />
        </>
      )}
      {content.kind === 'undecoded' && (
        <p className="stream-entry-undecoded">
          Payload in {content.encoding ? `${content.encoding} encoding` : 'an unnamed encoding'} · {content.byteSize}{' '}
          {content.byteSize === 1 ? 'byte' : 'bytes'}
        </p>
      )}
      {content.kind === 'structured' && <StreamStructuredValue isRecent={isRecent} value={content.value} />}
    </li>
  );
});

function StreamRelativeTime({ absoluteTime, createdTime }: { absoluteTime: string; createdTime: string }) {
  const nowMilliseconds = useContext(StreamClockContext);
  return (
    <time dateTime={createdTime} title={absoluteTime}>
      {formatRelativeTime(createdTime, nowMilliseconds) || absoluteTime}
    </time>
  );
}

function StreamProgress({ content, hasPercentAboveOne }: { content: StreamProgressContent; hasPercentAboveOne: boolean }) {
  const percent = progressPercentOf(content, hasPercentAboveOne);
  const roundedPercent = percent === null ? null : Math.round(percent);
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

/** Collapsible JSON that renders its text only while open; recent values open until the reader toggles them. */
function StreamStructuredValue({ isRecent, value }: { isRecent: boolean; value: unknown }) {
  const [readerOpenChoice, setReaderOpenChoice] = useState<boolean | null>(null);
  const isOpen = readerOpenChoice ?? isRecent;
  const json = useMemo(
    () => (isOpen ? JSON.stringify(value, storedValueJSONReplacer, 2) ?? String(value) : ''),
    [isOpen, value],
  );
  const fieldCount = value && typeof value === 'object' ? Object.keys(value).length : 0;
  const summary = Array.isArray(value)
    ? `List · ${value.length} ${value.length === 1 ? 'item' : 'items'}`
    : `Object · ${fieldCount} ${fieldCount === 1 ? 'field' : 'fields'}`;
  return (
    <details
      className="stream-json"
      onToggle={(event) => {
        // Programmatic changes already match isOpen; only a reader's toggle differs.
        if (event.currentTarget.open !== isOpen) setReaderOpenChoice(event.currentTarget.open);
      }}
      open={isOpen}
    >
      <summary>{summary}</summary>
      {isOpen && <pre>{json}</pre>}
    </details>
  );
}

const StreamClockContext = createContext(0);

/** Shares one ticking clock with the relative times, so a tick re-renders only them. */
function StreamClock({ children, isTicking }: { children: ReactNode; isTicking: boolean }) {
  const [nowMilliseconds, setNowMilliseconds] = useState(() => Date.now());
  useEffect(() => {
    if (!isTicking) return;
    setNowMilliseconds(Date.now());
    const timer = window.setInterval(() => setNowMilliseconds(Date.now()), RELATIVE_TIME_TICK_MILLISECONDS);
    return () => window.clearInterval(timer);
  }, [isTicking]);
  return <StreamClockContext.Provider value={nowMilliseconds}>{children}</StreamClockContext.Provider>;
}

function useThrottledValue<T>(value: T, intervalMilliseconds: number): T {
  const [throttledValue, setThrottledValue] = useState(value);
  const lastUpdateMillisecondsRef = useRef(0);
  useEffect(() => {
    if (Object.is(value, throttledValue)) return;
    const waitMilliseconds = Math.max(lastUpdateMillisecondsRef.current + intervalMilliseconds - Date.now(), 0);
    const timer = window.setTimeout(() => {
      lastUpdateMillisecondsRef.current = Date.now();
      setThrottledValue(value);
    }, waitMilliseconds);
    return () => window.clearTimeout(timer);
  }, [intervalMilliseconds, throttledValue, value]);
  return throttledValue;
}
