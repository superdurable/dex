// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it, vi } from 'vitest';
import {
  chunkCodecEntries,
  encodeReasonText,
  parseCodecEndpointURL,
  payloadCodecErrorMessage,
  PayloadDecodeStore,
  readableErrorMessage,
  readCodecBearerTokenRecord,
  readCodecEndpointPreference,
  resolveCodecEndpoint,
  saveCodecBearerTokenRecord,
  saveCodecEndpointPreference,
  type CodecEndpoint,
} from './payloadCodec';
import { replaceEncryptedPayloads } from './payloadEncryption';
import { DexAPIError } from './http';

const local = { configured: true, endpoint: 'http://127.0.0.1:53124', token: 'secret-token' };
const localCodec: CodecEndpoint = {
  endpoint: 'http://127.0.0.1:53124', includeCredentials: false, bearerToken: 'secret-token', source: 'local',
};
const context = { flowId: 'order-1', flowType: 'OrderFlow' };

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

async function settle() {
  await new Promise((resolve) => setTimeout(resolve, 0));
  await new Promise((resolve) => setTimeout(resolve, 0));
}

describe('codec endpoint settings', () => {
  it('uses the local dev codec server in auto mode and sends its token only there', () => {
    const auto = { mode: 'auto' as const, endpoint: '', includeCredentials: true, rememberBearerToken: true };
    expect(resolveCodecEndpoint(auto, local)).toEqual(localCodec);
    expect(resolveCodecEndpoint(auto, { configured: false, endpoint: '', token: '' })).toBeNull();
    expect(resolveCodecEndpoint({ ...auto, mode: 'off' }, local)).toBeNull();
    expect(resolveCodecEndpoint({ ...auto, mode: 'custom', endpoint: 'https://codec.example.com/' }, local))
      .toEqual({ endpoint: 'https://codec.example.com', includeCredentials: true, bearerToken: '', source: 'custom' });
  });

  it('accepts https, and http only for loopback hosts', () => {
    expect(parseCodecEndpointURL('https://codec.example.com/dex/')).toEqual({ endpoint: 'https://codec.example.com/dex' });
    expect(parseCodecEndpointURL('http://localhost:8080')).toEqual({ endpoint: 'http://localhost:8080' });
    expect(parseCodecEndpointURL('http://codec.example.com')).toHaveProperty('error');
    expect(parseCodecEndpointURL('https://user:pass@codec.example.com')).toHaveProperty('error');
  });

  it('saves only the mode, URL, credentials, and remember choices, never a token', () => {
    const storage = new Map<string, string>();
    const fake = { getItem: (key: string) => storage.get(key) ?? null, setItem: (key: string, value: string) => storage.set(key, value) };
    saveCodecEndpointPreference(fake, {
      mode: 'custom', endpoint: 'https://codec.example.com', includeCredentials: false, rememberBearerToken: false,
    });
    expect(JSON.parse(storage.get('dex-web-codec-endpoint') ?? '')).toEqual({
      mode: 'custom', endpoint: 'https://codec.example.com', includeCredentials: false, rememberBearerToken: false,
    });
    expect(readCodecEndpointPreference(fake)).toMatchObject({ mode: 'custom', rememberBearerToken: false });
    expect(readCodecEndpointPreference(undefined).mode).toBe('auto');
  });

  it('remembers the bearer token when an older saved preference has no remember field', () => {
    const saved = JSON.stringify({ mode: 'custom', endpoint: 'https://codec.example.com', includeCredentials: false });
    expect(readCodecEndpointPreference({ getItem: () => saved }).rememberBearerToken).toBe(true);
    expect(readCodecEndpointPreference(undefined).rememberBearerToken).toBe(true);
  });

  it('starts a new Custom configuration without cookies and keeps a saved choice', () => {
    expect(readCodecEndpointPreference(undefined).includeCredentials).toBe(false);
    const autoBefore = JSON.stringify({ mode: 'auto', endpoint: '', includeCredentials: true });
    expect(readCodecEndpointPreference({ getItem: () => autoBefore }).includeCredentials).toBe(false);
    const savedCustom = JSON.stringify({ mode: 'custom', endpoint: 'https://codec.example.com', includeCredentials: true });
    expect(readCodecEndpointPreference({ getItem: () => savedCustom }).includeCredentials).toBe(true);
  });

  it('uses the saved bearer token only in custom mode and only for its own URL', () => {
    const custom = { mode: 'custom' as const, endpoint: 'https://codec.example.com/', includeCredentials: false, rememberBearerToken: true };
    const saved = { endpoint: 'https://codec.example.com', token: 'custom-token' };
    expect(resolveCodecEndpoint(custom, local, saved)?.bearerToken).toBe('custom-token');
    expect(resolveCodecEndpoint({ ...custom, endpoint: 'https://other.example.com' }, local, saved)?.bearerToken).toBe('');
    expect(resolveCodecEndpoint({ ...custom, mode: 'auto' }, local, saved)?.bearerToken).toBe('secret-token');
    expect(resolveCodecEndpoint({ ...custom, mode: 'off' }, local, saved)).toBeNull();
  });
});

describe('bearer token storage', () => {
  const tokenKey = 'dex-web-codec-bearer-token';

  function fakeStorage() {
    const entries = new Map<string, string>();
    return {
      entries,
      getItem: (key: string) => entries.get(key) ?? null,
      setItem: (key: string, value: string) => { entries.set(key, value); },
      removeItem: (key: string) => { entries.delete(key); },
    };
  }

  const record = { endpoint: 'https://codec.example.com', token: 'custom-token' };
  const stored = (endpoint: string, token: string) => JSON.stringify({ endpoint, token });

  it('keeps a remembered token record in local storage only', () => {
    const storages = { local: fakeStorage(), session: fakeStorage() };
    saveCodecBearerTokenRecord(storages, record, true);
    expect(JSON.parse(storages.local.entries.get(tokenKey) ?? '')).toEqual(record);
    expect(storages.session.entries.has(tokenKey)).toBe(false);
    expect(readCodecBearerTokenRecord(storages, true)).toEqual(record);
  });

  it('keeps a tab-only token record in session storage only', () => {
    const storages = { local: fakeStorage(), session: fakeStorage() };
    saveCodecBearerTokenRecord(storages, record, false);
    expect(JSON.parse(storages.session.entries.get(tokenKey) ?? '')).toEqual(record);
    expect(storages.local.entries.has(tokenKey)).toBe(false);
    expect(readCodecBearerTokenRecord(storages, false)).toEqual(record);
  });

  it('moves the record when the remember choice changes', () => {
    const storages = { local: fakeStorage(), session: fakeStorage() };
    saveCodecBearerTokenRecord(storages, record, false);
    saveCodecBearerTokenRecord(storages, record, true);
    expect(storages.local.entries.has(tokenKey)).toBe(true);
    expect(storages.session.entries.has(tokenKey)).toBe(false);
    saveCodecBearerTokenRecord(storages, record, false);
    expect(storages.session.entries.has(tokenKey)).toBe(true);
    expect(storages.local.entries.has(tokenKey)).toBe(false);
  });

  it('reads the other storage when the preferred one has no record', () => {
    const storages = { local: fakeStorage(), session: fakeStorage() };
    storages.session.entries.set(tokenKey, stored('https://a.example.com', 'tab-token'));
    expect(readCodecBearerTokenRecord(storages, true)?.token).toBe('tab-token');
    storages.local.entries.set(tokenKey, stored('https://a.example.com', 'browser-token'));
    expect(readCodecBearerTokenRecord(storages, true)?.token).toBe('browser-token');
    expect(readCodecBearerTokenRecord(storages, false)?.token).toBe('tab-token');
  });

  it('removes the record from both storages when deleted', () => {
    const storages = { local: fakeStorage(), session: fakeStorage() };
    storages.local.entries.set(tokenKey, stored('https://a.example.com', 'browser-token'));
    storages.session.entries.set(tokenKey, stored('https://a.example.com', 'tab-token'));
    saveCodecBearerTokenRecord(storages, null, true);
    expect(storages.local.entries.has(tokenKey)).toBe(false);
    expect(storages.session.entries.has(tokenKey)).toBe(false);
  });

  it('ignores an unreadable record and survives missing or throwing storage', () => {
    const storages = { local: fakeStorage(), session: fakeStorage() };
    storages.local.entries.set(tokenKey, 'not-json');
    expect(readCodecBearerTokenRecord(storages, true)).toBeNull();
    const throwing = {
      getItem: () => { throw new Error('blocked'); },
      setItem: () => { throw new Error('blocked'); },
      removeItem: () => { throw new Error('blocked'); },
    };
    expect(readCodecBearerTokenRecord({ local: undefined, session: undefined }, true)).toBeNull();
    expect(readCodecBearerTokenRecord({ local: throwing, session: throwing }, true)).toBeNull();
    expect(() => saveCodecBearerTokenRecord({ local: throwing, session: throwing }, record, true)).not.toThrow();
  });
});

describe('codec request headers', () => {
  const customPreference = {
    mode: 'custom' as const, endpoint: 'https://codec.example.com', includeCredentials: false, rememberBearerToken: true,
  };
  const savedToken = { endpoint: 'https://codec.example.com', token: 'custom-token' };

  async function decodeWith(codec: CodecEndpoint | null) {
    const fetcher = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => (
      jsonResponse(200, { values: [{ stringValue: 'plain' }] })
    ));
    const store = new PayloadDecodeStore(codec, fetcher as typeof fetch);
    store.request(context, ['AAAA']);
    await settle();
    const init = fetcher.mock.calls[0][1];
    return { headers: init?.headers as Record<string, string>, credentials: init?.credentials, redirect: init?.redirect };
  }

  it('refuses redirects, so a token cannot follow one', async () => {
    expect((await decodeWith(resolveCodecEndpoint(customPreference, null, savedToken))).redirect).toBe('error');
    const failing = vi.fn(async () => { throw new TypeError('redirect'); });
    const failure = await encodeReasonText(resolveCodecEndpoint(customPreference, null, savedToken), context, 'why', failing as typeof fetch)
      .catch((error: unknown) => error);
    expect(failure).toMatchObject({ category: 'unavailable' });
  });

  it('keeps a 401 unauthenticated whatever category its body names', async () => {
    const fetcher = vi.fn(async () => jsonResponse(401, { error: { category: 'forbidden', message: 'no' } }));
    const codec = resolveCodecEndpoint(customPreference, null, savedToken);
    const encodeFailure = await encodeReasonText(codec, context, 'why', fetcher as typeof fetch).catch((error: unknown) => error);
    expect(encodeFailure).toMatchObject({ category: 'unauthenticated' });
    expect((encodeFailure as Error).message).toContain('bearer token');
    const store = new PayloadDecodeStore(codec, fetcher as typeof fetch);
    store.request(context, ['AAAA']);
    await settle();
    expect(store.failure()).toBe('unauthenticated');
  });

  it('sends the custom bearer token to /decode and /encode', async () => {
    const codec = resolveCodecEndpoint(customPreference, null, savedToken);
    expect((await decodeWith(codec)).headers.Authorization).toBe('Bearer custom-token');
    const fetcher = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => jsonResponse(200, { envelopes: ['RU5W'] }));
    await encodeReasonText(codec, context, 'why', fetcher as typeof fetch);
    expect(fetcher.mock.calls[0][0]).toBe('https://codec.example.com/encode');
    expect((fetcher.mock.calls[0][1]?.headers as Record<string, string>).Authorization).toBe('Bearer custom-token');
  });

  it('sends no Authorization header without a token or after the token is cleared', async () => {
    expect((await decodeWith(resolveCodecEndpoint(customPreference, null))).headers).not.toHaveProperty('Authorization');
    expect((await decodeWith(resolveCodecEndpoint(customPreference, null, null))).headers).not.toHaveProperty('Authorization');
  });

  it('sends the token together with cookies when Send cookies is selected', async () => {
    const withCookies = await decodeWith(resolveCodecEndpoint({ ...customPreference, includeCredentials: true }, null, savedToken));
    expect(withCookies.headers.Authorization).toBe('Bearer custom-token');
    expect(withCookies.credentials).toBe('include');
    const withoutCookies = await decodeWith(resolveCodecEndpoint(customPreference, null, savedToken));
    expect(withoutCookies.credentials).toBe('omit');
  });

  it('names the bearer token in a custom 401 message without echoing it', () => {
    const missing = payloadCodecErrorMessage('unauthenticated', resolveCodecEndpoint(customPreference, null));
    expect(missing).toContain('bearer token');
    const wrong = payloadCodecErrorMessage('unauthenticated', resolveCodecEndpoint(customPreference, null, savedToken));
    expect(wrong).toContain('bearer token');
    expect(wrong).not.toContain('custom-token');
    expect(payloadCodecErrorMessage('unauthenticated', localCodec)).not.toContain('bearer token');
  });

  it('names the bearer token when /encode answers 401, without echoing it', async () => {
    const fetcher = vi.fn(async () => jsonResponse(401, { error: { category: 'unauthenticated', message: 'no' } }));
    const codec = resolveCodecEndpoint(customPreference, null, savedToken);
    const failure = await encodeReasonText(codec, context, 'why', fetcher as typeof fetch).catch((error: unknown) => error);
    expect(failure).toBeInstanceOf(Error);
    expect((failure as Error).message).toContain('bearer token');
    expect((failure as Error).message).not.toContain('custom-token');
  });
});

describe('PayloadDecodeStore', () => {
  it('batches envelopes of one Flow into one /decode request with the bearer token', async () => {
    const fetcher = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
      const body = JSON.parse(String(init?.body)) as { envelopes: string[] };
      return jsonResponse(200, { values: body.envelopes.map((envelope) => ({ stringValue: `plain-${envelope}` })) });
    });
    const store = new PayloadDecodeStore(localCodec, fetcher as typeof fetch);
    store.request(context, ['AAAA', 'BBBB']);
    store.request(context, ['BBBB', 'CCCC']);
    await settle();
    expect(fetcher).toHaveBeenCalledTimes(1);
    const [url, init] = fetcher.mock.calls[0];
    expect(url).toBe('http://127.0.0.1:53124/decode');
    expect(init?.credentials).toBe('omit');
    expect((init?.headers as Record<string, string>).Authorization).toBe('Bearer secret-token');
    expect(JSON.parse(String(init?.body))).toEqual({
      context: { flow_type: 'OrderFlow', flow_id: 'order-1' }, envelopes: ['AAAA', 'BBBB', 'CCCC'],
    });
    expect(store.lookup(context)('CCCC')).toEqual({ value: { stringValue: 'plain-CCCC' } });
  });

  it('isolates the failing entry of a 422 and decodes the rest', async () => {
    const fetcher = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
      const envelopes = (JSON.parse(String(init?.body)) as { envelopes: string[] }).envelopes;
      const badIndex = envelopes.indexOf('BAD=');
      if (badIndex >= 0) {
        return jsonResponse(422, { error: { category: 'unknown_key', message: 'key id k9 is unknown', index: badIndex } });
      }
      return jsonResponse(200, { values: envelopes.map(() => ({ boolValue: true })) });
    });
    const store = new PayloadDecodeStore(localCodec, fetcher as typeof fetch);
    store.request(context, ['GOOD', 'BAD=', 'MORE']);
    await settle();
    const lookup = store.lookup(context);
    expect(lookup('BAD=')).toEqual({ problem: 'unknown_key' });
    expect(lookup('GOOD')).toEqual({ value: { boolValue: true } });
    expect(lookup('MORE')).toEqual({ value: { boolValue: true } });
    expect(store.failure()).toBeNull();
  });

  it('keeps isolating failing entries until the rest decode, even past eight failures', async () => {
    const bad = Array.from({ length: 9 }, (_, index) => `BAD${index}`);
    const fetcher = vi.fn(async (_url: RequestInfo | URL, init?: RequestInit) => {
      const envelopes = (JSON.parse(String(init?.body)) as { envelopes: string[] }).envelopes;
      const badIndex = envelopes.findIndex((envelope) => envelope.startsWith('BAD'));
      if (badIndex >= 0) {
        return jsonResponse(422, { error: { category: 'authentication_failed', message: 'no', index: badIndex } });
      }
      return jsonResponse(200, { values: envelopes.map(() => ({ stringValue: 'ok' })) });
    });
    const store = new PayloadDecodeStore(localCodec, fetcher as typeof fetch);
    store.request(context, [...bad, 'GOOD']);
    await vi.waitFor(() => expect(store.lookup(context)('GOOD')).toEqual({ value: { stringValue: 'ok' } }));
    expect(bad.map((envelope) => store.lookup(context)(envelope)))
      .toEqual(bad.map(() => ({ problem: 'authentication_failed' })));
    expect(fetcher).toHaveBeenCalledTimes(10);
  });

  it('marks every entry unauthenticated after a 401 and reports the failure', async () => {
    const fetcher = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => (
      jsonResponse(401, { error: { category: 'unauthenticated', message: 'no' } })
    ));
    const store = new PayloadDecodeStore({ ...localCodec, bearerToken: '', includeCredentials: true, source: 'custom' }, fetcher as typeof fetch);
    store.request(context, ['AAAA']);
    await settle();
    expect(fetcher.mock.calls[0][1]?.credentials).toBe('include');
    expect(store.lookup(context)('AAAA')).toEqual({ problem: 'unauthenticated' });
    expect(store.failure()).toBe('unauthenticated');
  });

  it('decrypts a whole tree once every envelope has an outcome', async () => {
    const fetcher = vi.fn(async () => jsonResponse(200, { values: [{ stringValue: 'secret' }] }));
    const store = new PayloadDecodeStore(localCodec, fetcher as typeof fetch);
    const tree = { value: { __dexEnvelope: { encoding: 'dex/1', payload: 'AAAA' } } };
    expect(await store.decryptTree(context, tree)).toEqual({ value: { __dexDecryptedValue: { stringValue: 'secret' } } });
    const unconfigured = new PayloadDecodeStore(null, fetcher as typeof fetch);
    expect(replaceEncryptedPayloads(tree, unconfigured.lookup(context)).value)
      .toEqual({ __dexEncryptedValue: { keyId: '', problem: 'not_configured' } });
  });
});

describe('encodeReasonText', () => {
  it('escapes a prefixed reason without a codec and leaves other text unchanged', async () => {
    expect(await encodeReasonText(null, context, 'dex-encoded:typed by a person')).toBe(' dex-encoded:typed by a person');
    expect(await encodeReasonText(null, context, 'plain')).toBe('plain');
    expect(await encodeReasonText(localCodec, context, '')).toBe('');
  });

  it('encodes the reason as a string Value with a codec', async () => {
    const fetcher = vi.fn(async () => jsonResponse(200, { envelopes: ['RU5W'] }));
    expect(await encodeReasonText(localCodec, context, 'manual fix', fetcher as typeof fetch)).toBe('dex-encoded:RU5W');
    const [url, init] = fetcher.mock.calls[0] as unknown as [string, RequestInit];
    expect(url).toBe('http://127.0.0.1:53124/encode');
    expect(JSON.parse(String(init.body))).toEqual({
      context: { flow_type: 'OrderFlow', flow_id: 'order-1' }, values: [{ stringValue: 'manual fix' }],
    });
  });
});

describe('readableErrorMessage', () => {
  it('decodes an encrypted gRPC status message that Dex Web passed through', async () => {
    const fetcher = vi.fn(async () => jsonResponse(200, { values: [{ stringValue: 'card declined' }] }));
    const error = new DexAPIError('The request failed with an encrypted error message.', 409, 9, undefined, 'dex-encoded:AAAA');
    expect(await readableErrorMessage(error, localCodec, context, 'fallback', fetcher as typeof fetch)).toBe('card declined');
    expect(await readableErrorMessage(error, null, context, 'fallback')).toBe(error.message);
    expect(await readableErrorMessage(new DexAPIError('plain', 409), localCodec, context, 'fallback')).toBe('plain');
  });
});

describe('chunkCodecEntries', () => {
  it('limits entries per request', () => {
    const chunks = chunkCodecEntries(Array.from({ length: 1200 }, (_, index) => index), () => 1);
    expect(chunks.map((chunk) => chunk.length)).toEqual([500, 500, 200]);
  });
});
