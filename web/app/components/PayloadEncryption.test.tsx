// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { decodePendingInterrupts } from '../agents/sessionSchema';
import { stateWriteValueFields } from '../flows/details/StateWriteDialog';
import { streamEntryContentOf } from '../flows/details/StreamOutput';
import { escapeApplicationJSON, unescapeApplicationJSON } from '@/lib/payloadEncryption';
import { storedValueJSONReplacer } from '@/lib/blobs';
import type { CodecEndpoint } from '@/lib/payloadCodec';
import type { FlowSummary } from '@/lib/types';
import { EventValueBlock, InlineValue, StructuredValue } from './StructuredValue';

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

const envelope = 'CAESAmsxGgMBAgM=';

describe('encrypted value rendering', () => {
  it('renders a decrypted Value like plaintext with an open lock badge', () => {
    render(<EventValueBlock name="Step output" value={{ __dexDecryptedValue: { stringValue: 'shipped' } }} />);
    expect(screen.getByText('shipped')).toBeTruthy();
    expect(screen.getByRole('img', { name: 'Decrypted in this browser' })).toBeTruthy();
  });

  it('renders a decrypted JSON object structurally beside its badge', () => {
    const payload = btoa('{"orderId":"A-1"}');
    render(<StructuredValue value={{ __dexDecryptedValue: { objValue: { encoding: 'json', payload } } }} />);
    expect(screen.getByText('orderId')).toBeTruthy();
    expect(screen.getByText('A-1')).toBeTruthy();
    expect(screen.getByRole('img', { name: 'Decrypted in this browser' })).toBeTruthy();
  });

  it('shows a placeholder with the key ID and never the base64 envelope', () => {
    const { container } = render(
      <div>
        <InlineValue value={{ __dexEncryptedValue: { keyId: 'k1', problem: 'not_configured' } }} />
        <InlineValue value={{ __dexEnvelope: { encoding: 'dex/1', payload: envelope } }} />
      </div>,
    );
    expect(screen.getAllByText('Encrypted (key id: k1)')).toHaveLength(2);
    expect(screen.getAllByRole('img', { name: 'Encrypted' })).toHaveLength(2);
    expect(container.textContent).not.toContain(envelope);
  });
});

describe('application JSON that looks like a Dex Web marker', () => {
  it('renders an escaped envelope look-alike as the application fields, not as encrypted', () => {
    const lookalike = { __dexEscapedObject: { __dexEnvelope: { encoding: 'dex/1', payload: envelope } } };
    const { container } = render(
      <div>
        <StructuredValue value={lookalike} />
        <InlineValue value={lookalike} />
      </div>,
    );
    expect(screen.getAllByText('__dexEnvelope').length).toBeGreaterThan(0);
    expect(container.textContent).toContain(envelope);
    expect(container.textContent).not.toContain('Encrypted');
    expect(container.textContent).not.toContain('__dexEscapedObject');
    expect(screen.queryByRole('img')).toBeNull();
  });

  it('renders an escaped blob reference look-alike without loading anything', () => {
    const lookalike = { __dexEscapedObject: { __dexBlobReference: { id: 'b9', kind: 'object' } } };
    const { container } = render(<EventValueBlock name="Attribute" value={lookalike} />);
    expect(container.textContent).toContain('"__dexBlobReference"');
    expect(container.textContent).not.toContain('Loading stored value');
  });
});

describe('consumers that read application JSON', () => {
  it('reads Stream progress from an escaped value with __dex keys', () => {
    const progress = { percent: 40, message: 'Copying', __dexTrace: 'app data' };
    const escaped = escapeApplicationJSON(progress);
    expect(streamEntryContentOf(escaped)).toEqual({ ...streamEntryContentOf(progress), value: escaped });
    expect(streamEntryContentOf(escaped).kind).toBe('progress');
  });

  it('keeps a Stream progress value as received, so expand and Copy show nested look-alikes unchanged', () => {
    const original = {
      percent: 40,
      detail: { __dexEscapedObject: { foo: 1 } },
      extra: { __dexEnvelope: { encoding: 'dex/1', payload: envelope } },
    };
    const escaped = escapeApplicationJSON(original) as Record<string, unknown>;
    const content = streamEntryContentOf(escaped);
    expect(content.kind).toBe('progress');
    if (content.kind !== 'progress') return;
    expect(content.value).toBe(escaped);
    // Expand and Copy both serialize content.value with the stored value replacer.
    expect(JSON.stringify(content.value, storedValueJSONReplacer, 2)).toBe(JSON.stringify(original, null, 2));
  });

  it('reads pending interrupts from an escaped inbox value', () => {
    const interrupts = [{
      interrupt_id: 'c2', kind: 'tool_approval', tool_call_id: 'c2', tool_name: 'tool_c2', arguments: '{}', created_at: 'x',
      __dexTrace: 'app data',
    }];
    expect(decodePendingInterrupts(unescapeApplicationJSON(escapeApplicationJSON(interrupts))))
      .toEqual(decodePendingInterrupts(interrupts));
  });
});

describe('Attribute writes follow the stored form', () => {
  const summary = { flowId: 'f1', runId: 'r1', flowType: 'OrderFlow' } as FlowSummary;
  const codec: CodecEndpoint = { endpoint: 'http://127.0.0.1:1', includeCredentials: false, bearerToken: 't', source: 'local' };

  function fetcherWith(attributes: unknown[]) {
    return vi.fn(async (url: RequestInfo | URL) => {
      const body = String(url).includes('/encode') ? { envelopes: ['RU5W'] } : { attributes };
      return new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } });
    }) as unknown as typeof fetch;
  }

  it('writes a new Attribute in plaintext even with a codec', async () => {
    expect(await stateWriteValueFields('attribute', summary, 'note', 'string', 'hi', codec, fetcherWith([])))
      .toEqual({ valueType: 'string', value: 'hi' });
  });

  it('encrypts an update of an encrypted Attribute', async () => {
    const fetcher = fetcherWith([{ key: 'note', value: { __dexEnvelope: { encoding: 'dex/1', payload: envelope } } }]);
    expect(await stateWriteValueFields('attribute', summary, 'note', 'string', 'hi', codec, fetcher))
      .toEqual({ encodedValue: { objValue: { encoding: 'dex/1', payload: 'RU5W' } } });
  });

  it('keeps a plaintext Attribute plaintext', async () => {
    const fetcher = fetcherWith([{ key: 'note', value: 'old' }]);
    expect(await stateWriteValueFields('attribute', summary, 'note', 'string', 'hi', codec, fetcher))
      .toEqual({ valueType: 'string', value: 'hi' });
  });

  it('refuses to replace an encrypted Attribute with plaintext without a codec', async () => {
    const fetcher = fetcherWith([{ key: 'note', value: { __dexEnvelope: { encoding: 'dex/1', payload: envelope } } }]);
    await expect(stateWriteValueFields('attribute', summary, 'note', 'string', 'hi', null, fetcher))
      .rejects.toThrow('Attribute note is encrypted');
  });

  it('encrypts a Channel message whenever a codec is configured', async () => {
    expect(await stateWriteValueFields('channel', summary, 'approvals', 'bool', true, codec, fetcherWith([])))
      .toEqual({ encodedValue: { objValue: { encoding: 'dex/1', payload: 'RU5W' } } });
    expect(await stateWriteValueFields('channel', summary, 'approvals', 'bool', true, null, fetcherWith([])))
      .toEqual({ valueType: 'bool', value: true });
  });
});
