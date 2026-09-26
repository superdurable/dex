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
