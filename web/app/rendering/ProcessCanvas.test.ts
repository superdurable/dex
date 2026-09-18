// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import {
  buildProcessCanvasScene,
  type FlowDefinitionGraph,
} from '@superdurable/flow-definition-renderer';

describe('Process Canvas', () => {
  it('builds ordered group bands and collapses nested control edges to Steps', () => {
    const scene = buildProcessCanvasScene(graph, 'collapsed', 'tb');

    expect(scene.nodes.map((node) => node.id)).toEqual([
      'group:intake', 'step:start', 'group:control', 'step:review',
    ]);
    expect(scene.edges).toHaveLength(1);
    expect(scene.edges[0]).toMatchObject({
      source: 'step:start', target: 'step:review', label: '2 paths',
      sourceHandle: 'vertical-source', targetHandle: 'vertical-target',
    });
  });

  it('changes group-band direction and expanded Step dimensions', () => {
    const topDown = buildProcessCanvasScene(graph, 'collapsed', 'tb');
    const leftRight = buildProcessCanvasScene(graph, 'expanded', 'lr');
    const firstTopDownGroup = topDown.nodes.find((node) => node.id === 'group:intake');
    const secondTopDownGroup = topDown.nodes.find((node) => node.id === 'group:control');
    const firstLeftRightGroup = leftRight.nodes.find((node) => node.id === 'group:intake');
    const secondLeftRightGroup = leftRight.nodes.find((node) => node.id === 'group:control');
    const expandedStep = leftRight.nodes.find((node) => node.id === 'step:start');

    expect(secondTopDownGroup?.position.y).toBeGreaterThan(firstTopDownGroup?.position.y ?? 0);
    expect(secondLeftRightGroup?.position.x).toBeGreaterThan(firstLeftRightGroup?.position.x ?? 0);
    expect(expandedStep?.style?.width).toBe(284);
    expect(expandedStep?.style?.height).toBe(172);
    expect(leftRight.edges[0].sourceHandle).toBe('horizontal-source');
  });

  it('ignores unknown nodes, dangling edges, and malformed group entries', () => {
    const hostile = {
      ...graph,
      groups: [
        { id: 42, label: { unsafe: true }, stepIds: ['missing'] },
        { id: 'safe', label: 'Safe', stepIds: ['step:start', 'step:start'] },
      ],
      edges: [...graph.edges, { id: 'dangling', kind: 'transition', from: 'missing', to: 'step:start' }],
    } as unknown as FlowDefinitionGraph;

    const scene = buildProcessCanvasScene(hostile);

    expect(scene.nodes.map((node) => node.id)).toEqual([
      'group:safe', 'step:start', 'group:ungrouped', 'step:review',
    ]);
    expect(scene.edges.every((edge) => edge.source !== 'missing')).toBe(true);
  });
});

const graph: FlowDefinitionGraph = {
  schemaVersion: '2.0',
  valid: true,
  source: { language: 'go', path: 'refund.go' },
  flow: { name: 'RefundFlow', startStepId: 'step:start' },
  nodes: [
    { id: 'step:start', kind: 'step', name: 'Start', start: true },
    { id: 'wait:start', kind: 'wait', name: 'Wait', parentId: 'step:start', wait: {
      type: 'allOf', conditions: [{ kind: 'timer', label: 'review window' }],
    } },
    { id: 'step:review', kind: 'step', name: 'Review' },
    { id: 'unknown:target', kind: 'unknown', name: 'Unknown' },
  ],
  edges: [
    { id: 'first', kind: 'transition', from: 'wait:start', to: 'step:review' },
    { id: 'second', kind: 'transition', from: 'step:start', to: 'step:review' },
    { id: 'resource', kind: 'uses', from: 'step:start', to: 'unknown:target' },
  ],
  diagnostics: [],
  groups: [
    { id: 'intake', label: 'Intake', stepIds: ['step:start'] },
    { id: 'control', label: 'Control', stepIds: ['step:review'] },
  ],
  supervision: {
    indexedAttributes: [],
    summary: { rpcName: 'GetDexSummary', fields: [] },
    display: { rpcName: 'GetDexDisplay', fields: [] },
    actions: [],
  },
};
