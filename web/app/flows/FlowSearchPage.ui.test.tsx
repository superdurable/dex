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
import { PreferencesProvider } from '../providers';
import { FlowSearchPage } from './FlowSearchPage';

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

function flow(flowId: string) {
  return {
    flowId,
    runId: `${flowId}-run`,
    flowType: 'CheckoutFlow',
    flowStatus: 'Running',
    flowStatusCode: 1,
    startTime: null,
    closeTime: null,
    indexedAttributes: [],
  };
}

interface SearchRequest {
  query: string;
  nextPageToken: string;
}

beforeEach(() => {
  vi.stubGlobal('localStorage', memoryStorage());
  window.history.replaceState(null, '', '/v1/flows');
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Flow search query builder and pagination', () => {
  it('searches the generated query and pages with the returned tokens', async () => {
    const requests: SearchRequest[] = [];
    vi.stubGlobal('fetch', vi.fn(async (_input: string, init?: RequestInit) => {
      const request = JSON.parse(String(init?.body)) as SearchRequest;
      requests.push(request);
      const isSecondPage = request.nextPageToken === 'page-2-token';
      return new Response(JSON.stringify({
        flows: [flow(isSecondPage ? 'checkout-2' : 'checkout-1')],
        nextPageToken: isSecondPage ? '' : 'page-2-token',
      }));
    }));
    render(
      <MemoryRouter>
        <PreferencesProvider>
          <FlowSearchPage />
        </PreferencesProvider>
      </MemoryRouter>,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'Add filter' }));
    fireEvent.change(screen.getByRole('textbox', { name: 'Filter value' }), { target: { value: 'Checkout' } });
    const generated = screen.getByText(/FlowType/).textContent ?? '';
    expect(generated).toContain('Checkout');

    fireEvent.click(screen.getByRole('button', { name: 'Search' }));
    await screen.findByText('checkout-1');
    expect(screen.getByText('flow on page 1', { exact: false }).textContent).toBe('1 flow on page 1');
    expect(requests.at(-1)).toEqual(expect.objectContaining({ query: generated, nextPageToken: '' }));

    fireEvent.click(screen.getByRole('button', { name: 'Next' }));
    await screen.findByText('checkout-2');
    expect(requests.at(-1)).toEqual(expect.objectContaining({ query: generated, nextPageToken: 'page-2-token' }));
    expect(screen.getByText('Page 2')).toBeTruthy();
    expect((screen.getByRole('button', { name: 'Next' }) as HTMLButtonElement).disabled).toBe(true);

    fireEvent.click(screen.getByRole('button', { name: 'Previous' }));
    await screen.findByText('checkout-1');
    await waitFor(() => expect(requests.at(-1)?.nextPageToken).toBe(''));
    expect(screen.getByText('Page 1')).toBeTruthy();
  });

  it('names its tabs Filters and Query and its hand-written field Search query', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ flows: [], nextPageToken: '' }))));
    render(
      <MemoryRouter>
        <PreferencesProvider>
          <FlowSearchPage />
        </PreferencesProvider>
      </MemoryRouter>,
    );
    expect(await screen.findByRole('tab', { name: 'Filters' })).toBeTruthy();
    fireEvent.click(screen.getByRole('tab', { name: 'Query' }));
    expect(screen.getByRole('textbox', { name: 'Search query' })).toBeTruthy();
    expect(document.body.textContent).not.toMatch(/visibility|Basic|Advanced/i);
  });
});
