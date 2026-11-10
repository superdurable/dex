// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { Link, MemoryRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom';
import { afterEach, describe, expect, it } from 'vitest';
import {
  BASE_PATH_META_NAME,
  canonicalBasePath,
  joinWebPath,
  routerBasename,
  scopedStorageKey,
  webBasePath,
  webURL,
} from './basePath';

function setBasePathMeta(content: string | null) {
  document.head.querySelector(`meta[name="${BASE_PATH_META_NAME}"]`)?.remove();
  if (content === null) return;
  const meta = document.createElement('meta');
  meta.name = BASE_PATH_META_NAME;
  meta.content = content;
  document.head.appendChild(meta);
}

afterEach(() => {
  cleanup();
  setBasePathMeta(null);
});

describe('canonicalBasePath', () => {
  it.each([
    [null, '/'],
    ['', '/'],
    ['/', '/'],
    ['/dex', '/dex'],
    ['/dex/', '/dex'],
    ['/dex-web/server-a', '/dex-web/server-a'],
    ['dex', '/'],
    ['/dex//a', '/'],
    ['/dex/../a', '/'],
    ['/dex%2Fa', '/'],
    ['/dex?a=1', '/'],
  ])('reads %j as %j', (value, expected) => {
    expect(canonicalBasePath(value)).toBe(expected);
  });
});

describe('scopedStorageKey', () => {
  it('keeps the plain key at the root and adds the base path elsewhere', () => {
    expect(scopedStorageKey('dex-web-recent-queries')).toBe('dex-web-recent-queries');
    setBasePathMeta('/dex-web/server-a/');
    expect(scopedStorageKey('dex-web-recent-queries')).toBe('dex-web-recent-queries@/dex-web/server-a');
    expect(scopedStorageKey('dex-web-recent-queries', '/dex-web/server-b')).toBe('dex-web-recent-queries@/dex-web/server-b');
  });
});

describe('webURL', () => {
  it('keeps root URLs without a meta tag', () => {
    expect(webBasePath()).toBe('/');
    expect(webURL('/api/v2/search')).toBe('/api/v2/search');
    expect(routerBasename()).toBeUndefined();
  });

  it('prefixes API and page URLs with the served base path', () => {
    setBasePathMeta('/dex-web/server-a');
    expect(webBasePath()).toBe('/dex-web/server-a');
    expect(webURL('/api/flows/summary?flowId=a%2Fb')).toBe('/dex-web/server-a/api/flows/summary?flowId=a%2Fb');
    expect(webURL('/v1/flows/flow-1')).toBe('/dex-web/server-a/v1/flows/flow-1');
    expect(routerBasename()).toBe('/dex-web/server-a');
  });

  it('rejects a path that is not rooted', () => {
    expect(() => joinWebPath('/dex', 'api/v2/search')).toThrow('must start with /');
  });
});

function Location() {
  const location = useLocation();
  return <output aria-label="location">{`${location.pathname}${location.search}`}</output>;
}

describe('router basename', () => {
  it('builds links and redirects under the base path', async () => {
    render(
      <MemoryRouter basename={routerBasename('/dex')} initialEntries={['/dex/v2/connectors?view=session']}>
        <Routes>
          <Route path="/v2/*" element={<Navigate to={{ pathname: '/v1/connectors', search: '?view=session' }} replace />} />
          <Route path="/v1/connectors" element={<Link to="/v1/flows/flow-1?view=session">Flow</Link>} />
        </Routes>
        <Location />
      </MemoryRouter>,
    );
    await waitFor(() => expect(screen.getByLabelText('location').textContent).toBe('/v1/connectors?view=session'));
    expect(screen.getByRole('link', { name: 'Flow' }).getAttribute('href')).toBe('/dex/v1/flows/flow-1?view=session');
  });
});
