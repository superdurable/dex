// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { cleanup, render, screen } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { FlowDefinitionGraph } from '@superdurable/flow-definition-renderer';
import type { FlowDefinitionEntry } from '@/lib/types';
import {
  flowDefinitionLimitProblem,
  MAX_FLOW_DEFINITION_DEPTH,
  MAX_FLOW_DEFINITION_EDGES,
  MAX_FLOW_DEFINITION_NODES,
  MAX_FLOW_DEFINITION_STRING_CHARACTERS,
} from './definitionLimits';
import { FlowDesignGraph } from './FlowDesignGraph';

vi.mock('./FlowDesignCanvas', () => ({
  FlowDesignCanvas: ({ graph }: { graph: FlowDefinitionGraph }) => {
    if (graph.flow.name === 'BrokenFlow') throw new Error('layout failed');
    return <div data-testid="canvas">{graph.flow.name}</div>;
  },
}));


function graphOf(name: string, overrides: Partial<FlowDefinitionGraph> = {}): FlowDefinitionGraph {
  return {
    schemaVersion: '2.0',
    valid: true,
    source: { language: 'go', path: 'flow.go' },
    flow: { name },
    nodes: [{ id: 'step:a', kind: 'step', name: 'A' }],
    edges: [],
    diagnostics: [],
    ...overrides,
  } as FlowDefinitionGraph;
}

function entryOf(graph: FlowDefinitionGraph): FlowDefinitionEntry {
  return {
    id: graph.flow.name, file: '', schemaVersion: '2.0', flowName: graph.flow.name, sourceLanguage: 'go',
    sourcePath: 'flow.go', valid: true, source: 'uploaded', graph,
  };
}

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});

describe('Flow definition limits', () => {
  it('accepts a graph within every limit', () => {
    expect(flowDefinitionLimitProblem(graphOf('OrderFlow'))).toBeNull();
  });

  it('names the limit a graph exceeds', () => {
    const node = { id: 'n', kind: 'attribute', name: 'A' };
    const edge = { id: 'e', kind: 'control', from: 'a', to: 'b' };
    let deep: unknown = 'leaf';
    for (let depth = 0; depth <= MAX_FLOW_DEFINITION_DEPTH; depth += 1) deep = [deep];
    expect(flowDefinitionLimitProblem(graphOf('F', { nodes: Array(MAX_FLOW_DEFINITION_NODES + 1).fill(node) })))
      .toContain('nodes');
    expect(flowDefinitionLimitProblem(graphOf('F', { edges: Array(MAX_FLOW_DEFINITION_EDGES + 1).fill(edge) })))
      .toContain('edges');
    expect(flowDefinitionLimitProblem(graphOf('x'.repeat(MAX_FLOW_DEFINITION_STRING_CHARACTERS + 1)))).toContain('longer than');
    expect(flowDefinitionLimitProblem(graphOf('F', { diagnostics: deep as FlowDefinitionGraph['diagnostics'] })))
      .toContain('deeper than');
    expect(flowDefinitionLimitProblem({ nodes: 'none' })).not.toBeNull();
  });

  it('counts characters, not UTF-16 units, as the server does', () => {
    const emoji = '\u{1F600}'.repeat(MAX_FLOW_DEFINITION_STRING_CHARACTERS);
    expect(flowDefinitionLimitProblem(graphOf(emoji))).toBeNull();
  });
});

describe('Flow design graph guards', () => {
  it('shows a too-large state instead of drawing an oversized graph', () => {
    const nodes = Array.from({ length: MAX_FLOW_DEFINITION_NODES + 1 }, (_, index) => ({ id: `n${index}`, kind: 'step', name: `S${index}` }));
    render(<MemoryRouter><FlowDesignGraph definition={entryOf(graphOf('HugeFlow', { nodes } as Partial<FlowDefinitionGraph>))} /></MemoryRouter>);
    expect(screen.getByRole('alert').textContent).toContain('too large to draw');
    expect(screen.queryByTestId('canvas')).toBeNull();
  });

  it('keeps a canvas that throws from taking down the page', () => {
    vi.spyOn(console, 'error').mockImplementation(() => undefined);
    render(
      <MemoryRouter>
        <FlowDesignGraph definition={entryOf(graphOf('BrokenFlow'))} />
        <p>Rest of the page</p>
      </MemoryRouter>,
    );
    expect(screen.getByRole('alert').textContent).toContain('could not be drawn');
    expect(screen.getByRole('alert').textContent).toContain('layout failed');
    expect(screen.getByText('Rest of the page')).toBeTruthy();
  });

  it('draws a graph within the limits', () => {
    render(<MemoryRouter><FlowDesignGraph definition={entryOf(graphOf('OrderFlow'))} /></MemoryRouter>);
    expect(screen.getByTestId('canvas').textContent).toBe('OrderFlow');
  });
});
