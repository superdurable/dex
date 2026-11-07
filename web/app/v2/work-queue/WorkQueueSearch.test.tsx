// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '../../providers';
import { WebCatalogProvider } from '../WebCatalogProvider';
import { SEARCH_COPY } from '../workspace/searchCopy';
import { WorkQueueWorkspace } from './WorkQueueWorkspace';

const view = (rpcName: string) => ({ rpcName, fields: [] });

function contract(uiSlotAttribute: string, requiredPermission: string) {
  return {
    indexedAttributes: [{
      attributeKey: uiSlotAttribute,
      indexKey: 'keyword1',
      indexType: 'keyword',
      valueType: 'string',
      description: uiSlotAttribute,
    }],
    summary: view('GetDexSummary'),
    display: {
      rpcName: 'GetDexDisplay',
      fields: [{ attributeKey: uiSlotAttribute, valueType: 'string', description: uiSlotAttribute, uiSlot: 'title' }],
    },
    actions: [{ rpcName: 'Approve', label: 'Approve', requiredPermission }],
  };
}

function graphEntry(flowName: string, steps: string[]) {
  return {
    id: flowName,
    file: `${flowName}.json`,
    schemaVersion: '2.0',
    flowName,
    sourceLanguage: 'go',
    sourcePath: `${flowName}.go`,
    valid: true,
    graph: { nodes: steps.map((name) => ({ id: `step:${name}`, kind: 'step', name })), edges: [] },
  };
}

const run = (flowId: string, flowType: string, indexedAttributes: Record<string, unknown>) => ({
  flowId,
  flowType,
  flowStatus: 'Running',
  flowStatusCode: 1,
  startTime: '2026-11-06T10:00:00Z',
  closeTime: null,
  indexedAttributes,
});

interface SearchBody {
  flowTypes: string[];
  workQueuePermissions: string[];
  filters: { field: string; operator: string; values: unknown[] }[];
}

function stubServer(flowsFor: (body: SearchBody) => unknown[]) {
  const searches: SearchBody[] = [];
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200 });
    if (url === '/api/flow-definitions') {
      return json({
        configured: true,
        definitionRevision: 'r1',
        definitionCount: 2,
        definitions: [graphEntry('OrderFlow', ['AwaitPacking']), graphEntry('RefundFlow', ['AwaitApproval'])],
      });
    }
    if (url === '/api/v2/catalog') {
      return json({
        enabled: true,
        definitionRevision: 'r1',
        flows: [
          { flowType: 'OrderFlow', definition: contract('warehouse', 'order.manage') },
          { flowType: 'RefundFlow', definition: contract('case-title', 'refund.manage') },
        ],
      });
    }
    if (url === '/api/v2/search') {
      const body = JSON.parse(String(init?.body)) as SearchBody;
      searches.push(body);
      return json({ flows: flowsFor(body), nextPageToken: '' });
    }
    return new Response('{}', { status: 404 });
  }));
  return searches;
}

function LocationProbe() {
  return <output aria-label="location">{useLocation().pathname}</output>;
}

function renderQueue() {
  render(
    <PreferencesProvider>
      <MemoryRouter initialEntries={['/v2/work-queue/RefundFlow']}>
        <WebCatalogProvider>
          <Routes>
            <Route path="/v2/work-queue/:flowType" element={<WorkQueueWorkspace />} />
            <Route path="/v2/work-queue/:flowType/:flowId" element={<WorkQueueWorkspace />} />
          </Routes>
          <LocationProbe />
        </WebCatalogProvider>
      </MemoryRouter>
    </PreferencesProvider>,
  );
}

async function openAdvanced() {
  const heading = await screen.findByText(SEARCH_COPY.advanced);
  const details = heading.closest('details');
  if (details === null) throw new Error('Advanced search has no disclosure');
  details.open = true;
  return details;
}

const submit = () => fireEvent.click(screen.getByRole('button', { name: SEARCH_COPY.search }));
const lastSearch = (searches: SearchBody[]) => searches[searches.length - 1];

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Work Queue search', () => {
  it('filters by a Step type and says why the list can be empty', async () => {
    const searches = stubServer(() => []);
    renderQueue();
    const advanced = await openAdvanced();

    fireEvent.click(within(advanced).getByRole('checkbox', { name: 'AwaitApproval' }));
    submit();

    await waitFor(() => expect(lastSearch(searches).filters).toContainEqual({
      field: 'activeStepTypes', operator: 'in', values: ['AwaitApproval'],
    }));
    expect(lastSearch(searches).flowTypes).toEqual(['RefundFlow']);
    expect(await screen.findByText(SEARCH_COPY.noActiveStepMatch)).toBeTruthy();
    expect(screen.queryByText('Nothing open on this page.')).toBeNull();
  });

  it('keeps its own empty text when no Step filter ran', async () => {
    stubServer(() => []);
    renderQueue();

    expect(await screen.findByText('Nothing open on this page.')).toBeTruthy();
    expect(screen.queryByText(SEARCH_COPY.noActiveStepMatch)).toBeNull();
  });

  it('searches several Flow types in one request and offers all their Steps', async () => {
    const searches = stubServer(() => []);
    renderQueue();
    const advanced = await openAdvanced();
    expect(within(advanced).queryByRole('checkbox', { name: 'AwaitPacking' })).toBeNull();
    const pageFlowType = within(advanced).getByRole('checkbox', { name: 'RefundFlow' }) as HTMLInputElement;
    expect(pageFlowType.checked).toBe(true);
    expect(pageFlowType.disabled).toBe(true);
    const searchesBefore = searches.length;

    fireEvent.click(within(advanced).getByRole('checkbox', { name: 'OrderFlow' }));
    fireEvent.click(within(advanced).getByRole('checkbox', { name: 'AwaitPacking' }));
    expect(searches.length).toBe(searchesBefore);
    submit();

    await waitFor(() => expect(lastSearch(searches).flowTypes).toEqual(['OrderFlow', 'RefundFlow']));
    expect(searches.length).toBe(searchesBefore + 1);
  });

  it('labels rows with their own Flow, and opens a row under its own Flow type', async () => {
    stubServer((body) => body.flowTypes.length > 1 ? [
      run('refund-1', 'RefundFlow', { 'case-title': 'Damaged item' }),
      run('order-1', 'OrderFlow', { warehouse: 'West' }),
    ] : []);
    renderQueue();
    const advanced = await openAdvanced();

    fireEvent.click(within(advanced).getByRole('checkbox', { name: 'OrderFlow' }));
    submit();

    const orderRow = (await screen.findByText('West')).closest('button');
    expect(screen.getByText('Damaged item')).toBeTruthy();
    expect(orderRow?.textContent).toContain('OrderFlow');
    fireEvent.click(orderRow as HTMLElement);

    await waitFor(() => expect(screen.getByLabelText('location').textContent)
      .toBe('/v2/work-queue/OrderFlow/order-1'));
    // Opening a run does not narrow the list it came from.
    expect(screen.getByText('Damaged item')).toBeTruthy();
  });

  it('replaces the Attribute filter and keeps Working as while several Flow types are searched', async () => {
    const searches = stubServer(() => []);
    renderQueue();
    const advanced = await openAdvanced();
    const permissionControl = await screen.findByLabelText('Working as');
    fireEvent.change(permissionControl, { target: { value: 'refund.manage' } });
    expect(within(advanced).getByLabelText(SEARCH_COPY.attributeLabel)).toBeTruthy();

    fireEvent.click(within(advanced).getByRole('checkbox', { name: 'OrderFlow' }));
    expect(within(advanced).queryByLabelText(SEARCH_COPY.attributeLabel)).toBeNull();
    expect(within(advanced).getByText(SEARCH_COPY.attributeNeedsOneFlowType)).toBeTruthy();
    submit();

    await waitFor(() => expect(lastSearch(searches).flowTypes).toEqual(['OrderFlow', 'RefundFlow']));
    expect(lastSearch(searches).workQueuePermissions).toEqual(['refund.manage']);
    const options = within(screen.getByLabelText('Working as')).getAllByRole('option');
    expect(options.map((option) => option.textContent)).toEqual(['Anyone', 'order.manage', 'refund.manage']);
  });

  it('goes back to one Flow type when the reader picks one', async () => {
    const searches = stubServer(() => []);
    renderQueue();
    const advanced = await openAdvanced();
    fireEvent.click(within(advanced).getByRole('checkbox', { name: 'OrderFlow' }));
    submit();
    await waitFor(() => expect(lastSearch(searches).flowTypes).toHaveLength(2));

    fireEvent.change(screen.getByLabelText(SEARCH_COPY.flowTypeLabel), { target: { value: 'OrderFlow' } });

    await waitFor(() => expect(lastSearch(searches).flowTypes).toEqual(['OrderFlow']));
    expect(screen.getByLabelText('location').textContent).toBe('/v2/work-queue/OrderFlow');
  });
});
