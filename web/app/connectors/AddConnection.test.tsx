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
