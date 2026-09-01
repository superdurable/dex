// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import { describe, expect, it } from 'vitest';
import {
  buildDefinitionScene,
  type DefinitionVisibility,
  type FlowDefinitionGraph,
} from '@superdurable/flow-definition-renderer';

const visible: DefinitionVisibility = {
  control: true,
  waits: true,
  handlers: true,
  attributes: true,
  channels: true,
  streams: false,
  subflows: true,
};

describe('Flow Definition Graph layout', () => {
  it('nests WaitFor above Execute and keeps resources in their rails', () => {
    const scene = buildDefinitionScene(graph, visible);
    const flow = requiredNode(scene, 'definition:flow');
    const start = requiredNode(scene, 'step:start');
    const wait = requiredNode(scene, 'wait:start:10:1');
    const decision = requiredNode(scene, 'decision:step:start:20:1');
    const firstChannel = requiredNode(scene, 'resource:channel:first');
    const secondChannel = requiredNode(scene, 'resource:channel:second');
    const attributes = requiredNode(scene, 'definition:attributes');
    const rpc = requiredNode(scene, 'rpc:approve');
    const timeout = requiredNode(scene, 'timeout_handler:ExampleFlow');
    const subflow = requiredNode(scene, 'subflow:Child:11');

    expect(start.parentId).toBe(flow.id);
    expect(wait.parentId).toBe(start.id);
    expect(decision.parentId).toBe(start.id);
    expect(wait.position.y).toBeLessThan(decision.position.y);
    expect(firstChannel.position.x).toBe(secondChannel.position.x);
    expect(firstChannel.position.y).toBeLessThan(secondChannel.position.y);
    expect(attributes.data.definitions).toHaveLength(2);
    expect(rpc.position.x).toBeLessThan(0);
    expect(timeout.type).toBe('definitionTimeout');
    expect(timeout.data.kind).toBe('timeout');
    expect(overlaps(rpc, firstChannel)).toBe(false);
    expect(overlaps(rpc, attributes)).toBe(false);
    expect(firstChannel.position.x - (rpc.position.x + Number(rpc.style?.width))).toBeGreaterThanOrEqual(40);
    expect(subflow.parentId).toBe(flow.id);
  });

  it('uses solid recovery and dashed resource and SubFlow relations', () => {
    const scene = buildDefinitionScene(graph, visible);
    const recovery = scene.edges.find((edge) => edge.id === 'recovery')!;
    const publish = scene.edges.find((edge) => edge.id === 'publish')!;
    const subflow = scene.edges.find((edge) => edge.id === 'subflow')!;

    expect(recovery.style?.strokeDasharray).toBeUndefined();
    expect(recovery.style?.stroke).toBe('#c43b62');
    expect(recovery.label).toContain('skip WaitFor');
    expect(recovery.sourceHandle).toBe('step-recovery-source');
    expect(recovery.targetHandle).toBe('step-target');
    expect(publish.style?.strokeDasharray).toBe('6 5');
    expect(subflow.style?.strokeDasharray).toBe('6 5');
  });

  it('is deterministic and keeps top-level Steps disjoint', () => {
    const first = buildDefinitionScene(graph, visible);
    const second = buildDefinitionScene(graph, visible);
    expect(second).toEqual(first);

    const steps = first.nodes.filter((node) => node.data.kind === 'step');
    for (let left = 0; left < steps.length; left += 1) {
      for (let right = left + 1; right < steps.length; right += 1) {
        expect(overlaps(steps[left], steps[right])).toBe(false);
      }
    }
    const start = requiredNode(first, 'step:start');
    const next = requiredNode(first, 'step:next');
    expect(next.position.y - (start.position.y + Number(start.style?.height))).toBeGreaterThanOrEqual(140);
  });

  it('routes self transitions through a selectable outer lane and keeps full branch text', () => {
    const scene = buildDefinitionScene(graph, visible);
    const selfTransition = scene.edges.find((edge) => edge.id === 'self-transition')!;
    const branch = scene.edges.find((edge) => edge.id.startsWith('branch:'))!;

    expect(selfTransition.source).toBe('step:start');
    expect(selfTransition.sourceHandle).toBe('step-control-outer-source');
    expect(selfTransition.targetHandle).toBe('step-control-outer-target');
    expect(selfTransition.data?.route).toBe('outer-right');
    expect(selfTransition.interactionWidth).toBeGreaterThanOrEqual(24);
    expect(branch.data?.displayLabel).toBe('input.HasAnExtremelyLongConditionThatMustRemainComplete()');
  });

  it('reserves Attribute height for row gaps and wrapped values', () => {
    const attributeNames = [
      'buyerID',
      'currentActionIndexToExecute',
      'currentState',
      'itemID',
      'pendingPreConditionName',
      'pendingPreConditionState',
      'processDefinition',
      'processID',
      'stateData',
    ];
    const denseGraph: FlowDefinitionGraph = {
      ...graph,
      nodes: [
        ...graph.nodes.filter((node) => node.kind !== 'attribute'),
        ...attributeNames.map((name, index) => ({
          id: `resource:attribute:${index}`,
          kind: 'attribute' as const,
          name,
          resource: { valueType: index === attributeNames.length - 1 ? 'map[string]string' : 'string' },
        })),
      ],
    };
    const attributes = requiredNode(buildDefinitionScene(denseGraph, visible), 'definition:attributes');

    expect(Number(attributes.style?.height)).toBeGreaterThanOrEqual(267);
  });
});

function requiredNode(scene: ReturnType<typeof buildDefinitionScene>, id: string) {
  const node = scene.nodes.find((candidate) => candidate.id === id);
  expect(node, `missing ${id}`).toBeDefined();
  return node!;
}

function overlaps(
  left: ReturnType<typeof buildDefinitionScene>['nodes'][number],
  right: ReturnType<typeof buildDefinitionScene>['nodes'][number],
): boolean {
  const leftWidth = Number(left.style?.width ?? 0);
  const leftHeight = Number(left.style?.height ?? 0);
  const rightWidth = Number(right.style?.width ?? 0);
  const rightHeight = Number(right.style?.height ?? 0);
  return left.position.x < right.position.x + rightWidth
    && left.position.x + leftWidth > right.position.x
    && left.position.y < right.position.y + rightHeight
    && left.position.y + leftHeight > right.position.y;
}

const graph: FlowDefinitionGraph = {
  schemaVersion: '1.0',
  valid: true,
  source: { language: 'go', path: 'example.go' },
  flow: { name: 'ExampleFlow', startStepId: 'step:start' },
  nodes: [
    { id: 'step:start', kind: 'step', name: 'StartStep', start: true },
    { id: 'step:next', kind: 'step', name: 'NextStep' },
    {
      id: 'wait:start:10:1', kind: 'wait', name: 'anyOf', parentId: 'step:start',
      wait: {
        type: 'anyOf',
        conditions: [
          { kind: 'channel', label: 'first.for 2', resourceId: 'resource:channel:first' },
          { kind: 'timer', label: '1 hour timer', expression: 'time.Hour' },
          { kind: 'subflow', label: 'Child', subFlowId: 'subflow:Child:11' },
        ],
      },
    },
    {
      id: 'decision:step:start:20:1', kind: 'decision', name: 'goTo', parentId: 'step:start',
      condition: 'input.HasAnExtremelyLongConditionThatMustRemainComplete()',
      decision: { type: 'goTo', checkedChannels: ['resource:channel:second'] },
    },
    {
      id: 'decision:step:start:21:1', kind: 'decision', name: 'forceFail', parentId: 'step:start',
      condition: 'otherwise', decision: { type: 'forceFail' },
    },
    {
      id: 'decision:step:next:30:1', kind: 'decision', name: 'gracefulComplete', parentId: 'step:next',
      decision: { type: 'gracefulComplete' },
    },
    { id: 'resource:channel:first', kind: 'channel', name: 'first', resource: { valueType: 'string' } },
    { id: 'resource:channel:second', kind: 'channel', name: 'second', resource: { valueType: 'string' } },
    { id: 'resource:attribute:name', kind: 'attribute', name: 'name', resource: { valueType: 'string' } },
    { id: 'resource:attribute:count', kind: 'attribute', name: 'count', resource: { valueType: 'int' } },
    { id: 'rpc:approve', kind: 'rpc', name: 'Approve' },
    { id: 'timeout_handler:ExampleFlow', kind: 'timeout_handler', name: 'handleTimeout' },
    {
      id: 'decision:timeout:40:1', kind: 'decision', name: 'goTo', parentId: 'timeout_handler:ExampleFlow', phase: 'timeout',
      decision: { type: 'goTo' },
    },
    { id: 'subflow:Child:11', kind: 'subflow', name: 'Child', external: true },
  ],
  edges: [
    { id: 'transition', kind: 'transition', from: 'decision:step:start:20:1', to: 'step:next' },
    { id: 'self-transition', kind: 'transition', from: 'decision:step:start:20:1', to: 'step:start' },
    { id: 'timeout-transition', kind: 'transition', from: 'decision:timeout:40:1', to: 'step:next' },
    { id: 'recovery', kind: 'failure_transition', from: 'step:start', to: 'step:next', metadata: { skipWaitFor: true } },
    { id: 'publish', kind: 'resource_publish', from: 'rpc:approve', to: 'resource:channel:first' },
    { id: 'consume', kind: 'wait_condition', from: 'resource:channel:first', to: 'wait:start:10:1' },
    { id: 'write', kind: 'resource_write', from: 'step:start', to: 'resource:attribute:name' },
    { id: 'read', kind: 'resource_read', from: 'resource:attribute:name', to: 'step:next' },
    { id: 'subflow', kind: 'subflow', from: 'wait:start:10:1', to: 'subflow:Child:11' },
  ],
  diagnostics: [],
};
