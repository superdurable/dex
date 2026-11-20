// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { useEffect } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
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
let localCodecServer = { configured: false, endpoint: '', token: '' };
const localCodecServerConfigured = { configured: true, endpoint: 'http://127.0.0.1:53199', token: 'local-token' };

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
  localCodecServer = { configured: false, endpoint: '', token: '' };
  fetchMock = vi.fn(async (url: RequestInfo | URL) => {
    if (String(url).endsWith('/api/codec-server')) return jsonResponse(200, localCodecServer);
    if (codecStatus !== 200) return jsonResponse(codecStatus, { error: { category: 'unauthenticated', message: 'no' } });
    return jsonResponse(200, { values: [{ stringValue: 'plain' }] });
  });
  vi.stubGlobal('fetch', fetchMock);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/** Renders the setting and waits until Dex Web answered about its local codec server, so the modes show. */
async function renderSetting() {
  const result = render(
    <PayloadCodecProvider>
      <CodecEndpointSetting />
      <DecodeProbe />
    </PayloadCodecProvider>,
  );
  await screen.findByRole('button', { name: 'Off' });
  return result;
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
    const { container } = await renderSetting();
    typeToken(token);
    expect(tokenInput().getAttribute('value')).toBeNull();
    expect(container.innerHTML).not.toContain(token);

    save();
    expect(tokenInput().value).toBe('');
    expect(container.innerHTML).not.toContain(token);
    expect(container.textContent).toContain('Token saved in this browser.');
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
  });

  it('remembers the token in local storage by default, never in the preference', async () => {
    await renderSetting();
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

  it('keeps the token in session storage when Remember is cleared', async () => {
    await renderSetting();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Remember on this browser' }));
    typeToken(token);
    save();
    expect(JSON.parse(window.sessionStorage.getItem(tokenKey) ?? '')).toEqual({ endpoint: endpointA, token });
    expect(window.localStorage.getItem(tokenKey)).toBeNull();
    expect(screen.getByText('Token saved in this tab’s session storage.')).toBeTruthy();
  });

  it('moves the saved token when Remember changes and Save runs again', async () => {
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    await renderSetting();
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
    const { container } = await renderSetting();
    expect(screen.getByText('Token saved in this browser.')).toBeTruthy();
    expect(container.innerHTML).not.toContain(token);
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
  });

  it('deletes the token from both storages with the small delete button', async () => {
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    window.sessionStorage.setItem(tokenKey, tokenRecord(endpointA));
    await renderSetting();
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
    await renderSetting();
    expect(await settledDecodeHeaders()).not.toHaveProperty('Authorization');
    expect(screen.queryByRole('button', { name: 'Delete saved bearer token' })).toBeNull();
  });
});

describe('CodecEndpointSetting token and URL', () => {
  it('drops the token when Save runs with a different URL', async () => {
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    await renderSetting();
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
    await renderSetting();
    expect(screen.getByText('The saved token is for another URL.')).toBeTruthy();
    expect(await settledDecodeHeaders()).not.toHaveProperty('Authorization');

    const requestsBefore = codecRequests().length;
    fireEvent.change(screen.getByLabelText('Codec endpoint URL'), { target: { value: endpointA } });
    save();
    expect(await nextDecodeHeaders(requestsBefore)).toMatchObject({ Authorization: `Bearer ${token}` });
  });

  it('never sends the custom token in Auto or Off, and sends it again back in Custom', async () => {
    localCodecServer = localCodecServerConfigured;
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    await renderSetting();
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
    let requestsBefore = codecRequests().length;
    fireEvent.click(screen.getByRole('button', { name: 'Off' }));
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(codecRequests().length).toBe(requestsBefore);

    fireEvent.click(screen.getByRole('button', { name: 'Auto' }));
    expect(await nextDecodeHeaders(requestsBefore)).toMatchObject({ Authorization: 'Bearer local-token' });
    expect(String(codecRequests().at(-1)?.[0])).toBe('http://127.0.0.1:53199/decode');

    requestsBefore = codecRequests().length;
    fireEvent.click(screen.getByRole('button', { name: 'Custom' }));
    expect(await nextDecodeHeaders(requestsBefore)).toMatchObject({ Authorization: `Bearer ${token}` });
  });

  it('follows another tab that saves or deletes the token', async () => {
    await renderSetting();
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
    await renderSetting();
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
    await renderSetting();
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
    const requestsBefore = codecRequests().length;
    fireEvent.click(screen.getByRole('button', { name: 'Delete saved bearer token' }));

    expect(screen.getByText('Could not delete the saved token. Try again.')).toBeTruthy();
    expect(screen.getByRole('button', { name: 'Delete saved bearer token' })).toBeTruthy();
    expect(await nextDecodeHeaders(requestsBefore)).not.toHaveProperty('Authorization');
  });

  it('reports a move to session storage whose shared copy cannot be removed', async () => {
    const local = failingStorage('removeItem');
    local.setItem(tokenKey, tokenRecord(endpointA));
    vi.stubGlobal('localStorage', local);
    savePreference({ mode: 'custom', endpoint: endpointA, includeCredentials: false, rememberBearerToken: true });
    await renderSetting();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Remember on this browser' }));
    save();

    expect(screen.getByText('Could not save the token in this browser.')).toBeTruthy();
    expect(screen.queryByText('Token saved in this tab’s session storage.')).toBeNull();
    expect(JSON.parse(window.localStorage.getItem(preferenceKey) ?? '{}').rememberBearerToken).toBe(true);
    expect(screen.getByRole('button', { name: 'Delete saved bearer token' })).toBeTruthy();
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
  });

  it('keeps the saved Remember choice when a replacement token fails to move', async () => {
    const local = failingStorage('removeItem');
    local.setItem(tokenKey, tokenRecord(endpointA));
    vi.stubGlobal('localStorage', local);
    savePreference({ mode: 'custom', endpoint: endpointA, includeCredentials: false, rememberBearerToken: true });
    await renderSetting();
    typeToken('replacement-token');
    fireEvent.click(screen.getByRole('checkbox', { name: 'Remember on this browser' }));
    save();

    expect(screen.getByText('Could not save the token in this browser.')).toBeTruthy();
    expect(JSON.parse(window.localStorage.getItem(preferenceKey) ?? '{}').rememberBearerToken).toBe(true);
  });

  it('keeps a token whose delete failed revoked when other tabs change storage', async () => {
    const local = failingStorage('removeItem');
    local.setItem(tokenKey, tokenRecord(endpointA));
    vi.stubGlobal('localStorage', local);
    savePreference({ mode: 'custom', endpoint: endpointA, includeCredentials: false });
    await renderSetting();
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
    fireEvent.click(screen.getByRole('button', { name: 'Delete saved bearer token' }));

    const requestsBefore = codecRequests().length;
    act(() => { window.dispatchEvent(new StorageEvent('storage', { key: tokenKey })); });
    await new Promise((resolve) => setTimeout(resolve, 10));
    expect(codecRequests().slice(requestsBefore).every(([, init]) => (
      !('Authorization' in ((init as RequestInit).headers as object))
    ))).toBe(true);
    expect(lastRequest().headers).not.toHaveProperty('Authorization');
  });
});

describe('CodecEndpointSetting texts', () => {
  function modeButtons() {
    return ['Auto', 'Custom', 'Off'].filter((name) => screen.queryByRole('button', { name }) !== null);
  }

  it('offers Auto, Custom, and Off when this Dex Web provides a local codec server', async () => {
    localCodecServer = localCodecServerConfigured;
    window.localStorage.removeItem(preferenceKey);
    const { container } = await renderSetting();
    expect(modeButtons()).toEqual(['Auto', 'Custom', 'Off']);
    expect(screen.getByRole('button', { name: 'Auto' }).getAttribute('aria-pressed')).toBe('true');
    const modeLine = screen.getByTestId('codec-endpoint-mode');
    expect(modeLine.textContent)
      .toBe('Uses the codec server that this Dex Web provides, such as the one dexcli dev -codec-key-file starts.');
    expect(screen.getByTestId('codec-endpoint-status').textContent).toBe('Local dev codec server at http://127.0.0.1:53199.');

    fireEvent.click(screen.getByRole('button', { name: 'Off' }));
    expect(modeLine.textContent).toBe('Does not decrypt. Encrypted values show as locked.');
    expect(screen.queryByTestId('codec-endpoint-status')).toBeNull();
    expect(container.textContent).not.toMatch(/stay encrypted|reports no local codec server|stay in this page|No local codec server/);
  });

  it('hides Auto and starts in Custom when this Dex Web provides no local codec server', async () => {
    window.localStorage.removeItem(preferenceKey);
    await renderSetting();
    expect(modeButtons()).toEqual(['Custom', 'Off']);
    expect(screen.getByRole('button', { name: 'Custom' }).getAttribute('aria-pressed')).toBe('true');
    expect(screen.getByTestId('codec-endpoint-mode').textContent).toBe('Uses the codec server at the URL below.');
    expect(screen.getByTestId('codec-endpoint-status').textContent).toBe('Enter the codec server URL and save.');
    expect(screen.getByLabelText('Codec endpoint URL')).toBeTruthy();
  });

  it('shows a saved Auto choice as Custom without rewriting it', async () => {
    savePreference({ mode: 'auto', endpoint: endpointA, includeCredentials: false, rememberBearerToken: true });
    window.localStorage.setItem(tokenKey, tokenRecord(endpointA));
    await renderSetting();
    expect(modeButtons()).toEqual(['Custom', 'Off']);
    expect(screen.getByRole('button', { name: 'Custom' }).getAttribute('aria-pressed')).toBe('true');
    expect(await settledDecodeHeaders()).toMatchObject({ Authorization: `Bearer ${token}` });
    expect(String(codecRequests().at(-1)?.[0])).toBe(`${endpointA}/decode`);
    expect(JSON.parse(window.localStorage.getItem(preferenceKey) ?? '{}').mode).toBe('auto');
  });

  it('shows no mode until Dex Web answers, so Auto never flashes', async () => {
    let answerLocalServer: (response: Response) => void = () => undefined;
    fetchMock.mockImplementationOnce(() => new Promise<Response>((resolve) => { answerLocalServer = resolve; }));
    window.localStorage.removeItem(preferenceKey);
    render(
      <PayloadCodecProvider>
        <CodecEndpointSetting />
      </PayloadCodecProvider>,
    );
    expect(modeButtons()).toEqual([]);
    expect(screen.queryByTestId('codec-endpoint-mode')).toBeNull();
    expect(screen.queryByLabelText('Codec endpoint URL')).toBeNull();

    answerLocalServer(jsonResponse(200, { configured: false, endpoint: '', token: '' }));
    await screen.findByRole('button', { name: 'Custom' });
    expect(modeButtons()).toEqual(['Custom', 'Off']);
  });

  it('shows the codec endpoint explanation behind the heading "?"', async () => {
    await renderSetting();
    const help = screen.getByRole('button', { name: 'What is a codec endpoint?' });
    const text = screen.getByText(/Your keys stay in the codec server/);
    expect(document.getElementById(help.getAttribute('aria-controls') ?? '')).toBe(text);
    expect(text.hidden).toBe(true);
    fireEvent.click(help);
    expect(help.getAttribute('aria-expanded')).toBe('true');
    expect(text.hidden).toBe(false);
    fireEvent.keyDown(help, { key: 'Escape' });
    expect(help.getAttribute('aria-expanded')).toBe('false');
    expect(text.hidden).toBe(true);
  });
});

/** A browser tab with the shared local storage, its own session storage, and its own storage events. */
function browserTabWith(sharedLocal: Storage, session: Storage) {
  const events = new EventTarget();
  return {
    localStorage: sharedLocal,
    sessionStorage: session,
    addEventListener: (type: 'storage', listener: (event: StorageEvent) => void) => {
      events.addEventListener(type, listener as EventListener);
    },
    removeEventListener: (type: 'storage', listener: (event: StorageEvent) => void) => {
      events.removeEventListener(type, listener as EventListener);
    },
    /** Delivers the event a browser sends to this tab when another tab changes the shared token. */
    receiveTokenChange: () => {
      act(() => { events.dispatchEvent(new StorageEvent('storage', { key: tokenKey })); });
    },
  };
}

/** Decodes one envelope for a Flow named after the tab, so requests show which tab sent them. */
function TabDecodeProbe({ flowId }: { flowId: string }) {
  const { store } = usePayloadCodec();
  useEffect(() => {
    store.request({ flowId, flowType: 'OrderFlow' }, ['AAAA']);
  }, [flowId, store]);
  return null;
}

function tabRequests(flowId: string) {
  return codecRequests().filter(([, init]) => String((init as RequestInit).body).includes(`"flow_id":"${flowId}"`));
}

async function nextTabHeaders(flowId: string, requestsBefore: number): Promise<Record<string, string>> {
  await waitFor(() => expect(tabRequests(flowId).length).toBeGreaterThan(requestsBefore));
  return (tabRequests(flowId).at(-1)?.[1] as RequestInit).headers as Record<string, string>;
}

describe('CodecEndpointSetting across two tabs', () => {
  async function renderTab(tab: ReturnType<typeof browserTabWith>, flowId: string) {
    const { container } = render(
      <PayloadCodecProvider browserTab={tab}>
        <CodecEndpointSetting />
        <TabDecodeProbe flowId={flowId} />
      </PayloadCodecProvider>,
    );
    const tabView = within(container);
    await tabView.findByRole('button', { name: 'Off' });
    return tabView;
  }

  it('follows tab A through Save, the Remember toggle, and Delete in tab B', async () => {
    const shared = memoryStorage();
    shared.setItem(preferenceKey, JSON.stringify({
      mode: 'custom', endpoint: endpointA, includeCredentials: false, rememberBearerToken: false,
    }));
    const sessionA = memoryStorage();
    const tabA = browserTabWith(shared, sessionA);
    const a = await renderTab(tabA, 'tab-a');

    // Tab A saves with Remember off.
    fireEvent.change(a.getByLabelText('Codec endpoint bearer token'), { target: { value: token } });
    fireEvent.click(a.getByRole('button', { name: 'Save' }));
    expect(sessionA.getItem(tokenKey)).toBe(tokenRecord(endpointA));
    expect(shared.getItem(tokenKey)).toBeNull();

    // Tab B is duplicated from A and inherits its session copy.
    const sessionB = memoryStorage();
    sessionB.setItem(tokenKey, sessionA.getItem(tokenKey) ?? '');
    const tabB = browserTabWith(shared, sessionB);
    await renderTab(tabB, 'tab-b');
    expect(await nextTabHeaders('tab-b', 0)).toMatchObject({ Authorization: `Bearer ${token}` });

    // Tab A turns Remember on and saves: the token moves to shared storage.
    fireEvent.click(a.getByRole('checkbox', { name: 'Remember on this browser' }));
    fireEvent.click(a.getByRole('button', { name: 'Save' }));
    expect(shared.getItem(tokenKey)).toBe(tokenRecord(endpointA));
    expect(sessionA.getItem(tokenKey)).toBeNull();
    let requestsBefore = tabRequests('tab-b').length;
    tabB.receiveTokenChange();
    expect(sessionB.getItem(tokenKey)).toBeNull();
    expect(await nextTabHeaders('tab-b', requestsBefore)).toMatchObject({ Authorization: `Bearer ${token}` });

    // Tab A deletes the token.
    fireEvent.click(a.getByRole('button', { name: 'Delete saved bearer token' }));
    expect(shared.getItem(tokenKey)).toBeNull();
    requestsBefore = tabRequests('tab-b').length;
    tabB.receiveTokenChange();
    expect(sessionB.getItem(tokenKey)).toBeNull();
    expect(await nextTabHeaders('tab-b', requestsBefore)).not.toHaveProperty('Authorization');
  });

  it('drops the token in tab B even when its inherited session copy cannot be removed', async () => {
    const shared = memoryStorage();
    shared.setItem(preferenceKey, JSON.stringify({
      mode: 'custom', endpoint: endpointA, includeCredentials: false, rememberBearerToken: false,
    }));
    const sessionB = { ...memoryStorage(), removeItem: () => { throw new Error('blocked'); } } as Storage;
    sessionB.setItem(tokenKey, tokenRecord(endpointA));
    const tabB = browserTabWith(shared, sessionB);
    await renderTab(tabB, 'tab-b');
    expect(await nextTabHeaders('tab-b', 0)).toMatchObject({ Authorization: `Bearer ${token}` });

    // Tab A deleted the shared token; the session copy in B stays, but B stops using it.
    const requestsBefore = tabRequests('tab-b').length;
    tabB.receiveTokenChange();
    expect(sessionB.getItem(tokenKey)).toBe(tokenRecord(endpointA));
    expect(await nextTabHeaders('tab-b', requestsBefore)).not.toHaveProperty('Authorization');
  });
});

describe('CodecEndpointSetting cookies and help', () => {
  it('starts a new Custom configuration with Send cookies cleared', async () => {
    window.localStorage.removeItem(preferenceKey);
    await renderSetting();
    fireEvent.click(screen.getByRole('button', { name: 'Custom' }));
    expect(screen.getByRole('checkbox', { name: 'Send cookies (SSO proxy)' })).toHaveProperty('checked', false);
    expect(screen.getByRole('checkbox', { name: 'Remember on this browser' })).toHaveProperty('checked', true);
  });

  it('sends the token together with cookies when Send cookies is selected', async () => {
    await renderSetting();
    fireEvent.click(screen.getByRole('checkbox', { name: 'Send cookies (SSO proxy)' }));
    typeToken(token);
    save();
    await waitFor(() => expect(codecRequests().some(([, init]) => (
      (init as RequestInit).credentials === 'include'
      && ((init as RequestInit).headers as Record<string, string>).Authorization === `Bearer ${token}`
    ))).toBe(true));
  });

  it('opens and closes each explanation with its "?" button and Escape', async () => {
    await renderSetting();
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
    const { container } = await renderSetting();
    const message = await screen.findByRole('status');
    expect(message.textContent).toContain('bearer token is likely wrong');
    expect(container.innerHTML).not.toContain(token);

    fireEvent.click(screen.getByRole('button', { name: 'Delete saved bearer token' }));
    await waitFor(() => expect(screen.getByRole('status').textContent).toContain('likely needs a bearer token'));
  });
});
