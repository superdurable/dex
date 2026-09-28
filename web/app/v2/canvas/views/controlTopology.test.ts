// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { describe, expect, it } from 'vitest';
import refundGraph from '../../../../../docs/src/data/flow-definitions/customer-refund-agentic.json';
import { safeDecode } from '../model/decode';
import type { FlowDefinitionGraph } from '../model/fdg';
import { controlTopologyView } from './controlTopology';
import type { StepGroup } from './groups';
import type { Box, ViewOpts } from './types';

describe('control topology group bands', () => {
  it('labels every disjoint region of a split group', () => {
    const flow = safeDecode(splitResolutionGraph, { generated: true, note: 'test' });
    const scene = controlTopologyView.layout(flow, viewOpts([
      { id: 'resolution', label: 'Resolution', reason: 'Resolution', stepTypes: ['StartStep', 'CloseStep'] },
    ]));
    const resolution = scene.bands.filter((band) => band.style === 'group');

    expect(resolution.length).toBeGreaterThan(1);
    expect(resolution.map((band) => band.label)).toEqual(
      resolution.map(() => 'Resolution'),
    );
  });

  /*
   * The boundary case for lane-by-group, and it used to assert the opposite.
   *
   * `BillingFailedStep` is reached by an ORDINARY transition and resumes the flow, so its
   * `recoveryRole` is `none` and reachability puts it on the spine; only `SubscriptionFailedStep` is
   * reached by failing. One handling member of two is exactly the ratio the old MAJORITY test rejected
   * (`1 * 2 <= 2`), so the group was left split and this test asserted the split -- a Failure region on
   * the spine and a second one in the gutter.
   *
   * Presence rather than majority is what changed: a group holding any Step you reach by failing shares
   * the recovery lane, so both members go aside and the group is ONE region. Splitting a declared group
   * across two lanes is the defect `group-cohesion` measures, so the assertion inverts.
   *
   * Labelling of genuinely disjoint regions is still covered by `labels every disjoint region of a split
   * group` above, whose members cannot be pulled together.
   */
  it('keeps a Failure group in one region when only some members are reached by failing', () => {
    const flow = safeDecode(splitFailureGraph, { generated: true, note: 'test' });
    const scene = controlTopologyView.layout(flow, viewOpts([
      { id: 'resolution', label: 'Resolution', reason: 'Resolution', stepTypes: ['ApplyStep'] },
      { id: 'failure', label: 'Failure', reason: 'Failure', stepTypes: ['BillingFailedStep', 'SubscriptionFailedStep'] },
      { id: 'close', label: 'Close', reason: 'Close', stepTypes: ['CloseStep'] },
    ]));
    const failure = scene.bands.filter((band) => band.label === 'Failure');

    expect(failure).toHaveLength(1);
    expect(scene.bands.filter((band) => band.style === 'group' && !band.label)).toEqual([]);
  });

  /*
   * The guard the row gap needs, run over the real two-gate refund graph rather than a fixture.
   *
   * A hand-built linear flow does not reproduce this: the collision needs wrapped wide ranks and a
   * band spanning non-adjacent ranks, which is a shape easier to import than to describe. Verified
   * to fail at rankBase 36 and below, which is how the floor of 44 was chosen.
   */
  it('never overlaps two group bands on the shipped refund graph', () => {
    const scene = refundScene();
    const groups = scene.bands.filter((band) => band.style === 'group');

    expect(groups.length).toBeGreaterThan(1);
    expect(overlappingPairs(groups)).toEqual([]);
  });

  it('never overlaps two Step boxes on the shipped refund graph', () => {
    expect(overlappingPairs(refundScene().boxes)).toEqual([]);
  });

  it('marks the shipped OpenAI Connector Step with its semantic icon', () => {
    const connector = refundScene().boxes.find((box) => box.id === 'step:GenerateCustomerMessageStep');

    expect(connector?.icon).toBe('connector');
  });
});

describe('control topology for a Flow with no Steps', () => {
  it('draws one card per RPC, Actions first and the Summary and Display views last', () => {
    const scene = entityListScene('collapsed', 'tb');

    expect(scene.boxes.map((box) => [box.title, box.kind, box.subtitle, box.rpcRole])).toEqual([
      ['RemoveEntry', 'rpcCard', 'Action: Remove entry', 'action'],
      ['AddEntry', 'rpcCard', 'changes state', undefined],
      ['ListEntries', 'rpcCard', 'reads state', undefined],
      ['GetDexSummary', 'rpcCard', 'Summary view', 'view'],
      ['GetDexDisplay', 'rpcCard', 'Display view', 'view'],
    ]);
    expect(scene.links).toEqual([]);
    expect(scene.bands).toEqual([]);
    expect(scene.notes).toEqual(['This Flow has no Steps, so each of its 5 RPCs is drawn as a card.']);
  });

  it('lists the Attributes each RPC reads and writes and the Channels it publishes to when expanded', () => {
    const addEntry = cardOf(entityListScene('expanded', 'tb'), 'rpc:AddEntry');

    expect(addEntry.rows).toEqual([]);
    expect(addEntry.sections).toEqual([
      { label: 'Reads', rows: [{ glyph: '▫', text: 'entries' }] },
      { label: 'Writes', rows: [{ glyph: '▪', text: 'entries' }, { glyph: '▪', text: 'entry-count' }] },
      { label: 'Publishes to', rows: [{ glyph: '✉', text: 'entry-events' }] },
    ]);
  });

  it('says each kind of access in one row when collapsed', () => {
    const addEntry = cardOf(entityListScene('collapsed', 'tb'), 'rpc:AddEntry');

    expect(addEntry.sections).toEqual([]);
    expect(addEntry.rows?.map((row) => row.text)).toEqual([
      'reads entries',
      'writes entries, entry-count',
      'publishes to entry-events',
    ]);
  });

  it('counts the names a collapsed row has no room for', () => {
    const graph = entityListGraph();
    graph.nodes = graph.nodes.map((node) => node.id === 'resource:attribute:entryCount'
      ? { ...node, name: 'entity-list-entry-count' }
      : node);
    const flow = safeDecode(graph, { generated: true, note: 'test' });
    const scene = controlTopologyView.layout(flow, { ...viewOpts([]), detail: 'collapsed' });

    expect(cardOf(scene, 'rpc:AddEntry').rows?.map((row) => row.text)).toContain(
      'writes entity-list-entry-count +1',
    );
  });

  it('marks an Action with its required permission at both detail levels', () => {
    for (const detail of ['collapsed', 'expanded'] as const) {
      expect(cardOf(entityListScene(detail, 'tb'), 'rpc:RemoveEntry').rows?.[0]).toEqual({
        glyph: '◈',
        text: 'requires entries.manage',
        tone: 'quiet',
      });
    }
  });

  it('never overlaps two cards, and keeps every card inside the scene, in all four view modes', () => {
    for (const detail of ['collapsed', 'expanded'] as const) {
      for (const direction of ['tb', 'lr'] as const) {
        const scene = entityListScene(detail, direction);

        expect(scene.boxes).toHaveLength(5);
        expect(overlappingPairs(scene.boxes), `${detail}/${direction}`).toEqual([]);
        for (const box of scene.boxes) {
          expect(box.x + box.w).toBeLessThanOrEqual(scene.width);
          expect(box.y + box.h).toBeLessThanOrEqual(scene.height);
        }
      }
    }
  });

  it('grows each card when expanded', () => {
    const collapsed = entityListScene('collapsed', 'tb');
    const expanded = entityListScene('expanded', 'tb');

    expect(collapsed.boxes).toHaveLength(5);
    for (const box of collapsed.boxes) {
      expect(cardOf(expanded, box.id).h).toBeGreaterThan(box.h);
    }
  });

  it('fills rows top-down and columns left-right', () => {
    const topDown = entityListScene('collapsed', 'tb').boxes;
    const leftRight = entityListScene('collapsed', 'lr').boxes;

    expect(new Set(topDown.slice(0, 3).map((box) => box.y)).size).toBe(1);
    expect(topDown[3].y).toBeGreaterThan(topDown[0].y);
    expect(new Set(leftRight.slice(0, 3).map((box) => box.x)).size).toBe(1);
    expect(leftRight[3].x).toBeGreaterThan(leftRight[0].x);
  });

  it('still reports no Steps for a Flow with neither Steps nor RPCs', () => {
    const flow = safeDecode(graph([], [], 'step:none'), { generated: true, note: 'test' });

    expect(controlTopologyView.layout(flow, viewOpts([]))).toEqual({
      boxes: [], links: [], bands: [], width: 640, height: 200, notes: ['No steps.'],
    });
  });

  // The agentic refund graph declares four Actions and both views, so the join could leak here.
  it('draws a Flow with Steps the same with or without its Flow v2 RPC contract', () => {
    for (const detail of ['collapsed', 'expanded'] as const) {
      for (const direction of ['tb', 'lr'] as const) {
        const withContract = refundScene({ detail, direction });
        const withoutContract = refundScene({ detail, direction }, { v2: undefined });

        expect(withContract.boxes.some((box) => box.kind === 'rpcCard')).toBe(false);
        expect(withContract).toEqual(withoutContract);
      }
    }
  });
});

interface FdgGroup { id: string; label: string; stepIds: string[] }

function refundScene(
  mode: Partial<Pick<ViewOpts, 'detail' | 'direction'>> = {},
  override: Partial<FlowDefinitionGraph> = {},
) {
  const graph = { ...(refundGraph as unknown as FlowDefinitionGraph & { groups?: FdgGroup[] }), ...override };
  const flow = safeDecode(graph, { generated: true, note: 'customer-refund-agentic' });
  const groups = (graph.groups ?? []).map((group: FdgGroup) => ({
    id: group.id,
    label: group.label,
    reason: group.label,
    stepTypes: group.stepIds.flatMap((stepId: string) => {
      const step = flow.steps.find((candidate) => candidate.id === stepId);
      return step === undefined ? [] : [step.stepType];
    }),
  }));
  return controlTopologyView.layout(flow, { ...viewOpts(groups), ...mode });
}

function entityListScene(detail: ViewOpts['detail'], direction: ViewOpts['direction']) {
  const flow = safeDecode(entityListGraph(), { generated: true, note: 'entity-list' });
  return controlTopologyView.layout(flow, { ...viewOpts([]), detail, direction });
}

function cardOf(scene: { boxes: Box[] }, id: string): Box {
  const box = scene.boxes.find((candidate) => candidate.id === id);
  if (box === undefined) throw new Error(`no card ${id}`);
  return box;
}

/** A step-less entity Flow in the shape `dexcli visualize` emits for a Go Flow with only RPCs. */
function entityListGraph(): FlowDefinitionGraph {
  const rpc = (name: string) => [
    { id: `rpc:${name}`, kind: 'rpc', name },
    { id: `decision:rpc:${name}`, kind: 'decision', name: 'rpcResult', parentId: `rpc:${name}`, decision: { type: 'rpcResult' } },
  ];
  const edges: [string, string, string][] = [
    ['resource_read', 'resource:attribute:entries', 'rpc:AddEntry'],
    ['resource_write', 'rpc:AddEntry', 'resource:attribute:entryCount'],
    ['resource_write', 'rpc:AddEntry', 'resource:attribute:entries'],
    ['resource_publish', 'rpc:AddEntry', 'resource:channel:entryEvents'],
    ['resource_read', 'resource:attribute:entries', 'rpc:RemoveEntry'],
    ['resource_write', 'rpc:RemoveEntry', 'resource:attribute:entryCount'],
    ['resource_write', 'rpc:RemoveEntry', 'resource:attribute:entries'],
    ['resource_read', 'resource:attribute:entries', 'rpc:ListEntries'],
    ['resource_read', 'resource:attribute:entryCount', 'rpc:GetDexSummary'],
    ['resource_read', 'resource:attribute:entryCount', 'rpc:GetDexDisplay'],
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
      ...['AddEntry', 'RemoveEntry', 'ListEntries', 'GetDexSummary', 'GetDexDisplay'].flatMap(rpc),
    ],
    edges: edges.map(([kind, from, to], i) => ({ id: `edge:${i}`, kind, from, to, metadata: { phase: 'rpc' } })),
    diagnostics: [],
    v2: {
      summary: { rpcName: 'GetDexSummary' },
      display: { rpcName: 'GetDexDisplay' },
      actions: [{ rpcName: 'RemoveEntry', label: 'Remove entry', requiredPermission: 'entries.manage' }],
    },
  };
}

interface Rect { id: string; x: number; y: number; w: number; h: number }

/** Pairs sharing more than a hairline of area, named so a failure says which collided. */
function overlappingPairs(rects: readonly Rect[]): string[] {
  const hits: string[] = [];
  for (let left = 0; left < rects.length; left += 1) {
    for (let right = left + 1; right < rects.length; right += 1) {
      const a = rects[left];
      const b = rects[right];
      const overlapX = Math.min(a.x + a.w, b.x + b.w) - Math.max(a.x, b.x);
      const overlapY = Math.min(a.y + a.h, b.y + b.h) - Math.max(a.y, b.y);
      if (overlapX > 0.5 && overlapY > 0.5) hits.push(`${a.id} + ${b.id}`);
    }
  }
  return hits;
}

function viewOpts(groups: StepGroup[]): ViewOpts {
  return { detail: 'collapsed', direction: 'tb', selectedId: null, run: null, groups };
}

function step(id: string, name: string, start = false) {
  return { id, kind: 'step', name, start };
}

function edge(id: string, kind: 'transition' | 'failure_transition', from: string, to: string) {
  return { id, kind, from, to };
}

const splitResolutionGraph = graph(
  [
    step('step:start', 'StartStep', true),
    step('step:mid', 'MidStep'),
    step('step:close', 'CloseStep'),
  ],
  [
    edge('e1', 'transition', 'step:start', 'step:mid'),
    edge('e2', 'transition', 'step:mid', 'step:close'),
  ],
  'step:start',
);

const splitFailureGraph = graph(
  [
    step('step:start', 'StartStep', true),
    step('step:apply', 'ApplyStep'),
    step('step:billing', 'BillingFailedStep'),
    step('step:sub', 'SubscriptionFailedStep'),
    step('step:close', 'CloseStep'),
  ],
  [
    edge('e1', 'transition', 'step:start', 'step:apply'),
    edge('e2', 'transition', 'step:apply', 'step:close'),
    edge('e3', 'transition', 'step:apply', 'step:billing'),
    edge('e4', 'transition', 'step:billing', 'step:close'),
    edge('e5', 'failure_transition', 'step:apply', 'step:sub'),
  ],
  'step:start',
);

function graph(
  nodes: FlowDefinitionGraph['nodes'],
  edges: FlowDefinitionGraph['edges'],
  startStepId: string,
): FlowDefinitionGraph {
  return {
    schemaVersion: '2.0',
    valid: true,
    source: { language: 'go', path: 'test.go' },
    flow: { name: 'TestFlow', startStepId },
    nodes,
    edges,
    diagnostics: [],
  };
}
