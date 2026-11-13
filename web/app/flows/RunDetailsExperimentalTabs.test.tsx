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
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, useLocation, useNavigate } from 'react-router-dom';
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
  SessionView: ({ flowId, workQueuePermissions }: { flowId: string; workQueuePermissions: string[] }) => (
    <div data-testid="session-view" data-flow-id={flowId} data-permissions={workQueuePermissions.join(',')} />
  ),
}));

const emptyState: FlowState = {
  flowConfig: {},
  attributes: [],
  activeStepExecutions: [],
  queuedSteps: [],
  pendingChannelMessages: {},
  completedSteps: [],
};

function summaryOf(flowType: string, overrides: Partial<FlowSummary> = {}): FlowSummary {
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
    ...overrides,
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
    source: 'directory',
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

/** The session parts Dex Web's server reads from a session graph, or undefined for any other graph. */
function agentSessionOf(definitions: FlowDefinitionEntry[]) {
  const graph = definitions.find((definition) => definition.schemaVersion === '2.0' && definition.valid)?.graph;
  const isSession = graph?.nodes.some((node) => node.kind === 'step' && node.name === 'StartAgentSession' && node.start === true);
  if (!graph || !isSession) return undefined;
  return {
    language: graph.source.language,
    hasQuestions: graph.nodes.some((node) => node.kind === 'step' && node.name === 'AwaitQuestionAnswer'),
    rpcNames: graph.nodes.filter((node) => node.kind === 'rpc').map((node) => node.name),
  };
}

interface DexWebStub {
  flowType: string;
  definitions: FlowDefinitionEntry[];
  operatorFlows?: { flowType: string; definition: FlowV2Definition }[];
  /** Overrides the summary of the run on screen, run-1. */
  summary?: Partial<FlowSummary>;
  /** The run a summary request without a run ID resolves to. */
  currentRunId?: string;
  /** Holds the definitions response until this promise settles. */
  definitionsReleased?: Promise<void>;
}

/** Serves one run, its definitions and an optional operator catalog, and records every request path. */
function stubDexWeb({
  flowType,
  definitions,
  operatorFlows,
  summary,
  currentRunId = 'run-1',
  definitionsReleased,
}: DexWebStub) {
  const requests: string[] = [];
  const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    const url = new URL(input, 'http://dex.test');
    requests.push(url.pathname);
    if (url.pathname === '/api/flow-definition-types') {
      const flowTypes = [...new Set(definitions.map((definition) => definition.flowName))];
      return json({
        configured: true,
        operatorAPI: operatorFlows !== undefined,
        flowTypes: flowTypes.map((name) => ({ flowType: name, source: 'directory' })),
      });
    }
    if (url.pathname === '/api/flow-definitions/by-type') {
      await definitionsReleased;
      const requested = url.searchParams.get('flowType') ?? '';
      const matches = definitions.filter((definition) => definition.flowName === requested);
      if (matches.length === 0) return json({ code: 'FLOW_DEFINITION_NOT_FOUND', error: 'not found' }, 404);
      const operatorFlow = operatorFlows?.find((candidate) => candidate.flowType === requested);
      return json({
        flowType: requested,
        definitionRevision: `r-${requested}`,
        definitions: matches,
        v2: operatorFlow ? { ...operatorFlow, definitionRevision: `r-${requested}`, agentSession: agentSessionOf(matches) } : null,
      });
    }
    if (url.pathname === '/api/v2/catalog') {
      return operatorFlows ? json({ enabled: true, definitionRevision: 'r1', flows: operatorFlows }) : json({}, 404);
    }
    if (url.pathname === '/api/flows/summary' && !url.searchParams.has('runId')) {
      return json(summaryOf(flowType, { runId: currentRunId }));
    }
    if (url.pathname === '/api/flows/summary') return json(summaryOf(flowType, summary));
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
    // Answers promptly, so a wait loop that should not run shows up as repeated requests.
    if (url.pathname === '/api/flows/wait') {
      await new Promise((resolveWait) => setTimeout(resolveWait, 5));
      return json({}, 408);
    }
    return json({ error: `unexpected ${url.pathname}` }, 500);
  }));
  return requests;
}

function LocationProbe() {
  const location = useLocation();
  return <output aria-label="location">{`${location.pathname}${location.search}`}</output>;
}

function NavigateButton({ to }: { to: string }) {
  const navigate = useNavigate();
  return <button onClick={() => navigate(to)} type="button">Navigate</button>;
}

function renderRun(entry = '/v1/flows/run-flow/run-1', navigateTo = '/v1/flows/run-flow/run-1') {
  render(
    <MemoryRouter initialEntries={[entry]}>
      <PreferencesProvider>
        <WebCatalogProvider>
          <RunDetailsPage flowId="run-flow" runId="run-1" runPath={(runID) => `/v1/flows/run-flow/${runID}`} />
          <LocationProbe />
          <NavigateButton to={navigateTo} />
        </WebCatalogProvider>
      </PreferencesProvider>
    </MemoryRouter>,
  );
}

/** Requests that read the run's history, state, stored values, or wait for new events. */
function runDataRequests(requests: string[]): string[] {
  const runDataPaths = new Set(['/api/flows/history', '/api/flows/state', '/api/blobs/load', '/api/flows/wait']);
  return requests.filter((path) => runDataPaths.has(path));
}

/** Lets every queued fetch and timer callback run, so work the page started becomes visible. */
async function settle() {
  for (let round = 0; round < 5; round += 1) {
    await act(async () => {
      await new Promise((resolveRound) => setTimeout(resolveRound, 10));
    });
  }
}

function tabLabels(): string[] {
  const group = screen.getByRole('tablist', { name: 'Run views' });
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
  // Runs open on the Timeline; the selected-event connector and the vertical Timeline observe sizes.
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    unobserve() {}
    disconnect() {}
  });
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
    expect(tabLabels()).toEqual(['timeline', 'steps', 'overview', 'stream', 'design']);
    const design = screen.getByRole('tab', { name: /^Design/ });
    expect(design.querySelector('.experimental-dot')?.getAttribute('aria-label')).toBe('Experimental');
  });

  it('reads only the definitions of the run Flow type, never every definition', async () => {
    const requests = stubDexWeb({
      flowType: 'OrderFlow',
      definitions: [definitionEntry('order', 'OrderFlow', plainGraph), definitionEntry('other', 'OtherFlow', plainGraph)],
    });
    renderRun();
    await waitFor(() => expect(tabLabels()).toContain('design'));
    const definitionRequests = requests.filter((path) => path.includes('definition') || path === '/api/v2/catalog');
    expect(definitionRequests).toEqual(['/api/flow-definition-types', '/api/flow-definitions/by-type']);
  });

  it('opens a running Flow on Design without reading history, state, stored values or waiting', async () => {
    const requests = stubDexWeb({ flowType: 'OrderFlow', definitions: [definitionEntry('order', 'OrderFlow', plainGraph)] });
    renderRun('/v1/flows/run-flow/run-1?view=design');
    expect((await screen.findByTestId('design-graph')).getAttribute('data-definition')).toBe('order');
    await settle();
    expect(requests).toContain('/api/flows/summary');
    expect(runDataRequests(requests)).toEqual([]);

    fireEvent.click(screen.getByRole('tab', { name: 'Execution graph' }));
    await waitFor(() => expect(runDataRequests(requests)).toEqual(expect.arrayContaining([
      '/api/flows/history',
      '/api/flows/state',
      '/api/flows/wait',
    ])));
  });

  it('waits for the definitions before reading history when the summary arrives first', async () => {
    let releaseDefinitions = () => {};
    const definitionsReleased = new Promise<void>((resolveDefinitions) => { releaseDefinitions = resolveDefinitions; });
    const requests = stubDexWeb({
      flowType: 'OrderFlow',
      definitions: [definitionEntry('order', 'OrderFlow', plainGraph)],
      definitionsReleased,
    });
    renderRun('/v1/flows/run-flow/run-1?view=design');
    await screen.findByText('Running');
    await settle();
    expect(runDataRequests(requests)).toEqual([]);

    releaseDefinitions();
    expect(await screen.findByTestId('design-graph')).toBeTruthy();
    await settle();
    expect(runDataRequests(requests)).toEqual([]);
  });

  it('stops reading history and waiting while Design is shown', async () => {
    const requests = stubDexWeb({ flowType: 'OrderFlow', definitions: [definitionEntry('order', 'OrderFlow', plainGraph)] });
    renderRun('/v1/flows/run-flow/run-1?view=steps');
    await waitFor(() => expect(requests).toContain('/api/flows/wait'));
    fireEvent.click(await screen.findByRole('tab', { name: /^Design/ }));
    await screen.findByTestId('design-graph');
    await settle();
    const opened = requests.length;
    await settle();
    expect(runDataRequests(requests.slice(opened))).toEqual([]);
  });

  it('shows the matched definition\'s diagnostics under the Design graph', async () => {
    const graphWithDiagnostics = {
      ...plainGraph,
      valid: false,
      diagnostics: [{ severity: 'error', code: 'FDG_MISSING_START', message: 'The start Step is not declared.' }],
    } as unknown as FlowDefinitionGraph;
    stubDexWeb({
      flowType: 'OrderFlow',
      definitions: [{ ...definitionEntry('order', 'OrderFlow', graphWithDiagnostics), valid: false }],
    });
    renderRun('/v1/flows/run-flow/run-1?view=design');
    await screen.findByTestId('design-graph');
    const diagnostics = screen.getByRole('list', { name: 'Definition diagnostics' });
    expect(diagnostics.textContent).toContain('FDG_MISSING_START');
    expect(diagnostics.textContent).toContain('The start Step is not declared.');
  });

  it('keeps the clicked tab in the URL and follows a later view change', async () => {
    stubDexWeb({ flowType: 'OrderFlow', definitions: [definitionEntry('order', 'OrderFlow', plainGraph)] });
    renderRun('/v1/flows/run-flow/run-1?runAttempt=1', '/v1/flows/run-flow/run-1?view=overview');
    fireEvent.click(await screen.findByRole('tab', { name: /^Design/ }));
    await waitFor(() => expect(screen.getByLabelText('location').textContent)
      .toBe('/v1/flows/run-flow/run-1?runAttempt=1&view=design'));
    expect(window.localStorage.getItem('dex.run.selected-tab')).toBe('design');

    fireEvent.click(screen.getByRole('button', { name: 'Navigate' }));
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Overview' }).getAttribute('aria-selected')).toBe('true'));
    expect(screen.queryByTestId('design-graph')).toBeNull();
  });

  it('hides Design when no loaded definition matches the Flow type', async () => {
    stubDexWeb({ flowType: 'RefundFlow', definitions: [definitionEntry('order', 'OrderFlow', plainGraph)] });
    renderRun();
    await screen.findByText('Running');
    await new Promise((resolve) => setTimeout(resolve, 20));
    expect(tabLabels()).toEqual(['timeline', 'steps', 'overview', 'stream']);
  });

  it('falls back to the Timeline for a remembered tab that does not apply', async () => {
    window.localStorage.setItem('dex.run.selected-tab', 'design');
    stubDexWeb({ flowType: 'RefundFlow', definitions: [] });
    renderRun();
    await screen.findByText('Running');
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Timeline' }).getAttribute('aria-selected')).toBe('true'));
    // Only loaded history draws an event, so the Timeline really read it.
    await waitFor(() => expect(document.querySelector('[data-event-id="1"]')).not.toBeNull());
  });

  it('opens on the Timeline when no tab is remembered', async () => {
    window.localStorage.removeItem('dex.run.selected-tab');
    stubDexWeb({ flowType: 'RefundFlow', definitions: [] });
    renderRun();
    await screen.findByText('Running');
    await waitFor(() => expect(screen.getByRole('tab', { name: 'Timeline' }).getAttribute('aria-selected')).toBe('true'));
    // Only loaded history draws an event, so the Timeline really read it.
    await waitFor(() => expect(document.querySelector('[data-event-id="1"]')).not.toBeNull());
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
    expect(tabLabels()).toEqual(['timeline', 'steps', 'overview', 'stream', 'session', 'actions', 'design']);
  });

  it('hides Agent session and Actions without the operator API', async () => {
    const graph = sessionGraph();
    stubDexWeb({ flowType: graph.flow.name, definitions: [definitionEntry('session', graph.flow.name, graph)] });
    renderRun('/v1/flows/run-flow/run-1?view=session');
    await waitFor(() => expect(tabLabels()).toContain('design'));
    expect(tabLabels()).toEqual(['timeline', 'steps', 'overview', 'stream', 'design']);
    expect(screen.queryByTestId('session-view')).toBeNull();
  });

  it('shares one Working as choice between Agent session and Actions', async () => {
    const graph = sessionGraph();
    const flowType = graph.flow.name;
    stubDexWeb({
      flowType,
      definitions: [definitionEntry('session', flowType, graph)],
      operatorFlows: [{ flowType, definition: graph.v2 as FlowV2Definition }],
    });
    renderRun('/v1/flows/run-flow/run-1?view=session');
    await screen.findByTestId('session-view');
    const sessionWorkingAs = screen.getByRole('combobox', { name: 'Working as' }) as HTMLSelectElement;
    const permission = [...sessionWorkingAs.options].map((option) => option.value).find((value) => value !== '');
    expect(permission).toBeTruthy();
    fireEvent.change(sessionWorkingAs, { target: { value: permission } });
    expect(screen.getByTestId('session-view').getAttribute('data-permissions')).toBe(permission);

    fireEvent.click(screen.getByRole('tab', { name: /^Actions/ }));
    await screen.findByRole('region', { name: 'Actions' });
    expect((screen.getByRole('combobox', { name: 'Working as' }) as HTMLSelectElement).value).toBe(permission);
  });

  it.each([
    ['an earlier run attempt', { runAttempt: 2 } as Partial<FlowSummary>, '&runAttempt=1', 'run-1'],
    ['a run its Flow ID no longer resolves to', {} as Partial<FlowSummary>, '', 'run-2'],
  ])('offers no Agent session or Actions on %s', async (_name, summary, search, currentRunId) => {
    const graph = sessionGraph();
    const flowType = graph.flow.name;
    const requests = stubDexWeb({
      flowType,
      summary,
      currentRunId,
      definitions: [definitionEntry('session', flowType, graph)],
      operatorFlows: [{ flowType, definition: graph.v2 as FlowV2Definition }],
    });
    renderRun(`/v1/flows/run-flow/run-1?view=actions${search}`);
    const currentRunLink = await screen.findByRole('link', { name: 'Open the current run' });
    expect(currentRunLink.getAttribute('href')).toBe('/v1/flows/run-flow?view=actions');
    expect(tabLabels()).toEqual(['timeline', 'steps', 'overview', 'stream', 'design']);
    await settle();
    expect(screen.queryByRole('region', { name: 'Actions' })).toBeNull();
    expect(screen.queryByTestId('session-view')).toBeNull();
    expect(requests.filter((path) => path.startsWith('/api/v2/') && path !== '/api/v2/catalog' && path !== '/api/v2/connector-connections')).toEqual([]);
  });
});
