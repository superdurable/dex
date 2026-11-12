// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, fireEvent, render, screen, waitFor, within } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import { ThemeProvider } from '../theme';
import {
  ConnectorsPage,
  connectionDeleteLabel,
  connectionNameProblem,
  connectorCatalogOptionText,
  filterConnectorCatalog,
  isConnectionDeletable,
  oauthReturnConnectionKey,
  selectedConnectionAfterLoad,
  type ConnectorCatalogEntry,
} from './ConnectorsPage';

const slackEntry: ConnectorCatalogEntry = {
  connectorId: 'slack', displayName: 'Slack', description: 'Exchange Slack thread messages.',
  modulePath: 'github.com/superdurable/dex-connectors-library/connectors/slack/go', moduleVersion: 'v1.3.0',
};
const gmailEntry: ConnectorCatalogEntry = {
  connectorId: 'gmail', displayName: 'Gmail', description: 'Send mail.',
  modulePath: 'github.com/superdurable/dex-connectors-library/connectors/google/gmail/go', moduleVersion: 'v0.25.2',
};

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Add connection helpers', () => {
  it.each([
    ['', 'Enter a connection name.'],
    [' support', 'The connection name must not start or end with a space.'],
    ['a'.repeat(257), 'The connection name must have at most 256 characters.'],
    ['team/support', 'The connection name must not contain a slash or a control character.'],
    ['support\nslack', 'The connection name must not contain a slash or a control character.'],
    ['support-slack', ''],
    ['Support Slack 2', ''],
  ])('checks the connection name %j', (name, problem) => {
    expect(connectionNameProblem(name)).toBe(problem);
  });

  it('filters the catalog by every query word and keeps the chosen entry', () => {
    expect(filterConnectorCatalog([slackEntry, gmailEntry], 'thread SLACK').map((entry) => entry.connectorId)).toEqual(['slack']);
    expect(filterConnectorCatalog([slackEntry, gmailEntry], '').map((entry) => entry.connectorId)).toEqual(['slack', 'gmail']);
    expect(filterConnectorCatalog([slackEntry, gmailEntry], 'mail', 'slack').map((entry) => entry.connectorId)).toEqual(['slack', 'gmail']);
  });

  it('names the catalog release in each option', () => {
    expect(connectorCatalogOptionText(slackEntry)).toBe('Slack · v1.3.0');
    expect(connectorCatalogOptionText({ ...gmailEntry, displayName: 'Google Gmail', localOverride: true }))
      .toBe('Google Gmail (gmail) · local override v0.25.2');
  });

  it('selects a named connection, the first one, or keeps the current one after a reload', () => {
    const connections = [
      { connectorId: 'gmail', connectionName: 'sender' },
      { connectorId: 'slack', connectionName: 'support-slack' },
    ];
    expect(selectedConnectionAfterLoad(connections, connections[0], 'slack\u0000support-slack')).toBe(connections[1]);
    expect(selectedConnectionAfterLoad(connections, connections[1], '')).toBe(connections[0]);
    expect(selectedConnectionAfterLoad(connections, connections[1])).toBe(connections[1]);
    expect(selectedConnectionAfterLoad(connections, { connectorId: 'x', connectionName: 'gone' })).toBeNull();
    expect(selectedConnectionAfterLoad(connections, null)).toBe(connections[0]);
  });

  it('reads the connection an OAuth redirect names', () => {
    expect(oauthReturnConnectionKey('?oauth=success&connectorId=slack&connectionName=support-slack')).toBe('slack\u0000support-slack');
    expect(oauthReturnConnectionKey('?connectorId=slack&connectionName=support-slack')).toBeUndefined();
    expect(oauthReturnConnectionKey('')).toBeUndefined();
  });

  it('lets a standalone connection be removed before it is saved', () => {
    expect(isConnectionDeletable({ standalone: true, status: 'Missing' })).toBe(true);
    expect(connectionDeleteLabel({ standalone: true, status: 'Missing' })).toBe('Remove connection');
    expect(connectionDeleteLabel({ standalone: true, status: 'Ready' })).toBe('Delete connection');
    expect(isConnectionDeletable({ standalone: true, status: 'Unsupported' })).toBe(false);
    expect(isConnectionDeletable({ status: 'Missing' })).toBe(false);
    expect(connectionDeleteLabel({ status: 'Ready' })).toBe('Delete local credentials');
  });
});

describe('Add connection on the Connectors page', () => {
  it('adds a named connection from the catalog and opens its authorization', async () => {
    const added = {
      connectorId: 'slack', displayName: 'Slack', connectionName: 'support-slack', modulePath: slackEntry.modulePath,
      moduleVersion: 'v1.3.0', status: 'Missing', uses: [], standalone: true,
    };
    let connections: unknown[] = [];
    const requests: { method: string; url: string; body?: string; headers?: HeadersInit }[] = [];
    vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
      requests.push({ method: init?.method ?? 'GET', url, body: init?.body as string | undefined, headers: init?.headers });
      if (url === '/api/v2/connector-connections' && init?.method === 'POST') {
        connections = [added];
        return new Response(JSON.stringify(added));
      }
      if (url === '/api/v2/connector-connections') {
        return new Response(JSON.stringify({
          enabled: true, definitionRevision: 'r1', csrfToken: 'csrf', filePath: '/home/dev/.dex/connectors/connections.json', connections,
        }));
      }
      if (url === '/api/v2/connector-catalog') {
        return new Response(JSON.stringify({ connectors: [gmailEntry, slackEntry], publicCatalogAvailable: true }));
      }
      if (url === '/api/v2/connector-ui-sessions') {
        return new Response(JSON.stringify({
          connectorId: 'slack', connectionName: 'support-slack', manifest: {
            metadata: { displayName: 'Slack', description: 'Slack threads.' },
            spec: { provider: 'slack', configuration: { fields: [] }, auth: { type: 'apiKey', fields: [
              { name: 'bot_token', type: 'secretString', description: 'Bot token.', required: true },
            ] } },
          },
        }));
      }
      return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 });
    }));
    render(
      <MemoryRouter initialEntries={['/v1/connectors']}>
        <ThemeProvider>
          <ConnectorsPage />
        </ThemeProvider>
      </MemoryRouter>,
    );

    fireEvent.click(await screen.findByRole('button', { name: 'Add connection' }));
    const form = await screen.findByRole('form', { name: 'Add connection' });
    const connector = await within(form).findByRole('combobox', { name: /Connector/ });
    fireEvent.click(within(form).getByRole('button', { name: 'Add connection' }));
    expect(within(form).getByText('Choose a Connector.')).not.toBeNull();
    expect(within(form).getByText('Enter a connection name.')).not.toBeNull();
    expect(requests.some((request) => request.method === 'POST')).toBe(false);

    fireEvent.change(within(form).getByRole('searchbox', { name: 'Search the catalog' }), { target: { value: 'slack' } });
    expect(within(connector).getAllByRole('option').map((option) => option.textContent)).toEqual(['Choose a Connector (1)', 'Slack · v1.3.0']);
    fireEvent.change(connector, { target: { value: 'slack' } });
    fireEvent.change(within(form).getByRole('textbox', { name: /Connection name/ }), { target: { value: 'support-slack' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Add connection' }));

    await waitFor(() => expect(screen.queryByRole('form', { name: 'Add connection' })).toBeNull());
    const post = requests.find((request) => request.method === 'POST' && request.url === '/api/v2/connector-connections');
    expect(JSON.parse(post?.body ?? '{}')).toEqual({
      connectorId: 'slack', connectionName: 'support-slack', modulePath: slackEntry.modulePath, moduleVersion: 'v1.3.0',
    });
    expect((post?.headers as Record<string, string>)['X-Dex-CSRF-Token']).toBe('csrf');
    expect(await screen.findByText(/No loaded Flow uses this connection/)).not.toBeNull();
    expect(await screen.findByRole('button', { name: 'Remove connection' })).not.toBeNull();
    expect(await screen.findByRole('button', { name: 'Save local credentials' })).not.toBeNull();
  });
});

const hackerNewsEntry: ConnectorCatalogEntry = {
  connectorId: 'hacker-news-daily', displayName: 'Hacker News Daily',
  description: 'Read public Hacker News feeds, stories, and discussions for a daily developer digest with direct source links.',
  modulePath: 'github.com/superdurable/dex-connectors-library/connectors/hackernews/go', moduleVersion: 'v0.24.0',
};

// The UI session dexcli dev returned for this credential-free release; auth has no fields key.
const hackerNewsSession = {
  connectorId: 'hacker-news-daily', connectionName: 'public', manifest: {
    apiVersion: 'connectors.dex.dev/v1alpha1', kind: 'Connector',
    metadata: { name: 'hacker-news-daily', displayName: 'Hacker News Daily', description: hackerNewsEntry.description },
    spec: {
      provider: 'hacker-news',
      configuration: { fields: [
        { name: 'baseUrl', type: 'url', description: 'Hacker News Firebase API base URL.', required: false, default: 'https://hacker-news.firebaseio.com/v0' },
        { name: 'timeout', type: 'duration', description: 'Maximum duration for one request.', required: false, default: '15s' },
        { name: 'maxResponseBytes', type: 'integer', description: 'Maximum bytes accepted.', required: false, default: 1048576 },
      ] },
      auth: { type: 'none', connectionKind: 'none' },
    },
  },
};

type ConnectionFixture = { connectorId: string; connectionName: string } & Record<string, unknown>;

function connectionView(connectorId: string, connectionName: string, status: string, modulePath: string, moduleVersion: string): ConnectionFixture {
  const displayName = [hackerNewsEntry, slackEntry].find((entry) => entry.connectorId === connectorId)?.displayName;
  return { connectorId, displayName, connectionName, modulePath, moduleVersion, status, uses: [], standalone: true };
}

type RecordedRequest = { method: string; url: string; body?: string };

interface ConnectorAPIStubOptions {
  /** The Hacker News Daily UI session body; defaults to the captured one. */
  hackerNewsSessionBody?: unknown;
  /** Answers the Slack UI session; defaults to the 502 dexcli dev returned. */
  slackSession?: () => Promise<Response>;
  /** Replaces the connection list after a DELETE removes a connection. */
  afterDelete?: (connections: ConnectionFixture[]) => ConnectionFixture[];
}

function slackReleaseUnavailableResponse() {
  return new Response(JSON.stringify({
    error: 'Connector release metadata or UI is unavailable', code: 'CONNECTOR_RELEASE_UNAVAILABLE',
  }), { status: 502 });
}

// Answers like dexcli dev: the Slack release is unavailable, and Hacker News Daily needs no credentials.
function stubConnectorAPI(initialConnections: ConnectionFixture[], options: ConnectorAPIStubOptions = {}) {
  let connections = initialConnections;
  const requests: RecordedRequest[] = [];
  vi.stubGlobal('fetch', vi.fn(async (url: string, init?: RequestInit) => {
    const method = init?.method ?? 'GET';
    const body = init?.body as string | undefined;
    requests.push({ method, url, body });
    if (url === '/api/v2/connector-connections' && method === 'POST') {
      const request = JSON.parse(body ?? '{}') as { connectorId: string; connectionName: string; modulePath: string; moduleVersion: string };
      const added = connectionView(request.connectorId, request.connectionName, 'Missing', request.modulePath, request.moduleVersion);
      connections = [added, ...connections];
      return new Response(JSON.stringify(added));
    }
    if (url === '/api/v2/connector-connections') {
      return new Response(JSON.stringify({ enabled: true, definitionRevision: 'r1', csrfToken: 'csrf', connections }));
    }
    if (url === '/api/v2/connector-catalog') {
      return new Response(JSON.stringify({ connectors: [hackerNewsEntry, slackEntry], publicCatalogAvailable: true }));
    }
    if (url === '/api/v2/connector-ui-sessions') {
      const request = JSON.parse(body ?? '{}') as { connectorId: string };
      if (request.connectorId === 'slack') return (options.slackSession ?? (async () => slackReleaseUnavailableResponse()))();
      return new Response(JSON.stringify(options.hackerNewsSessionBody ?? hackerNewsSession));
    }
    if (url === '/api/v2/connector-connections/hacker-news-daily/public' && method === 'PUT') {
      connections = connections.map((connection) => connection.connectorId === 'hacker-news-daily'
        ? { ...connection, status: 'Ready', provider: 'hacker-news' }
        : connection);
      return new Response(JSON.stringify({ connectorId: 'hacker-news-daily', connectionName: 'public', status: 'Ready' }));
    }
    if (url.startsWith('/api/v2/connector-connections/') && method === 'DELETE') {
      const [connectorId, connectionName] = url.slice('/api/v2/connector-connections/'.length).split('/').map(decodeURIComponent);
      const remaining = connections.filter((connection) => connection.connectorId !== connectorId || connection.connectionName !== connectionName);
      connections = options.afterDelete?.(remaining) ?? remaining;
      return new Response(JSON.stringify({ deleted: true }));
    }
    return new Response(JSON.stringify({ error: 'unexpected' }), { status: 404 });
  }));
  return requests;
}

function renderConnectorsPage() {
  render(
    <MemoryRouter initialEntries={['/v1/connectors']}>
      <ThemeProvider>
        <ConnectorsPage />
      </ThemeProvider>
    </MemoryRouter>,
  );
}

const slackConnection = connectionView('slack', 'support-slack', 'Ready', slackEntry.modulePath, 'v1.3.0');
const pendingHackerNewsConnection = connectionView(
  'hacker-news-daily', 'public', 'Missing', hackerNewsEntry.modulePath, hackerNewsEntry.moduleVersion,
);
// A release without an auth object fails to render even after the page reads absent lists as empty.
const unrenderableHackerNewsSession = {
  ...hackerNewsSession,
  manifest: { metadata: { displayName: 'Hacker News Daily', description: '' }, spec: { provider: 'hacker-news' } },
};

describe('A credential-free connection on the Connectors page', () => {
  it('adds and saves a connection whose release declares no credential fields', async () => {
    const requests = stubConnectorAPI([slackConnection]);
    renderConnectorsPage();
    expect((await screen.findByRole('alert')).textContent).toBe('Connector release metadata or UI is unavailable');

    fireEvent.click(await screen.findByRole('button', { name: 'Add connection' }));
    const form = await screen.findByRole('form', { name: 'Add connection' });
    const connector = await within(form).findByRole('combobox', { name: /Connector/ });
    fireEvent.change(connector, { target: { value: 'hacker-news-daily' } });
    fireEvent.change(within(form).getByRole('textbox', { name: /Connection name/ }), { target: { value: 'public' } });
    fireEvent.click(within(form).getByRole('button', { name: 'Add connection' }));

    fireEvent.click(await screen.findByRole('button', { name: 'Save connection' }));
    expect(await screen.findByText('Authorization complete')).not.toBeNull();
    const put = requests.find((request) => request.method === 'PUT');
    expect(JSON.parse(put?.body ?? '{}')).toEqual({
      modulePath: hackerNewsEntry.modulePath, moduleVersion: 'v0.24.0', provider: 'hacker-news', authMethodId: '',
      configuration: {}, credentials: {}, keepCredentialFields: [], credentialExpiresAt: null,
    });
    expect(screen.getByRole('list', { name: 'Connections' }).textContent).toContain('Slack');
  });

  it('opens with an unsaved credential-free connection selected', async () => {
    stubConnectorAPI([pendingHackerNewsConnection, slackConnection]);
    renderConnectorsPage();
    expect(await screen.findByRole('button', { name: 'Save connection' })).not.toBeNull();
    const list = screen.getByRole('list', { name: 'Connections' });
    expect(list.textContent).toContain('Hacker News Daily');
    expect(list.textContent).toContain('Slack');
  });

  it('renders a Connector UI unit whose server JSON has null bindings and configuration', async () => {
    const readyConnection = {
      ...pendingHackerNewsConnection, status: 'Ready', standalone: undefined,
      uses: [{
        flowName: 'DigestFlow', stepId: 'read', stepName: 'ReadStoriesStep', operationId: 'listStories', operationKind: 'query',
        configurationUI: { units: [{ id: 'feed', unitId: 'feedPicker', label: 'Feed', required: false, bindings: null }] },
        configuration: null, configured: false,
      }],
    };
    stubConnectorAPI([readyConnection], { hackerNewsSessionBody: {
      ...hackerNewsSession,
      sessionNonce: 'nonce', entrypointUrl: '/api/v2/connector-ui-sessions/nonce/index.html',
      manifest: { ...hackerNewsSession.manifest, spec: {
        ...hackerNewsSession.manifest.spec, studio: { setup: { backendCapabilities: null }, units: [{ id: 'feedPicker', description: 'Feed.', outputs: null }] },
      } },
    } });
    renderConnectorsPage();
    expect(await screen.findByTitle('Hacker News Daily Connector Feed')).not.toBeNull();
    expect(screen.getByRole('tab', { name: /listStories/ }).getAttribute('aria-selected')).toBe('true');
    expect(screen.queryByRole('alert')).toBeNull();
  });

  it('shows a connection that cannot render as an error and keeps the page', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    stubConnectorAPI([pendingHackerNewsConnection, slackConnection], { hackerNewsSessionBody: unrenderableHackerNewsSession });
    renderConnectorsPage();
    const alert = await screen.findByRole('alert');
    expect(alert.textContent).toContain('This connection could not be shown');
    expect(consoleError).toHaveBeenCalled();
    consoleError.mockRestore();

    const list = screen.getByRole('list', { name: 'Connections' });
    fireEvent.click(within(list).getByRole('button', { name: /Slack/ }));
    expect(await screen.findByRole('heading', { name: 'Slack' })).not.toBeNull();
    expect((await screen.findByRole('alert')).textContent).toBe('Connector release metadata or UI is unavailable');
  });

  it('removes a connection that cannot render, and a list row recovers on the next load', async () => {
    const consoleError = vi.spyOn(console, 'error').mockImplementation(() => undefined);
    // A display name that is not a string makes the list row itself fail to render.
    const unrenderableSlackRow = { ...slackConnection, displayName: 42 };
    const requests = stubConnectorAPI([pendingHackerNewsConnection, unrenderableSlackRow], {
      hackerNewsSessionBody: unrenderableHackerNewsSession,
      afterDelete: (connections) => connections.map((connection) => ({ ...connection, displayName: 'Slack' })),
    });
    renderConnectorsPage();
    await waitFor(() => expect(screen.getAllByRole('alert').map((alert) => alert.textContent)).toEqual([
      expect.stringContaining('This connection could not be shown'),
      expect.stringContaining('This connection could not be shown'),
    ]));

    fireEvent.click(screen.getByRole('button', { name: 'Remove connection' }));
    expect(await screen.findByRole('heading', { name: 'Slack' })).not.toBeNull();
    expect(requests.some((request) => request.method === 'DELETE'
      && request.url === '/api/v2/connector-connections/hacker-news-daily/public')).toBe(true);
    const list = screen.getByRole('list', { name: 'Connections' });
    expect(within(list).getByRole('button', { name: /Slack/ }).getAttribute('aria-current')).toBe('true');
    expect(list.textContent).not.toContain('could not be shown');
    consoleError.mockRestore();
  });

  it('never shows the previous connection\'s setup while the next UI session loads', async () => {
    let answerSlackSession: (response: Response) => void = () => undefined;
    const slackSession = new Promise<Response>((resolve) => { answerSlackSession = resolve; });
    stubConnectorAPI([pendingHackerNewsConnection, { ...slackConnection, status: 'Missing' }], {
      slackSession: () => slackSession,
    });
    renderConnectorsPage();
    expect(await screen.findByRole('heading', { name: 'Hacker News Daily setup' })).not.toBeNull();
    expect(screen.getByRole('button', { name: 'Save connection' })).not.toBeNull();

    const list = screen.getByRole('list', { name: 'Connections' });
    fireEvent.click(within(list).getByRole('button', { name: /Slack/ }));
    expect(await screen.findByRole('heading', { name: 'Slack' })).not.toBeNull();
    expect(screen.getByRole('status').textContent).toBe('Loading Connector release…');
    expect(screen.queryByRole('heading', { name: 'Hacker News Daily setup' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Save connection' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Save local credentials' })).toBeNull();

    answerSlackSession(new Response(JSON.stringify({
      connectorId: 'slack', connectionName: 'support-slack', manifest: {
        metadata: { displayName: 'Slack', description: 'Slack threads.' },
        spec: { provider: 'slack', configuration: { fields: [] }, auth: { type: 'apiKey', fields: [
          { name: 'bot_token', type: 'secretString', description: 'Bot token.', required: true },
        ] } },
      },
    })));
    expect(await screen.findByRole('heading', { name: 'Slack setup' })).not.toBeNull();
    expect(screen.getByRole('button', { name: 'Save local credentials' })).not.toBeNull();
    expect(screen.queryByRole('heading', { name: 'Hacker News Daily setup' })).toBeNull();
    expect(screen.queryByRole('button', { name: 'Save connection' })).toBeNull();
  });
});
