// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/**
 * Carries the gRPC code so a caller can tell one unreadable run apart from an
 * unreachable server. The code is absent, never 0, when the body omitted it.
 */
export class DexAPIError extends Error {
  readonly httpStatus: number;
  readonly grpcCode: number | undefined;
  readonly code: string | undefined;

  constructor(message: string, httpStatus: number, grpcCode?: number, code?: string) {
    super(message);
    this.name = 'DexAPIError';
    this.httpStatus = httpStatus;
    this.grpcCode = grpcCode;
    this.code = code;
    Object.setPrototypeOf(this, DexAPIError.prototype);
  }
}

export async function readResponseJSON<T>(response: Response): Promise<T> {
  const data = await parseResponseJSON<T & { error?: string; grpcCode?: number; code?: string }>(response);
  if (!response.ok) {
    throw new DexAPIError(
      data.error?.trim() || failedRequestMessage(response),
      response.status,
      typeof data.grpcCode === 'number' ? data.grpcCode : undefined,
      data.code,
    );
  }
  return data;
}

const transientGatewayStatusCodes = new Set([502, 503, 504]);

export function isTransientGatewayResponse(response: Response): boolean {
  return transientGatewayStatusCodes.has(response.status);
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
