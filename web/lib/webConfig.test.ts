// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { afterEach, describe, expect, it, vi } from 'vitest';
import { dexFetch, isEmbedded, webBasePath, webPath } from './webConfig';

afterEach(() => {
  vi.unstubAllGlobals();
});

describe('Dex Web request configuration', () => {
  it('defaults to the standalone root path during server-side rendering', () => {
    expect(webBasePath()).toBe('/');
    expect(isEmbedded()).toBe(false);
    expect(webPath('/api/flow-definitions')).toBe('/api/flow-definitions');
  });

  it('prefixes app paths under the request-specific mount', () => {
    stubWebConfig({ basePath: '/apps/project-1/dex', embedded: true });

    expect(webBasePath()).toBe('/apps/project-1/dex');
    expect(isEmbedded()).toBe(true);
    expect(webPath('/v2/run')).toBe('/apps/project-1/dex/v2/run');
  });

  it('rejects paths that are not rooted', () => {
    expect(() => webPath('api/flow-definitions')).toThrow('Dex Web path must start with /');
  });

  it('adds the proxy CSRF token only to mutating requests', async () => {
    stubWebConfig({
      basePath: '/apps/project-1/dex',
      embedded: true,
      csrfHeaderName: 'X-CSRF-Token',
      csrfToken: 'host-token',
    });
    const fetcher = vi.fn(async (_input: RequestInfo | URL, _init?: RequestInit) => (
      new Response(null, { status: 204 })
    ));
    vi.stubGlobal('fetch', fetcher);

    await dexFetch('/api/v2/catalog');
    await dexFetch('/api/v2/actions', {
      method: 'POST',
      headers: { 'X-Definition-Revision': 'revision-1' },
    });

    expect(fetcher).toHaveBeenNthCalledWith(
      1,
      '/apps/project-1/dex/api/v2/catalog',
      expect.objectContaining({ headers: expect.any(Headers) }),
    );
    const getHeaders = fetcher.mock.calls[0][1]?.headers as Headers;
    expect(getHeaders.has('X-CSRF-Token')).toBe(false);
    const mutationHeaders = fetcher.mock.calls[1][1]?.headers as Headers;
    expect(mutationHeaders.get('X-CSRF-Token')).toBe('host-token');
    expect(mutationHeaders.get('X-Definition-Revision')).toBe('revision-1');
  });
});

function stubWebConfig(config: Record<string, unknown>) {
  vi.stubGlobal('window', { __DEX_WEB_CONFIG__: config });
}
