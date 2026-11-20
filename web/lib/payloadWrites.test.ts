// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it, vi } from 'vitest';
import { textToBase64 } from './payloadEncryption';
import { debugValueJSON, naturalJSONValue, readStoredAttributeForm, startInputValue } from './payloadWrites';

function jsonResponse(body: unknown): Response {
  return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
}

describe('plaintext Values the browser encrypts', () => {
  it('matches the Values Dex Web builds for typed debug values', () => {
    expect(debugValueJSON('string', 'hi')).toEqual({ stringValue: 'hi' });
    expect(debugValueJSON('int64', '9007199254740993')).toEqual({ intValue: '9007199254740993' });
    expect(debugValueJSON('double', 1.5)).toEqual({ doubleValue: 1.5 });
    expect(debugValueJSON('bool', false)).toEqual({ boolValue: false });
    expect(debugValueJSON('json', { a: 1 })).toEqual({ objValue: { encoding: 'json', payload: textToBase64('{"a":1}') } });
  });

  it('maps a stop output like Dex Web, keeping large integers exact', () => {
    expect(naturalJSONValue(' 42 ')).toEqual({ intValue: '42' });
    expect(naturalJSONValue('4.5')).toEqual({ doubleValue: 4.5 });
    expect(naturalJSONValue('99999999999999999999')).toEqual({ doubleValue: 1e20 });
    expect(naturalJSONValue('"done"')).toEqual({ stringValue: 'done' });
    expect(naturalJSONValue('{"id": 9007199254740993}'))
      .toEqual({ objValue: { encoding: 'json', payload: textToBase64('{"id": 9007199254740993}') } });
  });

  it('maps a Start input by its schema kind and leaves null plaintext', () => {
    expect(startInputValue('string', '"a"')).toEqual({ stringValue: 'a' });
    expect(startInputValue('integer', '7')).toEqual({ intValue: '7' });
    expect(startInputValue('string', 'null')).toBeNull();
    expect(startInputValue('null', 'null')).toBeNull();
    expect(startInputValue('object', '{"a":1}')).toEqual({ objValue: { encoding: 'json', payload: textToBase64('{"a":1}') } });
  });
});

describe('readStoredAttributeForm', () => {
  it('reports missing, plaintext, and encrypted Attributes, loading blobs first', async () => {
    const fetcher = vi.fn(async (url: RequestInfo | URL) => {
      if (String(url).includes('/api/flows/state')) {
        return jsonResponse({
          attributes: [
            { key: 'plain', value: 'text' },
            { key: 'inline', value: { __dexEnvelope: { encoding: 'dex/1', payload: 'AAAA' } } },
            { key: 'blob', value: { __dexBlobReference: { id: 'b1', kind: 'object' } } },
          ],
        });
      }
      return jsonResponse({ values: { 'object:b1': { __dexEnvelope: { encoding: 'dex/1', payload: 'BBBB' } } } });
    });
    const read = (key: string) => readStoredAttributeForm('f1', 'r1', key, fetcher as typeof fetch);
    expect(await read('absent')).toBe('missing');
    expect(await read('plain')).toBe('plaintext');
    expect(await read('inline')).toBe('encrypted');
    expect(await read('blob')).toBe('encrypted');
  });
});
