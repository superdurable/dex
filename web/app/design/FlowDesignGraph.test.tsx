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
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
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

// The docs ship only schema 2.0, so entryOf drops this file's 2.0 additions to make a 1.0 definition.
const SCHEMA_1_FILE = '../docs/src/data/flow-definitions/money-transfer.json';
const SCHEMA_2_FILE = '../docs/src/data/flow-definitions/customer-refund-agentic.json';

function entryOf(file: string): FlowDefinitionEntry {
  const shipped = JSON.parse(readFileSync(resolve(process.cwd(), file), 'utf8')) as FlowDefinitionGraph;
  const graph: FlowDefinitionGraph = file === SCHEMA_1_FILE
    ? { ...shipped, schemaVersion: '1.0', groups: undefined, v2: undefined }
    : shipped;
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
  vi.restoreAllMocks();
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

  it('keeps the legend at the top right and hides it from the panel header', async () => {
    render(<MemoryRouter><FlowDesignGraph definition={entryOf(SCHEMA_2_FILE)} /></MemoryRouter>);
    const toggle = await screen.findByRole('button', { name: 'Legend' }, { timeout: 4_000 });
    expect(screen.queryByRole('button', { name: 'Hide legend' })).toBeNull();
    // The legend and the zoom controls share the top-right corner, after the top-left toolbar.
    const corner = document.querySelector('.dg-overlay > .dg-corner');
    expect(corner?.contains(toggle)).toBe(true);
    expect(corner?.contains(screen.getByRole('group', { name: 'Zoom' }))).toBe(true);
    expect(document.querySelector('.dg-overlay')?.firstElementChild?.classList.contains('dg-toolbar')).toBe(true);

    fireEvent.click(toggle);
    expect(toggle.getAttribute('aria-expanded')).toBe('true');
    const hide = screen.getByRole('button', { name: 'Hide legend' });
    expect(hide.getAttribute('title')).toBe('Hide legend');
    // It shares the header row with the toggle, above the scrolling body.
    const card = document.querySelector('.dg-legend');
    const head = card?.firstElementChild;
    expect(head?.classList.contains('dg-legend-head')).toBe(true);
    expect(head?.contains(toggle)).toBe(true);
    expect(head?.lastElementChild).toBe(hide);
    expect(card?.querySelector('.dg-legend-body')?.contains(hide)).toBe(false);

    hide.focus();
    fireEvent.click(hide);
    expect(toggle.getAttribute('aria-expanded')).toBe('false');
    expect(screen.queryByRole('button', { name: 'Hide legend' })).toBeNull();
    expect(document.querySelector('.dg-legend-body')).toBeNull();
    expect(document.activeElement).toBe(toggle);
  });

  it('labels the zoom controls and returns from a zoomed view when Fit is pressed', async () => {
    // Instant camera moves, so each transform lands synchronously.
    vi.stubGlobal('matchMedia', (query: string) => ({
      matches: query.includes('prefers-reduced-motion'),
      media: query,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
    }));
    // jsdom lays nothing out; the camera frames only a pane it can measure.
    for (const [property, size] of [['clientWidth', 1200], ['clientHeight', 800], ['offsetWidth', 1200], ['offsetHeight', 800]] as const) {
      vi.spyOn(HTMLElement.prototype, property, 'get').mockReturnValue(size);
    }
    render(<MemoryRouter><FlowDesignGraph definition={entryOf(SCHEMA_2_FILE)} /></MemoryRouter>);
    const fit = await screen.findByRole('button', { name: 'Fit the whole Flow' }, { timeout: 4_000 });
    expect(fit.getAttribute('title')).toBe('Fit the whole Flow');
    expect(document.querySelector('.react-flow__controls')).toBeNull();
    await waitFor(() => expect(document.querySelectorAll('.react-flow__node').length).toBeGreaterThan(0), { timeout: 4_000 });
    const viewport = () => document.querySelector<HTMLElement>('.react-flow__viewport')?.style.transform ?? '';

    fireEvent.click(fit);
    await waitFor(() => expect(viewport()).toMatch(/scale/));
    await new Promise((resolve) => setTimeout(resolve, 100));
    const fitted = viewport();
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    await waitFor(() => expect(viewport()).not.toBe(fitted));

    fireEvent.click(fit);
    // React Flow skipped fitView while any node lacked a measured size, leaving the zoomed view.
    await waitFor(() => expect(viewport()).toBe(fitted));
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
