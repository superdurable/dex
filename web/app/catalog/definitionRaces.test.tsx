// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { FlowDefinitionGraph } from '@superdurable/flow-definition-renderer';
import { DexAPIError } from '@/lib/http';
import { FlowDefinitionDiagnostics } from '../rendering/FlowDefinitionDiagnostics';
import { groupsFromDefinition } from '../design/groupsFromGraph';
import type { PocFlow } from '../design/canvas/model/pocFlow';
import { flowDefinitionShapeProblem } from '../design/definitionLimits';
import { clearFlowTypeDefinitionCache, drawableDefinitions, fetchFlowTypeDefinitions } from './flowTypeDefinitionCache';
import { useFlowTypeDefinitions, useWebCatalog, WebCatalogProvider } from './WebCatalogProvider';

const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });

const answer = (revision: string) => ({ flowType: 'OrderFlow', definitionRevision: revision, definitions: [], v2: null });

/** A fetch whose per-type answers are released by the test in any order. */
function heldDefinitionFetch() {
  const pending: { revision: string; release: () => void }[] = [];
  let revisionCount = 0;
  const fetchMock = vi.fn((input: string, _init?: RequestInit) => {
    if (input === '/api/flow-definition-types') return Promise.resolve(json({ configured: true, operatorAPI: false, flowTypes: [] }));
    revisionCount += 1;
    const revision = `r${revisionCount}`;
    return new Promise<Response>((resolve) => {
      pending.push({ revision, release: () => resolve(json(answer(revision))) });
    });
  });
  vi.stubGlobal('fetch', fetchMock);
  return { pending, fetchMock };
}

function RevisionProbe() {
  const state = useFlowTypeDefinitions('OrderFlow');
  const { handleDefinitionError } = useWebCatalog();
  return (
    <>
      <output aria-label="revision">{state?.value?.definitionRevision ?? ''}</output>
      <button
        onClick={() => handleDefinitionError(new DexAPIError('changed', 409, undefined, 'FLOW_DEFINITION_CHANGED'))}
        type="button"
      >
        Reload
      </button>
    </>
  );
}

beforeEach(() => {
  clearFlowTypeDefinitionCache();
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('stale definition answers', () => {
  it('never let an older answer replace a newer one in the page state', async () => {
    const { pending } = heldDefinitionFetch();
    render(<WebCatalogProvider><RevisionProbe /></WebCatalogProvider>);
    await waitFor(() => expect(pending).toHaveLength(1));
    fireEvent.click(screen.getByRole('button', { name: 'Reload' }));
    await waitFor(() => expect(pending).toHaveLength(2));

    await act(async () => { pending[1].release(); });
    await waitFor(() => expect(screen.getByLabelText('revision').textContent).toBe('r2'));
    await act(async () => { pending[0].release(); });
    expect(screen.getByLabelText('revision').textContent).toBe('r2');
  });

  it('never let an older answer replace a newer one in the cache', async () => {
    const { pending, fetchMock } = heldDefinitionFetch();
    const older = fetchFlowTypeDefinitions('OrderFlow');
    const newer = fetchFlowTypeDefinitions('OrderFlow');
    pending[1].release();
    expect((await newer)?.definitionRevision).toBe('r2');
    pending[0].release();
    expect((await older)?.definitionRevision).toBe('r1');

    const third = fetchFlowTypeDefinitions('OrderFlow');
    expect(new Headers(fetchMock.mock.calls[2][1]?.headers).get('If-None-Match')).toBe('"r2"');
    pending[2].release();
    await third;
  });
});

describe('hostile graphs in the browser', () => {
  const graph = (overrides: Record<string, unknown>) => ({
    schemaVersion: '2.0', valid: true, source: { language: 'go', path: 'f.go' }, flow: { name: 'F' },
    nodes: [{ id: 'step:a', kind: 'step', name: 'A' }], edges: [], diagnostics: [], ...overrides,
  });

  it('names a malformed element and keeps it out of the drawable definitions', () => {
    for (const overrides of [
      { nodes: [null] }, { nodes: [{ id: 'a', kind: 7, name: 'A' }] }, { edges: [null] },
      { diagnostics: [null] }, { diagnostics: [{ severity: 'error', code: 'c' }] },
      { groups: [null] }, { groups: [{ id: 'g', label: 'G', stepIds: [1] }] },
    ]) {
      expect(flowDefinitionShapeProblem(graph(overrides))).not.toBeNull();
      const value = {
        flowType: 'F', definitionRevision: 'r', v2: null,
        definitions: [{ id: 'f', file: '', schemaVersion: '2.0' as const, flowName: 'F', sourceLanguage: 'go', sourcePath: 'f.go',
          valid: true, source: 'uploaded' as const, graph: graph(overrides) as unknown as FlowDefinitionGraph }],
      };
      expect(drawableDefinitions(value)).toEqual([]);
    }
    expect(flowDefinitionShapeProblem(graph({}))).toBeNull();
  });

  it('lists diagnostics without crashing on one that is not an object', () => {
    const definition = {
      id: 'f', file: '', schemaVersion: '2.0' as const, flowName: 'F', sourceLanguage: 'go', sourcePath: 'f.go', valid: true,
      source: 'uploaded' as const,
      graph: graph({ diagnostics: [null, { severity: 'error" onclick', code: 'c', message: 'kept' }] }) as unknown as FlowDefinitionGraph,
    };
    render(<FlowDefinitionDiagnostics definition={definition} />);
    const items = screen.getAllByRole('listitem');
    expect(items).toHaveLength(1);
    expect(items[0].className).toBe('flow-definition-diagnostic is-warning');
  });

  it('groups Steps through a map and skips Step IDs that name no Step', () => {
    const steps = Array.from({ length: 2000 }, (_, index) => ({ id: `step:${index}`, stepType: `S${index}` }));
    const groups = Array.from({ length: 1000 }, (_, index) => ({
      id: `g${index}`, label: 'G', stepIds: [`step:${index}`, `missing:${index}`, `step:${index + 1000}`],
    }));
    const result = groupsFromDefinition({ groups } as unknown as FlowDefinitionGraph, { steps } as unknown as PocFlow);
    expect(result).toHaveLength(1000);
    expect(result[999].stepIds).toEqual(['step:999', 'step:1999']);
  });
});
