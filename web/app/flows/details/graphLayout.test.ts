// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import type { Edge, Node } from '@xyflow/react';
import { describe, expect, it } from 'vitest';
import { graphBounds, layoutGraph } from './graphLayout';

function testNode(id: string): Node<Record<string, unknown>> {
  return {
    id,
    data: {},
    position: { x: 0, y: 0 },
    style: { height: 116, width: 300 },
  };
}

describe('step graph layout', () => {
  it('keeps a 90-execution fan-out and fan-in flow narrow and vertically readable', () => {
    const nodes: Array<Node<Record<string, unknown>>> = [testNode('start')];
    const edges: Edge[] = [];
    let parent = 'start';
    for (let index = 1; index <= 30; index += 1) {
      const id = `serial-before-${index}`;
      nodes.push(testNode(id));
      edges.push({ id: `${parent}->${id}`, source: parent, target: id });
      parent = id;
    }

    const parallelIDs = Array.from({ length: 12 }, (_, index) => `parallel-${index + 1}`);
    for (const id of parallelIDs) {
      nodes.push(testNode(id));
      edges.push({ id: `${parent}->${id}`, source: parent, target: id });
    }
    nodes.push(testNode('fan-in'));
    for (const id of parallelIDs) {
      edges.push({ id: `${id}->fan-in`, source: id, target: 'fan-in' });
    }
    parent = 'fan-in';

    for (let index = 1; index <= 47; index += 1) {
      const id = `serial-after-${index}`;
      nodes.push(testNode(id));
      edges.push({ id: `${parent}->${id}`, source: parent, target: id });
      parent = id;
    }

    const layouted = layoutGraph(nodes, edges);
    const bounds = graphBounds(layouted);
    const positions = new Set(layouted.map((node) => `${node.position.x}:${node.position.y}`));
    const parallelRows = new Map<number, number>();
    for (const node of layouted.filter((node) => node.id.startsWith('parallel-'))) {
      parallelRows.set(node.position.y, (parallelRows.get(node.position.y) ?? 0) + 1);
    }

    expect(layouted).toHaveLength(91);
    expect(positions.size).toBe(91);
    expect(bounds.width).toBeLessThanOrEqual(700);
    expect(bounds.height).toBeGreaterThan(14_000);
    expect(Math.max(...parallelRows.values())).toBeLessThanOrEqual(2);
    expect(parallelRows.size).toBe(6);
  });

  it('keeps uneven fan-out branches together', () => {
    const nodes = [
      testNode('source'),
      ...Array.from({ length: 6 }, (_, index) => testNode(`branch-${index + 1}`)),
      testNode('long-2'),
      testNode('long-3'),
      testNode('terminal'),
    ];
    const edges: Edge[] = Array.from({ length: 6 }, (_, index) => ({
      id: `source->branch-${index + 1}`,
      source: 'source',
      target: `branch-${index + 1}`,
    }));
    edges.push(
      { id: 'branch-1->long-2', source: 'branch-1', target: 'long-2' },
      { id: 'long-2->long-3', source: 'long-2', target: 'long-3' },
      { id: 'long-3->terminal', source: 'long-3', target: 'terminal' },
      ...Array.from({ length: 5 }, (_, index) => ({
        id: `branch-${index + 2}->terminal`,
        source: `branch-${index + 2}`,
        target: 'terminal',
      })),
    );

    const layouted = layoutGraph(nodes, edges);
    const branchRows = new Set(layouted
      .filter((node) => node.id.startsWith('branch-'))
      .map((node) => node.position.y));

    expect(branchRows.size).toBe(3);
    expect(Math.max(...branchRows) - Math.min(...branchRows)).toBe(360);
  });
});
