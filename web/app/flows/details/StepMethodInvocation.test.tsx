// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { renderToStaticMarkup } from 'react-dom/server';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '@/app/providers';
import { StepMethodInvocation } from './StepMethodInvocation';

function renderInvocation(value: unknown): string {
  return renderToStaticMarkup(
    <PreferencesProvider>
      <StepMethodInvocation value={value} label="Execute invocation" />
    </PreferencesProvider>,
  );
}

describe('Step method invocation', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date('2026-11-05T08:05:30Z'));
  });
  afterEach(() => {
    vi.useRealTimers();
  });

  it.each([
    [1, 'Waiting for a Worker'],
    [2, 'Waiting for a Worker that supports this Step type'],
    [3, 'Waiting for locks'],
  ])('shows pending reason %s and how long the attempt has waited', (pendingReason, expected) => {
    const markup = renderInvocation({ phase: 1, pendingReason, attempt: 2, pendingSinceTime: '2026-11-05T08:00:00Z' });
    expect(markup).toContain('Execute invocation');
    expect(markup).toContain('Pending');
    expect(markup).toContain(expected);
    expect(markup).toContain('Attempt');
    expect(markup).toContain('>2<');
    expect(markup).toContain('waiting 5m 30s');
  });

  it('shows the start of a running attempt without a pending reason', () => {
    const markup = renderInvocation({ phase: 2, attempt: 1, pendingSinceTime: '2026-11-05T08:00:00Z', attemptStartedTime: '2026-11-05T08:01:00Z' });
    expect(markup).toContain('Running');
    expect(markup).toContain('Attempt started');
    expect(markup).not.toContain('Pending reason');
    expect(markup).not.toContain('Pending since');
  });

  it('shows when the backoff ends', () => {
    const markup = renderInvocation({ phase: 3, attempt: 3, backoffFiresAtTime: '2026-11-05T08:10:00Z' });
    expect(markup).toContain('Backoff');
    expect(markup).toContain('Backoff ends');
  });

  it('renders nothing for Servers without invocation state', () => {
    expect(renderToStaticMarkup(<StepMethodInvocation value={undefined} label="WaitFor invocation" />)).toBe('');
  });
});
