// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { collectBlobReferences, storedValueJSONReplacer } from './blobs';
import {
  bytesToBase64,
  canonicalBase64Bytes,
  collectPayloadEnvelopes,
  containsEncryptedPayload,
  decryptedPayloadDisplayValue,
  encryptedPayloadDisplayValue,
  escapeApplicationJSON,
  markApplicationContent,
  unescapeApplicationJSON,
  MAX_ENCODED_TEXT_BYTES,
  payloadEnvelopeKeyID,
  replaceEncryptedPayloads,
  textToBase64,
  type PayloadDecodeLookup,
} from './payloadEncryption';

/** A PayloadEnvelope with cipher 1, the key ID, and three data bytes, in standard base64. */
function testEnvelope(keyId: string, data = [1, 2, 3]): string {
  const key = new TextEncoder().encode(keyId);
  return bytesToBase64(new Uint8Array([0x08, 0x01, 0x12, key.length, ...key, 0x1a, data.length, ...data]));
}

/** A stored Value the Dex Web server marked as encrypted. */
function marked(payload: string, encoding = 'dex/1') {
  return { __dexEnvelope: { encoding, payload } };
}

const forceFail = 4;

describe('payload envelope detection', () => {
  const envelope = testEnvelope('k1');

  it('reads the key ID in clear and rejects invalid key IDs', () => {
    expect(payloadEnvelopeKeyID(envelope)).toBe('k1');
    expect(payloadEnvelopeKeyID(testEnvelope('has space'))).toBe('');
    expect(payloadEnvelopeKeyID('not base64!')).toBe('');
    expect(payloadEnvelopeKeyID('A'.repeat(MAX_ENCODED_TEXT_BYTES + 4))).toBe('');
  });

  it('accepts only canonical padded base64', () => {
    expect(canonicalBase64Bytes('AAE=')).toEqual(new Uint8Array([0, 1]));
    expect(canonicalBase64Bytes('AAF=')).toBeNull();
    expect(canonicalBase64Bytes('AAE')).toBeNull();
    expect(canonicalBase64Bytes('AA E=')).toBeNull();
  });

  it('finds marked Values and prefix texts only at protocol read points', () => {
    const textEnvelope = testEnvelope('k2');
    const tree = {
      history: { value: marked(envelope) },
      failure: { errorMessage: `dex-encoded:${textEnvelope}`, detail: 'plain' },
      // An application string Value that starts with the prefix is data, never decoded.
      appString: { stringValue: `dex-encoded:${testEnvelope('k3')}` },
      forceFail: { closeDecisionType: forceFail, closeInput: { stringValue: `dex-encoded:${testEnvelope('k4')}` } },
      complete: { closeDecisionType: 2, closeInput: `dex-encoded:${testEnvelope('k5')}` },
    };
    expect(collectPayloadEnvelopes(tree).sort()).toEqual([envelope, textEnvelope, testEnvelope('k4')].sort());
    expect(containsEncryptedPayload({ value: 1 })).toBe(false);
    expect(containsEncryptedPayload(tree)).toBe(true);
  });

  it('never treats application JSON as an envelope or protocol text', () => {
    const lookalike = { encoding: 'dex/1', payload: envelope };
    const loaded = { reason: `dex-encoded:${envelope}`, nested: { objValue: { encoding: 'dex/1', payload: envelope } } };
    markApplicationContent(loaded);
    const tree = {
      attributes: [{ key: 'note', value: { detail: `dex-encoded:${envelope}`, envelope: lookalike } }],
      stepInput: loaded,
    };
    expect(collectPayloadEnvelopes(tree)).toEqual([]);
    expect(containsEncryptedPayload(tree)).toBe(false);
    expect(replaceEncryptedPayloads(tree, () => ({ problem: 'unknown_key' }))).toBe(tree);
    const stream = { reason: `dex-encoded:${envelope}` };
    expect(collectPayloadEnvelopes(stream, { textReadPoints: false })).toEqual([]);
  });
});

describe('escaped application JSON', () => {
  const envelopeLookalike = { __dexEnvelope: { encoding: 'dex/1', payload: testEnvelope('k1') } };
  const blobLookalike = { __dexBlobReference: { id: 'b9', kind: 'object' } };

  it('round-trips exactly and never reads as a marker', () => {
    for (const original of [envelopeLookalike, blobLookalike, { list: [envelopeLookalike], plain: 1 }]) {
      const escaped = escapeApplicationJSON(original);
      expect(unescapeApplicationJSON(escaped)).toEqual(original);
      expect(JSON.stringify(escaped, storedValueJSONReplacer)).toBe(JSON.stringify(original));
      const tree = { attributes: [{ key: 'note', value: escaped }], view: escaped, stepInput: escaped };
      expect(collectPayloadEnvelopes(tree)).toEqual([]);
      expect(containsEncryptedPayload(tree)).toBe(false);
      expect(replaceEncryptedPayloads(tree, () => ({ problem: 'unknown_key' }))).toBe(tree);
      expect(collectBlobReferences(tree)).toEqual([]);
    }
  });

  it('escapes look-alikes in plaintext that the browser decrypted', () => {
    const payload = textToBase64(JSON.stringify(envelopeLookalike));
    expect(decryptedPayloadDisplayValue({ objValue: { encoding: 'json', payload } }))
      .toEqual({ __dexEscapedObject: envelopeLookalike });
  });
});

describe('replaceEncryptedPayloads', () => {
  const envelope = testEnvelope('k1');
  const decoded: PayloadDecodeLookup = (candidate) => (
    candidate === envelope ? { value: { stringValue: 'hello' } } : undefined
  );

  it('replaces a decoded Value with a marker and keeps unchanged branches', () => {
    const untouched = { other: [1, 2] };
    const tree = { slot: marked(envelope), untouched };
    const replaced = replaceEncryptedPayloads(tree, decoded);
    expect(replaced.slot).toEqual({ __dexDecryptedValue: { stringValue: 'hello' } });
    expect(replaced.untouched).toBe(untouched);
    expect(encryptedPayloadDisplayValue(replaced.slot)).toBe('hello');
    const plain = { a: 1 };
    expect(replaceEncryptedPayloads(plain, decoded)).toBe(plain);
  });

  it('shows placeholders with the key ID, never the base64 payload', () => {
    const tree = [marked(testEnvelope('k9'))];
    const pending = replaceEncryptedPayloads(tree, () => undefined);
    expect(encryptedPayloadDisplayValue(pending[0])).toBe('Decrypting…');
    const failed = replaceEncryptedPayloads(tree, () => ({ problem: 'unknown_key' }));
    expect(failed[0]).toEqual({ __dexEncryptedValue: { keyId: 'k9', problem: 'unknown_key' } });
    expect(encryptedPayloadDisplayValue(failed[0])).toBe('Encrypted (key id: k9)');
    expect(JSON.stringify(failed)).not.toContain(testEnvelope('k9'));
  });

  it('treats other dex/ markers and oversized payloads as placeholders without decoding', () => {
    const tree = {
      future: marked('AAAA', 'dex/2'),
      huge: marked('A'.repeat(MAX_ENCODED_TEXT_BYTES + 4)),
    };
    expect(collectPayloadEnvelopes(tree)).toEqual([]);
    const replaced = replaceEncryptedPayloads(tree, decoded);
    expect(replaced.future).toEqual({ __dexEncryptedValue: { keyId: '', problem: 'unsupported_marker' } });
    expect(replaced.huge).toEqual({ __dexEncryptedValue: { keyId: '', problem: 'limit_exceeded' } });
  });

  it('decodes prefix texts in place and requires a string Value', () => {
    const textEnvelope = testEnvelope('t1');
    const lookup: PayloadDecodeLookup = (candidate) => (candidate === textEnvelope
      ? { value: { stringValue: 'boom' } }
      : { value: { intValue: '1' } });
    const replaced = replaceEncryptedPayloads({
      reason: `dex-encoded:${textEnvelope}`,
      detail: `dex-encoded:${testEnvelope('t2')}`,
      errorMessage: ' dex-encoded:kept with its space',
    }, lookup);
    expect(replaced).toEqual({
      reason: 'boom',
      detail: 'Encrypted (key id: t2)',
      errorMessage: ' dex-encoded:kept with its space',
    });
  });

  it('hides malformed and oversized prefix texts behind placeholders', () => {
    const tree = {
      stackTrace: 'dex-encoded:not-base64',
      detail: `dex-encoded:${'A'.repeat(MAX_ENCODED_TEXT_BYTES + 4)}`,
    };
    expect(collectPayloadEnvelopes(tree)).toEqual([]);
    expect(containsEncryptedPayload(tree)).toBe(true);
    expect(replaceEncryptedPayloads(tree, decoded)).toEqual({
      stackTrace: 'Encrypted (malformed)',
      detail: 'Encrypted (limit_exceeded)',
    });
  });

  it('decodes a ForceFail reason that blob loading unwrapped to a string', () => {
    const textEnvelope = testEnvelope('t3');
    const replaced = replaceEncryptedPayloads(
      { closeDecision: { closeDecisionType: forceFail, closeInput: `dex-encoded:${textEnvelope}` } },
      (candidate) => (candidate === textEnvelope ? { value: { stringValue: 'declined' } } : undefined),
    );
    expect(replaced.closeDecision.closeInput).toBe('declined');
  });

  it('renders decrypted kinds like plaintext', () => {
    expect(decryptedPayloadDisplayValue({ intValue: '42' })).toBe('42');
    expect(decryptedPayloadDisplayValue({ objValue: { encoding: 'json', payload: textToBase64('{"a":1}') } })).toEqual({ a: 1 });
    expect(decryptedPayloadDisplayValue({ objValue: { encoding: 'proto', payload: 'AAE=' } })).toEqual({ encoding: 'proto', byteSize: 2 });
    expect(decryptedPayloadDisplayValue({ nullValue: null })).toBeNull();
  });
});
