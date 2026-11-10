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

describe('agentSessionSchema', () => {
  it('recognises the published session graph and its four Actions', () => {
    const schema = agentSessionSchema(publishedSessionGraph(), undefined);
    expect(schema).not.toBeNull();
    expect(schema?.resolveInterrupt?.rpcName).toBe(SESSION_RPC.resolveInterrupt);
    expect(schema?.answerQuestion?.rpcName).toBe(SESSION_RPC.answerQuestion);
    expect(schema?.steerUserMessage?.rpcName).toBe(SESSION_RPC.steerUserMessage);
    expect(schema?.cancelTurn?.rpcName).toBe(SESSION_RPC.cancelTurn);
    expect(schema?.hasQuestions).toBe(true);
  });

  it('declares every Attribute the view requires', () => {
    const names = publishedSessionGraph().nodes.filter((node) => node.kind === 'attribute').map((node) => node.name);
    for (const name of REQUIRED_SESSION_ATTRIBUTES) expect(names).toContain(name);
  });

  it('reads a reduced session that offers only approvals', () => {
    const graph = publishedSessionGraph();
    const reduced: FlowDefinitionGraph = {
      ...graph,
      source: { language: 'go', path: 'session_flow.go' },
      nodes: graph.nodes.filter((node) => node.name !== 'AwaitQuestionAnswer'),
      v2: { ...graph.v2!, actions: graph.v2!.actions.filter((action) => action.rpcName === 'resolve_interrupt') },
    };
    const schema = agentSessionSchema(reduced, undefined);
    expect(schema?.resolveInterrupt).toBeDefined();
    expect(schema?.steerUserMessage).toBeUndefined();
    expect(schema?.cancelTurn).toBeUndefined();
    expect(schema?.hasQuestions).toBe(false);
  });

  it('rejects a Flow without the session Attributes', () => {
    const graph = publishedSessionGraph();
    expect(agentSessionSchema({ ...graph, nodes: graph.nodes.filter((node) => node.name !== 'AgentToolCalls') }, undefined)).toBeNull();
    expect(agentSessionSchema({ ...graph, valid: false }, undefined)).toBeNull();
    expect(agentSessionSchema(undefined, undefined)).toBeNull();
  });

  it('rejects a session Attribute that is not an AttributeMap where one is required', () => {
    const graph = publishedSessionGraph();
    const nodes = graph.nodes.map((node) => node.name === 'AgentMessages'
      ? { ...node, resource: { valueType: 'AgentMessage' } }
      : node);
    expect(agentSessionSchema({ ...graph, nodes }, undefined)).toBeNull();
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
