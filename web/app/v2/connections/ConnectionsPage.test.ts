// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createElement } from 'react';
import { renderToStaticMarkup } from 'react-dom/server';
import { describe, expect, it } from 'vitest';
import { DexAPIError } from '@/lib/http';
import {
  ConnectorForm,
  connectionKey,
  connectionDeleteLabel,
  connectorConfigurationEffectText,
  connectorHostReadyMessage,
  connectorSetupTabs,
  connectorWriteHeaders,
  initialConnectorSetupTabKey,
  isStudioCommand,
  isStudioFrameResize,
  descriptionParts,
  manifestFieldDefaultText,
  studioCommandFailureOutcome,
  selectedManifestAuth,
  studioHostCapabilities,
  studioState,
} from './ConnectionsPage';
import { connectorStudioStylesheet } from './connectorStudioTheme';

describe('Connections contract', () => {
  it('distinguishes hosted revision effects from local file effects', () => {
    expect(connectionDeleteLabel('hosted')).toBe('Delete connection');
    expect(connectorConfigurationEffectText('hosted')).toContain('require redeployment');
    expect(connectorConfigurationEffectText('hosted')).toContain('next Connector call');
    expect(connectionDeleteLabel('local')).toBe('Delete local credentials');
    expect(connectorConfigurationEffectText('local')).toContain('app restart');
  });

  it('selects the declared auth method and falls back to the manifest default', () => {
    const auth = {
      fields: [],
      defaultMethod: 'googleOAuth',
      methods: [
        {id: 'googleOAuth', displayName: 'Google OAuth', description: 'Recommended', recommended: true, type: 'oauth2', fields: [{name: 'access_token'}]},
        {id: 'workspaceServiceAccount', displayName: 'Workspace service account', description: 'Advanced', type: 'serviceAccount', fields: [{name: 'service_account_key'}]},
      ],
    } as never;
    expect(selectedManifestAuth(auth, 'workspaceServiceAccount').id).toBe('workspaceServiceAccount');
    expect(selectedManifestAuth(auth, 'missing').id).toBe('googleOAuth');
  });

  it('shows non-secret manifest defaults parenthetically and never exposes secret defaults', () => {
    expect(manifestFieldDefaultText({name: 'endpoint', type: 'url', description: '', required: false, default: 'https://api.example.com/v1'})).toBe('(Default: https://api.example.com/v1)');
    expect(manifestFieldDefaultText({name: 'limits', type: 'stringMap', description: '', required: false, default: {rows: 10}})).toBe('(Default: {"rows":10})');
    expect(manifestFieldDefaultText({name: 'api_key', type: 'secretString', description: '', required: true, default: 'never-render'})).toBeUndefined();
  });

  it('renders required Connector fields before an expanded optional settings group', () => {
    const markup = renderConnectorForm({
      metadata: {displayName: 'Stripe', description: 'Stripe connection'},
      spec: {
        provider: 'stripe',
        configuration: {fields: [
          {name: 'endpoint', type: 'url', description: 'API endpoint.', required: false, default: 'https://api.stripe.com/v1'},
          {name: 'required_with_default', type: 'integer', description: 'Defaulted limit.', required: true, default: 1048576},
          {name: 'nickname', type: 'string', description: 'Optional label.', required: false},
        ]},
        auth: {type: 'apiKey', fields: [
          {name: 'secret_key', type: 'secretString', description: 'Stripe key.', required: true},
          {name: 'webhook_secret', type: 'secretString', description: 'Webhook secret.', required: true},
        ]},
      },
    });

    expect(markup.indexOf('<legend>Required</legend>')).toBeLessThan(markup.indexOf('secret_key *'));
    expect(markup.indexOf('secret_key *')).toBeLessThan(markup.indexOf('webhook_secret *'));
    expect(markup.indexOf('webhook_secret *')).toBeLessThan(markup.indexOf('<legend>Optional settings (3)</legend>'));
    expect(markup.indexOf('endpoint')).toBeLessThan(markup.indexOf('required_with_default'));
    expect(markup.indexOf('required_with_default')).toBeLessThan(markup.indexOf('nickname'));
    expect(markup).toMatch(/<span>secret_key \*<\/span><input[^>]*required=""[^>]*type="password"/);
    expect(markup).toMatch(/<span>required_with_default<\/span><input(?![^>]*required="")[^>]*type="text"/);
    expect(markup).toContain('(Default: 1048576)');
  });

  it('keeps OAuth fields first and omits mapped and derived credentials', () => {
    const markup = renderConnectorForm({
      metadata: {displayName: 'OAuth Connector', description: 'OAuth connection'},
      spec: {
        provider: 'oauth-provider',
        configuration: {fields: [
          {name: 'endpoint', type: 'url', description: 'API endpoint.', required: false, default: 'https://api.example.com'},
        ]},
        auth: {
          type: 'oauth2',
          fields: [
            {name: 'access_token', type: 'secretString', description: 'Mapped token.', required: true},
            {name: 'primary_email', type: 'string', description: 'Derived identity.', required: true},
            {name: 'app_token', type: 'secretString', description: 'Additional token.', required: true},
          ],
          oauth2: {
            scopes: ['messages:write'],
            credentialMappings: [{credential: 'access_token', source: 'access_token'}],
            credentialDerivations: [{credential: 'primary_email', endpoint: 'https://api.example.com/me', source: 'email'}],
          },
        },
      },
    });

    expect(markup.indexOf('OAuth client ID *')).toBeLessThan(markup.indexOf('OAuth client secret *'));
    expect(markup.indexOf('OAuth client secret *')).toBeLessThan(markup.indexOf('app_token *'));
    expect(markup.indexOf('app_token *')).toBeLessThan(markup.indexOf('<legend>Optional settings (1)</legend>'));
    expect(markup).not.toContain('access_token');
    expect(markup).not.toContain('primary_email');
  });

  it('omits empty required and optional Connector field groups', () => {
    const requiredOnlyMarkup = renderConnectorForm({
      metadata: {displayName: 'Required', description: 'Required fields'},
      spec: {
        provider: 'required', configuration: {fields: []},
        auth: {type: 'apiKey', fields: [{name: 'api_key', type: 'secretString', description: 'API key.', required: true}]},
      },
    });
    const optionalOnlyMarkup = renderConnectorForm({
      metadata: {displayName: 'Optional', description: 'Optional fields'},
      spec: {
        provider: 'optional',
        configuration: {fields: [{name: 'endpoint', type: 'url', description: 'API endpoint.', required: false}]},
        auth: {type: 'apiKey', fields: []},
      },
    });

    expect(requiredOnlyMarkup).toContain('<legend>Required</legend>');
    expect(requiredOnlyMarkup).not.toContain('Optional settings');
    expect(optionalOnlyMarkup).not.toContain('<legend>Required</legend>');
    expect(optionalOnlyMarkup).toContain('<legend>Optional settings (1)</legend>');
  });

  it('turns provider guidance URLs into explicit links', () => {
    expect(descriptionParts('Start at https://example.com/settings/keys and copy the value.')).toEqual([
      {text: 'Start at '},
      {text: 'https://example.com/settings/keys', url: 'https://example.com/settings/keys'},
      {text: ' and copy the value.'},
    ]);
  });

  it('keys named connections independently and maps visible status', () => {
    expect(connectionKey({ connectorId: 'gmail', connectionName: 'sender' } as never)).toBe('gmail\u0000sender');
    expect(connectionKey({ connectorId: 'gmail', connectionName: 'support' } as never)).toBe('gmail\u0000support');
    expect(studioState('Ready')).toBe('connected');
    expect(studioState('Expired')).toBe('expired');
    expect(studioState('Missing')).toBe('not_configured');
  });

  it('binds writes to CSRF and the current definition revision', () => {
    expect(connectorWriteHeaders({ csrfToken: 'csrf', definitionRevision: 'sha256:current' } as never)).toEqual({
      'Content-Type': 'application/json',
      'X-Dex-CSRF-Token': 'csrf',
      'X-Dex-Flow-Definition-Revision': 'sha256:current',
    });
  });

  it('accepts iframe commands only for the active connector session', () => {
    const session = { connectorId: 'gmail', sessionNonce: 'nonce' } as never;
    expect(isStudioCommand({
      type: 'connector.command', protocolVersion: '0.2.0', sessionNonce: 'nonce',
      connectorId: 'gmail', requestId: 'request', command: 'oauth.connect',
    }, session)).toBe(true);
    expect(isStudioCommand({
      type: 'connector.command', protocolVersion: '0.2.0', sessionNonce: 'other',
      connectorId: 'gmail', requestId: 'request', command: 'oauth.connect',
    }, session)).toBe(false);
    expect(isStudioCommand({
      type: 'connector.command', protocolVersion: '0.2.0', sessionNonce: 'nonce',
      connectorId: 'gmail', requestId: 'request', command: 'credential.read',
    }, session)).toBe(false);
  });

  it('accepts bounded iframe heights only from the active connector session', () => {
    const session = { connectorId: 'slack', sessionNonce: 'nonce' } as never;
    const resize = {
      type: 'connector.frame.resize', protocolVersion: '0.2.0', sessionNonce: 'nonce',
      connectorId: 'slack', height: 384,
    };
    expect(isStudioFrameResize(resize, session)).toBe(true);
    expect(isStudioFrameResize({...resize, sessionNonce: 'other'}, session)).toBe(false);
    expect(isStudioFrameResize({...resize, height: 4097}, session)).toBe(false);
  });

  it('sends the Dex Web theme, its tokens, and the Studio stylesheet as optional fields of the 0.2.0 ready message', () => {
    const session = {
      connectorId: 'slack', sessionNonce: 'nonce',
      manifest: { spec: { studio: { setup: { backendCapabilities: ['use.configuration.write'] } } } },
    } as never;
    const connection = { connectorId: 'slack', connectionName: 'workspace', status: 'Ready' } as never;
    const appearance = { theme: 'dark', themeTokens: { '--studio-cta': '#70eea9' }, stylesheet: connectorStudioStylesheet } as const;
    const ready = connectorHostReadyMessage(session, connection, { kind: 'connection' }, appearance);
    expect(ready).toEqual({
      type: 'connector.host.ready', protocolVersion: '0.2.0', sessionNonce: 'nonce', connectorId: 'slack',
      capabilities: ['use.configuration.write'],
      connection: { state: 'connected', grantedScopes: [], detail: 'Ready' },
      target: { kind: 'connection' },
      theme: 'dark',
      themeTokens: { '--studio-cta': '#70eea9' },
      stylesheet: connectorStudioStylesheet,
    });
    expect(ready.stylesheet).toContain('.studio-button {');
    expect(structuredClone(ready)).toEqual(ready);
  });

  it('advertises only host capabilities that are implemented', () => {
    expect(studioHostCapabilities({
      manifest: { spec: { studio: {
        setup: { backendCapabilities: ['oauth.connection.manage', 'use.configuration.write', 'provider.resources-list', 'credential.read'] },
        commands: [{id: 'listResources', capability: 'provider.resources-list'}],
      } } },
    } as never)).toEqual(['oauth.connection.manage', 'use.configuration.write', 'provider.resources-list']);
  });

  it('reports provider command failures only to the Studio frame and save failures to both', () => {
    const connection = { connectorId: 'llm', connectionName: 'default' } as never;
    const session = { connectorId: 'llm', connectionName: 'default', sessionNonce: 'nonce' } as never;
    const frameResult = (requestId: string, message: string) => ({
      type: 'connector.command.result', protocolVersion: '0.2.0', sessionNonce: 'nonce',
      connectorId: 'llm', requestId, ok: false, error: { code: 'COMMAND_FAILED', message },
    });
    const providerCommandFailures = [
      new DexAPIError('Connector provider command failed', 502, undefined, 'CONNECTOR_PROVIDER_COMMAND_FAILED'),
      new DexAPIError('Connector UI session is missing or expired', 404, undefined, 'CONNECTOR_UI_SESSION_NOT_FOUND'),
      new DexAPIError('Connector Studio command parameters are invalid', 400, undefined, 'CONNECTOR_STUDIO_COMMAND_INVALID'),
      new Error('parameters must contain string values'),
      new TypeError('Failed to fetch'),
    ];
    for (const commandError of providerCommandFailures) {
      const outcome = studioCommandFailureOutcome(connection, session, { requestId: 'list', command: 'provider.command.execute' }, commandError);
      expect(outcome.pageBannerMessage).toBeUndefined();
      expect(outcome.frameResult).toEqual(frameResult('list', commandError.message));
    }
    const staleRevision = new DexAPIError('Flow Definition revision is stale', 409, undefined, 'FLOW_DEFINITION_REVISION_CONFLICT');
    expect(studioCommandFailureOutcome(connection, session, { requestId: 'save', command: 'use.configuration.save' }, staleRevision)).toEqual({
      pageBannerMessage: 'Flow Definition revision is stale',
      frameResult: frameResult('save', 'Flow Definition revision is stale'),
    });
    expect(studioCommandFailureOutcome(connection, session, { requestId: 'save', command: 'use.configuration.save' }, 'offline')).toEqual({
      pageBannerMessage: 'Connector request failed',
      frameResult: frameResult('save', 'Connector request failed'),
    });
  });

  it('orders authorization before configurable operations and triggers', () => {
    const connection = {
      connectionName: 'workspace',
      status: 'Ready',
      uses: [
        {flowName: 'ReadThread', stepId: 'read', operationId: 'listMessages', operationKind: 'query', configured: false, configurationUI: {units: []}},
        {flowName: 'PostCompletion', stepId: 'post', operationId: 'postReply', operationKind: 'mutation', configured: false, configurationUI: {units: [{id: 'reply'}]}},
      ],
      triggerUses: [
        {flowName: 'Approval', bindingName: 'reply', triggerName: 'threadReplyCreated', configured: true, configurationUI: {units: [{id: 'channel'}]}},
      ],
    } as never;
    expect(connectorSetupTabs(connection).map((tab) => [tab.kind, tab.label, tab.configured])).toEqual([
      ['authorize', 'Authorize', true],
      ['operation', 'postReply', false],
      ['trigger', 'threadReplyCreated', true],
    ]);
    expect(initialConnectorSetupTabKey(connection)).toBe('operation:PostCompletion:post');
  });

  it('keeps configuration tabs behind authorization and opens the first tab when all are configured', () => {
    const missing = {
      connectionName: 'workspace', status: 'Missing',
      uses: [{flowName: 'Flow', stepId: 'step', operationId: 'send', operationKind: 'mutation', configured: false, configurationUI: {units: [{id: 'message'}]}}],
      triggerUses: [],
    };
    expect(initialConnectorSetupTabKey(missing as never)).toBe('authorize');
    const ready = {
      ...missing, status: 'Ready', uses: missing.uses.map((use: {configured: boolean}) => ({...use, configured: true})),
    };
    expect(initialConnectorSetupTabKey(ready as never)).toBe('operation:Flow:step');
  });
});

function renderConnectorForm(manifest: unknown): string {
  return renderToStaticMarkup(createElement(ConnectorForm, {
    catalog: {} as never,
    connection: {connectorId: 'test-connector'} as never,
    session: {manifest} as never,
    onConfigured: async () => undefined,
    onError: () => undefined,
  }));
}
