// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, render, screen, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ThemeProvider } from '../theme';
import { ConnectorsPage } from './ConnectorsPage';

function renderConnectors() {
  render(
    <MemoryRouter initialEntries={['/v1/connectors']}>
      <ThemeProvider>
        <ConnectorsPage />
      </ThemeProvider>
    </MemoryRouter>,
  );
}

async function expectFlowsBreadcrumb() {
  const breadcrumb = await screen.findByRole('navigation', { name: 'Breadcrumb' });
  expect(within(breadcrumb).getByRole('link', { name: 'Flows' }).getAttribute('href')).toBe('/v1/flows');
  expect(breadcrumb.querySelector('[aria-current="page"]')?.textContent).toBe('Connectors');
}

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Connectors breadcrumb', () => {
  it('leads the loaded page back to Flows', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({
      enabled: true, definitionRevision: 'r1', csrfToken: 't', connections: [],
    }))));
    renderConnectors();
    await expectFlowsBreadcrumb();
  });

  it('leads a failed listing back to Flows', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'listing unavailable' }), { status: 503 })));
    renderConnectors();
    await screen.findByRole('alert');
    await expectFlowsBreadcrumb();
  });
});
