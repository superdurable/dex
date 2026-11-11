// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readFileSync } from 'node:fs';
import type { FlowDefinitionGraph } from '@superdurable/flow-definition-renderer';
import { describe, expect, it } from 'vitest';
import type { V2Catalog, V2CatalogEntry } from '@/lib/types';
import { agentSessionFlowTypes } from './NewSessionDialog';
import {
  agentSessionSchema,
  decodeAgentToolCall,
  decodePendingInterrupts,
  REQUIRED_SESSION_ATTRIBUTES,
  SESSION_RPC,
} from './sessionSchema';

/** The graph Dex Agents publishes; a rename there fails here first. */
function publishedSessionGraph(): FlowDefinitionGraph {
  const path = new URL('../../../agents/conformance/session_graph.json', import.meta.url);
  return JSON.parse(readFileSync(path, 'utf8')) as FlowDefinitionGraph;
}

/** A catalog entry as Dex Web's server describes the published session graph. */
function sessionEntry(flowType: string, overrides: Partial<V2CatalogEntry> = {}): V2CatalogEntry {
  const graph = publishedSessionGraph();
  return {
    flowType,
    definition: graph.v2!,
    definitionRevision: 'sha256:r',
    agentSession: {
      language: graph.source.language,
      hasQuestions: graph.nodes.some((node) => node.kind === 'step' && node.name === 'AwaitQuestionAnswer'),
      rpcNames: graph.nodes.filter((node) => node.kind === 'rpc').map((node) => node.name),
    },
    ...overrides,
  };
}

describe('agentSessionSchema', () => {
  it('recognises the published session graph and its four Actions', () => {
    const schema = agentSessionSchema(sessionEntry('SupportAgent'));
    expect(schema).not.toBeNull();
    expect(schema?.resolveInterrupt?.rpcName).toBe(SESSION_RPC.resolveInterrupt);
    expect(schema?.answerQuestion?.rpcName).toBe(SESSION_RPC.answerQuestion);
    expect(schema?.steerUserMessage?.rpcName).toBe(SESSION_RPC.steerUserMessage);
    expect(schema?.cancelTurn?.rpcName).toBe(SESSION_RPC.cancelTurn);
    expect(schema?.hasQuestions).toBe(true);
    expect(schema?.rpcNames).toEqual(expect.arrayContaining([SESSION_RPC.sendUserMessage, SESSION_RPC.updateSessionSettings]));
  });

  it('offers New session only for entries the server marks as sessions', () => {
    const catalog: V2Catalog = {
      enabled: true, definitionRevision: 'r',
      flows: [sessionEntry('SupportAgent'), sessionEntry('ImpostorAgent', { agentSession: undefined })],
    };
    expect(agentSessionFlowTypes(catalog)).toEqual(['SupportAgent']);
  });

  it('declares every Attribute the view requires', () => {
    const names = publishedSessionGraph().nodes.filter((node) => node.kind === 'attribute').map((node) => node.name);
    for (const name of REQUIRED_SESSION_ATTRIBUTES) expect(names).toContain(name);
  });

  it('reads a reduced session that offers only approvals', () => {
    const entry = sessionEntry('GoAgent');
    const reduced = sessionEntry('GoAgent', {
      definition: { ...entry.definition, actions: entry.definition.actions.filter((action) => action.rpcName === 'resolve_interrupt') },
      agentSession: { ...entry.agentSession!, language: 'go', hasQuestions: false },
    });
    const schema = agentSessionSchema(reduced);
    expect(schema?.language).toBe('go');
    expect(schema?.resolveInterrupt).toBeDefined();
    expect(schema?.steerUserMessage).toBeUndefined();
    expect(schema?.cancelTurn).toBeUndefined();
    expect(schema?.hasQuestions).toBe(false);
  });

  it('rejects an entry the server does not mark as a session', () => {
    expect(agentSessionSchema(sessionEntry('PlainFlow', { agentSession: undefined }))).toBeNull();
    expect(agentSessionSchema(null)).toBeNull();
  });
});

describe('decoders', () => {
  it('ignores unknown fields and keeps unknown enum values', () => {
    const record = decodeAgentToolCall({
      tool_call_id: 'c1', tool_name: 'refund', status: 'paused_by_future_release', effect: 'read',
      arguments: '{}', requires_approval: false, a_field_from_later: 1,
    });
    expect(record?.status).toBe('paused_by_future_release');
  });

  it('refuses a record without its identity', () => {
    expect(decodeAgentToolCall({ tool_name: 'refund', status: 'succeeded' })).toBeNull();
    expect(decodePendingInterrupts([{ kind: 'tool_approval' }])).toBeNull();
    expect(decodePendingInterrupts(null)).toEqual([]);
  });
});
