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
import { HOST_NAME_META_NAME, HOST_URL_META_NAME } from '@/lib/hostLink';
import { AppBar } from './AppBar';
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
              <AppBar />
              {page}
              <Link to="/v1/flows/a">Open run a</Link>
              <Link to="/v1/flows/b">Open run b</Link>
              <Link to="/v1/flows?q=status%3DRunning">Search running</Link>
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

function sectionToggle() {
  return screen.queryByRole('button', { name: 'Switch page' });
}

/** Opens the section menu when it is closed and returns the named item, or null. */
function sectionItem(name: string) {
  const button = sectionToggle();
  if (!button) return null;
  if (button.getAttribute('aria-expanded') !== 'true') fireEvent.click(button);
  return screen.queryByRole('menuitem', { name: new RegExp(`^${name}`) });
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

describe('Dex Web bar trail', () => {
  it('starts at the Dex mark, which leads to Flows, when no host serves Dex Web', async () => {
    stubDexWeb([0]);
    renderBar('/v1/work-queue');
    const trail = screen.getByRole('navigation', { name: 'Trail' });
    expect(within(trail).getByRole('link', { name: 'Dex home' }).getAttribute('href')).toBe('/v1/flows');
  });

  it('starts at the host page when the server names one', async () => {
    stubDexWeb([0]);
    for (const [name, content] of [[HOST_NAME_META_NAME, 'test'], [HOST_URL_META_NAME, '/servers/test']]) {
      const meta = document.createElement('meta');
      meta.name = name;
      meta.content = content;
      document.head.append(meta);
    }
    renderBar('/v1/flows');
    const host = screen.getByRole('link', { name: 'test' });
    expect(host.getAttribute('href')).toBe('/servers/test');
    expect(screen.queryByRole('link', { name: 'Dex home' })).toBeNull();
  });

  it('ignores a host URL that is not http(s) or a rooted path', async () => {
    stubDexWeb([0]);
    for (const [name, content] of [[HOST_NAME_META_NAME, 'test'], [HOST_URL_META_NAME, 'javascript:alert(1)']]) {
      const meta = document.createElement('meta');
      meta.name = name;
      meta.content = content;
      document.head.append(meta);
    }
    renderBar('/v1/flows');
    expect(screen.getByRole('link', { name: 'Dex home' })).toBeTruthy();
  });

  it('names the section of a run page Flows and returns to the last Flows search', async () => {
    stubDexWeb([0]);
    renderBar('/v1/flows');
    fireEvent.click(screen.getByText('Search running'));
    fireEvent.click(screen.getByText('Open run a'));
    const flows = await screen.findByRole('link', { name: 'Flows' });
    expect(flows.getAttribute('href')).toBe('/v1/flows?q=status%3DRunning');
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

describe('Dex Web bar sections', () => {
  it('lists Flows, Work Queue, Designs and Connectors when they apply', async () => {
    stubOperatorDexWeb({ flows: 1, connectors: 2 });
    renderBar('/v1/flows');
    await waitFor(() => expect(sectionItem('Connectors')).not.toBeNull());
    const items = screen.getAllByRole('menuitem');
    expect(items.map((item) => item.getAttribute('href'))).toEqual(['/v1/flows', '/v1/work-queue', '/v1/rendering', '/v1/connectors']);
    expect(items.every((item) => (item.querySelector('small')?.textContent ?? '').length > 0)).toBe(true);
  });

  it('offers only Designs besides Flows without the operator API', async () => {
    stubDexWeb([1]);
    renderBar('/v1/flows');
    await waitFor(() => expect(sectionItem('Designs')).not.toBeNull());
    expect(screen.getAllByRole('menuitem')).toHaveLength(2);
  });

  it('has no section menu when only Flows applies', async () => {
    const dex = stubDexWeb([0]);
    renderBar('/v1/flows');
    await waitFor(() => expect(dex.readCount()).toBe(2));
    expect(sectionToggle()).toBeNull();
    expect(screen.getByRole('link', { name: 'Flows' })).toBeTruthy();
  });

  it('names the open section in the trail', async () => {
    stubOperatorDexWeb({ flows: 1, connectors: 1 });
    renderBar('/v1/work-queue/Flow0');
    expect(await screen.findByRole('link', { name: 'Work Queue' })).toBeTruthy();
  });

  it('shows Designs while loading only after the list answers', async () => {
    const pendingReads: Array<() => void> = [];
    vi.stubGlobal('fetch', vi.fn((input: string) => {
      if (input === '/api/v2/connector-connections') return Promise.resolve(new Response('', { status: 404 }));
      return new Promise<Response>((resolve) => { pendingReads.push(() => resolve(definitionsResponse(1))); });
    }));
    renderBar('/v1/flows');
    expect(sectionToggle()).toBeNull();
    await act(async () => { pendingReads.forEach((release) => release()); });
    await waitFor(() => expect(sectionItem('Designs')).not.toBeNull());
  });

  it('recovers after a failed read on the next navigation', async () => {
    const dex = stubDexWeb(['fail', 'fail', 2]);
    renderBar('/v1/flows');
    await waitFor(() => expect(dex.readCount()).toBe(2));
    expect(sectionToggle()).toBeNull();
    fireEvent.click(screen.getByText('Open run a'));
    await waitFor(() => expect(sectionItem('Designs')).not.toBeNull());
  });

  it('asks for the Connector setup listing once per app load, not on every page', async () => {
    stubOperatorDexWeb({ flows: 1, connectors: 1 });
    renderBar('/v1/flows');
    await waitFor(() => expect(sectionItem('Connectors')).not.toBeNull());
    fireEvent.click(screen.getByText('Open run a'));
    fireEvent.click(screen.getByText('Open run b'));
    await waitFor(() => expect(
      vi.mocked(fetch).mock.calls.filter(([input]) => input === '/api/flow-definition-types').length,
    ).toBeGreaterThanOrEqual(4));
    expect(vi.mocked(fetch).mock.calls.filter(([input]) => input === '/api/v2/connector-connections')).toHaveLength(1);
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
