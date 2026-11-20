// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { useEffect } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { PayloadCodecProvider, usePayloadCodec } from '../../payloadCodecContext';
import { CodecEndpointSetting } from './CodecEndpointSetting';

const token = 'tok-3f9a2c-secret';
const tokenKey = 'dex-web-codec-bearer-token';
const preferenceKey = 'dex-web-codec-endpoint';
const endpointA = 'http://127.0.0.1:53124';
const endpointB = 'http://127.0.0.1:53125';

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

function savePreference(preference: Record<string, unknown>) {
  window.localStorage.setItem(preferenceKey, JSON.stringify(preference));
}

function tokenRecord(endpoint: string) {
  return JSON.stringify({ endpoint, token });
}

beforeEach(() => {
  codecStatus = 200;
  vi.stubGlobal('localStorage', memoryStorage());
  vi.stubGlobal('sessionStorage', memoryStorage());
  // An older saved preference without rememberBearerToken.
  savePreference({ mode: 'custom', endpoint: endpointA, includeCredentials: false });
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

function lastRequest(): RequestInit {
  return codecRequests().at(-1)?.[1] as RequestInit;
}

/** Waits until the local codec server lookup and the decode request after it have settled. */
async function settledDecodeHeaders(): Promise<Record<string, string>> {
  await waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url).endsWith('/api/codec-server'))).toBe(true));
  await waitFor(() => expect(codecRequests().length).toBeGreaterThan(0));
  await new Promise((resolve) => setTimeout(resolve, 10));
  return lastRequest().headers as Record<string, string>;
}

/** Waits for a decode request after the given count and returns its headers. */
async function nextDecodeHeaders(requestsBefore: number): Promise<Record<string, string>> {
  await waitFor(() => expect(codecRequests().length).toBeGreaterThan(requestsBefore));
  return lastRequest().headers as Record<string, string>;
}

function tokenInput() {
  return screen.getByLabelText('Codec endpoint bearer token') as HTMLInputElement;
}

function typeToken(value: string) {
  fireEvent.change(tokenInput(), { target: { value } });
}

function save() {
  fireEvent.click(screen.getByRole('button', { name: 'Save' }));
}

describe('CodecEndpointSetting bearer token', () => {
  it('keeps a typed token out of the DOM, also before Save', async () => {
    const { container } = renderSetting();
    typeToken(token);
    expect(tokenInput().getAttribute('value')).toBeNull();
    expect(container.innerHTML).not.toContain(token);

    save();
    expect(tokenInput().value).toBe('');
    expect(container.innerHTML).not.toContain(token);
    expect(container.textContent).toContain('Token saved in this browser.');
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
  });

  it('remembers the token in local storage by default, never in the preference', () => {
    renderSetting();
    expect(screen.getByRole('checkbox', { name: 'Remember on this browser' })).toHaveProperty('checked', true);
    typeToken(token);
    save();
    expect(JSON.parse(window.localStorage.getItem(tokenKey) ?? '')).toEqual({ endpoint: endpointA, token });
    expect(window.sessionStorage.getItem(tokenKey)).toBeNull();
    expect(window.localStorage.getItem(preferenceKey)).not.toContain(token);
    expect(JSON.parse(window.localStorage.getItem(preferenceKey) ?? '{}')).toEqual({
      mode: 'custom', endpoint: endpointA, includeCredentials: false, rememberBearerToken: true,
    });
  });

  it('keeps the token for this tab only when Remember is cleared', () => {
    renderSetting();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Remember on this browser' }));
    typeToken(token);
    save();
    expect(JSON.parse(window.sessionStorage.getItem(tokenKey) ?? '')).toEqual({ endpoint: endpointA, token });
    expect(window.localStorage.getItem(tokenKey)).toBeNull();
    expect(screen.getByText('Token saved for this tab.')).toBeTruthy();
  });

  it('moves the saved token when Remember changes and Save runs again', () => {
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    renderSetting();
    const remember = screen.getByRole('checkbox', { name: 'Remember on this browser' });
    fireEvent.click(remember);
    save();
    expect(window.localStorage.getItem(tokenKey)).toBeNull();
    expect(window.sessionStorage.getItem(tokenKey)).toBe(tokenRecord(endpointA));
    fireEvent.click(remember);
    save();
    expect(window.localStorage.getItem(tokenKey)).toBe(tokenRecord(endpointA));
    expect(window.sessionStorage.getItem(tokenKey)).toBeNull();
  });

  it('reads the saved token after a reload', async () => {
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    const { container } = renderSetting();
    expect(screen.getByText('Token saved in this browser.')).toBeTruthy();
    expect(container.innerHTML).not.toContain(token);
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
  });

  it('deletes the token from both storages with the small delete button', async () => {
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    window.sessionStorage.setItem(tokenKey, tokenRecord(endpointA));
    renderSetting();
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
    const requestsBefore = codecRequests().length;
    const deleteButton = screen.getByRole('button', { name: 'Delete saved bearer token' });
    expect(deleteButton.getAttribute('title')).toBe('Delete saved bearer token from this browser');
    fireEvent.click(deleteButton);

    expect(window.localStorage.getItem(tokenKey)).toBeNull();
    expect(window.sessionStorage.getItem(tokenKey)).toBeNull();
    expect(screen.queryByRole('button', { name: 'Delete saved bearer token' })).toBeNull();
    expect(await nextDecodeHeaders(requestsBefore)).not.toHaveProperty('Authorization');
  });

  it('sends no Authorization header without a token', async () => {
    renderSetting();
    expect(await settledDecodeHeaders()).not.toHaveProperty('Authorization');
    expect(screen.queryByRole('button', { name: 'Delete saved bearer token' })).toBeNull();
  });
});

describe('CodecEndpointSetting token and URL', () => {
  it('drops the token when Save runs with a different URL', async () => {
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    renderSetting();
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
    const requestsBefore = codecRequests().length;
    fireEvent.change(screen.getByLabelText('Codec endpoint URL'), { target: { value: endpointB } });
    save();

    expect(String(codecRequests().at(-1)?.[0])).not.toBe(`${endpointB}/decode`);
    const headers = await nextDecodeHeaders(requestsBefore);
    expect(String(codecRequests().at(-1)?.[0])).toBe(`${endpointB}/decode`);
    expect(headers).not.toHaveProperty('Authorization');
    expect(window.localStorage.getItem(tokenKey)).toBeNull();
  });

  it('uses a token saved for another URL only when that URL is chosen again', async () => {
    // Another tab changed the shared URL to B while this token belongs to A.
    savePreference({ mode: 'custom', endpoint: endpointB, includeCredentials: false });
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    renderSetting();
    expect(screen.getByText('The saved token is for another URL.')).toBeTruthy();
    expect(await settledDecodeHeaders()).not.toHaveProperty('Authorization');

    const requestsBefore = codecRequests().length;
    fireEvent.change(screen.getByLabelText('Codec endpoint URL'), { target: { value: endpointA } });
    save();
    expect(await nextDecodeHeaders(requestsBefore)).toMatchObject({ Authorization: `Bearer ${token}` });
  });

  it('never sends the custom token in Auto or Off, and sends it again back in Custom', async () => {
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    renderSetting();
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
    const requestsBefore = codecRequests().length;
    fireEvent.click(screen.getByRole('button', { name: 'Off' }));
    fireEvent.click(screen.getByRole('button', { name: 'Auto' }));
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(codecRequests().length).toBe(requestsBefore);

    fireEvent.click(screen.getByRole('button', { name: 'Custom' }));
    expect(await nextDecodeHeaders(requestsBefore)).toMatchObject({ Authorization: `Bearer ${token}` });
  });

  it('follows another tab that saves or deletes the token', async () => {
    renderSetting();
    expect(await settledDecodeHeaders()).not.toHaveProperty('Authorization');

    let requestsBefore = codecRequests().length;
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    act(() => { window.dispatchEvent(new StorageEvent('storage', { key: tokenKey })); });
    expect(await nextDecodeHeaders(requestsBefore)).toMatchObject({ Authorization: `Bearer ${token}` });
    expect(screen.getByText('Token saved in this browser.')).toBeTruthy();

    requestsBefore = codecRequests().length;
    window.localStorage.removeItem(tokenKey);
    act(() => { window.dispatchEvent(new StorageEvent('storage', { key: tokenKey })); });
    expect(await nextDecodeHeaders(requestsBefore)).not.toHaveProperty('Authorization');
    expect(screen.queryByText('Token saved in this browser.')).toBeNull();
  });
});

describe('CodecEndpointSetting storage failures and inherited copies', () => {
  function failingStorage(fail: 'setItem' | 'removeItem'): Storage {
    const storage = memoryStorage();
    return { ...storage, [fail]: () => { throw new Error('blocked'); } } as Storage;
  }

  it('keeps the saved token in place when it cannot move to session storage', async () => {
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    vi.stubGlobal('sessionStorage', failingStorage('setItem'));
    renderSetting();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Remember on this browser' }));
    save();

    expect(window.localStorage.getItem(tokenKey)).toBe(tokenRecord(endpointA));
    expect(JSON.parse(window.localStorage.getItem(preferenceKey) ?? '{}').rememberBearerToken).toBe(true);
    expect(screen.getByText('Could not save the token in this browser.')).toBeTruthy();
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
  });

  it('stops using the token when it cannot be deleted from storage, and offers a retry', async () => {
    const local = failingStorage('removeItem');
    local.setItem(tokenKey, tokenRecord(endpointA));
    vi.stubGlobal('localStorage', local);
    savePreference({ mode: 'custom', endpoint: endpointA, includeCredentials: false });
    renderSetting();
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
    const requestsBefore = codecRequests().length;
    fireEvent.click(screen.getByRole('button', { name: 'Delete saved bearer token' }));

    expect(screen.getByText('Could not delete the saved token. Try again.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Delete saved bearer token' })).toBeTruthy();
    expect(await nextDecodeHeaders(requestsBefore)).not.toHaveProperty('Authorization');
  });

  it('drops an inherited session copy when another tab deletes the shared token', async () => {
    // Tab A saved with Remember off; tab B was duplicated from it and inherited the session copy.
    savePreference({ mode: 'custom', endpoint: endpointA, includeCredentials: false, rememberBearerToken: false });
    window.sessionStorage.setItem(tokenKey, tokenRecord(endpointA));
    renderSetting();
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });

    // Tab A saves with Remember on, so the shared record appears, and then deletes it.
    let requestsBefore = codecRequests().length;
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    act(() => { window.dispatchEvent(new StorageEvent('storage', { key: tokenKey })); });
    expect(window.sessionStorage.getItem(tokenKey)).toBeNull();
    expect(await nextDecodeHeaders(requestsBefore)).toMatchObject({ Authorization: `Bearer ${token}` });

    requestsBefore = codecRequests().length;
    window.localStorage.removeItem(tokenKey);
    act(() => { window.dispatchEvent(new StorageEvent('storage', { key: tokenKey })); });
    expect(await nextDecodeHeaders(requestsBefore)).not.toHaveProperty('Authorization');
    expect(screen.queryByRole('button', { name: 'Delete saved bearer token' })).toBeNull();
  });
});

describe('CodecEndpointSetting cookies and help', () => {
  it('starts a new Custom configuration with Send cookies cleared', () => {
    window.localStorage.removeItem(preferenceKey);
    renderSetting();
    fireEvent.click(screen.getByRole('button', { name: 'Custom' }));
    expect(screen.getByRole('checkbox', { name: 'Send cookies (SSO proxy)' })).toHaveProperty('checked', false);
    expect(screen.getByRole('checkbox', { name: 'Remember on this browser' })).toHaveProperty('checked', true);
  });

  it('sends the token together with cookies when Send cookies is selected', async () => {
    renderSetting();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Send cookies (SSO proxy)' }));
    typeToken(token);
    save();
    await waitFor(() => expect(codecRequests().some(([, init]) => (
      (init as RequestInit).credentials === 'include'
      && ((init as RequestInit).headers as Record<string, string>).Authorization === `Bearer ${token}`
    ))).toBe(true));
  });

  it('opens and closes each explanation with its "?" button and Escape', () => {
    renderSetting();
    const cookiesHelp = screen.getByRole('button', { name: 'What is Send cookies?' });
    const rememberHelp = screen.getByRole('button', { name: 'What is Remember on this browser?' });
    expect(cookiesHelp.getAttribute('aria-expanded')).toBe('false');

    const cookiesText = screen.getByText(/Leave it off for dexcli codec-server/);
    const rememberText = screen.getByText(/Turn it off on a shared computer/);
    // aria-controls always points at an element that exists, hidden while closed.
    expect(document.getElementById(cookiesHelp.getAttribute('aria-controls') ?? '')).toBe(cookiesText);
    expect(document.getElementById(rememberHelp.getAttribute('aria-controls') ?? '')).toBe(rememberText);
    expect(cookiesText.hidden).toBe(true);

    fireEvent.click(cookiesHelp);
    expect(cookiesHelp.getAttribute('aria-expanded')).toBe('true');
    expect(cookiesText.hidden).toBe(false);
    fireEvent.click(cookiesHelp);
    expect(cookiesHelp.getAttribute('aria-expanded')).toBe('false');
    expect(cookiesText.hidden).toBe(true);

    fireEvent.click(rememberHelp);
    expect(rememberText.hidden).toBe(false);
    fireEvent.keyDown(rememberHelp, { key: 'Escape' });
    expect(rememberHelp.getAttribute('aria-expanded')).toBe('false');
    expect(rememberText.hidden).toBe(true);
  });

  it('names the bearer token when the codec endpoint answers 401, without echoing it', async () => {
    codecStatus = 401;
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    const { container } = renderSetting();
    const message = await screen.findByRole('status');
    expect(message.textContent).toContain('bearer token is likely wrong');
    expect(container.innerHTML).not.toContain(token);

    fireEvent.click(screen.getByRole('button', { name: 'Delete saved bearer token' }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('likely needs a bearer token'));
  });
});
