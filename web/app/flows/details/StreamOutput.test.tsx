// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { formatDate } from '@/lib/format';
import type { StreamMessage } from '@/lib/types';
import { VALUE_BLOB_UNAVAILABLE } from '@/lib/unavailable';
import { decodedValue } from '../../components/StructuredValue';
import {
  appendStreamEntry,
  EMPTY_STREAM_ENTRIES,
  formatRelativeTime,
  isScrolledToLatest,
  progressPercentOf,
  StreamOutput,
  streamEntryContentOf,
} from './StreamOutput';
import type { StreamEntriesState, StreamListeningStatus } from './StreamOutput';

vi.mock('@/lib/format', async (importOriginal) => {
  const original = await importOriginal<typeof import('@/lib/format')>();
  return { ...original, formatDate: vi.fn(original.formatDate) };
});

function jsonValue(value: unknown) {
  return { objValue: { encoding: 'json', payload: btoa(JSON.stringify(value)) } };
}

function streamMessage(value: unknown, sequence: number): StreamMessage {
  return {
    value,
    resumeToken: `token-${sequence}`,
    createdTime: `2026-11-09T12:00:0${sequence}Z`,
    source: '#Summarize-1',
  };
}

function appendMessages(
  initial: StreamEntriesState,
  messages: StreamMessage[],
  valueType = '',
  maxKeptMessages?: number,
): StreamEntriesState {
  return messages.reduce(
    (state, message) => appendStreamEntry(
      state,
      {
        message,
        content: streamEntryContentOf(message.value, valueType),
        sequenceNumber: state.droppedMessageCount + state.records.length + 1,
      },
      maxKeptMessages,
    ),
    initial,
  );
}

function entriesOf(messages: StreamMessage[], valueType = '', maxKeptMessages?: number): StreamEntriesState {
  return appendMessages(EMPTY_STREAM_ENTRIES, messages, valueType, maxKeptMessages);
}

function renderOutput(messages: StreamMessage[], status: StreamListeningStatus = 'listening') {
  return render(<StreamOutput entries={entriesOf(messages)} status={status} timezone="UTC" />);
}

/** The display percent of each progress message, read in Stream order. */
function streamPercents(values: unknown[], valueType = ''): Array<number | null> {
  const state = entriesOf(values.map((value, index) => streamMessage(jsonValue(value), index + 1)), valueType);
  return state.records.map((record) => (
    record.content.kind === 'progress' ? progressPercentOf(record.content, state.hasPercentAboveOne) : null
  ));
}

function setScrollMetrics(element: HTMLElement, metrics: { scrollTop: number; scrollHeight: number; clientHeight: number }) {
  Object.defineProperty(element, 'scrollHeight', { configurable: true, value: metrics.scrollHeight });
  Object.defineProperty(element, 'clientHeight', { configurable: true, value: metrics.clientHeight });
  element.scrollTop = metrics.scrollTop;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('streamEntryContentOf', () => {
  it('reads text, scalars, progress, and structured values', () => {
    expect(streamEntryContentOf({ stringValue: 'Hello' })).toEqual({ kind: 'text', text: 'Hello' });
    expect(streamEntryContentOf({ intValue: 42 })).toEqual({ kind: 'scalar', text: '42' });
    expect(streamEntryContentOf({ boolValue: true })).toEqual({ kind: 'scalar', text: 'true' });
    expect(streamEntryContentOf(jsonValue({ callId: 'c-1', sequence: 2, phase: 'generating', message: 'Writing', percent: 0.42 })))
      .toMatchObject({ kind: 'progress', amount: 0.42, isFractionEligible: true, label: 'Writing', phase: 'generating' });
    expect(streamEntryContentOf(jsonValue({ sequence: 1, phase: 'queued' })))
      .toMatchObject({ kind: 'progress', amount: null, label: '', phase: 'queued' });
    expect(streamEntryContentOf(jsonValue({ progress: 75 }))).toMatchObject({ kind: 'progress', amount: 75 });
    expect(streamEntryContentOf(jsonValue({ orderId: 'o-1', total: 12 })))
      .toEqual({ kind: 'structured', value: { orderId: 'o-1', total: 12 } });
  });

  it('describes payloads in other encodings by encoding and size', () => {
    const rawValue = { objValue: { encoding: 'raw', payload: btoa('\u0000\u0001\u0002binary') } };
    expect(streamEntryContentOf(rawValue)).toEqual({ kind: 'undecoded', encoding: 'raw', byteSize: 9 });
    expect(decodedValue(rawValue)).toEqual({ encoding: 'raw', byteSize: 9 });
    expect(decodedValue({ objValue: { encoding: 'text', payload: btoa('plain') } })).toBe('plain');
  });
});

describe('progressPercentOf', () => {
  it('computes progress over total and keeps a progress count without a usable total structured', () => {
    expect(streamPercents([{ progress: 3, total: 10 }])).toEqual([30]);
    expect(streamEntryContentOf(jsonValue({ progress: 3, total: 'many' }))).toMatchObject({ kind: 'structured' });
  });

  it('reads a percent of 1 as 1% in a 0 to 100 Stream', () => {
    expect(streamPercents([{ percent: 1 }])).toEqual([1]);
    expect(streamPercents([
      { percent: 40, phase: 'working', sequence: 1 },
      { percent: 1, phase: 'working', sequence: 2 },
    ])).toEqual([40, 1]);
  });

  it('reads ProgressUpdate amounts as fractions', () => {
    expect(streamPercents([{ phase: 'started', percent: 0.1 }, { phase: 'done', percent: 1 }], 'sdkgo.ProgressUpdate'))
      .toEqual([10, 100]);
    expect(streamPercents([{ phase: 'started', sequence: 1, percent: 0.25 }])).toEqual([25]);
  });

  it('reads demo-like percent, phase, and message records as 0 to 100', () => {
    const values = [0, 1, 50, 100].map((percent) => ({ percent, phase: 'working', message: `At ${percent}` }));
    expect(streamPercents(values)).toEqual([0, 1, 50, 100]);
  });
});

describe('appendStreamEntry', () => {
  it('keeps only the newest messages and counts the dropped ones', () => {
    const messages = [1, 2, 3, 4, 5].map((sequence) => streamMessage({ stringValue: `m${sequence}` }, sequence));
    const state = entriesOf(messages, '', 3);
    expect(state.records.map((record) => record.sequenceNumber)).toEqual([3, 4, 5]);
    expect(state.droppedMessageCount).toBe(2);

    render(<StreamOutput entries={state} status="stopped" timezone="UTC" />);
    expect(screen.getByText(/2 older messages dropped/)).toBeTruthy();
  });
});

describe('isScrolledToLatest', () => {
  it('follows the latest message only near the bottom', () => {
    expect(isScrolledToLatest({ scrollTop: 600, scrollHeight: 1000, clientHeight: 400 })).toBe(true);
    expect(isScrolledToLatest({ scrollTop: 580, scrollHeight: 1000, clientHeight: 400 })).toBe(true);
    expect(isScrolledToLatest({ scrollTop: 300, scrollHeight: 1000, clientHeight: 400 })).toBe(false);
  });
});

describe('formatRelativeTime', () => {
  it('formats short relative times', () => {
    const now = Date.parse('2026-11-09T12:10:00Z');
    expect(formatRelativeTime('2026-11-09T12:09:58Z', now)).toBe('just now');
    expect(formatRelativeTime('2026-11-09T12:09:30Z', now)).toBe('30s ago');
    expect(formatRelativeTime('2026-11-09T12:07:00Z', now)).toBe('3m ago');
    expect(formatRelativeTime(null, now)).toBe('');
  });
});

describe('StreamOutput', () => {
  it('appends text messages into one transcript that keeps newlines', () => {
    const messages = [
      streamMessage({ stringValue: 'Hello, ' }, 1),
      streamMessage({ stringValue: 'world.\nSecond line' }, 2),
    ];
    const { container, rerender } = renderOutput(messages);

    const transcript = container.querySelector('.stream-transcript');
    expect(transcript?.textContent).toBe('Hello, world.\nSecond line');
    expect(transcript?.getAttribute('aria-live')).toBeNull();
    expect(screen.getByRole('button', { name: 'Transcript' }).getAttribute('aria-pressed')).toBe('true');

    rerender(
      <StreamOutput
        entries={entriesOf([...messages, streamMessage({ stringValue: ' Done.' }, 3)])}
        status="listening"
        timezone="UTC"
      />,
    );
    expect(container.querySelector('.stream-transcript')?.textContent).toBe('Hello, world.\nSecond line Done.');

    fireEvent.click(screen.getByRole('button', { name: 'Entries' }));
    expect(container.querySelectorAll('.stream-entry')).toHaveLength(3);
    expect(screen.getByText('#3')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Copy message 3' })).toBeTruthy();
    expect(container.querySelector('.stream-entries')?.getAttribute('aria-live')).toBeNull();
  });

  it('shows the transcript caret only while listening', () => {
    const entries = entriesOf([streamMessage({ stringValue: 'Hello' }, 1)]);
    const { container, rerender } = render(<StreamOutput entries={entries} status="listening" timezone="UTC" />);
    expect(container.querySelector('.stream-transcript-caret')).toBeTruthy();

    rerender(<StreamOutput entries={entries} status="stopped" timezone="UTC" />);
    expect(container.querySelector('.stream-transcript-caret')).toBeNull();
    rerender(<StreamOutput entries={entries} status="error" timezone="UTC" />);
    expect(container.querySelector('.stream-transcript-caret')).toBeNull();
  });

  it('opens number and boolean Streams as entries, not a joined transcript', () => {
    const { container } = renderOutput([
      streamMessage({ intValue: 1 }, 1),
      streamMessage({ intValue: 2 }, 2),
      streamMessage({ boolValue: false }, 3),
    ]);

    expect(screen.queryByRole('button', { name: 'Transcript' })).toBeNull();
    expect(container.querySelector('.stream-transcript')).toBeNull();
    expect([...container.querySelectorAll('.stream-entry-scalar')].map((entry) => entry.textContent))
      .toEqual(['1', '2', 'false']);
  });

  it('shows raw payloads by encoding and size, never in the transcript', () => {
    const { container } = renderOutput([
      streamMessage({ stringValue: 'Hello' }, 1),
      streamMessage({ objValue: { encoding: 'raw', payload: btoa('abc') } }, 2),
    ]);

    expect(container.querySelector('.stream-transcript')).toBeNull();
    expect(container.querySelector('.stream-entry-undecoded')?.textContent).toBe('Payload in raw encoding · 3 bytes');
  });

  it('shows a missing stored value as unavailable in the JSON and the copied text', async () => {
    const writeText = vi.fn(async (text: string) => {
      void text;
    });
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const { container } = renderOutput([
      streamMessage({ report: { __dexStoredValueUnavailable: true }, orderId: 'o-1' }, 1),
    ]);

    const json = container.querySelector('details.stream-json pre')?.textContent ?? '';
    expect(json).toContain(VALUE_BLOB_UNAVAILABLE);
    expect(json).not.toContain('__dexStoredValueUnavailable');

    fireEvent.click(screen.getByRole('button', { name: 'Copy message 1' }));
    await vi.waitFor(() => expect(writeText).toHaveBeenCalledTimes(1));
    expect(writeText.mock.calls[0][0]).toContain(VALUE_BLOB_UNAVAILABLE);
    expect(writeText.mock.calls[0][0]).not.toContain('__dexStoredValueUnavailable');
  });

  it('opens only the newest structured values and renders no JSON for closed ones', () => {
    const messages = [1, 2, 3, 4, 5].map((sequence) => streamMessage(jsonValue({ orderId: `o-${sequence}` }), sequence));
    const { container } = renderOutput(messages);

    const details = [...container.querySelectorAll('details.stream-json')];
    expect(details.map((detail) => detail.hasAttribute('open'))).toEqual([false, false, true, true, true]);
    expect(container.querySelectorAll('details.stream-json pre')).toHaveLength(3);
  });

  it('re-renders only the new entry and the one leaving the newest few when a message arrives', () => {
    const messages = [1, 2, 3, 4].map((sequence) => streamMessage(jsonValue({ orderId: `o-${sequence}` }), sequence));
    const firstEntries = entriesOf(messages.slice(0, 3));
    const { rerender } = render(<StreamOutput entries={firstEntries} status="listening" timezone="UTC" />);
    const formatDateMock = vi.mocked(formatDate);
    formatDateMock.mockClear();

    rerender(<StreamOutput entries={appendMessages(firstEntries, messages.slice(3))} status="listening" timezone="UTC" />);

    expect(formatDateMock.mock.calls.map(([createdTime]) => createdTime)).toEqual([
      messages[0].createdTime,
      messages[3].createdTime,
    ]);
  });

  it('pretty-prints structured values in collapsible entries', () => {
    const { container } = renderOutput([streamMessage(jsonValue({ orderId: 'o-1', total: 12 }), 1)]);

    expect(screen.queryByRole('button', { name: 'Transcript' })).toBeNull();
    const details = container.querySelector('details.stream-json');
    expect(details?.querySelector('summary')?.textContent).toBe('Object · 2 fields');
    expect(details?.querySelector('pre')?.textContent).toBe('{\n  "orderId": "o-1",\n  "total": 12\n}');
  });

  it('shows progress updates as progress bars with the latest one in focus and the full JSON collapsed', () => {
    const { container } = renderOutput([
      streamMessage(jsonValue({ sequence: 1, phase: 'started', percent: 0.1 }), 1),
      streamMessage(jsonValue({ sequence: 2, phase: 'generating', message: 'Writing summary', percent: 0.6, callId: 'c-1' }), 2),
    ]);

    const bars = screen.getAllByRole('progressbar');
    expect(bars.map((bar) => bar.getAttribute('aria-valuenow'))).toEqual(['60', '10', '60']);
    expect(container.querySelector('.stream-progress-focus')?.textContent).toContain('Writing summary');
    expect(container.querySelector('.stream-progress-focus')?.textContent).toContain('60%');

    const progressJSON = container.querySelectorAll('.stream-entry--progress details.stream-json');
    expect(progressJSON).toHaveLength(2);
    expect(progressJSON[1].hasAttribute('open')).toBe(false);
    expect(progressJSON[1].querySelector('summary')?.textContent).toBe('Object · 5 fields');
  });

  it('pauses following when scrolled up and offers a jump to the latest message', () => {
    const first = [streamMessage({ stringValue: 'one' }, 1)];
    const { rerender } = renderOutput(first);
    const scrollBox = screen.getByTestId('stream-scroll');

    setScrollMetrics(scrollBox, { scrollTop: 100, scrollHeight: 1000, clientHeight: 300 });
    fireEvent.scroll(scrollBox);
    rerender(
      <StreamOutput entries={entriesOf([...first, streamMessage({ stringValue: ' two' }, 2)])} status="listening" timezone="UTC" />,
    );

    expect(scrollBox.scrollTop).toBe(100);
    const jump = screen.getByRole('button', { name: /Jump to latest/ });
    expect(jump.textContent).toContain('1 new');

    fireEvent.click(jump);
    expect(scrollBox.scrollTop).toBe(1000);
    expect(screen.queryByRole('button', { name: /Jump to latest/ })).toBeNull();
  });
});
