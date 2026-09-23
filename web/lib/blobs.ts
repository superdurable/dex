// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readResponseJSON } from './http';
import { VALUE_BLOB_UNAVAILABLE } from './unavailable';
import { dexFetch, webPath } from './webConfig';

export type BlobKind = 'string' | 'object';

export interface BlobReference {
  id: string;
  kind: BlobKind;
}

export interface BlobHydrationResult<T> {
  value: T;
  error?: string;
}

type BlobReferenceValue = {
  __dexBlobReference: BlobReference;
};

const unavailableValue = { __dexStoredValueUnavailable: true } as const;

export function blobCacheKey(flowId: string, reference: BlobReference): string {
  return `${flowId.length}:${flowId}${blobReferenceKey(reference)}`;
}

export function isBlobReferenceValue(value: unknown): value is BlobReferenceValue {
  if (!isRecord(value) || !isRecord(value.__dexBlobReference)) return false;
  const reference = value.__dexBlobReference;
  return typeof reference.id === 'string'
    && reference.id.length > 0
    && (reference.kind === 'string' || reference.kind === 'object');
}

export function isStoredValueUnavailable(value: unknown): boolean {
  return isRecord(value) && value.__dexStoredValueUnavailable === true;
}

export function storedValueJSONReplacer(_key: string, value: unknown): unknown {
  if (isBlobReferenceValue(value)) return 'Loading stored value…';
  if (isStoredValueUnavailable(value)) return VALUE_BLOB_UNAVAILABLE;
  return value;
}

export function collectBlobReferences(value: unknown): BlobReference[] {
  const references = new Map<string, BlobReference>();
  collect(value, references);
  return [...references.values()];
}

export async function hydrateBlobs<T>(
  flowId: string,
  value: T,
  cache: Map<string, unknown>,
  signal?: AbortSignal,
  fetcher?: typeof fetch,
): Promise<BlobHydrationResult<T>> {
  const references = collectBlobReferences(value);
  const missing = references.filter((reference) => !cache.has(blobCacheKey(flowId, reference)));
  if (missing.length === 0) {
    return { value: replaceBlobReferences(flowId, value, cache, false) as T };
  }
  try {
    const request: RequestInit = {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ flowId, values: missing }),
      cache: 'no-store',
      signal,
    };
    const response = fetcher
      ? await fetcher(webPath('/api/blobs/load'), request)
      : await dexFetch('/api/blobs/load', request);
    const result = await readResponseJSON<{ values?: Record<string, unknown> }>(response);
    for (const [key, resolved] of Object.entries(result.values ?? {})) {
      const separator = key.indexOf(':');
      if (separator < 0) continue;
      cache.set(blobCacheKey(flowId, {
        kind: key.slice(0, separator) as BlobKind,
        id: key.slice(separator + 1),
      }), resolved);
    }
    const unresolved = missing.some((reference) => !cache.has(blobCacheKey(flowId, reference)));
    return {
      value: replaceBlobReferences(flowId, value, cache, true) as T,
      ...(unresolved ? { error: VALUE_BLOB_UNAVAILABLE } : {}),
    };
  } catch (error) {
    if (error instanceof DOMException && error.name === 'AbortError') throw error;
    return {
      value: replaceBlobReferences(flowId, value, cache, true) as T,
      error: VALUE_BLOB_UNAVAILABLE,
    };
  }
}

function collect(value: unknown, references: Map<string, BlobReference>) {
  if (isBlobReferenceValue(value)) {
    const reference = value.__dexBlobReference;
    references.set(blobReferenceKey(reference), reference);
    return;
  }
  if (Array.isArray(value)) {
    value.forEach((entry) => collect(entry, references));
    return;
  }
  if (isRecord(value)) {
    Object.values(value).forEach((entry) => collect(entry, references));
  }
}

function replaceBlobReferences(
  flowId: string,
  value: unknown,
  cache: Map<string, unknown>,
  unavailableWhenMissing: boolean,
): unknown {
  if (isBlobReferenceValue(value)) {
    const key = blobCacheKey(flowId, value.__dexBlobReference);
    if (cache.has(key)) return cache.get(key);
    return unavailableWhenMissing ? unavailableValue : value;
  }
  if (Array.isArray(value)) {
    return value.map((entry) => replaceBlobReferences(flowId, entry, cache, unavailableWhenMissing));
  }
  if (isRecord(value)) {
    return Object.fromEntries(Object.entries(value).map(([key, entry]) => [
      key,
      replaceBlobReferences(flowId, entry, cache, unavailableWhenMissing),
    ]));
  }
  return value;
}

function blobReferenceKey(reference: BlobReference): string {
  return `${reference.kind}:${reference.id}`;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}
