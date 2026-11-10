// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { Link, MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '../providers';
import { ThemeProvider } from '../theme';
import { WebCatalogProvider } from '../catalog/WebCatalogProvider';
import { AppHeader } from './AppHeader';

type DefinitionsReply = 'fail' | number;

function definitionsResponse(reply: DefinitionsReply): Response {
  if (reply === 'fail') {
    return new Response(JSON.stringify({ error: 'definitions unavailable' }), { status: 503 });
  }
  return new Response(JSON.stringify({
    configured: true,
    directory: '/defs',
    definitionRevision: 'r1',
    definitionCount: reply,
    definitions: Array.from({ length: reply }, (_, index) => ({ id: `d${index}` })),
  }), { status: 200 });
}

function stubDexWeb(replies: DefinitionsReply[]) {
  let readCount = 0;
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    if (input === '/api/v2/catalog') return new Response('{}', { status: 404 });
    readCount += 1;
    const reply = replies[Math.min(readCount - 1, replies.length - 1)];
    return definitionsResponse(reply);
  }));
  return { readCount: () => readCount };
}

function renderHeader(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <PreferencesProvider>
        <ThemeProvider>
        <WebCatalogProvider>
          <AppHeader />
          <Link to="/v1/flows/a">Open run a</Link>
          <Link to="/v1/flows/b">Open run b</Link>
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

function experimentalMenuButton() {
  return screen.queryByRole('button', { name: /Experimental/ });
}

/** Opens the Experimental menu when it is closed and returns the named item, or null. */
function experimentalItem(name: string) {
  const button = experimentalMenuButton();
  if (!button) return null;
  if (button.getAttribute('aria-expanded') !== 'true') fireEvent.click(button);
  return screen.queryByRole('menuitem', { name: new RegExp(`^${name}`) });
}

function designsLink() {
  return experimentalItem('Designs');
}

beforeEach(() => {
  vi.stubGlobal('localStorage', memoryStorage());
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Dex Web header Docs link', () => {
  it('opens the Dex documentation in a new tab', async () => {
    const dex = stubDexWeb([0]);
    renderHeader('/v1/flows');
    await waitFor(() => expect(dex.readCount()).toBeGreaterThan(0));
    const link = screen.getByRole('link', { name: 'Dex documentation' });
    expect(link.getAttribute('href')).toBe('https://docs.superdurable.io/');
    expect(link.getAttribute('target')).toBe('_blank');
    expect(link.getAttribute('rel')).toBe('noopener noreferrer');
    expect(link.textContent).toBe('Docs');
  });
});

describe('Dex Web header Designs entry', () => {
  it('stays hidden while loading', async () => {
    const pendingReads: Array<() => void> = [];
    vi.stubGlobal('fetch', vi.fn((input: string) => new Promise<Response>((resolve) => {
      if (input === '/api/v2/catalog') {
        resolve(new Response('{}', { status: 404 }));
        return;
      }
      pendingReads.push(() => resolve(definitionsResponse(1)));
    })));
    renderHeader('/v1/flows');
    expect(designsLink()).toBeNull();
    await act(async () => { pendingReads.forEach((release) => release()); });
    await waitFor(() => expect(designsLink()).not.toBeNull());
  });

  it('recovers after a failed read on the next navigation', async () => {
    const dex = stubDexWeb(['fail', 'fail', 2]);
    renderHeader('/v1/flows');
    await waitFor(() => expect(dex.readCount()).toBe(2));
    expect(designsLink()).toBeNull();
    fireEvent.click(screen.getByText('Open run a'));
    await waitFor(() => expect(designsLink()).not.toBeNull());
  });

  it('follows the directory from empty to loaded and back', async () => {
    const dex = stubDexWeb([0, 0, 1, 0]);
    renderHeader('/v1/flows');
    await waitFor(() => expect(dex.readCount()).toBe(2));
    expect(designsLink()).toBeNull();
    fireEvent.click(screen.getByText('Open run a'));
    await waitFor(() => expect(designsLink()).not.toBeNull());
    fireEvent.click(screen.getByText('Open run b'));
    await waitFor(() => expect(designsLink()).toBeNull());
    expect(dex.readCount()).toBe(4);
  });

});

function stubOperatorDexWeb({ flows, connectors }: { flows: number; connectors: 'missing' | 'disabled' | number }) {
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    if (input === '/api/v2/catalog') {
      return new Response(JSON.stringify({
        enabled: true,
        definitionRevision: 'r1',
        flows: Array.from({ length: flows }, (_, index) => ({ flowType: `Flow${index}`, definition: {} })),
      }));
    }
    if (input === '/api/v2/connector-connections') {
      if (connectors === 'missing') return new Response('404 page not found', { status: 404 });
      return new Response(JSON.stringify({
        enabled: connectors !== 'disabled',
        definitionRevision: 'r1',
        csrfToken: 't',
        connections: Array.from({ length: connectors === 'disabled' ? 0 : connectors }, (_, index) => ({ connectorId: `c${index}` })),
      }));
    }
    return definitionsResponse(1);
  }));
}

describe('Dex Web header experimental pages', () => {
  it('lists Designs, Work Queue and Connectors in one Experimental menu when they apply', async () => {
    stubOperatorDexWeb({ flows: 1, connectors: 2 });
    renderHeader('/v1/flows');
    await waitFor(() => expect(experimentalItem('Connectors')).not.toBeNull());
    const items = screen.getAllByRole('menuitem');
    expect(items.map((item) => item.getAttribute('href'))).toEqual(['/v1/rendering', '/v1/work-queue', '/v1/connectors']);
    expect(items.map((item) => item.querySelector('b')?.textContent)).toEqual(['Designs', 'Work Queue', 'Connectors']);
    expect(items.every((item) => (item.querySelector('small')?.textContent ?? '').length > 0)).toBe(true);
    const nav = screen.getByRole('navigation', { name: 'Primary navigation' });
    expect(nav.querySelectorAll(':scope > a:not(.header-docs-link)')).toHaveLength(0);
  });

  it('offers only Designs without the operator API', async () => {
    stubDexWeb([1]);
    renderHeader('/v1/flows');
    await waitFor(() => expect(designsLink()).not.toBeNull());
    expect(screen.getAllByRole('menuitem')).toHaveLength(1);
  });

  it.each([
    ['no valid Flow types and no Connector setup', 0, 'missing' as const],
    ['Connector setup turned off', 0, 'disabled' as const],
  ])('leaves out what does not apply: %s', async (_name, flows, connectors) => {
    stubOperatorDexWeb({ flows, connectors });
    renderHeader('/v1/flows');
    await waitFor(() => expect(designsLink()).not.toBeNull());
    expect(screen.queryByRole('menuitem', { name: /Work Queue/ })).toBeNull();
    expect(screen.queryByRole('menuitem', { name: /Connectors/ })).toBeNull();
  });

  it('offers Connectors where Connector setup runs, before any connection and without Flow definitions', async () => {
    vi.stubGlobal('fetch', vi.fn(async (input: string) => {
      if (input === '/api/v2/catalog') {
        return new Response(JSON.stringify({ enabled: true, definitionRevision: 'r0', flows: [] }));
      }
      if (input === '/api/v2/connector-connections') {
        return new Response(JSON.stringify({ enabled: true, definitionRevision: 'r0', csrfToken: 't', connections: [] }));
      }
      return new Response(JSON.stringify({
        configured: false, definitionRevision: 'r0', definitionCount: 0, definitions: [],
      }));
    }));
    renderHeader('/v1/flows');
    await waitFor(() => expect(experimentalItem('Connectors')).not.toBeNull());
    expect(screen.getAllByRole('menuitem').map((item) => item.getAttribute('href'))).toEqual(['/v1/connectors']);
  });

  it('hides the Experimental entry when nothing applies', async () => {
    const dex = stubDexWeb([0]);
    renderHeader('/v1/flows');
    await waitFor(() => expect(dex.readCount()).toBe(2));
    expect(experimentalMenuButton()).toBeNull();
  });

  it('highlights the Experimental entry on one of its pages', async () => {
    stubOperatorDexWeb({ flows: 1, connectors: 1 });
    renderHeader('/v1/work-queue/Flow0');
    await waitFor(() => expect(experimentalMenuButton()?.getAttribute('data-active')).toBe('true'));
  });

  it('has no Dex Web version picker', async () => {
    stubOperatorDexWeb({ flows: 1, connectors: 1 });
    renderHeader('/v1/flows');
    await waitFor(() => expect(experimentalMenuButton()).not.toBeNull());
    expect(screen.queryByRole('combobox', { name: 'Dex Web version' })).toBeNull();
    expect(screen.queryByText('Version')).toBeNull();
  });
});

describe('Dex Web header logo', () => {
  it.each(['/v1/flows', '/v1/work-queue'])('opens the company site in the same tab on %s', async (path) => {
    stubDexWeb([0]);
    renderHeader(path);
    const logo = screen.getByRole('link', { name: 'Super Durable home' });
    expect(logo.getAttribute('href')).toBe('https://superdurable.io');
    expect(logo.getAttribute('target')).toBeNull();
    expect(screen.getAllByRole('link', { name: 'Super Durable home' })).toHaveLength(1);
  });
});

describe('Dex Web header Flows link', () => {
  it.each(['/v1/flows', '/v1/work-queue', '/v1/rendering', '/v1/connectors'])('is absent on %s, where page breadcrumbs lead back instead', async (path) => {
    stubOperatorDexWeb({ flows: 1, connectors: 1 });
    renderHeader(path);
    await waitFor(() => expect(experimentalMenuButton()).not.toBeNull());
    expect(screen.queryByRole('link', { name: 'Flows' })).toBeNull();
  });
});
