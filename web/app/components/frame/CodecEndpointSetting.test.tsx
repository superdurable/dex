// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { useEffect } from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PayloadCodecProvider, usePayloadCodec } from '../../payloadCodecContext';
import { CodecEndpointSetting } from './CodecEndpointSetting';

const token = 'tok-3f9a2c-secret';
const tokenKey = 'dex-web-codec-bearer-token';
const preferenceKey = 'dex-web-codec-endpoint';

function jsonResponse(status: number, body: unknown): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } });
}

/** Asks the codec endpoint in use to decode one envelope, as a page with an encrypted value does. */
function DecodeProbe() {
  const { store } = usePayloadCodec();
  useEffect(() => {
    store.request({ flowId: 'order-1', flowType: 'OrderFlow' }, ['AAAA']);
  }, [store]);
  return null;
}

// Some Node versions shadow the jsdom storage, so each test installs its own.
function memoryStorage(): Storage {
  const entries = new Map<string, string>();
  return {
    get length() { return entries.size; },
    clear: () => entries.clear(),
    getItem: (key) => entries.get(key) ?? null,
    key: (index) => [...entries.keys()][index] ?? null,
    removeItem: (key) => { entries.delete(key); },
    setItem: (key, value) => { entries.set(key, String(value)); },
  };
}

let codecStatus = 200;
let fetchMock: ReturnType<typeof vi.fn>;

beforeEach(() => {
  codecStatus = 200;
  vi.stubGlobal('localStorage', memoryStorage());
  vi.stubGlobal('sessionStorage', memoryStorage());
  window.localStorage.setItem(preferenceKey, JSON.stringify({
    mode: 'custom', endpoint: 'http://127.0.0.1:53124', includeCredentials: false,
  }));
  fetchMock = vi.fn(async (url: RequestInfo | URL) => {
    if (String(url).endsWith('/api/codec-server')) return jsonResponse(200, { configured: false, endpoint: '', token: '' });
    if (codecStatus !== 200) return jsonResponse(codecStatus, { error: { category: 'unauthenticated', message: 'no' } });
    return jsonResponse(200, { values: [{ stringValue: 'plain' }] });
  });
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

function renderSetting() {
  return render(
    <PayloadCodecProvider>
      <CodecEndpointSetting />
      <DecodeProbe />
    </PayloadCodecProvider>,
  );
}

function codecRequests() {
  return fetchMock.mock.calls.filter(([url]) => String(url).endsWith('/decode'));
}

/** Waits until the local codec server lookup and the decode request after it have settled. */
async function settledDecodeHeaders(): Promise<Record<string, string>> {
  await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith('/api/codec-server'))).toBe(true));
  await waitFor(() => expect(codecRequests().length).toBeGreaterThan(0));
  await new Promise((resolve) => setTimeout(resolve, 10));
  return (codecRequests().at(-1)?.[1] as RequestInit).headers as Record<string, string>;
}

function saveToken(value: string) {
  fireEvent.change(screen.getByLabelText('Codec endpoint bearer token'), { target: { value } });
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
}

describe('CodecEndpointSetting bearer token', () => {
  it('keeps the token in session storage only and out of the page', async () => {
    const { container } = renderSetting();
    saveToken(token);

    expect(window.sessionStorage.getItem(tokenKey)).toBe(token);
    expect(window.localStorage.getItem(preferenceKey)).not.toContain(token);
    expect(JSON.parse(window.localStorage.getItem(preferenceKey) ?? '{}')).toEqual({
      mode: 'custom', endpoint: 'http://127.0.0.1:53124', includeCredentials: false,
    });
    expect(screen.getByText('Bearer token saved for this tab.')).toBeTruthy();
    expect((screen.getByLabelText('Codec endpoint bearer token') as HTMLInputElement).value).toBe('');
    expect(container.innerHTML).not.toContain(token);
    await waitFor(() => expect(codecRequests().length).toBeGreaterThan(0));
    const [, init] = codecRequests().at(-1) as [string, RequestInit];
    expect((init.headers as Record<string, string>).Authorization).toBe(`Bearer ${token}`);
  });

  it('reads the saved token after a reload of the same tab', async () => {
    window.sessionStorage.setItem(tokenKey, token);
    const { container } = renderSetting();
    expect(screen.getByText('Bearer token saved for this tab.')).toBeTruthy();
    expect(container.innerHTML).not.toContain(token);
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
  });

  it('removes the token with Clear token and stops sending it', async () => {
    window.sessionStorage.setItem(tokenKey, token);
    renderSetting();
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
    const requestsBeforeClear = codecRequests().length;
    fireEvent.click(screen.getByRole('button', { name: 'Clear token' }));

    expect(window.sessionStorage.getItem(tokenKey)).toBeNull();
    expect(screen.queryByText('Bearer token saved for this tab.')).toBeNull();
    await waitFor(() => expect(codecRequests().length).toBeGreaterThan(requestsBeforeClear));
    expect((codecRequests().at(-1)?.[1] as RequestInit).headers).not.toHaveProperty('Authorization');
  });

  it('sends no Authorization header without a token', async () => {
    renderSetting();
    expect(await settledDecodeHeaders()).not.toHaveProperty('Authorization');
    expect(codecRequests().every(([, init]) => !('Authorization' in ((init as RequestInit).headers as object)))).toBe(true);
    expect(window.sessionStorage.getItem(tokenKey)).toBeNull();
  });

  it('sends the token together with cookies when Send cookies is selected', async () => {
    renderSetting();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Send cookies (SSO proxy)' }));
    saveToken(token);
    await waitFor(() => expect(codecRequests().some(([, init]) => (
      (init as RequestInit).credentials === 'include'
      && ((init as RequestInit).headers as Record<string, string>).Authorization === `Bearer ${token}`
    ))).toBe(true));
  });

  it('names the bearer token when the codec endpoint answers 401, without echoing it', async () => {
    codecStatus = 401;
    window.sessionStorage.setItem(tokenKey, token);
    const { container } = renderSetting();
    const message = await screen.findByRole('status');
    expect(message.textContent).toContain('bearer token is likely wrong');
    expect(container.innerHTML).not.toContain(token);

    fireEvent.click(screen.getByRole('button', { name: 'Clear token' }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('likely needs a bearer token'));
  });
});
