// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation, useNavigate } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '../providers';
import { WebCatalogProvider } from '../catalog/WebCatalogProvider';
import { SEARCH_COPY } from '../workspace/searchCopy';
import { WORK_QUEUE_COPY } from './copy';
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
    source: 'directory',
    graph: { nodes: steps.map((name) => ({ id: `step:${name}`, kind: 'step', name })), edges: [], diagnostics: [] },
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

const graphs: Record<string, ReturnType<typeof graphEntry>> = {
  OrderFlow: graphEntry('OrderFlow', ['AwaitPacking']),
  PayFlow: graphEntry('PayFlow', ['AwaitPayment']),
  RefundFlow: graphEntry('RefundFlow', ['AwaitApproval']),
};

/** The Flow types whose graphs the page read, one entry per read. */
let definitionReads: string[] = [];

function stubServer(flowsFor: (body: SearchBody) => unknown[], nextPageToken = '') {
  const searches: SearchBody[] = [];
  definitionReads = [];
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const json = (value: unknown) => new Response(JSON.stringify(value), { status: 200 });
    if (url === '/api/flow-definition-types') {
      return json({
        configured: true,
        operatorAPI: true,
        flowTypes: Object.keys(graphs).map((flowType) => ({ flowType, source: 'directory' })),
      });
    }
    if (url.startsWith('/api/flow-definitions/by-type?')) {
      const flowType = new URL(url, 'http://dex.test').searchParams.get('flowType') ?? '';
      definitionReads.push(flowType);
      return json({ flowType, definitionRevision: `r-${flowType}`, definitions: [graphs[flowType]], v2: null });
    }
    if (url === '/api/v2/catalog') {
      return json({
        enabled: true,
        definitionRevision: 'r1',
        flows: [
          { flowType: 'OrderFlow', definition: contract('warehouse', 'order.manage'), definitionRevision: 'r-OrderFlow' },
          { flowType: 'PayFlow', definition: contract('payment', 'pay.manage'), definitionRevision: 'r-PayFlow' },
          { flowType: 'RefundFlow', definition: contract('case-title', 'refund.manage'), definitionRevision: 'r-RefundFlow' },
        ],
      });
    }
    if (url === '/api/v2/search') {
      const body = JSON.parse(String(init?.body)) as SearchBody;
      searches.push(body);
      return json({ flows: flowsFor(body), nextPageToken });
    }
    return new Response('{}', { status: 404 });
  }));
  return searches;
}

function LocationProbe() {
  const navigate = useNavigate();
  return (
    <>
      <output aria-label="location">{useLocation().pathname}</output>
      <button onClick={() => navigate(-1)} type="button">Back</button>
    </>
  );
}

function renderQueue() {
  render(
    <PreferencesProvider>
      <MemoryRouter initialEntries={['/v1/work-queue/RefundFlow']}>
        <WebCatalogProvider>
          <Routes>
            <Route path="/v1/work-queue/:flowType" element={<WorkQueueWorkspace />} />
            <Route path="/v1/work-queue/:flowType/:flowId" element={<WorkQueueWorkspace />} />
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

const submit = async () => fireEvent.click(await screen.findByRole('button', { name: SEARCH_COPY.search }));
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
    await submit();

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
    // The page-load search runs in an effect that can land after the panel renders.
    await waitFor(() => expect(searches).toHaveLength(1));
    // The Step filter reads the graphs of the searched Flow types only, one Flow type at a time.
    await waitFor(() => expect(definitionReads).toEqual(['RefundFlow']));

    fireEvent.click(within(advanced).getByRole('checkbox', { name: 'OrderFlow' }));
    fireEvent.click(await within(advanced).findByRole('checkbox', { name: 'AwaitPacking' }));
    expect(definitionReads).toEqual(['RefundFlow', 'OrderFlow']);
    expect(searches).toHaveLength(1);
    await submit();

    await waitFor(() => expect(lastSearch(searches).flowTypes).toEqual(['OrderFlow', 'RefundFlow']));
    expect(searches).toHaveLength(2);
  });

  it('labels rows with their own Flow, and opens a row under its own Flow type', async () => {
    stubServer((body) => body.flowTypes.length > 1 ? [
      run('refund-1', 'RefundFlow', { 'case-title': 'Damaged item' }),
      run('order-1', 'OrderFlow', { warehouse: 'West' }),
    ] : []);
    renderQueue();
    const advanced = await openAdvanced();

    fireEvent.click(within(advanced).getByRole('checkbox', { name: 'OrderFlow' }));
    await submit();

    const orderRow = (await screen.findByText('West')).closest('button');
    expect(screen.getByText('Damaged item')).toBeTruthy();
    expect(orderRow?.textContent).toContain('OrderFlow');
    fireEvent.click(orderRow as HTMLElement);

    await waitFor(() => expect(screen.getByLabelText('location').textContent)
      .toBe('/v1/work-queue/OrderFlow/order-1'));
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
    await submit();

    await waitFor(() => expect(lastSearch(searches).flowTypes).toEqual(['OrderFlow', 'RefundFlow']));
    expect(lastSearch(searches).workQueuePermissions).toEqual(['refund.manage']);
    const options = within(screen.getByLabelText('Working as')).getAllByRole('option');
    expect(options.map((option) => option.textContent)).toEqual(['Anyone', 'order.manage', 'refund.manage']);
  });

  it('drops a widening that Back leaves behind', async () => {
    const searches = stubServer(() => []);
    renderQueue();
    await screen.findByText(SEARCH_COPY.advanced);
    fireEvent.change(screen.getByLabelText(SEARCH_COPY.flowTypeLabel), { target: { value: 'OrderFlow' } });
    await waitFor(() => expect(lastSearch(searches).flowTypes).toEqual(['OrderFlow']));
    const advanced = await openAdvanced();
    fireEvent.click(within(advanced).getByRole('checkbox', { name: 'PayFlow' }));
    await submit();
    await waitFor(() => expect(lastSearch(searches).flowTypes).toEqual(['OrderFlow', 'PayFlow']));

    fireEvent.click(screen.getByRole('button', { name: 'Back' }));

    await waitFor(() => expect(screen.getByLabelText('location').textContent).toBe('/v1/work-queue/RefundFlow'));
    await waitFor(() => expect(lastSearch(searches).flowTypes).toEqual(['RefundFlow']));
    const refund = within(advanced).getByRole('checkbox', { name: 'RefundFlow' }) as HTMLInputElement;
    const pay = within(advanced).getByRole('checkbox', { name: 'PayFlow' }) as HTMLInputElement;
    expect([refund.checked, pay.checked]).toEqual([true, false]);
    expect(within(advanced).getByLabelText(SEARCH_COPY.attributeLabel)).toBeTruthy();
  });

  it('resets Working as when the search no longer offers that permission', async () => {
    const searches = stubServer(() => []);
    renderQueue();
    fireEvent.change(await screen.findByLabelText('Working as'), { target: { value: 'refund.manage' } });

    fireEvent.change(screen.getByLabelText(SEARCH_COPY.flowTypeLabel), { target: { value: 'OrderFlow' } });

    await waitFor(() => expect(lastSearch(searches).flowTypes).toEqual(['OrderFlow']));
    expect(lastSearch(searches).workQueuePermissions).toEqual([]);
    expect((screen.getByLabelText('Working as') as HTMLSelectElement).value).toBe('');
  });

  it('says when more runs match than the list shows', async () => {
    stubServer(() => [run('refund-1', 'RefundFlow', { 'case-title': 'Damaged item' })], 'next-page');
    renderQueue();

    expect(await screen.findByText(SEARCH_COPY.moreRuns(1))).toBeTruthy();
  });

  it('says nothing about more runs on the last page', async () => {
    stubServer(() => [run('refund-1', 'RefundFlow', { 'case-title': 'Damaged item' })]);
    renderQueue();

    await screen.findByText('Damaged item');
    expect(screen.queryByText(SEARCH_COPY.moreRuns(1))).toBeNull();
  });

  it('goes back to one Flow type when the reader picks one', async () => {
    const searches = stubServer(() => []);
    renderQueue();
    const advanced = await openAdvanced();
    fireEvent.click(within(advanced).getByRole('checkbox', { name: 'OrderFlow' }));
    await submit();
    await waitFor(() => expect(lastSearch(searches).flowTypes).toHaveLength(2));

    fireEvent.change(screen.getByLabelText(SEARCH_COPY.flowTypeLabel), { target: { value: 'OrderFlow' } });

    await waitFor(() => expect(lastSearch(searches).flowTypes).toEqual(['OrderFlow']));
    expect(screen.getByLabelText('location').textContent).toBe('/v1/work-queue/OrderFlow');
  });

  it('searches as soon as a status or time chip is chosen', async () => {
    const searches = stubServer(() => []);
    renderQueue();
    const statuses = await screen.findByRole('radiogroup', { name: SEARCH_COPY.statusLabel });
    await waitFor(() => expect(searches).toHaveLength(1));
    expect(lastSearch(searches).filters).toContainEqual({ field: 'executionStatus', operator: 'eq', values: ['Running'] });
    expect(within(statuses).getByRole('radio', { name: 'Running' }).getAttribute('aria-checked')).toBe('true');

    fireEvent.click(within(statuses).getByRole('radio', { name: 'Failed' }));
    await waitFor(() => expect(lastSearch(searches).filters)
      .toContainEqual({ field: 'executionStatus', operator: 'eq', values: ['Failed'] }));
    expect(within(statuses).getByRole('radio', { name: 'Failed' }).getAttribute('aria-checked')).toBe('true');

    fireEvent.change(screen.getByLabelText(`${SEARCH_COPY.statusLabel}: ${WORK_QUEUE_COPY.moreStatuses}`), {
      target: { value: 'TimedOut' },
    });
    await waitFor(() => expect(lastSearch(searches).filters)
      .toContainEqual({ field: 'executionStatus', operator: 'eq', values: ['TimedOut'] }));
    expect(within(statuses).getByTitle(WORK_QUEUE_COPY.moreStatuses).getAttribute('data-checked')).toBe('true');

    fireEvent.click(within(statuses).getByRole('radio', { name: 'All' }));
    await waitFor(() => expect(lastSearch(searches).filters.map((filter) => filter.field)).not.toContain('executionStatus'));

    fireEvent.change(screen.getByLabelText(SEARCH_COPY.sinceLabel), { target: { value: '24h' } });
    await waitFor(() => expect(lastSearch(searches).filters.map((filter) => filter.field)).toContain('startTime'));
    expect(searches).toHaveLength(5);

    fireEvent.change(screen.getByLabelText('Working as'), { target: { value: 'refund.manage' } });
    await waitFor(() => expect(lastSearch(searches).workQueuePermissions).toEqual(['refund.manage']));
    expect(searches).toHaveLength(6);
  });

  it('names the Flow type instead of repeating the page title, and guides the empty panel', async () => {
    stubServer(() => [run('refund-1', 'RefundFlow', { 'case-title': 'Damaged item' })]);
    renderQueue();

    expect(await screen.findByText(WORK_QUEUE_COPY.selectHeading)).toBeTruthy();
    expect(await screen.findByText(WORK_QUEUE_COPY.listedRuns(1, false))).toBeTruthy();
    expect(screen.queryByText(WORK_QUEUE_COPY.appName, { selector: 'span, h2' })).toBeNull();
    expect((screen.getByLabelText(SEARCH_COPY.flowTypeLabel) as HTMLSelectElement).value).toBe('RefundFlow');

    const row = (await screen.findByText('Damaged item')).closest('button') as HTMLElement;
    fireEvent.click(row);
    await waitFor(() => expect(row.getAttribute('aria-current')).toBe('true'));
    expect(screen.queryByText(WORK_QUEUE_COPY.selectHeading)).toBeNull();
  });

  it('points at the filters when nothing matches', async () => {
    stubServer(() => []);
    renderQueue();

    expect(await screen.findByText(WORK_QUEUE_COPY.noRunsHeading)).toBeTruthy();
    expect(screen.getByText(WORK_QUEUE_COPY.noRunsPrompt)).toBeTruthy();
  });
});
