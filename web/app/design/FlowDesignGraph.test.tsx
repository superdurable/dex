// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { cleanup, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { FlowDefinitionGraph } from '@superdurable/flow-definition-renderer';
import type { FlowDefinitionEntry } from '@/lib/types';
import { safeDecode } from './canvas/model/decode';
import { stageBox } from './canvas/views/stageCard';
import { stepBox } from './canvas/views/stepBox';
import { FlowDesignGraph } from './FlowDesignGraph';
import { groupsFromDefinition } from './groupsFromGraph';

class StubResizeObserver {
  observe() {}
  unobserve() {}
  disconnect() {}
}

const SCHEMA_1_FILE = '../docs/src/data/flow-definitions/money-transfer.json';
const SCHEMA_2_FILE = '../docs/src/data/flow-definitions/customer-refund-agentic.json';

function entryOf(file: string): FlowDefinitionEntry {
  const graph = JSON.parse(readFileSync(resolve(process.cwd(), file), 'utf8')) as FlowDefinitionGraph;
  return {
    id: file,
    file,
    schemaVersion: graph.schemaVersion === '2.0' ? '2.0' : '1.0',
    flowName: graph.flow.name,
    sourceLanguage: graph.source.language,
    sourcePath: graph.source.path,
    valid: graph.valid,
    graph,
  };
}

beforeEach(() => {
  vi.stubGlobal('ResizeObserver', StubResizeObserver);
  vi.stubGlobal('fetch', vi.fn(async (input: string) => {
    throw new Error(`the design graph must not request ${input}`);
  }));
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

describe('Flow design graph', () => {
  it.each([
    ['a schema 1.0 file', SCHEMA_1_FILE],
    ['a schema 2.0 file', SCHEMA_2_FILE],
  ])('opens %s from the definition alone, with no request', async (_name, file) => {
    render(<MemoryRouter><FlowDesignGraph definition={entryOf(file)} /></MemoryRouter>);
    // The layout runs asynchronously, so a loaded machine draws later.
    await waitFor(() => expect(document.querySelector('.v2-shell.dg')).not.toBeNull(), { timeout: 4_000 });
    expect(screen.queryByText('This Flow Definition Graph has no Steps to draw.')).toBeNull();
    expect(fetch).not.toHaveBeenCalled();
  });

  it.each([SCHEMA_1_FILE, SCHEMA_2_FILE])('puts no outcome or status annotation on a card of %s', (file) => {
    const { graph } = entryOf(file);
    const flow = safeDecode(graph, { generated: true, note: file });
    const groups = groupsFromDefinition(graph, flow);
    const opts = { direction: 'tb' as const, run: null, groups };
    const steps = flow.steps.map((step) => stepBox(flow, step, opts, { jumps: [], arriving: 0 }));
    const stages = groups.map((group) => stageBox(
      flow,
      group,
      flow.steps.filter((step) => group.stepIds.includes(step.id)),
      opts,
      { jumps: [], loops: false },
    ));
    const boxes = [...steps, ...stages];
    expect(boxes.length).toBeGreaterThan(0);
    const chips = boxes.flatMap((box) => (box.chips ?? []).flat());
    expect(chips.filter((chip) => ['ends', 'stops', 'fails'].includes(chip.kind))).toEqual([]);
    expect(boxes.filter((box) => box.pill !== undefined)).toEqual([]);
    const words = [...chips.map((chip) => chip.text), ...stages.map((box) => box.stage?.facts ?? '')].join(' | ');
    for (const removed of ['can fail the flow', 'fails the flow', 'completes', 'stops this branch', 'paths run', 'for someone']) {
      expect(words).not.toContain(removed);
    }
  });
});
