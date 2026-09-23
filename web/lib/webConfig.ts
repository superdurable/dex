// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

export type WorkQueuePermissionMode = 'local-selector' | 'trusted-header';

interface DexWebConfig {
  workQueuePermissionMode?: WorkQueuePermissionMode;
  basePath?: string;
  embedded?: boolean;
  csrfHeaderName?: string;
  csrfToken?: string;
}

declare global {
  interface Window {
    __DEX_WEB_CONFIG__?: DexWebConfig;
  }
}

export function workQueuePermissionMode(): WorkQueuePermissionMode {
  return readWebConfig().workQueuePermissionMode ?? 'local-selector';
}

export function webBasePath(): string {
  return readWebConfig().basePath ?? '/';
}

export function isEmbedded(): boolean {
  return readWebConfig().embedded === true;
}

export function webPath(rootedPath: string): string {
  if (!rootedPath.startsWith('/')) {
    throw new Error(`Dex Web path must start with /: ${rootedPath}`);
  }
  const basePath = webBasePath();
  return basePath === '/' ? rootedPath : `${basePath}${rootedPath}`;
}

export function dexFetch(rootedPath: string, init?: RequestInit): Promise<Response> {
  const config = readWebConfig();
  const headers = new Headers(init?.headers);
  if (isMutatingMethod(init?.method) && config.csrfHeaderName && config.csrfToken) {
    headers.set(config.csrfHeaderName, config.csrfToken);
  }
  return fetch(webPath(rootedPath), { ...init, headers });
}

export function definitionRevisionHeaders(revision: string): HeadersInit {
  return revision === '' ? {} : { 'X-Dex-Flow-Definition-Revision': revision };
}

function readWebConfig(): DexWebConfig {
  if (typeof window === 'undefined') return {};
  return window.__DEX_WEB_CONFIG__ ?? {};
}

function isMutatingMethod(method: string | undefined): boolean {
  const normalized = (method ?? 'GET').toUpperCase();
  return normalized !== 'GET' && normalized !== 'HEAD' && normalized !== 'OPTIONS';
}
