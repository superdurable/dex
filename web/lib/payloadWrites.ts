// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { webURL } from './basePath';
import { hydrateBlobs } from './blobs';
import { readResponseJSON } from './http';
import { isEncryptedStoredValue, textToBase64, type PayloadValueJSON } from './payloadEncryption';
import type { FlowState } from './types';

const int64Minimum = -(2n ** 63n);
const int64Maximum = 2n ** 63n - 1n;

function jsonObjectValue(jsonText: string): PayloadValueJSON {
  return { objValue: { encoding: 'json', payload: textToBase64(jsonText) } };
}

/**
 * The Value that Dex Web writes for a value typed into Set attribute or Publish channel message.
 *
 * It matches the Value Dex Web builds from valueType and value, so encrypting it in the browser stores the same
 * plaintext. int64 values are decimal strings.
 */
export function debugValueJSON(valueType: 'string' | 'int64' | 'double' | 'bool' | 'json', value: unknown): PayloadValueJSON {
  switch (valueType) {
    case 'string':
      return { stringValue: String(value) };
    case 'int64':
      return { intValue: String(value) };
    case 'double':
      return { doubleValue: value as number };
    case 'bool':
      return { boolValue: value as boolean };
    case 'json':
      return jsonObjectValue(JSON.stringify(value) ?? 'null');
  }
}

/**
 * The Value of one natural JSON text, by the rules Dex Web uses for a stop output: a string, a boolean, an integer
 * that fits int64, a double, or a JSON object Value for anything else.
 */
export function naturalJSONValue(text: string): PayloadValueJSON {
  const trimmed = text.trim();
  const parsed = JSON.parse(trimmed) as unknown;
  if (typeof parsed === 'string') return { stringValue: parsed };
  if (typeof parsed === 'boolean') return { boolValue: parsed };
  if (typeof parsed === 'number') {
    if (/^-?\d+$/.test(trimmed)) {
      const integer = BigInt(trimmed);
      if (integer >= int64Minimum && integer <= int64Maximum) return { intValue: integer.toString() };
    }
    return { doubleValue: parsed };
  }
  // The raw text keeps large integers inside objects exact.
  return jsonObjectValue(trimmed);
}

/**
 * The Start Step input Value for a schema kind and the input JSON the Start form built; null for a null input,
 * which is never encrypted.
 */
export function startInputValue(schemaKind: string, inputJSON: string): PayloadValueJSON | null {
  const trimmed = inputJSON.trim();
  const parsed = JSON.parse(trimmed) as unknown;
  if (parsed === null && schemaKind !== 'object' && schemaKind !== 'array' && schemaKind !== 'map') return null;
  switch (schemaKind) {
    case 'null':
      return null;
    case 'string':
      return { stringValue: parsed as string };
    case 'integer':
      return { intValue: trimmed };
    case 'number':
      return { doubleValue: parsed as number };
    case 'boolean':
      return { boolValue: parsed as boolean };
    default:
      return jsonObjectValue(trimmed);
  }
}

/** How an Attribute is stored now: absent, plaintext, or a dex/1 envelope. */
export type StoredAttributeForm = 'missing' | 'plaintext' | 'encrypted';

/**
 * Reads the current stored form of one Attribute, loading its blob when the value is stored as one.
 *
 * Writes follow this form: a new Attribute is written in plaintext, and an update keeps its current form.
 */
export async function readStoredAttributeForm(
  flowId: string,
  runId: string,
  key: string,
  fetcher: typeof fetch = fetch,
): Promise<StoredAttributeForm> {
  const state = await readResponseJSON<FlowState>(await fetcher(
    webURL(`/api/flows/state?flowId=${encodeURIComponent(flowId)}&runId=${encodeURIComponent(runId)}`),
    { cache: 'no-store' },
  ));
  const entry = state.attributes.find((attribute) => attribute.key === key);
  if (!entry || entry.value === null || entry.value === undefined) return 'missing';
  const hydrated = await hydrateBlobs(flowId, entry.value, new Map(), undefined, fetcher);
  if (hydrated.error) throw new Error(`The current value of Attribute ${key} could not be loaded`);
  return isEncryptedStoredValue(hydrated.value) ? 'encrypted' : 'plaintext';
}
