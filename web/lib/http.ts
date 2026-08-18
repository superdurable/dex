// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

export async function readResponseJSON<T>(response: Response): Promise<T> {
  const data = await parseResponseJSON<T & { error?: string }>(response);
  if (!response.ok) {
    throw new Error(data.error?.trim() || failedRequestMessage(response));
  }
  return data;
}

async function parseResponseJSON<T>(response: Response): Promise<T> {
  const body = await response.text();
  if (!body.trim()) {
    throw new Error(emptyResponseMessage(response));
  }
  try {
    return JSON.parse(body) as T;
  } catch {
    throw new Error(nonJSONResponseMessage(response, body));
  }
}

function emptyResponseMessage(response: Response): string {
  return `${responseDescription(response, 'an empty response')}. The Dex server may be unreachable.`;
}

function nonJSONResponseMessage(response: Response, body: string): string {
  const snippet = bodySnippet(body);
  const description = responseDescription(response, 'a non-JSON response');
  return snippet ? `${description}: ${snippet}` : `${description}.`;
}

function failedRequestMessage(response: Response): string {
  return responseDescription(response, 'an error');
}

function responseDescription(response: Response, kind: string): string {
  const path = requestPath(response);
  const status = `HTTP ${response.status}`;
  return path
    ? `Dex API returned ${kind} (${status}) for ${path}`
    : `Dex API returned ${kind} (${status})`;
}

function requestPath(response: Response): string {
  if (!response.url) return '';
  try {
    const url = new URL(response.url, 'http://127.0.0.1');
    return `${url.pathname}${url.search}`;
  } catch {
    return '';
  }
}

function bodySnippet(body: string): string {
  const trimmed = body.replace(/\s+/g, ' ').trim();
  if (!trimmed || trimmed.startsWith('<')) return '';
  return trimmed.length > 160 ? `${trimmed.slice(0, 157)}...` : trimmed;
}
