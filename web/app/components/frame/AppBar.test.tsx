// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { Link, MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '../../providers';
import { ThemeProvider } from '../../theme';
import { WebCatalogProvider } from '../../catalog/WebCatalogProvider';
import { HOST_NAME_META_NAME, HOST_PAGE_META_NAME, HOST_URL_META_NAME } from '@/lib/hostLink';
import { AppFrame } from './AppBar';
import { AppBarSlot, AppBarSlotsProvider } from './AppBarSlots';

type DefinitionsReply = 'fail' | number;

/** The Flow type list, which is all the bar and the app root read about definitions. */
function definitionsResponse(reply: DefinitionsReply, isOperatorAPI = false): Response {
  if (reply === 'fail') {
    return new Response(JSON.stringify({ error: 'definitions unavailable' }), { status: 503 });
  }
  return new Response(JSON.stringify({
    configured: true,
    operatorAPI: isOperatorAPI,
    flowTypes: Array.from({ length: reply }, (_, index) => ({ flowType: `Flow${index}`, source: 'directory' })),
  }), { status: 200 });
}

function stubDexWeb(replies: DefinitionsReply[]) {
  let readCount = 0;
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    if (input === '/api/v2/connector-connections') return new Response('404 page not found', { status: 404 });
    if (input !== '/api/flow-definition-types') throw new Error(`unexpected request ${input}`);
    readCount += 1;
    return definitionsResponse(replies[Math.min(readCount - 1, replies.length - 1)]);
  }));
  return { readCount: () => readCount };
}

function stubOperatorDexWeb({ flows, connectors }: { flows: number; connectors: 'missing' | 'disabled' | number }) {
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    if (input === '/api/v2/catalog') throw new Error('the bar must not read the v2 catalog');
    if (input === '/api/v2/connector-connections') {
      if (connectors === 'missing') return new Response('404 page not found', { status: 404 });
      return new Response(JSON.stringify({
        enabled: connectors !== 'disabled',
        definitionRevision: 'r1',
        csrfToken: 't',
        connections: Array.from({ length: connectors === 'disabled' ? 0 : connectors }, (_, index) => ({ connectorId: `c${index}` })),
      }));
    }
    return definitionsResponse(flows, true);
  }));
}

function renderBar(path: string, page?: React.ReactNode) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <PreferencesProvider>
        <ThemeProvider>
          <WebCatalogProvider>
            <AppBarSlotsProvider>
              <AppFrame>
                {page}
                <Link to="/v1/flows/a">Open run a</Link>
                <Link to="/v1/flows/b">Open run b</Link>
                <Link to="/v1/flows?q=status%3DRunning">Search running</Link>
              </AppFrame>
            </AppBarSlotsProvider>
          </WebCatalogProvider>
        </ThemeProvider>
      </PreferencesProvider>
    </MemoryRouter>,
  );
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

function rail() {
  return screen.getByRole('navigation', { name: 'Dex Web' });
}

/** The rail's section links, in order. */
function railSections() {
  return within(rail()).getAllByRole('link').filter((link) => !link.classList.contains('app-rail-host') && !link.classList.contains('app-rail-host-page'));
}

function addMeta(name: string, content: string, attributes: Record<string, string> = {}) {
  const meta = document.createElement('meta');
  meta.name = name;
  meta.content = content;
  for (const [key, value] of Object.entries(attributes)) meta.setAttribute(key, value);
  document.head.append(meta);
}

beforeEach(() => {
  vi.stubGlobal('localStorage', memoryStorage());
  vi.stubGlobal('sessionStorage', memoryStorage());
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  document.head.querySelectorAll('meta[name^="dex-web-host"]').forEach((meta) => meta.remove());
});

describe('Dex Web rail', () => {
  it('starts at the Dex mark, which leads to Flows, when no host serves Dex Web', async () => {
    stubDexWeb([0]);
    renderBar('/v1/work-queue');
    expect(within(rail()).getByRole('link', { name: 'Dex home' }).getAttribute('href')).toBe('/v1/flows');
  });

  it('starts at the host page, then lists the host pages, when the server names them', async () => {
    stubDexWeb([0]);
    addMeta(HOST_NAME_META_NAME, 'test');
    addMeta(HOST_URL_META_NAME, '/servers/test');
    addMeta(HOST_PAGE_META_NAME, 'Settings', { 'data-url': '/servers/test/settings' });
    addMeta(HOST_PAGE_META_NAME, 'Bad', { 'data-url': 'javascript:alert(1)' });
    renderBar('/v1/flows');
    expect(within(rail()).getByRole('link', { name: 'test' }).getAttribute('href')).toBe('/servers/test');
    expect(within(rail()).getByRole('link', { name: 'Settings' }).getAttribute('href')).toBe('/servers/test/settings');
    expect(within(rail()).queryByRole('link', { name: 'Bad' })).toBeNull();
    expect(screen.queryByRole('link', { name: 'Dex home' })).toBeNull();
  });

  it('ignores a host URL that is not http(s) or a rooted path', async () => {
    stubDexWeb([0]);
    addMeta(HOST_NAME_META_NAME, 'test');
    addMeta(HOST_URL_META_NAME, 'javascript:alert(1)');
    renderBar('/v1/flows');
    expect(screen.getByRole('link', { name: 'Dex home' })).toBeTruthy();
  });

  it('lists Flows, then the experimental Designs, Work Queue and Connectors under their heading', async () => {
    stubOperatorDexWeb({ flows: 1, connectors: 2 });
    renderBar('/v1/flows');
    await waitFor(() => expect(railSections()).toHaveLength(4));
    expect(railSections().map((link) => link.getAttribute('href'))).toEqual(['/v1/flows', '/v1/rendering', '/v1/work-queue', '/v1/connectors']);
    expect(within(rail()).getByText('Experimental')).toBeTruthy();
  });

  it('offers only Designs besides Flows without the operator API', async () => {
    stubDexWeb([1]);
    renderBar('/v1/flows');
    await waitFor(() => expect(railSections()).toHaveLength(2));
  });

  it('has only Flows, and no Experimental heading, when nothing else applies', async () => {
    const dex = stubDexWeb([0]);
    renderBar('/v1/flows');
    await waitFor(() => expect(dex.readCount()).toBeGreaterThanOrEqual(1));
    expect(railSections()).toHaveLength(1);
    expect(within(rail()).queryByText('Experimental')).toBeNull();
  });

  it('marks the open section', async () => {
    stubOperatorDexWeb({ flows: 1, connectors: 1 });
    renderBar('/v1/work-queue/Flow0');
    await waitFor(() => expect(within(rail()).getByRole('link', { name: 'Work Queue' }).getAttribute('aria-current')).toBe('page'));
  });

  it('recovers after a failed read on the next navigation', async () => {
    const dex = stubDexWeb(['fail', 'fail', 2]);
    renderBar('/v1/flows');
    await waitFor(() => expect(dex.readCount()).toBeGreaterThanOrEqual(1));
    expect(railSections()).toHaveLength(1);
    fireEvent.click(screen.getByText('Open run a'));
    await waitFor(() => expect(railSections()).toHaveLength(2));
  });

  it('asks for the Connector setup listing once per app load, not on every page', async () => {
    stubOperatorDexWeb({ flows: 1, connectors: 1 });
    renderBar('/v1/flows');
    await waitFor(() => expect(railSections()).toHaveLength(4));
    fireEvent.click(screen.getByText('Open run a'));
    fireEvent.click(screen.getByText('Open run b'));
    await waitFor(() => expect(
      vi.mocked(fetch).mock.calls.filter(([input]) => input === '/api/flow-definition-types').length,
    ).toBeGreaterThanOrEqual(3));
    expect(vi.mocked(fetch).mock.calls.filter(([input]) => input === '/api/v2/connector-connections')).toHaveLength(1);
  });

  it('starts collapsed and remembers when it is expanded', async () => {
    stubDexWeb([0]);
    renderBar('/v1/flows');
    expect(rail().classList.contains('is-expanded')).toBe(false);
    fireEvent.click(within(rail()).getByRole('button', { name: 'Expand navigation' }));
    expect(rail().classList.contains('is-expanded')).toBe(true);
    expect(localStorage.getItem('dex.rail.expanded')).toBe('true');
  });

  it('opens on a phone from the bar and closes after a link', async () => {
    stubDexWeb([0]);
    renderBar('/v1/flows');
    fireEvent.click(screen.getByRole('button', { name: 'Open navigation' }));
    expect(rail().classList.contains('is-phone-open')).toBe(true);
    fireEvent.click(screen.getByText('Open run a'));
    await waitFor(() => expect(rail().classList.contains('is-phone-open')).toBe(false));
  });
});

describe('Dex Web bar trail', () => {
  it('names the section of a run page Flows and returns to the last Flows search', async () => {
    stubDexWeb([0]);
    renderBar('/v1/flows');
    fireEvent.click(screen.getByText('Search running'));
    fireEvent.click(screen.getByText('Open run a'));
    const trail = screen.getByRole('navigation', { name: 'Trail' });
    await waitFor(() => expect(within(trail).getByRole('link', { name: 'Flows' }).getAttribute('href')).toBe('/v1/flows?q=status%3DRunning'));
  });

  it('names the open section in the trail', async () => {
    stubOperatorDexWeb({ flows: 1, connectors: 1 });
    renderBar('/v1/work-queue/Flow0');
    const trail = screen.getByRole('navigation', { name: 'Trail' });
    expect(await within(trail).findByRole('link', { name: 'Work Queue' })).toBeTruthy();
  });

  it('shows what a page puts in its slots', async () => {
    stubDexWeb([0]);
    renderBar('/v1/flows/a', (
      <>
        <AppBarSlot name="page"><span>order-1</span></AppBarSlot>
        <AppBarSlot name="actions"><button type="button">Operate</button></AppBarSlot>
      </>
    ));
    const bar = screen.getByRole('banner');
    expect(await within(bar).findByText('order-1')).toBeTruthy();
    expect(within(bar).getByRole('button', { name: 'Operate' })).toBeTruthy();
  });
});

describe('Dex Web bar settings', () => {
  it('holds the theme, the timezone and the Docs link', async () => {
    stubDexWeb([0]);
    renderBar('/v1/flows');
    fireEvent.click(screen.getByRole('button', { name: 'Settings' }));
    const settings = screen.getByRole('dialog', { name: 'Settings' });
    fireEvent.click(within(settings).getByRole('button', { name: 'Dark' }));
    expect(localStorage.getItem('dex-web.theme')).toBe('dark');
    fireEvent.click(within(settings).getByRole('button', { name: 'UTC' }));
    expect(localStorage.getItem('dex-web-timezone')).toBe('UTC');
    const docs = within(settings).getByRole('link', { name: 'Dex documentation' });
    expect(docs.getAttribute('href')).toBe('https://docs.superdurable.io/');
    expect(docs.getAttribute('target')).toBe('_blank');
    expect(docs.getAttribute('rel')).toBe('noopener noreferrer');
  });
});
