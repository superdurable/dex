// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it, vi } from 'vitest';
import { PreferencesProvider } from '@/app/providers';
import { listenToStream, StreamPanel } from './StreamPanel';

describe('StreamPanel', () => {
  it('shows one Stream name control before it starts listening', () => {
    const markup = renderToStaticMarkup(
      <PreferencesProvider>
        <StreamPanel flowId="checkout-1" flowType="CheckoutFlow" />
      </PreferencesProvider>,
    );

    expect(markup).toContain('Resumable stream');
    expect(markup).toContain('Stream name');
    expect(markup).toContain('placeholder="thinking"');
    expect(markup).toContain('Start listening');
    expect(markup).toContain('best effort');
  });

  it('continues after a long-poll timeout and resumes after each message', async () => {
    const controller = new AbortController();
    const fetcher = vi.fn(async (input: RequestInfo | URL) => {
      void input;
      if (fetcher.mock.calls.length === 3) controller.abort();
      if (fetcher.mock.calls.length === 1) {
        return new Response(JSON.stringify({
          value: 'first token',
          resumeToken: 'token-1',
          createdTime: '2026-08-27T12:34:56Z',
          idempotencyKey: 'run-1#Step-1',
        }), { status: 200 });
      }
      return new Response(JSON.stringify({ error: 'Stream read timed out' }), { status: 408 });
    });
    const messages: string[] = [];

    await listenToStream({
      flowId: 'checkout-1',
      flowType: 'CheckoutFlow',
      streamName: 'thinking',
      signal: controller.signal,
      fetcher: fetcher as typeof fetch,
      onMessage: async (message) => {
        messages.push(message.resumeToken);
      },
    });

    expect(messages).toEqual(['token-1']);
    expect(fetcher).toHaveBeenCalledTimes(3);
    expect(String(fetcher.mock.calls[2][0])).toContain('resumeToken=token-1');
  });
});
