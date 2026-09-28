// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import { decodeFlow } from './decode';
import type { FlowDefinitionGraph } from './fdg';

function stepNode(id: string, name: string, displayName?: string, start = false) {
  return {
    id,
    kind: 'step',
    name,
    phase: 'execute',
    start,
    metadata: displayName === undefined ? undefined : { displayName },
  };
}

describe('decodeFlow Step names', () => {
  it('keys Steps by registered Step type, labels them by displayName, and orders them by label', () => {
    const graph: FlowDefinitionGraph = {
      schemaVersion: '2.0',
      valid: true,
      source: { language: 'go', path: 'workflow.go' },
      flow: { name: 'fixture.Flow', startStepId: 'step:start' },
      nodes: [
        stepNode('step:omega', 'fixture.omega', 'omega'),
        stepNode('step:Bravo', 'Bravo'),
        stepNode('step:start', 'fixture.start', 'start', true),
        stepNode('step:alpha', 'zzz.alpha', 'alpha'),
      ],
      edges: [],
      diagnostics: [],
    };

    const flow = decodeFlow(graph, { generated: true, note: 'test' });

    expect(flow.steps.map((step) => [step.id, step.stepType, step.label])).toEqual([
      ['step:start', 'fixture.start', 'start'],
      ['step:alpha', 'zzz.alpha', 'alpha'],
      ['step:Bravo', 'Bravo', 'Bravo'],
      ['step:omega', 'fixture.omega', 'omega'],
    ]);
  });
});

describe('decodeFlow RPC contracts', () => {
  it('joins Flow v2 Actions and views to their RPCs by RPC name', () => {
    const flow = decodeFlow(entityListGraph(), { generated: true, note: 'test' });

    expect(flow.entries.map((entry) => [entry.name, entry.kind, entry.action, entry.views])).toEqual([
      ['AddEntry', 'rpc', undefined, []],
      ['GetDexDisplay', 'rpc', undefined, ['display']],
      ['GetDexSummary', 'rpc', undefined, ['summary']],
      ['OnFlowTimeout', 'timeoutHandler', undefined, []],
      ['RemoveEntry', 'rpc', { label: 'Remove entry', requiredPermission: 'entries.manage' }, []],
    ]);
  });

  it('marks no RPC when the definition has no Flow v2 section', () => {
    const flow = decodeFlow({ ...entityListGraph(), v2: undefined }, { generated: true, note: 'test' });

    expect(flow.entries.every((entry) => entry.action === undefined && entry.views.length === 0)).toBe(true);
  });

  it('keeps the resource references of a Flow with no Steps on its RPCs', () => {
    const flow = decodeFlow(entityListGraph(), { generated: true, note: 'test' });
    const addEntry = flow.entries.find((entry) => entry.name === 'AddEntry');

    expect(flow.steps).toEqual([]);
    expect(flow.transitions).toEqual([]);
    expect(flow.dropped).toEqual([]);
    expect(addEntry?.resources.map((ref) => [ref.access, ref.resourceId])).toEqual([
      ['read', 'resource:attribute:entries'],
      ['write', 'resource:attribute:entries'],
      ['write', 'resource:attribute:entryCount'],
      ['publish', 'resource:channel:entryEvents'],
    ]);
    expect(addEntry?.opensGates).toEqual([]);
  });
});

/** A step-less entity Flow in the shape `dexcli visualize` emits for a Go Flow with only RPCs. */
function entityListGraph(): FlowDefinitionGraph {
  const rpc = (name: string) => [
    { id: `rpc:${name}`, kind: 'rpc', name },
    { id: `decision:rpc:${name}`, kind: 'decision', name: 'rpcResult', parentId: `rpc:${name}`, decision: { type: 'rpcResult' } },
  ];
  const edges: [string, string, string][] = [
    ['resource_read', 'resource:attribute:entries', 'rpc:AddEntry'],
    ['resource_write', 'rpc:AddEntry', 'resource:attribute:entries'],
    ['resource_write', 'rpc:AddEntry', 'resource:attribute:entryCount'],
    ['resource_publish', 'rpc:AddEntry', 'resource:channel:entryEvents'],
    ['resource_write', 'rpc:RemoveEntry', 'resource:attribute:entries'],
    ['resource_read', 'resource:attribute:entryCount', 'rpc:GetDexSummary'],
    ['resource_read', 'resource:attribute:entries', 'rpc:GetDexDisplay'],
  ];
  return {
    schemaVersion: '2.0',
    valid: true,
    source: { language: 'go', path: 'entity_list_flow.go' },
    flow: { name: 'EntityListFlow' },
    nodes: [
      { id: 'resource:attribute:entries', kind: 'attribute', name: 'entries', resource: { valueType: '[]string' } },
      { id: 'resource:attribute:entryCount', kind: 'attribute', name: 'entry-count', resource: { valueType: 'int64' } },
      { id: 'resource:channel:entryEvents', kind: 'channel', name: 'entry-events', resource: { valueType: 'string' } },
      ...['AddEntry', 'RemoveEntry', 'GetDexSummary', 'GetDexDisplay'].flatMap(rpc),
      { id: 'timeout_handler:OnFlowTimeout', kind: 'timeout_handler', name: 'OnFlowTimeout' },
    ],
    edges: edges.map(([kind, from, to], i) => ({ id: `edge:${i}`, kind, from, to, metadata: { phase: 'rpc' } })),
    diagnostics: [],
    v2: {
      summary: { rpcName: 'GetDexSummary' },
      display: { rpcName: 'GetDexDisplay' },
      actions: [
        { rpcName: 'RemoveEntry', label: 'Remove entry', requiredPermission: 'entries.manage' },
        { rpcName: 'ArchiveEntry', label: 'Archive', requiredPermission: 'entries.manage' },
      ],
    },
  };
}
