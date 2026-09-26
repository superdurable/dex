// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import {
  connectionKey,
  connectorSetupTabs,
  connectorWriteHeaders,
  initialConnectorSetupTabKey,
  isStudioCommand,
  studioHostCapabilities,
  studioState,
} from './ConnectionsPage';

describe('Connections contract', () => {
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

  it('advertises only host capabilities that are implemented', () => {
    expect(studioHostCapabilities({
	  manifest: { spec: { studio: { setup: { backendCapabilities: ['oauth.connection.manage', 'use.configuration.write', 'slack.channels-list', 'slack.users-list', 'credential.read'] } } } },
	} as never)).toEqual(['oauth.connection.manage', 'use.configuration.write', 'slack.channels-list', 'slack.users-list']);
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
