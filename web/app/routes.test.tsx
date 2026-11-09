// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { AppRoutes } from './App';
import {
  agentInboxPath,
  flowsOfTypePath,
  legacyV2RedirectPath,
  runPath,
  sessionRunPath,
  workQueuePath,
} from './routes';

vi.mock('./flows/FlowSearchPage', () => ({ FlowSearchPage: () => <p>Flows page</p> }));
vi.mock('./flows/RunDetailsPage', () => ({ RunDetailsPage: () => <p>Run page</p> }));
vi.mock('./flows/CurrentRunRedirect', () => ({ CurrentRunRedirect: () => <p>Current run</p> }));
vi.mock('./rendering/FlowRenderingPage', () => ({ FlowRenderingPage: () => <p>Designs page</p> }));
vi.mock('./work-queue/WorkQueueWorkspace', () => ({ WorkQueueWorkspace: () => <p>Work Queue page</p> }));
vi.mock('./connectors/ConnectorsPage', () => ({ ConnectorsPage: () => <p>Connectors page</p> }));

function Location() {
  const location = useLocation();
  return <output aria-label="location">{`${location.pathname}${location.search}${location.hash}`}</output>;
}

function renderAt(entry: string) {
  render(
    <MemoryRouter initialEntries={[entry]}>
      <AppRoutes />
      <Routes><Route path="*" element={<Location />} /></Routes>
    </MemoryRouter>,
  );
}

function location() {
  return screen.getByLabelText('location').textContent;
}

afterEach(cleanup);

describe('Dex Web routes', () => {
  it('builds run, session and Work Queue paths under /v1', () => {
    expect(runPath('refund/42')).toBe('/v1/flows/refund%2F42');
    expect(runPath('refund/42', 'run/7')).toBe('/v1/flows/refund%2F42/run%2F7');
    expect(sessionRunPath('ses/1')).toBe('/v1/flows/ses%2F1?view=session');
    expect(workQueuePath('Refund Flow', 'refund/42')).toBe('/v1/work-queue/Refund%20Flow/refund%2F42');
    expect(agentInboxPath()).toBe('/v1/work-queue?view=agents');
  });

  it.each([
    ['/v2', '/v1/flows'],
    ['/v2/run', '/v1/flows'],
    ['/v2/run/Refund%20Flow', flowsOfTypePath('Refund Flow')],
    ['/v2/run/Refund%20Flow/refund%2F42', '/v1/flows/refund%2F42'],
    ['/v2/run/SupportAgent/ses-1?view=session', '/v1/flows/ses-1?view=session'],
    ['/v2/run/Refund%20Flow/refund-42/debug', '/v1/flows/refund-42'],
    ['/v2/run/Refund%20Flow/refund-42/debug/run%2F7', '/v1/flows/refund-42/run%2F7'],
    ['/v2/work-queue', '/v1/work-queue'],
    ['/v2/work-queue/Refund%20Flow/refund-42?view=agents', '/v1/work-queue/Refund%20Flow/refund-42?view=agents'],
    ['/v2/connectors?oauth=success&connectorId=llm', '/v1/connectors?oauth=success&connectorId=llm'],
    ['/v2/connections', '/v1/connectors'],
    ['/v2/connections/llm/default?a=1#setup', '/v1/connectors/llm/default?a=1#setup'],
    ['/v2/anything-else', '/v1/flows'],
  ])('maps %s to %s', (from, to) => {
    const url = new URL(from, 'http://dex.test');
    expect(legacyV2RedirectPath({ pathname: url.pathname, search: url.search, hash: url.hash })).toBe(to);
  });

  it.each([
    ['/', '/v1/flows', 'Flows page'],
    ['/v2/run/Refund%20Flow/refund-42', '/v1/flows/refund-42', 'Current run'],
    ['/v2/run/Refund%20Flow/refund-42/debug/run-7', '/v1/flows/refund-42/run-7', 'Run page'],
    ['/v2/work-queue/Refund%20Flow?view=agents', '/v1/work-queue/Refund%20Flow?view=agents', 'Work Queue page'],
    ['/v2/connections?oauth=success', '/v1/connectors?oauth=success', 'Connectors page'],
    ['/v1/rendering', '/v1/rendering', 'Designs page'],
    ['/no-such-page', '/v1/flows', 'Flows page'],
  ])('lands %s on %s', async (entry, landing, page) => {
    renderAt(entry);
    await waitFor(() => expect(location()).toBe(landing));
    expect(screen.getByText(page)).toBeTruthy();
  });
});
