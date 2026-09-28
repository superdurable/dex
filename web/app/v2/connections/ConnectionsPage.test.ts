// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { DexAPIError } from '@/lib/http';
import {
  connectionKey,
  connectorHostReadyMessage,
  connectorSetupTabs,
  connectorWriteHeaders,
  initialConnectorSetupTabKey,
  isStudioCommand,
  isStudioFrameResize,
  descriptionParts,
  manifestFieldDefaultText,
  studioCommandFailureOutcome,
  studioHostCapabilities,
  studioState,
} from './ConnectionsPage';
import { connectorStudioStylesheet } from './connectorStudioTheme';

describe('Connections contract', () => {
  it('shows non-secret manifest defaults parenthetically and never exposes secret defaults', () => {
    expect(manifestFieldDefaultText({name: 'endpoint', type: 'url', description: '', required: false, default: 'https://api.example.com/v1'})).toBe('(Default: https://api.example.com/v1)');
    expect(manifestFieldDefaultText({name: 'limits', type: 'stringMap', description: '', required: false, default: {rows: 10}})).toBe('(Default: {"rows":10})');
    expect(manifestFieldDefaultText({name: 'api_key', type: 'secretString', description: '', required: true, default: 'never-render'})).toBeUndefined();
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
