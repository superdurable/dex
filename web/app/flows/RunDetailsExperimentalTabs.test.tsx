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
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { FlowDefinitionGraph, FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { WebCatalogProvider } from '@/app/catalog/WebCatalogProvider';
import { PreferencesProvider } from '@/app/providers';
import type { FlowDefinitionEntry, FlowState, FlowSummary } from '@/lib/types';
import { RunDetailsPage } from './RunDetailsPage';

vi.mock('./details/ExecutionGraph', () => ({ ExecutionGraph: () => <div data-testid="execution-graph" /> }));
vi.mock('../design/FlowDesignGraph', () => ({
  FlowDesignGraph: ({ definition }: { definition: FlowDefinitionEntry }) => (
    <div data-testid="design-graph" data-definition={definition.id} />
  ),
}));
vi.mock('../agents/SessionView', () => ({
  SessionView: ({ flowId }: { flowId: string }) => <div data-testid="session-view" data-flow-id={flowId} />,
}));

const emptyState: FlowState = {
  flowConfig: {},
  attributes: [],
  activeStepExecutions: [],
  queuedSteps: [],
  pendingChannelMessages: {},
  completedSteps: [],
};

function summaryOf(flowType: string): FlowSummary {
  return {
    flowId: 'run-flow',
    runId: 'run-1',
    firstRunId: 'run-1',
    requestId: 'request-1',
    flowType,
    flowStatus: 'Running',
    flowStatusCode: 1,
    startTime: '2026-10-24T17:00:00Z',
    closeTime: null,
    runAttempt: 1,
  };
}

function sessionGraph(): FlowDefinitionGraph {
  const path = resolve(process.cwd(), '../agents/conformance/session_graph.json');
  return JSON.parse(readFileSync(path, 'utf8')) as FlowDefinitionGraph;
}

function definitionEntry(id: string, flowName: string, graph: FlowDefinitionGraph): FlowDefinitionEntry {
  return {
    id,
    file: `${id}.json`,
    schemaVersion: graph.schemaVersion === '2.0' ? '2.0' : '1.0',
    flowName,
    sourceLanguage: 'python',
    sourcePath: 'flow.py',
    valid: true,
    graph,
  };
}

const plainGraph = {
  schemaVersion: '1.0',
  valid: true,
  source: { language: 'go', path: 'flow.go' },
  flow: { name: 'OrderFlow', startStepId: 'step:Charge' },
  nodes: [{ id: 'step:Charge', kind: 'step', name: 'Charge' }],
  edges: [],
  diagnostics: [],
} as unknown as FlowDefinitionGraph;

interface DexWebStub {
  flowType: string;
  definitions: FlowDefinitionEntry[];
  operatorFlows?: { flowType: string; definition: FlowV2Definition }[];
}

/** Serves one run, its definitions and an optional operator catalog, and records every request path. */
function stubDexWeb({ flowType, definitions, operatorFlows }: DexWebStub) {
  const requests: string[] = [];
  const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
  vi.stubGlobal('fetch', vi.fn(async (input: string, init?: RequestInit) => {
    const url = new URL(input, 'http://dex.test');
    requests.push(url.pathname);
    if (url.pathname === '/api/flow-definitions') {
      return json({ configured: true, definitionRevision: 'r1', definitionCount: definitions.length, definitions });
    }
    if (url.pathname === '/api/v2/catalog') {
      return operatorFlows ? json({ enabled: true, definitionRevision: 'r1', flows: operatorFlows }) : json({}, 404);
    }
    if (url.pathname === '/api/flows/summary') return json(summaryOf(flowType));
    if (url.pathname === '/api/flows/state') return json(emptyState);
    if (url.pathname === '/api/flows/history') {
      return json({
        events: [{ eventId: 1, eventTime: '2026-10-24T17:00:00Z', type: 'FlowStartedOrContinued', payload: {}, runAttempt: 1 }],
        nextPageToken: '',
        nextInternalEventId: 2,
      });
    }
    if (url.pathname === '/api/flows/wait' && url.searchParams.get('nextInternalEventId') === '1') {
      return json({ eventAvailable: true, availableInternalEventId: '1' });
    }
    if (url.pathname === '/api/flows/wait') {
      await new Promise<void>((_resolve, reject) => {
        init?.signal?.addEventListener('abort', () => reject(new DOMException('aborted', 'AbortError')));
      });
    }
    return json({ error: `unexpected ${url.pathname}` }, 500);
  }));
  return requests;
}

function renderRun(entry = '/v1/flows/run-flow/run-1') {
  render(
    <MemoryRouter initialEntries={[entry]}>
      <PreferencesProvider>
        <WebCatalogProvider>
          <RunDetailsPage breadcrumb={null} flowId="run-flow" runId="run-1" runPath={(runID) => `/v1/flows/run-flow/${runID}`} />
        </WebCatalogProvider>
      </PreferencesProvider>
    </MemoryRouter>,
  );
}

function tabLabels(): string[] {
  const group = screen.getByRole('group', { name: 'Run views' });
  return [...group.querySelectorAll('button')].map((button) => button.getAttribute('data-tab') ?? '');
}

function memoryStorage(): Storage {
  const values = new Map<string, string>();
  return {
    get length() { return values.size; },
    clear: () => values.clear(),
    getItem: (key) => values.get(key) ?? null,
    key: (index) => [...values.keys()][index] ?? null,
    removeItem: (key) => { values.delete(key); },
    setItem: (key, value) => { values.set(key, value); },
  };
}

beforeEach(() => {
  vi.stubGlobal('localStorage', memoryStorage());
  Element.prototype.scrollIntoView = vi.fn();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Run page experimental tabs', () => {
  it('shows Design after Streams for a run whose Flow type has a loaded definition', async () => {
    stubDexWeb({ flowType: 'OrderFlow', definitions: [definitionEntry('order', 'OrderFlow', plainGraph)] });
    renderRun();
    await waitFor(() => expect(tabLabels()).toContain('design'));
    expect(tabLabels()).toEqual(['overview', 'steps', 'timeline', 'stream', 'design']);
    const design = screen.getByRole('button', { name: /^Design/ });
    expect(design.querySelector('.experimental-badge')?.textContent).toBe('Experimental');
  });

  it('draws only the definition: opening Design reads no history and no stored values', async () => {
    const requests = stubDexWeb({ flowType: 'OrderFlow', definitions: [definitionEntry('order', 'OrderFlow', plainGraph)] });
    renderRun();
    const design = await screen.findByRole('button', { name: /^Design/ });
    await waitFor(() => expect(requests).toContain('/api/flows/history'));
    const before = requests.length;
    fireEvent.click(design);
    expect((await screen.findByTestId('design-graph')).getAttribute('data-definition')).toBe('order');
    await new Promise((resolve) => setTimeout(resolve, 50));
    const opened = requests.slice(before);
    expect(opened.filter((path) => path === '/api/flows/history' || path === '/api/blobs/load')).toEqual([]);
  });

  it('hides Design when no loaded definition matches the Flow type', async () => {
    stubDexWeb({ flowType: 'RefundFlow', definitions: [definitionEntry('order', 'OrderFlow', plainGraph)] });
    renderRun();
    await screen.findByText('Running');
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(tabLabels()).toEqual(['overview', 'steps', 'timeline', 'stream']);
  });

  it('falls back to the Execution graph for a remembered tab that does not apply', async () => {
    window.localStorage.setItem('dex.run.selected-tab', 'design');
    stubDexWeb({ flowType: 'RefundFlow', definitions: [] });
    renderRun();
    expect(await screen.findByTestId('execution-graph')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Execution graph' }).getAttribute('aria-pressed')).toBe('true');
  });

  it('adds Agent session and Actions for a Dex Agents session served by the operator API', async () => {
    const graph = sessionGraph();
    const flowType = graph.flow.name;
    stubDexWeb({
      flowType,
      definitions: [definitionEntry('session', flowType, graph)],
      operatorFlows: [{ flowType, definition: graph.v2 as FlowV2Definition }],
    });
    renderRun('/v1/flows/run-flow/run-1?view=session');
    expect((await screen.findByTestId('session-view')).getAttribute('data-flow-id')).toBe('run-flow');
    expect(tabLabels()).toEqual(['overview', 'steps', 'timeline', 'stream', 'design', 'session', 'actions']);
  });

  it('hides Agent session and Actions without the operator API', async () => {
    const graph = sessionGraph();
    stubDexWeb({ flowType: graph.flow.name, definitions: [definitionEntry('session', graph.flow.name, graph)] });
    renderRun('/v1/flows/run-flow/run-1?view=session');
    await waitFor(() => expect(tabLabels()).toContain('design'));
    expect(tabLabels()).toEqual(['overview', 'steps', 'timeline', 'stream', 'design']);
    expect(screen.queryByTestId('session-view')).toBeNull();
  });
});
