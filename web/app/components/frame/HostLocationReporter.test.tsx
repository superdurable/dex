// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen } from '@testing-library/react';
import { Link, MemoryRouter, Navigate, Route, Routes } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { HOST_NAVIGATION_META_NAME } from '@/lib/hostNavigation';
import { HostLocationReporter } from './HostLocationReporter';

function setHostNavigationMeta(content: string | null) {
  document.head.querySelector(`meta[name="${HOST_NAVIGATION_META_NAME}"]`)?.remove();
  if (content === null) return;
  const meta = document.createElement('meta');
  meta.name = HOST_NAVIGATION_META_NAME;
  meta.content = content;
  document.head.appendChild(meta);
}

function renderReporter(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <HostLocationReporter />
      <Routes>
        <Route path="/v1/flows/:flowId" element={<Navigate to="/v1/flows/flow-1/run-2?view=session" replace />} />
        <Route path="*" element={<Link to="/v1/flows/flow-1/run-1?view=history">Open run</Link>} />
      </Routes>
    </MemoryRouter>,
  );
}

let parentPostMessage: ReturnType<typeof vi.fn>;

beforeEach(() => {
  parentPostMessage = vi.fn();
  vi.spyOn(window, 'parent', 'get').mockReturnValue({ postMessage: parentPostMessage } as unknown as Window);
});

afterEach(() => {
  cleanup();
  setHostNavigationMeta(null);
  vi.restoreAllMocks();
});

describe('HostLocationReporter', () => {
  it('posts the base-relative location to the parent after each route change under host navigation', () => {
    setHostNavigationMeta('true');
    renderReporter('/v1/flows?q=status%3DRunning');
    expect(parentPostMessage).toHaveBeenLastCalledWith(
      { type: 'dex-web:location', version: 1, path: '/v1/flows', search: '?q=status%3DRunning' },
      window.location.origin,
    );

    fireEvent.click(screen.getByRole('link', { name: 'Open run' }));
    expect(parentPostMessage).toHaveBeenLastCalledWith(
      { type: 'dex-web:location', version: 1, path: '/v1/flows/flow-1/run-1', search: '?view=history' },
      window.location.origin,
    );
  });

  it('posts the page a redirect lands on', () => {
    setHostNavigationMeta('true');
    renderReporter('/v1/flows/flow-1');
    expect(parentPostMessage).toHaveBeenLastCalledWith(
      { type: 'dex-web:location', version: 1, path: '/v1/flows/flow-1/run-2', search: '?view=session' },
      window.location.origin,
    );
  });

  it('posts nothing without host navigation', () => {
    renderReporter('/v1/flows');
    fireEvent.click(screen.getByRole('link', { name: 'Open run' }));
    expect(parentPostMessage).not.toHaveBeenCalled();
  });

  it('posts nothing when Dex Web is not in a frame', () => {
    setHostNavigationMeta('true');
    vi.spyOn(window, 'parent', 'get').mockReturnValue(window);
    const ownPostMessage = vi.spyOn(window, 'postMessage');
    renderReporter('/v1/flows');
    expect(ownPostMessage).not.toHaveBeenCalled();
    expect(parentPostMessage).not.toHaveBeenCalled();
  });
});
