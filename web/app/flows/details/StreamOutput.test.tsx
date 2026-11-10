// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it } from 'vitest';
import type { StreamMessage } from '@/lib/types';
import {
  formatRelativeTime,
  isScrolledToLatest,
  StreamOutput,
  streamEntryContentOf,
} from './StreamOutput';

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

function setScrollMetrics(element: HTMLElement, metrics: { scrollTop: number; scrollHeight: number; clientHeight: number }) {
  Object.defineProperty(element, 'scrollHeight', { configurable: true, value: metrics.scrollHeight });
  Object.defineProperty(element, 'clientHeight', { configurable: true, value: metrics.clientHeight });
  element.scrollTop = metrics.scrollTop;
}

afterEach(cleanup);

describe('streamEntryContentOf', () => {
  it('reads text, progress, and structured values', () => {
    expect(streamEntryContentOf({ stringValue: 'Hello' })).toEqual({ kind: 'text', text: 'Hello' });
    expect(streamEntryContentOf(jsonValue({ callId: 'c-1', sequence: 2, phase: 'generating', message: 'Writing', percent: 0.42 })))
      .toMatchObject({ kind: 'progress', percent: 42, label: 'Writing', phase: 'generating' });
    expect(streamEntryContentOf(jsonValue({ sequence: 1, phase: 'queued' })))
      .toMatchObject({ kind: 'progress', percent: null, label: '', phase: 'queued' });
    expect(streamEntryContentOf(jsonValue({ progress: 75 }))).toMatchObject({ kind: 'progress', percent: 75 });
    expect(streamEntryContentOf(jsonValue({ orderId: 'o-1', total: 12 })))
      .toEqual({ kind: 'structured', value: { orderId: 'o-1', total: 12 } });
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
    const { container, rerender } = render(<StreamOutput messages={messages} timezone="UTC" />);

    const transcript = container.querySelector('.stream-transcript');
    expect(transcript?.textContent).toBe('Hello, world.\nSecond line');
    expect(screen.getByRole('button', { name: 'Transcript' }).getAttribute('aria-pressed')).toBe('true');

    rerender(<StreamOutput messages={[...messages, streamMessage({ stringValue: ' Done.' }, 3)]} timezone="UTC" />);
    expect(container.querySelector('.stream-transcript')?.textContent).toBe('Hello, world.\nSecond line Done.');

    fireEvent.click(screen.getByRole('button', { name: 'Entries' }));
    expect(container.querySelectorAll('.stream-entry')).toHaveLength(3);
    expect(screen.getByText('#3')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Copy message 3' })).toBeTruthy();
  });

  it('pretty-prints structured values in collapsible entries', () => {
    const { container } = render(
      <StreamOutput messages={[streamMessage(jsonValue({ orderId: 'o-1', total: 12 }), 1)]} timezone="UTC" />,
    );

    expect(screen.queryByRole('button', { name: 'Transcript' })).toBeNull();
    const details = container.querySelector('details.stream-json');
    expect(details?.querySelector('summary')?.textContent).toBe('Object · 2 fields');
    expect(details?.querySelector('pre')?.textContent).toBe('{\n  "orderId": "o-1",\n  "total": 12\n}');
  });

  it('shows progress updates as progress bars with the latest one in focus', () => {
    const { container } = render(
      <StreamOutput
        messages={[
          streamMessage(jsonValue({ sequence: 1, phase: 'started', percent: 0.1 }), 1),
          streamMessage(jsonValue({ sequence: 2, phase: 'generating', message: 'Writing summary', percent: 0.6 }), 2),
        ]}
        timezone="UTC"
      />,
    );

    const bars = screen.getAllByRole('progressbar');
    expect(bars.map((bar) => bar.getAttribute('aria-valuenow'))).toEqual(['60', '10', '60']);
    expect(container.querySelector('.stream-progress-focus')?.textContent).toContain('Writing summary');
    expect(container.querySelector('.stream-progress-focus')?.textContent).toContain('60%');
  });

  it('pauses following when scrolled up and offers a jump to the latest message', () => {
    const first = [streamMessage({ stringValue: 'one' }, 1)];
    const { rerender } = render(<StreamOutput messages={first} timezone="UTC" />);
    const scrollBox = screen.getByTestId('stream-scroll');

    setScrollMetrics(scrollBox, { scrollTop: 100, scrollHeight: 1000, clientHeight: 300 });
    fireEvent.scroll(scrollBox);
    rerender(<StreamOutput messages={[...first, streamMessage({ stringValue: ' two' }, 2)]} timezone="UTC" />);

    expect(scrollBox.scrollTop).toBe(100);
    const jump = screen.getByRole('button', { name: /Jump to latest/ });
    expect(jump.textContent).toContain('1 new');

    fireEvent.click(jump);
    expect(scrollBox.scrollTop).toBe(1000);
    expect(screen.queryByRole('button', { name: /Jump to latest/ })).toBeNull();
  });
});
