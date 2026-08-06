// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { describe, expect, it, vi } from 'vitest';
import {
  collectBlobReferences,
  hydrateBlobs,
  isStoredValueUnavailable,
  storedValueJSONReplacer,
} from './blobs';
import { VALUE_BLOB_UNAVAILABLE } from './unavailable';

function reference(id: string, kind: 'string' | 'object') {
  return { __dexBlobReference: { id, kind } };
}

describe('blob hydration', () => {
  it('finds deep references and deduplicates by kind and id', () => {
    const value = {
      stepInput: reference('input', 'object'),
      attributes: [{ value: reference('attribute', 'string') }],
      channels: {
        pending: {
          values: [reference('channel', 'object'), reference('channel', 'object')],
        },
      },
      continued: {
        stepsToResume: [{ conditionResults: reference('result', 'object') }],
      },
    };

    expect(collectBlobReferences(value)).toEqual([
      { id: 'input', kind: 'object' },
      { id: 'attribute', kind: 'string' },
      { id: 'channel', kind: 'object' },
      { id: 'result', kind: 'object' },
    ]);
  });

  it('batches replacements and reuses cached values', async () => {
    const fetcher = vi.fn(async (_input: RequestInfo | URL, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body)) as {
        values: Array<{ id: string; kind: string }>;
      };
      expect(body.values).toEqual([
        { id: 'input', kind: 'object' },
        { id: 'attribute', kind: 'string' },
      ]);
      return new Response(JSON.stringify({
        values: {
          'object:input': { order: 42 },
          'string:attribute': 'ready',
        },
      }), { status: 200 });
    });
    const cache = new Map<string, unknown>();
    const source = {
      stepInput: reference('input', 'object'),
      attributes: [{ value: reference('attribute', 'string') }],
      conditionResults: {
        channelResults: [{ values: [reference('input', 'object')] }],
      },
    };

    const first = await hydrateBlobs(source, cache, undefined, fetcher as typeof fetch);
    expect(first.value).toEqual({
      stepInput: { order: 42 },
      attributes: [{ value: 'ready' }],
      conditionResults: { channelResults: [{ values: [{ order: 42 }] }] },
    });
    await hydrateBlobs(source, cache, undefined, fetcher as typeof fetch);
    expect(fetcher).toHaveBeenCalledTimes(1);
  });

  it('keeps the page usable without exposing unavailable blob ids', async () => {
    const fetcher = vi.fn(async () => new Response(
      JSON.stringify({ error: 'blob secret-object-id was cleaned up' }),
      { status: 502 },
    ));
    const result = await hydrateBlobs(
      { stepInput: reference('secret-object-id', 'object') },
      new Map(),
      undefined,
      fetcher as typeof fetch,
    );

    expect(result.error).toBe(VALUE_BLOB_UNAVAILABLE);
    expect(result.error).not.toContain('secret-object-id');
    expect(isStoredValueUnavailable(result.value.stepInput)).toBe(true);
    const rendered = JSON.stringify(result.value, storedValueJSONReplacer);
    expect(rendered).toContain(VALUE_BLOB_UNAVAILABLE);
    expect(rendered).not.toContain('secret-object-id');
  });
});
