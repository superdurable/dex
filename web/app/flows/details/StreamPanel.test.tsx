// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { FlowDefinitionGraph } from '@superdurable/flow-definition-renderer';
import { PreferencesProvider } from '@/app/providers';
import type { FlowDefinitionEntry } from '@/lib/types';
import { VALUE_BLOB_UNAVAILABLE } from '@/lib/unavailable';
import { decodedValue } from '../../components/StructuredValue';
import { definedStreamsOf, listenToStream, StreamPanel } from './StreamPanel';

vi.mock('../../components/StructuredValue', async (importOriginal) => {
  const original = await importOriginal<typeof import('../../components/StructuredValue')>();
  return { ...original, decodedValue: vi.fn(original.decodedValue) };
});

const CONNECTOR_FACTORY_FILE = '../docs/src/data/flow-definitions/connector-factory.json';

function readConnectorFactoryGraph(): FlowDefinitionGraph {
  return JSON.parse(readFileSync(resolve(process.cwd(), CONNECTOR_FACTORY_FILE), 'utf8')) as FlowDefinitionGraph;
}

function definitionWithStreams(streams: Array<{ name: string; valueType: string }>): FlowDefinitionEntry {
  const graph = readConnectorFactoryGraph();
  const nodes = graph.nodes.filter((node) => node.kind !== 'stream');
  for (const stream of streams) {
    nodes.push({ id: `resource:stream:${stream.name}`, kind: 'stream', name: stream.name, resource: { valueType: stream.valueType } });
  }
  return {
    id: 'connector-factory',
    file: 'connector-factory.json',
    schemaVersion: '2.0',
    flowName: 'CheckoutFlow',
    sourceLanguage: 'go',
    sourcePath: 'checkout.go',
    valid: true,
    source: 'directory',
    graph: { ...graph, nodes },
  };
}

function renderPanel(definition: FlowDefinitionEntry | null) {
  return render(
    <PreferencesProvider>
      <StreamPanel definition={definition} flowId="checkout-1" flowType="CheckoutFlow" />
    </PreferencesProvider>,
  );
}

function stubPendingFetch() {
  const fetcher = vi.fn((input: RequestInfo | URL) => {
    void input;
    return new Promise<Response>(() => {});
  });
  vi.stubGlobal('fetch', fetcher);
  return fetcher;
}

interface StreamResponseBody {
  value: unknown;
  resumeToken: string;
  createdTime: string;
  source: string;
}

/** Answers Stream reads with the given messages in order, then waits until aborted; blob loads fail. */
function stubStreamFetch(bodies: StreamResponseBody[]) {
  let readCount = 0;
  const fetcher = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
    if (String(input).startsWith('/api/blobs/load')) {
      return Promise.resolve(new Response(JSON.stringify({ error: 'blob store down' }), { status: 500 }));
    }
    const body = bodies[readCount];
    readCount += 1;
    if (body) return Promise.resolve(new Response(JSON.stringify(body), { status: 200 }));
    return new Promise<Response>((_resolve, reject) => {
      init?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
    });
  });
  vi.stubGlobal('fetch', fetcher);
  return fetcher;
}

function jsonStreamBody(value: unknown, sequence: number): StreamResponseBody {
  return {
    value: { objValue: { encoding: 'json', payload: btoa(JSON.stringify(value)) } },
    resumeToken: `token-${sequence}`,
    createdTime: '2026-11-09T12:00:00Z',
    source: '#Summarize-1',
  };
}

function streamNamesRequested(fetcher: ReturnType<typeof stubPendingFetch>): string[] {
  return fetcher.mock.calls.map(([input]) => new URL(String(input), 'http://web.test').searchParams.get('streamName') ?? '');
}

function intStreamBody(sequence: number): StreamResponseBody {
  return {
    value: { intValue: sequence },
    resumeToken: `token-${sequence}`,
    createdTime: '2026-11-09T12:00:00Z',
    source: '#Count-1',
  };
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  window.localStorage.clear();
});

describe('StreamPanel', () => {
  it('shows one Stream name control before it starts listening', () => {
    const markup = renderToStaticMarkup(
      <PreferencesProvider>
        <StreamPanel flowId="checkout-1" flowType="CheckoutFlow" />
      </PreferencesProvider>,
    );

    expect(markup).toContain('Stream messages');
    expect(markup).not.toContain('Resumable stream');
    expect(markup).toContain('Stream name');
    expect(markup).toContain('placeholder="a defined stream name"');
    expect(markup).toContain('Start listening');
    expect(markup).not.toContain('best effort');
  });

  it('continues after a long-poll timeout and resumes after each message', async () => {
    const controller = new AbortController();
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      void input;
      if (fetcher.mock.calls.length === 3) controller.abort();
      if (fetcher.mock.calls.length === 1) {
        return new Response(JSON.stringify({
          value: 'first token',
          resumeToken: 'token-1',
          createdTime: '2026-08-27T12:34:56Z',
          source: '#Step-1',
        }), { status: 200 });
      }
      return new Response(JSON.stringify({ error: 'Stream read timed out' }), { status: 408 });
    });
    const messages: string[] = [];

    await listenToStream({
      flowId: 'checkout-1',
      flowType: 'CheckoutFlow',
      streamName: 'thinking',
      signal: controller.signal,
      fetcher: fetcher as typeof fetch,
      onMessage: async (message) => {
        messages.push(`${message.resumeToken}:${message.source}`);
      },
    });

    expect(messages).toEqual(['token-1:#Step-1']);
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(String(fetcher.mock.calls[2][0])).toContain('resumeToken=token-1');
  });

  it('reads the defined Streams from a Flow Definition Graph file', () => {
    const definition = { ...definitionWithStreams([]), graph: readConnectorFactoryGraph() };

    expect(definedStreamsOf(definition)).toEqual([
      { name: 'customer-summary-progress', valueType: 'sdkgo.ProgressUpdate' },
      { name: 'customer-summary-text', valueType: 'string' },
    ]);
    expect(definedStreamsOf(null)).toEqual([]);
  });

  it('offers the defined Stream names when a definition matches and asks for a choice', () => {
    renderPanel(definitionWithStreams([
      { name: 'progress', valueType: 'sdkgo.ProgressUpdate' },
      { name: 'text', valueType: 'string' },
    ]));

    const select = screen.getByLabelText('Stream name') as HTMLSelectElement;
    expect(select.tagName).toBe('SELECT');
    expect(select.value).toBe('');
    expect([...select.options].map((option) => option.textContent)).toEqual([
      'Choose a Stream…',
      'progress · sdkgo.ProgressUpdate',
      'text · string',
      'Other name…',
    ]);
    expect(screen.queryByPlaceholderText('a defined stream name')).toBeNull();

    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));
    expect(screen.getByText('Choose a Stream.')).toBeTruthy();
  });

  it('selects the only defined Stream and listens to it', async () => {
    const fetcher = stubPendingFetch();
    renderPanel(definitionWithStreams([{ name: 'progress', valueType: 'string' }]));

    expect((screen.getByLabelText('Stream name') as HTMLSelectElement).value).toBe('progress');
    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));

    await waitFor(() => expect(streamNamesRequested(fetcher)).toEqual(['progress']));
  });

  it('listens to the chosen defined Stream', async () => {
    const fetcher = stubPendingFetch();
    renderPanel(definitionWithStreams([
      { name: 'progress', valueType: 'string' },
      { name: 'text', valueType: 'string' },
    ]));

    fireEvent.change(screen.getByLabelText('Stream name'), { target: { value: 'text' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));

    await waitFor(() => expect(streamNamesRequested(fetcher)).toEqual(['text']));
  });

  it('reveals a text box for another Stream name and listens to the typed name', async () => {
    const fetcher = stubPendingFetch();
    renderPanel(definitionWithStreams([{ name: 'progress', valueType: 'string' }]));

    const select = screen.getByLabelText('Stream name') as HTMLSelectElement;
    expect(screen.queryByLabelText('Other Stream name')).toBeNull();
    const otherOption = [...select.options].find((option) => option.textContent === 'Other name…');
    fireEvent.change(select, { target: { value: otherOption?.value } });
    fireEvent.change(screen.getByLabelText('Other Stream name'), { target: { value: ' dynamic-progress-7 ' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));

    await waitFor(() => expect(streamNamesRequested(fetcher)).toEqual(['dynamic-progress-7']));
  });

  it('shows live messages and stops and clears on request', async () => {
    let fetchCount = 0;
    const fetcher = vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      void input;
      fetchCount += 1;
      if (fetchCount === 1) {
        return Promise.resolve(new Response(JSON.stringify({
          value: { stringValue: 'Drafting the summary' },
          resumeToken: 'token-1',
          createdTime: '2026-11-09T12:00:00Z',
          source: '#Summarize-1',
        }), { status: 200 }));
      }
      return new Promise<Response>((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => reject(new DOMException('Aborted', 'AbortError')));
      });
    });
    vi.stubGlobal('fetch', fetcher);
    renderPanel(definitionWithStreams([{ name: 'summary-text', valueType: 'string' }]));
    expect(screen.getByText('Choose a Stream to watch')).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));
    await waitFor(() => expect(screen.getByText('Drafting the summary')).toBeTruthy());
    expect(screen.getByText('Live')).toBeTruthy();
    expect(screen.getByText('1 message', { selector: '.stream-session-count' })).toBeTruthy();

    fireEvent.click(screen.getByRole('button', { name: 'Stop' }));
    expect(screen.getByText('Stopped')).toBeTruthy();
    expect(screen.getByText('Drafting the summary')).toBeTruthy();
    expect(fetcher).toHaveBeenCalledTimes(2);

    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    expect(screen.queryByText('Drafting the summary')).toBeNull();
    expect(screen.getByText('No messages')).toBeTruthy();
  });

  it('keeps the text box when no definition matches or it declares no Streams', async () => {
    const fetcher = stubPendingFetch();
    const { unmount } = renderPanel(definitionWithStreams([]));
    expect(screen.queryByRole('combobox')).toBeNull();
    expect(screen.getByLabelText('Stream name').tagName).toBe('INPUT');
    unmount();

    renderPanel(null);
    expect(screen.queryByRole('combobox')).toBeNull();
    fireEvent.change(screen.getByLabelText('Stream name'), { target: { value: 'thinking' } });
    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));

    await waitFor(() => expect(streamNamesRequested(fetcher)).toEqual(['thinking']));
  });

  it('falls back to the default choice when the picked Stream leaves the definition', () => {
    const panelWith = (streams: Array<{ name: string; valueType: string }>) => (
      <PreferencesProvider>
        <StreamPanel definition={definitionWithStreams(streams)} flowId="checkout-1" flowType="CheckoutFlow" />
      </PreferencesProvider>
    );
    const { rerender } = render(panelWith([
      { name: 'progress', valueType: 'string' },
      { name: 'text', valueType: 'string' },
    ]));
    fireEvent.change(screen.getByLabelText('Stream name'), { target: { value: 'text' } });
    expect((screen.getByLabelText('Stream name') as HTMLSelectElement).value).toBe('text');

    rerender(panelWith([
      { name: 'progress', valueType: 'string' },
      { name: 'summary', valueType: 'string' },
    ]));
    expect((screen.getByLabelText('Stream name') as HTMLSelectElement).value).toBe('');

    rerender(panelWith([{ name: 'summary', valueType: 'string' }]));
    expect((screen.getByLabelText('Stream name') as HTMLSelectElement).value).toBe('summary');
  });

  it('keeps a name typed before the definition loads', () => {
    const panelWith = (definition: FlowDefinitionEntry | null) => (
      <PreferencesProvider>
        <StreamPanel definition={definition} flowId="checkout-1" flowType="CheckoutFlow" />
      </PreferencesProvider>
    );
    const { rerender } = render(panelWith(null));
    fireEvent.change(screen.getByLabelText('Stream name'), { target: { value: 'thinking' } });

    rerender(panelWith(definitionWithStreams([{ name: 'progress', valueType: 'string' }])));
    expect((screen.getByLabelText('Stream name') as HTMLSelectElement).value).not.toBe('progress');
    expect((screen.getByLabelText('Other Stream name') as HTMLInputElement).value).toBe('thinking');
  });

  it('classifies each message once and reads a declared ProgressUpdate Stream as fractions', async () => {
    stubStreamFetch([1, 2, 3, 4, 5, 6].map((sequence) => (
      jsonStreamBody({ callId: 'c-1', phase: 'generating', message: `Step ${sequence}`, percent: sequence / 10 }, sequence)
    )));
    const decodedValueMock = vi.mocked(decodedValue);
    decodedValueMock.mockClear();
    const { container } = renderPanel(definitionWithStreams([{ name: 'progress', valueType: 'sdkgo.ProgressUpdate' }]));

    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));
    await waitFor(() => expect(screen.getByText('6 messages', { selector: '.stream-session-count' })).toBeTruthy());

    expect(decodedValueMock).toHaveBeenCalledTimes(6);
    expect(container.querySelector('.stream-progress-focus [role="progressbar"]')?.getAttribute('aria-valuenow')).toBe('60');
    expect(container.querySelector('.stream-session-header .sr-only[aria-live="polite"]')).toBeTruthy();
  });

  it('keeps the last 1,000 messages by default and trims at once to a remembered lower limit', async () => {
    stubStreamFetch(Array.from({ length: 150 }, (_, index) => intStreamBody(index + 1)));
    const { container, unmount } = renderPanel(definitionWithStreams([{ name: 'count', valueType: 'int' }]));

    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));
    await waitFor(() => expect(screen.getByText('150 messages', { selector: '.stream-session-count' })).toBeTruthy());
    const keepLast = screen.getByLabelText('Keep last') as HTMLSelectElement;
    expect(keepLast.value).toBe('1000');
    expect([...keepLast.options].map((option) => option.value)).toEqual(['100', '500', '1000']);
    expect(container.querySelector('.stream-dropped-note')).toBeNull();

    fireEvent.change(keepLast, { target: { value: '100' } });
    expect(container.querySelectorAll('.stream-entry')).toHaveLength(100);
    expect(container.querySelector('.stream-entry-scalar')?.textContent).toBe('150');
    expect(container.querySelector('.stream-dropped-note')?.textContent)
      .toBe('50 older messages dropped · showing the newest 100');
    expect(screen.getByText('150 messages', { selector: '.stream-session-count' })).toBeTruthy();
    expect(window.localStorage.getItem('dex-web-stream-kept-messages')).toBe('100');
    unmount();

    stubPendingFetch();
    renderPanel(definitionWithStreams([{ name: 'count', valueType: 'int' }]));
    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));
    expect((screen.getByLabelText('Keep last') as HTMLSelectElement).value).toBe('100');
  });

  it('keeps 1,000 messages when the stored limit is invalid or storage is unavailable', () => {
    stubPendingFetch();
    window.localStorage.setItem('dex-web-stream-kept-messages', '5000');
    const { unmount } = renderPanel(definitionWithStreams([{ name: 'count', valueType: 'int' }]));
    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));
    expect((screen.getByLabelText('Keep last') as HTMLSelectElement).value).toBe('1000');
    unmount();

    // Only the Stream limit is blocked; the timezone preference does not guard its storage access.
    const originalGetItem = Storage.prototype.getItem;
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(function (this: Storage, key: string) {
      if (key === 'dex-web-stream-kept-messages') throw new DOMException('Blocked', 'SecurityError');
      return originalGetItem.call(this, key);
    });
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => {
      throw new DOMException('Blocked', 'SecurityError');
    });
    renderPanel(definitionWithStreams([{ name: 'count', valueType: 'int' }]));
    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));
    const keepLast = screen.getByLabelText('Keep last') as HTMLSelectElement;
    expect(keepLast.value).toBe('1000');
    fireEvent.change(keepLast, { target: { value: '500' } });
    expect(keepLast.value).toBe('500');
  });

  it('clears the stored value warning with the messages', async () => {
    stubStreamFetch([{
      value: { __dexBlobReference: { kind: 'object', id: 'blob-1' } },
      resumeToken: 'token-1',
      createdTime: '2026-11-09T12:00:00Z',
      source: '#Summarize-1',
    }]);
    const { container } = renderPanel(definitionWithStreams([{ name: 'report', valueType: 'string' }]));

    fireEvent.click(screen.getByRole('button', { name: 'Start listening' }));
    await waitFor(() => expect(container.querySelector('.warning-banner')?.textContent).toBe(VALUE_BLOB_UNAVAILABLE));

    fireEvent.click(screen.getByRole('button', { name: 'Clear' }));
    expect(container.querySelector('.warning-banner')).toBeNull();
  });
});
