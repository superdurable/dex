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
import type { ViewOpts } from './types';

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

  it('labels a Failure region on the spine and in the recovery gutter', () => {
    const flow = safeDecode(splitFailureGraph, { generated: true, note: 'test' });
    const scene = controlTopologyView.layout(flow, viewOpts([
      { id: 'resolution', label: 'Resolution', reason: 'Resolution', stepTypes: ['ApplyStep'] },
      { id: 'failure', label: 'Failure', reason: 'Failure', stepTypes: ['BillingFailedStep', 'SubscriptionFailedStep'] },
      { id: 'close', label: 'Close', reason: 'Close', stepTypes: ['CloseStep'] },
    ]));
    const failure = scene.bands.filter((band) => band.label === 'Failure');

    expect(failure.length).toBeGreaterThan(1);
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

interface FdgGroup { id: string; label: string; stepIds: string[] }

function refundScene() {
  const graph = refundGraph as unknown as FlowDefinitionGraph & { groups?: FdgGroup[] };
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
  return controlTopologyView.layout(flow, viewOpts(groups));
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
