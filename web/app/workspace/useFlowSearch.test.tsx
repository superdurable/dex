// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { act, cleanup, renderHook, waitFor } from '@testing-library/react';
import type { FlowV2Definition } from '@superdurable/flow-definition-renderer';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { V2Flow } from '@/lib/types';
import type { FilterRow } from './filters';
import { useFlowSearch } from './useFlowSearch';

const catalog = {
  revisionFor: () => '',
  definitionUpdateKey: 0,
  handleDefinitionError: () => false,
};
vi.mock('../catalog/WebCatalogProvider', () => ({ useWebCatalog: () => catalog }));

const definition = {} as FlowV2Definition;
const flowTypes = ['OrderFlow'];
const noFilters: FilterRow[] = [];

function flow(flowId: string): V2Flow {
  return {
    flowId,
    flowType: 'OrderFlow',
    flowStatus: 'Running',
    flowStatusCode: 1,
    startTime: null,
    closeTime: null,
    indexedAttributes: {},
  };
}

function searchReply(flowIds: string[], nextPageToken = ''): Response {
  return new Response(JSON.stringify({ flows: flowIds.map(flow), nextPageToken }), { status: 200 });
}

function requestedPageToken(init: RequestInit | undefined): string {
  return JSON.parse(init!.body as string).nextPageToken as string;
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('useFlowSearch', () => {
  it('keeps the newer result when an older search replies last', async () => {
    const replies: Array<(response: Response) => void> = [];
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => replies.push(resolve))));
    const { result } = renderHook(() => useFlowSearch(flowTypes, definition, noFilters));
    await waitFor(() => expect(replies).toHaveLength(1));

    act(() => result.current.runSearch());
    await waitFor(() => expect(replies).toHaveLength(2));

    await act(async () => replies[1](searchReply(['newer'])));
    await waitFor(() => expect(result.current.flows.map((row) => row.flowId)).toEqual(['newer']));
    expect(result.current.loading).toBe(false);

    await act(async () => replies[0](searchReply(['older'])));
    expect(result.current.flows.map((row) => row.flowId)).toEqual(['newer']);
    expect(result.current.loading).toBe(false);
  });

  it('keeps loading on while the newest search is still out', async () => {
    const replies: Array<(response: Response) => void> = [];
    vi.stubGlobal('fetch', vi.fn(() => new Promise<Response>((resolve) => replies.push(resolve))));
    const { result } = renderHook(() => useFlowSearch(flowTypes, definition, noFilters));
    await waitFor(() => expect(replies).toHaveLength(1));
    act(() => result.current.runSearch());
    await waitFor(() => expect(replies).toHaveLength(2));

    await act(async () => replies[0](searchReply(['older'])));
    expect(result.current.loading).toBe(true);
    expect(result.current.flows).toEqual([]);
  });

  it('starts again from the first page when the filters change', async () => {
    const fetchMock = vi.fn(async (_url: string, init?: RequestInit) => {
      const token = requestedPageToken(init);
      const searchName = JSON.parse(init!.body as string).filters.length === 0 ? 'all' : 'running';
      return searchReply([`row-after-${token || 'start'}`], token === '' ? `${searchName}-2` : `${searchName}-3`);
    });
    vi.stubGlobal('fetch', fetchMock);
    const { result, rerender } = renderHook(
      ({ filters }) => useFlowSearch(flowTypes, definition, filters),
      { initialProps: { filters: noFilters } },
    );
    await waitFor(() => expect(result.current.hasNextPage).toBe(true));

    act(() => result.current.goToNextPage());
    await waitFor(() => expect(result.current.page).toBe(1));

    const statusChip: FilterRow[] = [{ id: 'status', field: 'flowStatus', operator: 'in', value: '', values: ['Running'] }];
    rerender({ filters: statusChip });
    expect(result.current.page).toBe(0);
    expect(result.current.hasNextPage).toBe(false);
    await waitFor(() => expect(result.current.hasNextPage).toBe(true));
    expect(requestedPageToken(fetchMock.mock.calls.at(-1)![1])).toBe('');

    // The old search's tokens are gone, so going back from page 3 asks for the new search's page 2.
    act(() => result.current.goToNextPage());
    await waitFor(() => expect(result.current.page).toBe(1));
    await waitFor(() => expect(result.current.loading).toBe(false));
    act(() => result.current.goToNextPage());
    await waitFor(() => expect(result.current.page).toBe(2));
    act(() => result.current.goToPreviousPage());
    await waitFor(() => expect(result.current.page).toBe(1));
    expect(requestedPageToken(fetchMock.mock.calls.at(-1)![1])).toBe('running-2');
  });
});
