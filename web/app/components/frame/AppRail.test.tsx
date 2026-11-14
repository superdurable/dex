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
import { AppRail } from './AppRail';
import { FLOWS_SECTION } from './appSections';
import type { ServerReachabilityStatus } from './useServerReachability';

afterEach(cleanup);

function rail(reachability: ServerReachabilityStatus, isExpanded = true) {
  return (
    <MemoryRouter>
      <AppRail
        flowsHref="/v1/flows"
        isExpanded={isExpanded}
        isPhoneOpen={false}
        onExpandedChange={vi.fn()}
        onPhoneClose={vi.fn()}
        reachability={reachability}
        sections={[FLOWS_SECTION]}
      />
    </MemoryRouter>
  );
}

function dot() {
  return document.querySelector<HTMLElement>('.app-rail-reach')!;
}

function liveRegion() {
  return document.querySelector<HTMLElement>('[aria-live="polite"]')!;
}

describe('rail reachability', () => {
  it('shows a faint neutral dot and no words while checking', () => {
    render(rail({ state: 'checking' }));
    expect(dot().className).toContain('is-checking');
    expect(screen.queryByText('Dex Server unreachable')).toBeNull();
    expect(liveRegion().textContent).toBe('');
  });

  it('keeps a reachable server quiet: a faint dot and no words', () => {
    render(rail({ state: 'reachable', lastReachableAt: Date.now() }));
    expect(dot().className).toContain('is-reachable');
    expect(dot().getAttribute('title')).toBe('Dex Server reachable');
    expect(screen.queryByText('Dex Server unreachable')).toBeNull();
    expect(liveRegion().textContent).toBe('');
  });

  it('says an unreachable server in red words, with the detail in the tooltip', () => {
    const lastReachableAt = Date.parse('2026-11-13T20:00:00Z');
    render(rail({ state: 'unreachable', lastError: 'Dex FlowService is unavailable', lastReachableAt }));
    expect(dot().className).toContain('is-unreachable');
    expect(screen.getByText('Dex Server unreachable').className).toBe('app-rail-reach-text');
    const tooltip = dot().getAttribute('title') ?? '';
    expect(tooltip).toContain('Dex Web cannot reach the Dex Server.');
    expect(tooltip).toContain('Last error: Dex FlowService is unavailable.');
    expect(tooltip).toContain(`Last reached at ${new Date(lastReachableAt).toLocaleTimeString()}.`);
    expect(screen.getByRole('link', { name: 'Dex home. Dex Server unreachable' }).getAttribute('title')).toBe(tooltip);
  });

  it('keeps the words inside the collapsed rail label, so the rail never widens', () => {
    render(rail({ state: 'unreachable', lastError: 'HTTP 503' }, false));
    expect(screen.getByText('Dex Server unreachable').closest('.app-rail-label')).not.toBeNull();
    expect(document.querySelector('.app-rail.is-expanded')).toBeNull();
  });

  it('announces each change once, not on every poll', () => {
    const { rerender } = render(rail({ state: 'checking' }));
    rerender(rail({ state: 'reachable', lastReachableAt: 1 }));
    expect(liveRegion().textContent).toBe('');
    rerender(rail({ state: 'unreachable', lastError: 'HTTP 503' }));
    expect(liveRegion().textContent).toBe('Dex Server unreachable.');
    const announced = liveRegion().firstChild;
    rerender(rail({ state: 'unreachable', lastError: 'HTTP 504' }));
    expect(liveRegion().textContent).toBe('Dex Server unreachable.');
    expect(liveRegion().firstChild).toBe(announced);
    rerender(rail({ state: 'reachable', lastReachableAt: 2 }));
    expect(liveRegion().textContent).toBe('Dex Server reachable again.');
  });
});
