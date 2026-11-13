// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** The page of the host that serves this Dex Web, which the bar's trail starts from. */
export interface HostLink {
  name: string;
  url: string;
}

export const HOST_NAME_META_NAME = 'dex-web-host-name';
export const HOST_URL_META_NAME = 'dex-web-host-url';

/** Accepts only an http(s) URL or a rooted path, so a meta value can never run script. */
export function isSafeHostURL(value: string): boolean {
  if (value.startsWith('/') && !value.startsWith('//')) return true;
  try {
    const url = new URL(value);
    return url.protocol === 'https:' || url.protocol === 'http:';
  } catch {
    return false;
  }
}

/** Returns the host link the server injected, or null when Dex Web runs on its own. */
export function readHostLink(): HostLink | null {
  if (typeof document === 'undefined') return null;
  const name = document.querySelector(`meta[name="${HOST_NAME_META_NAME}"]`)?.getAttribute('content')?.trim() ?? '';
  const url = document.querySelector(`meta[name="${HOST_URL_META_NAME}"]`)?.getAttribute('content')?.trim() ?? '';
  if (!name || !url || !isSafeHostURL(url)) return null;
  return { name, url };
}
