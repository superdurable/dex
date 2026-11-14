// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { CurrentRunRedirect } from '../flows/CurrentRunRedirect';
import { FlowRenderingPage } from './FlowRenderingPage';

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('v1 pages without a run breadcrumb', () => {
  it('lead a failed current-run lookup back to Flows', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => new Response(JSON.stringify({ error: 'flow not found' }), { status: 404 })));
    render(<MemoryRouter><CurrentRunRedirect flowId="missing-flow" /></MemoryRouter>);
    const flows = await screen.findByRole('link', { name: 'Flows' });
    expect(flows.getAttribute('href')).toBe('/v1/flows');
    expect(screen.getByText('missing-flow')).toBeTruthy();
  });
});
