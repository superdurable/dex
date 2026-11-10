// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** Names the meta tag the Go server writes into index.html with the Dex Web base path. */
export const BASE_PATH_META_NAME = 'dex-web-base-path';

const basePathPattern = /^(?:\/[A-Za-z0-9._~-]+)+$/;

/**
 * Returns the canonical base path from a served page's meta value: '/' or a prefix such as '/dex'.
 * A missing or malformed value means the root, so the Vite dev server and tests keep root URLs.
 */
export function canonicalBasePath(metaValue: string | null | undefined): string {
  if (!metaValue || metaValue === '/') return '/';
  const trimmed = metaValue.endsWith('/') ? metaValue.slice(0, -1) : metaValue;
  if (!basePathPattern.test(trimmed)) return '/';
  if (trimmed.split('/').some((segment) => segment === '.' || segment === '..')) return '/';
  return trimmed;
}

/** Reads the base path the Go server served this page under. */
export function webBasePath(): string {
  if (typeof document === 'undefined') return '/';
  const meta = document.querySelector(`meta[name="${BASE_PATH_META_NAME}"]`);
  return canonicalBasePath(meta?.getAttribute('content'));
}

/** Prefixes a rooted Dex Web path, such as '/api/v2/search' or '/v1/flows', with a base path. */
export function joinWebPath(basePath: string, rootedPath: string): string {
  if (!rootedPath.startsWith('/')) {
    throw new Error(`Dex Web path must start with /: ${rootedPath}`);
  }
  return basePath === '/' ? rootedPath : `${basePath}${rootedPath}`;
}

/** Returns the URL of a rooted Dex Web path, such as an /api route, under this page's base path. */
export function webURL(rootedPath: string): string {
  return joinWebPath(webBasePath(), rootedPath);
}

/** Returns the React Router basename, which is undefined at the root. */
export function routerBasename(basePath: string = webBasePath()): string | undefined {
  return basePath === '/' ? undefined : basePath;
}
