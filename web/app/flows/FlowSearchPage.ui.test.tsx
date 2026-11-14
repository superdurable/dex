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
import { BASE_PATH_META_NAME } from '@/lib/basePath';
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
    expect(document.querySelector('.results-toolbar > div')?.textContent).toBe('1 flow');
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

  it('moves between the Filters and Query tabs with the keyboard', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ flows: [], nextPageToken: '' }))));
    render(
      <MemoryRouter>
        <PreferencesProvider>
          <FlowSearchPage />
        </PreferencesProvider>
      </MemoryRouter>,
    );
    const filters = await screen.findByRole('tab', { name: 'Filters' });
    const query = screen.getByRole('tab', { name: 'Query' });
    expect(filters.getAttribute('tabindex')).toBe('0');
    expect(query.getAttribute('tabindex')).toBe('-1');
    expect(screen.getByRole('tabpanel').getAttribute('aria-labelledby')).toBe(filters.id);
    expect(filters.getAttribute('aria-controls')).toBe(screen.getByRole('tabpanel').id);

    filters.focus();
    fireEvent.keyDown(filters, { key: 'ArrowRight' });
    expect(query.getAttribute('aria-selected')).toBe('true');
    expect(document.activeElement).toBe(query);
    expect(screen.getByRole('tabpanel').getAttribute('aria-labelledby')).toBe(query.id);

    fireEvent.keyDown(query, { key: 'Home' });
    expect(filters.getAttribute('aria-selected')).toBe('true');
    expect(document.activeElement).toBe(filters);
    fireEvent.keyDown(filters, { key: 'End' });
    expect(document.activeElement).toBe(query);
  });
});

function setBasePathMeta(content: string | null) {
  document.head.querySelector(`meta[name="${BASE_PATH_META_NAME}"]`)?.remove();
  if (content === null) return;
  const meta = document.createElement('meta');
  meta.name = BASE_PATH_META_NAME;
  meta.content = content;
  document.head.appendChild(meta);
}

/** Searches with the URL's query, saves it under name, and leaves the Queries panel open. */
async function searchAndSaveQuery(basePath: string, query: string, name: string) {
  setBasePathMeta(basePath === '/' ? null : basePath);
  const pagePath = basePath === '/' ? '/v1/flows' : `${basePath}/v1/flows`;
  window.history.replaceState(null, '', `${pagePath}?q=${encodeURIComponent(query)}`);
  vi.stubGlobal('prompt', vi.fn(() => name));
  render(
    <MemoryRouter>
      <PreferencesProvider>
        <FlowSearchPage />
      </PreferencesProvider>
    </MemoryRouter>,
  );
  fireEvent.click(await screen.findByRole('button', { name: 'Queries' }));
  await waitFor(() => expect(screen.getAllByText(query).length).toBeGreaterThan(0));
  fireEvent.click(screen.getByRole('button', { name: 'Save query' }));
  await screen.findByText(name);
}

describe('Flow search storage', () => {
  afterEach(() => setBasePathMeta(null));

  it('keeps recent and saved searches apart for each base path', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ flows: [], nextPageToken: '' }))));
    await searchAndSaveQuery('/dex-web/server-a', 'WorkflowId = "order-a"', 'Orders on A');
    cleanup();

    setBasePathMeta('/dex-web/server-b');
    window.history.replaceState(null, '', '/dex-web/server-b/v1/flows');
    render(
      <MemoryRouter>
        <PreferencesProvider>
          <FlowSearchPage />
        </PreferencesProvider>
      </MemoryRouter>,
    );
    fireEvent.click(await screen.findByRole('button', { name: 'Queries' }));
    expect(screen.getByText('No saved queries')).toBeTruthy();
    expect(screen.getByText('No recent queries')).toBeTruthy();
    expect(screen.queryByText('Orders on A')).toBeNull();
    expect(localStorage.getItem('dex-web-recent-queries@/dex-web/server-a')).toContain('order-a');
    expect(localStorage.getItem('dex-web-saved-queries@/dex-web/server-a')).toContain('Orders on A');
    expect(localStorage.getItem('dex-web-recent-queries')).toBeNull();
    expect(localStorage.getItem('dex-web-saved-queries')).toBeNull();
  });

  it('keeps the original keys at the root', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ flows: [], nextPageToken: '' }))));
    await searchAndSaveQuery('/', 'WorkflowId = "order-root"', 'Root orders');
    expect(localStorage.getItem('dex-web-recent-queries')).toContain('order-root');
    expect(localStorage.getItem('dex-web-saved-queries')).toContain('Root orders');
    expect([...Array(localStorage.length).keys()].map((index) => localStorage.key(index)))
      .not.toContainEqual(expect.stringContaining('@'));
  });
});

describe('Flows grouped by Flow type', () => {
  it('groups the runs by Flow type until the viewer turns it off', async () => {
    // A choice saved under the old key, before grouping was the default, no longer applies.
    localStorage.setItem('dex-web-group-by-type', 'false');
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ flows: [flow('checkout-1'), flow('checkout-2')], nextPageToken: '' }))));
    render(
      <MemoryRouter>
        <PreferencesProvider>
          <FlowSearchPage />
        </PreferencesProvider>
      </MemoryRouter>,
    );
    await screen.findByText('checkout-1');
    const toggle = screen.getByRole('button', { name: 'Group by type' });
    expect(toggle.getAttribute('aria-pressed')).toBe('true');
    expect(document.querySelector('.flow-group-row')?.textContent).toContain('CheckoutFlow');
    expect(document.querySelector('.flow-group-row')?.textContent).toContain('2 running');

    fireEvent.click(toggle);
    expect(document.querySelector('.flow-group-row')).toBeNull();
    expect(localStorage.getItem('dex-web.flows.group-by-type')).toBe('false');
  });
});
