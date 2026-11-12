// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { clearFlowTypeDefinitionCache, fetchFlowTypeDefinitions } from '../catalog/flowTypeDefinitionCache';
import { FLOW_DEFINITION_SOURCES_URL, FlowRenderingPage } from './FlowRenderingPage';

vi.mock('../design/FlowDesignGraph', () => ({
  FlowDesignGraph: ({ definition }: { definition: { id: string } }) => <div data-testid="design-graph" data-definition={definition.id} />,
}));

const graph = (name: string) => ({
  schemaVersion: '2.0', valid: true, source: { language: 'go', path: 'flow.go' }, flow: { name },
  nodes: [{ id: 'step:a', kind: 'step', name: 'A' }], edges: [], diagnostics: [],
});

const summary = (id: string, flowName: string, source: string, extra: Record<string, unknown> = {}) => ({
  id, file: source === 'directory' ? `${flowName}.json` : '', schemaVersion: '2.0', flowName,
  sourceLanguage: 'go', sourcePath: 'flow.go', valid: true, source, ...extra,
});

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });

beforeEach(() => {
  clearFlowTypeDefinitionCache();
  window.history.replaceState(null, '', '/v1/rendering');
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Flow type definition cache', () => {
  it('revalidates by revision and keeps the cached graph on 304', async () => {
    const answer = { flowType: 'OrderFlow', definitionRevision: 'sha256:one', definitions: [summary('d', 'OrderFlow', 'uploaded', { graph: graph('OrderFlow') })], v2: null };
    const fetchMock = vi.fn(async (_input: string, init?: RequestInit) => {
      const headers = new Headers(init?.headers);
      return headers.get('If-None-Match') === '"sha256:one' + '"' ? new Response(null, { status: 304 }) : json(answer);
    });
    vi.stubGlobal('fetch', fetchMock);
    expect(await fetchFlowTypeDefinitions('Order/Flow')).toEqual(answer);
    expect(await fetchFlowTypeDefinitions('Order/Flow')).toEqual(answer);
    expect(fetchMock.mock.calls.map(([input]) => input)).toEqual(['/api/flow-definitions/by-type?flowType=Order%2FFlow', '/api/flow-definitions/by-type?flowType=Order%2FFlow']);
    expect(new Headers(fetchMock.mock.calls[1][1]?.headers).get('If-None-Match')).toBe('"sha256:one"');
  });

  it('answers null for a Flow type without a definition', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ code: 'FLOW_DEFINITION_NOT_FOUND', error: 'none' }, 404)));
    expect(await fetchFlowTypeDefinitions('MissingFlow')).toBeNull();
  });
});

describe('Designs page', () => {
  it('lists metadata, then reads only the selected Flow type, and shows each source', async () => {
    const requests: string[] = [];
    vi.stubGlobal('fetch', vi.fn(async (input: string) => {
      requests.push(input);
      if (input === '/api/flow-definitions') {
        return json({
          configured: true, source: 'local+server', directory: '/defs', definitionCount: 3,
          definitions: [
            summary('order.json', 'OrderFlow', 'directory'),
            summary('uploaded:RefundFlow', 'RefundFlow', 'uploaded', { generatedAt: '2026-11-01T00:00:00Z', uploadedAt: '2026-11-02T00:00:00Z' }),
            { ...summary('uploaded:BadFlow', 'BadFlow', 'uploaded'), schemaVersion: '', valid: false, error: 'it has 3000 nodes; the limit is 2000' },
          ],
        });
      }
      const flowType = new URL(input, 'http://dex.test').searchParams.get('flowType') ?? '';
      const id = flowType === 'OrderFlow' ? 'order.json' : `uploaded:${flowType}`;
      return json({ flowType, definitionRevision: `r-${flowType}`, definitions: [summary(id, flowType, 'x', { graph: graph(flowType) })], v2: null });
    }));
    render(<MemoryRouter><FlowRenderingPage /></MemoryRouter>);

    expect((await screen.findByTestId('design-graph')).getAttribute('data-definition')).toBe('order.json');
    expect(screen.getByText('3 definitions')).toBeTruthy();
    expect(screen.getByText('Directory file')).toBeTruthy();
    expect(screen.getAllByText('Uploaded to the Dex Server')).toHaveLength(2);
    expect(screen.getByText(/^Generated /)).toBeTruthy();
    expect(screen.getByText(/^Uploaded (?!to the)/)).toBeTruthy();
    expect(requests).toEqual(['/api/flow-definitions', '/api/flow-definitions/by-type?flowType=OrderFlow']);

    fireEvent.click(screen.getByRole('button', { name: /RefundFlow/ }));
    await waitFor(() => expect(screen.getByTestId('design-graph').getAttribute('data-definition')).toBe('uploaded:RefundFlow'));

    fireEvent.click(screen.getByRole('button', { name: /BadFlow/ }));
    expect((await screen.findByRole('alert')).textContent).toContain('the limit is 2000');
    expect(requests).not.toContain('/api/flow-definitions/by-type?flowType=BadFlow');
  });

  it('shows a malformed graph as an error instead of crashing, and ignores a late answer for another selection', async () => {
    const releases: Record<string, () => void> = {};
    vi.stubGlobal('fetch', vi.fn((input: string) => {
      if (input === '/api/flow-definitions') {
        return Promise.resolve(json({
          configured: true, source: 'server', definitionCount: 2,
          definitions: [summary('uploaded:SlowFlow', 'SlowFlow', 'uploaded'), summary('uploaded:BrokenFlow', 'BrokenFlow', 'uploaded')],
        }));
      }
      const flowType = new URL(input, 'http://dex.test').searchParams.get('flowType') ?? '';
      const broken = { ...graph(flowType), diagnostics: [null] };
      const body = json({
        flowType, definitionRevision: `r-${flowType}`, v2: null,
        definitions: [summary(`uploaded:${flowType}`, flowType, 'uploaded', { graph: flowType === 'BrokenFlow' ? broken : graph(flowType) })],
      });
      return new Promise<Response>((resolve) => { releases[flowType] = () => resolve(body); });
    }));
    render(<MemoryRouter><FlowRenderingPage /></MemoryRouter>);
    await waitFor(() => expect(releases.SlowFlow).toBeDefined());
    fireEvent.click(screen.getByRole('button', { name: /BrokenFlow/ }));
    await waitFor(() => expect(releases.BrokenFlow).toBeDefined());
    releases.BrokenFlow();
    expect((await screen.findByRole('alert')).textContent).toContain('diagnostic 0 is malformed');
    releases.SlowFlow();
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(screen.queryByTestId('design-graph')).toBeNull();
    expect(screen.getByRole('alert').textContent).toContain('diagnostic 0 is malformed');
  });

  it('counts a single definition in the singular', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: string) => (input === '/api/flow-definitions'
      ? json({ configured: true, source: 'server', definitionCount: 1, definitions: [summary('uploaded:OrderFlow', 'OrderFlow', 'uploaded')] })
      : json({ flowType: 'OrderFlow', definitionRevision: 'r', v2: null, definitions: [summary('uploaded:OrderFlow', 'OrderFlow', 'uploaded', { graph: graph('OrderFlow') })] }))));
    render(<MemoryRouter><FlowRenderingPage /></MemoryRouter>);
    expect(await screen.findByText('1 definition')).toBeTruthy();
  });

  it('explains uploading definitions at Worker start when there are none', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => json({ configured: true, source: 'server', definitionCount: 0, definitions: [] })));
    render(<MemoryRouter><FlowRenderingPage /></MemoryRouter>);
    expect(await screen.findByText(/uploads it to the Dex Server when it starts/)).toBeTruthy();
    expect(screen.getByRole('link', { name: 'How Dex Web finds Flow definitions' }).getAttribute('href')).toBe(FLOW_DEFINITION_SOURCES_URL);
  });
});
