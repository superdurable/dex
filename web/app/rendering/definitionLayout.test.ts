// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import {
  buildDefinitionScene,
  filterDefinitionEdgesForSelection,
  displayName,
  recoveryOnlySteps,
  stepRole,
  waitSentence,
  withoutRecoveryPaths,
  type DefinitionVisibility,
  type FlowDefinitionGraph,
} from '@superdurable/flow-definition-renderer';
import aiAgentGraph from '../../../docs/src/data/flow-definitions/ai-agent.json';

const visible: DefinitionVisibility = {
  control: true,
  recovery: true,
  waits: true,
  decisions: true,
  rpcs: true,
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

  it('keeps timeout handlers visible when RPCs are hidden', () => {
    const scene = buildDefinitionScene(graph, { ...visible, rpcs: false });

    expect(scene.nodes.find((node) => node.id === 'rpc:approve')).toBeUndefined();
    expect(requiredNode(scene, 'timeout_handler:ExampleFlow').type).toBe('definitionTimeout');
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

  it('reveals resource relations only for the selected resource or handler', () => {
    const scene = buildDefinitionScene(graph, visible);
    const edgeIDs = (selectedNodeID: string) => filterDefinitionEdgesForSelection(
      scene.edges,
      graph.nodes,
      selectedNodeID,
    ).map((edge) => edge.id);

    expect(edgeIDs('')).not.toEqual(expect.arrayContaining(['publish', 'consume', 'write', 'read']));
    expect(edgeIDs('')).toEqual(expect.arrayContaining(['transition', 'recovery', 'subflow']));
    expect(edgeIDs('resource:channel:first')).toEqual(expect.arrayContaining(['publish', 'consume']));
    expect(edgeIDs('resource:channel:first')).not.toEqual(expect.arrayContaining(['write', 'read']));
    expect(edgeIDs('definition:attributes')).toEqual(expect.arrayContaining(['write', 'read']));
    expect(edgeIDs('definition:attributes')).not.toEqual(expect.arrayContaining(['publish', 'consume']));
    expect(edgeIDs('step:start')).toEqual(expect.arrayContaining(['consume', 'write']));
    expect(edgeIDs('wait:start:10:1')).toEqual(expect.arrayContaining(['consume', 'write']));
    expect(edgeIDs('rpc:approve')).toContain('publish');
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

  it('keeps the configured start Step above cyclic control flow', () => {
    const scene = buildDefinitionScene(aiAgentGraph as FlowDefinitionGraph, visible);
    const start = requiredNode(scene, 'step:Init');
    const stepPositions = scene.nodes
      .filter((node) => node.data.kind === 'step')
      .map((node) => node.position.y);

    expect(start.position.y).toBe(Math.min(...stepPositions));
  });

  it('merges decision branches with the same target and preserves selectable conditions', () => {
    const scene = buildDefinitionScene(aiAgentGraph as FlowDefinitionGraph, visible);
    const decisions = scene.nodes.filter(
      (node) => node.parentId === 'step:AwaitToolApproval' && node.data.kind === 'decision',
    );
    const checkSteered = decisions.find((node) => node.data.relatedEdges?.some(
      (edge) => edge.kind === 'transition' && edge.to === 'step:CheckSteered',
    ));

    expect(decisions).toHaveLength(1);
    expect(checkSteered?.data.definitions).toHaveLength(4);
    expect(checkSteered?.data.selectionDetails?.map((detail) => detail.label)).toEqual([
      'steered_messages',
      'not (steered_messages) and approvals[0].approved',
      'not (steered_messages) and not (approvals[0].approved) and self.flow.has_next_tool_call(context)',
      'not (steered_messages) and not (approvals[0].approved) and not (self.flow.has_next_tool_call(context))',
    ]);

    const transition = scene.edges.find(
      (edge) => edge.data?.sceneSourceID === checkSteered?.id && edge.target === 'step:CheckSteered',
    );
    expect(transition?.data?.selectionDetails).toHaveLength(4);

    const awaitUserDecisions = scene.nodes.filter(
      (node) => node.parentId === 'step:AwaitUser' && node.data.kind === 'decision',
    );
    const checkSteeredAfterUser = awaitUserDecisions.find((node) => node.data.relatedEdges?.some(
      (edge) => edge.kind === 'transition' && edge.to === 'step:CheckSteered',
    ));
    expect(awaitUserDecisions).toHaveLength(2);
    expect(checkSteeredAfterUser?.data.definitions).toHaveLength(3);
    const awaitUserBranch = scene.edges.find(
      (edge) => edge.target === checkSteeredAfterUser?.id && edge.data?.kind === 'branch',
    );
    expect(awaitUserBranch?.data?.selectionDetails).toHaveLength(3);
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

describe('recovery paths', () => {
  const gate = (over: Partial<FlowDefinitionGraph> = {}): FlowDefinitionGraph => ({
    schemaVersion: '1.0',
    valid: true,
    source: { language: 'python', path: 'flow.py' },
    flow: { name: 'WithRecovery', startStepId: 'step:first' },
    nodes: [
      { id: 'step:first', kind: 'step', name: 'first', start: true },
      { id: 'step:second', kind: 'step', name: 'second' },
      { id: 'step:gate', kind: 'step', name: 'gate' },
      { id: 'step:sink', kind: 'step', name: 'sink' },
      {
        id: 'decision:step:first:1:1', kind: 'decision', name: 'goTo', parentId: 'step:first',
        decision: { type: 'goTo' },
      },
      {
        id: 'decision:step:gate:2:1', kind: 'decision', name: 'goTo', parentId: 'step:gate',
        decision: { type: 'goTo' },
      },
    ],
    edges: [
      { id: 'e1', kind: 'transition', from: 'decision:step:first:1:1', to: 'step:second' },
      // Two Steps fail into the gate, which makes it an error-handling hub.
      { id: 'e2', kind: 'failure_transition', from: 'step:first', to: 'step:gate' },
      { id: 'e3', kind: 'failure_transition', from: 'step:second', to: 'step:gate' },
      // The gate routes back into the happy path, so it is reachable by a transition too.
      { id: 'e4', kind: 'transition', from: 'decision:step:gate:2:1', to: 'step:second' },
      // The sink is reachable only by failing into it.
      { id: 'e5', kind: 'failure_transition', from: 'step:second', to: 'step:sink' },
    ],
    diagnostics: [],
    ...over,
  });

  it('finds a hub by its inbound failure edges, not by its name', () => {
    expect([...recoveryOnlySteps(gate())].sort()).toEqual(['step:gate', 'step:sink']);
  });

  it('keeps a Step that only one other Step fails into, if a transition also reaches it', () => {
    const graph = gate();
    graph.edges = graph.edges.filter((edge) => edge.id !== 'e3');
    graph.edges.push({ id: 'e6', kind: 'transition', from: 'decision:step:first:1:1', to: 'step:gate' });
    expect([...recoveryOnlySteps(graph)]).toEqual(['step:sink']);
  });

  it('drops recovery Steps, their children and every exhausted-retry edge', () => {
    const filtered = withoutRecoveryPaths(gate());
    expect(filtered.nodes.map((node) => node.id)).toEqual([
      'step:first',
      'step:second',
      'decision:step:first:1:1',
    ]);
    expect(filtered.edges.map((edge) => edge.id)).toEqual(['e1']);
  });

  it('still drops exhausted-retry edges when nothing else is hidden', () => {
    const graph = gate();
    graph.edges = [graph.edges[0], { ...graph.edges[1], to: 'step:second' }];
    graph.nodes = graph.nodes.filter((node) => !['step:gate', 'step:sink'].includes(node.id));
    const filtered = withoutRecoveryPaths(graph);
    expect(filtered.nodes).toHaveLength(graph.nodes.length);
    expect(filtered.edges.map((edge) => edge.kind)).toEqual(['transition']);
  });

  it('hides only the hubs when the Flow records no start Step', () => {
    const graph = gate({ flow: { name: 'WithRecovery' } });
    expect([...recoveryOnlySteps(graph)]).toEqual(['step:gate']);
  });

  it('renders fewer nodes with the layer off than with it on', () => {
    const on = buildDefinitionScene(gate(), visible);
    const off = buildDefinitionScene(gate(), { ...visible, recovery: false });
    expect(off.nodes.length).toBeLessThan(on.nodes.length);
    expect(on.nodes.some((node) => node.id === 'step:gate')).toBe(true);
    expect(off.nodes.some((node) => node.id === 'step:gate')).toBe(false);
  });
});

describe('decisions layer', () => {
  it('re-sources a transition to the owning Step when the decision card is hidden', () => {
    const withCards = buildDefinitionScene(graph, visible);
    const collapsed = buildDefinitionScene(graph, { ...visible, decisions: false });
    const transition = (scene: typeof withCards) =>
      scene.edges.find((edge) => edge.id === 'transition');

    // With the card shown the edge leaves the decision; with it hidden the edge has to
    // leave the Step, or it would point at a node outside the scene and be dropped.
    expect(transition(withCards)?.source).toBe('decision:step:start:20:1');
    expect(transition(collapsed)?.source).toBe('step:start');
    expect(collapsed.nodes.some((node) => node.data.kind === 'decision')).toBe(false);
  });

  it('moves the branch guard onto the edge label, simplified', () => {
    const guarded: FlowDefinitionGraph = {
      ...graph,
      nodes: graph.nodes.map((node) => (
        node.id === 'decision:step:start:20:1'
          ? { ...node, condition: 'not (frozen.ready) and not (records != 0)' }
          : node
      )),
    };
    const collapsed = buildDefinitionScene(guarded, { ...visible, decisions: false });
    // Only the branch's own clause, with the negation folded into the comparison.
    expect(collapsed.edges.find((edge) => edge.id === 'transition')?.label).toBe('records == 0');
  });

  it('leaves the label alone while the decision card is visible', () => {
    const guarded: FlowDefinitionGraph = {
      ...graph,
      nodes: graph.nodes.map((node) => (
        node.id === 'decision:step:start:20:1' ? { ...node, condition: 'records == 0' } : node
      )),
    };
    const shown = buildDefinitionScene(guarded, visible);
    expect(shown.edges.find((edge) => edge.id === 'transition')?.label).toBe('');
  });
});

describe('derived legibility', () => {
  it('calls a Step a gate when its WaitFor holds a Channel condition', () => {
    // Dex already models human-in-the-loop as WaitFor(Channel). Naming it adds no
    // concept — it promotes one that was already in the graph.
    expect(stepRole(graph, 'step:start')).toBe('gate');
    expect(stepRole(graph, 'step:next')).toBe('work');
  });

  it('prefers a SubFlow batch over nothing, and Timers alone are not a gate', () => {
    const timerOnly: FlowDefinitionGraph = {
      ...graph,
      nodes: graph.nodes.map((node) => (
        node.id === 'wait:start:10:1'
          ? { ...node, wait: { type: 'anyOf', conditions: [{ kind: 'timer' as const, label: '1h' }] } }
          : node
      )),
    };
    expect(stepRole(timerOnly, 'step:start')).toBe('work');

    const batched: FlowDefinitionGraph = {
      ...graph,
      nodes: graph.nodes.map((node) => (
        node.id === 'wait:start:10:1'
          ? { ...node, wait: { type: 'allOf', conditions: [{ kind: 'subflow' as const, label: 'child' }] } }
          : node
      )),
    };
    expect(stepRole(batched, 'step:start')).toBe('batch');
  });

  it('writes the wait as a sentence, keeping the operator handles verbatim', () => {
    const sentence = waitSentence(graph, 'step:start');
    // The Channel name is what you publish to, so it survives unchanged...
    expect(sentence).toContain('first');
    // ...while the framework scaffolding around it does not.
    expect(sentence).not.toContain('anyOf');
    expect(sentence).not.toContain('.for 1');
    expect(waitSentence(graph, 'step:next')).toBe('');
  });

  it('shows a display name from the schema metadata but never loses the type name', () => {
    const step = graph.nodes.find((node) => node.id === 'step:start')!;
    expect(displayName(step)).toBe(step.name);
    expect(displayName({ ...step, metadata: { displayName: 'Rule on the difficult calls' } }))
      .toBe('Rule on the difficult calls');
    // An empty or non-string value must not blank the card.
    expect(displayName({ ...step, metadata: { displayName: '  ' } })).toBe(step.name);
    expect(displayName({ ...step, metadata: { displayName: 42 } })).toBe(step.name);
  });
});
