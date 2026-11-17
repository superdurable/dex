// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import type { ReactNode } from 'react';
import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { sequentialStepHistory, stepEvent } from '@/lib/historyTestServer';
import type { FlowHistoryEvent } from '@/lib/types';

interface FakeNode {
  id: string;
  position: { x: number; y: number };
  width?: number;
  height?: number;
  data: { label: ReactNode };
}

interface Viewport { x: number; y: number; zoom: number }

const flow = vi.hoisted(() => {
  const state = {
    nodes: [] as FakeNode[],
    viewport: { x: 0, y: 0, zoom: 1 } as Viewport,
    setViewportCalls: [] as { viewport: Viewport; options?: { duration?: number } }[],
    setCenterCalls: [] as { x: number; y: number }[],
    minZoom: 0,
  };
  const instance = {
    getNode: (id: string) => state.nodes.find((node) => node.id === id),
    getViewport: () => state.viewport,
    getZoom: () => state.viewport.zoom,
    setViewport: (viewport: Viewport, options?: { duration?: number }) => {
      state.viewport = viewport;
      state.setViewportCalls.push({ viewport, options });
      return Promise.resolve(true);
    },
    setCenter: (x: number, y: number) => {
      state.setCenterCalls.push({ x, y });
      return Promise.resolve(true);
    },
    zoomIn: () => {
      state.viewport = { ...state.viewport, zoom: state.viewport.zoom * 1.2 };
      return Promise.resolve(true);
    },
  };
  return { state, instance };
});

// React Flow needs layout APIs that jsdom lacks, so a stand-in draws the nodes and records viewport calls.
vi.mock('@xyflow/react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@xyflow/react')>();
  const { useEffect } = await import('react');
  function ReactFlow({ nodes, minZoom, onInit, children }: {
    nodes: FakeNode[];
    minZoom: number;
    onInit: (instance: unknown) => void;
    children: ReactNode;
  }) {
    flow.state.nodes = nodes;
    flow.state.minZoom = minZoom;
    useEffect(() => { onInit(flow.instance); }, [onInit]);
    return (
      <div>
        {nodes.map((node) => <div className="react-flow__node" data-id={node.id} key={node.id}>{node.data.label}</div>)}
        {children}
      </div>
    );
  }
  return {
    ...actual,
    ReactFlow,
    Background: () => null,
    MiniMap: () => null,
    useViewport: () => flow.state.viewport,
    useStore: (selector: (store: { width: number; height: number }) => number) => selector(canvasSize),
    Handle: () => null,
    Controls: ({ children }: { children: ReactNode }) => (
      <div>
        <button onClick={() => void flow.instance.zoomIn()} type="button">Zoom in</button>
        {children}
      </div>
    ),
    ControlButton: ({ children, onClick, title }: { children: ReactNode; onClick: () => void; title: string }) => (
      <button aria-label={title} onClick={onClick} type="button">{children}</button>
    ),
  };
});

const { ExecutionGraph } = await import('./ExecutionGraph');

const canvasSize = { width: 1200, height: 320 };

beforeEach(() => {
  flow.state.nodes = [];
  flow.state.viewport = { x: 0, y: 0, zoom: 1 };
  flow.state.setViewportCalls = [];
  flow.state.setCenterCalls = [];
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    disconnect() {}
  });
  vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function width(this: HTMLElement) {
    return this.classList.contains('graph-canvas') ? canvasSize.width : 0;
  });
  vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function height(this: HTMLElement) {
    return this.classList.contains('graph-canvas') ? canvasSize.height : 0;
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function renderGraph(events: FlowHistoryEvent[], selectedEvent: FlowHistoryEvent) {
  return (
    <MemoryRouter>
      <ExecutionGraph
        direction="lr"
        events={events}
        flowId="flow-1"
        onSelectEvent={vi.fn()}
        selectedEvent={selectedEvent}
        state={null}
      />
    </MemoryRouter>
  );
}

describe('Execution graph viewport', () => {
  it('frames the selection once and never undoes a control action when new events arrive', async () => {
    const events = sequentialStepHistory(41);
    const selected = events.find((event) => event.eventId === 40)!;
    const { rerender } = render(renderGraph(events, selected));
    await waitFor(() => expect(flow.state.setCenterCalls).toHaveLength(1));
    expect(flow.state.setViewportCalls).toHaveLength(1);

    // The reader zooms in with the controls.
    fireEvent.click(screen.getByRole('button', { name: 'Zoom in' }));
    const zoomed = flow.state.viewport;

    // Live adds events; the graph re-lays out with the same selection.
    rerender(renderGraph(sequentialStepHistory(51), selected));
    await new Promise((resolve) => setTimeout(resolve, 50));
    expect(flow.state.setViewportCalls).toHaveLength(1);
    expect(flow.state.setCenterCalls).toHaveLength(1);
    expect(flow.state.viewport).toBe(zoomed);
  });

  it('fits the whole graph with Fit view, below the usual zoom-out limit when needed', async () => {
    const events = sequentialStepHistory(81);
    render(renderGraph(events, events.at(-1)!));
    await waitFor(() => expect(flow.state.setViewportCalls).toHaveLength(1));
    fireEvent.click(screen.getByRole('button', { name: 'Fit view' }));
    const fit = flow.state.setViewportCalls.at(-1)!;
    expect(fit.options?.duration).toBe(200);
    expect(fit.viewport.zoom).toBeLessThan(0.2);
    expect(flow.state.minZoom).toBeLessThanOrEqual(fit.viewport.zoom);
  });
});

/** Rounds of Check then k Sync Steps; each Round differs, so none merge into a range. */
function growingRoundsHistory(roundCount: number): FlowHistoryEvent[] {
  const events: FlowHistoryEvent[] = [{
    eventId: 1, eventTime: null, type: 'FlowStartedOrContinued', payload: {}, runAttempt: 1,
  }];
  let previous = '__start__';
  const addStep = (stepType: string, index: number) => {
    const stepExecutionId = `${stepType}-${index}`;
    const context = { stepExecutionId, fromStepExecutionId: previous, stepType };
    events.push(stepEvent(events.length + 1, 'StepExecuteCompleted', context));
    previous = stepExecutionId;
  };
  let syncIndex = 0;
  for (let round = 1; round <= roundCount; round += 1) {
    addStep('Check', round);
    for (let sync = 0; sync < round; sync += 1) addStep('Sync', (syncIndex += 1));
  }
  return events;
}

describe('Folded Graph initial view', () => {
  beforeEach(() => {
    const values = new Map([['dex.run.graph-folded', 'true']]);
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: (key: string, value: string) => { values.set(key, value); },
      removeItem: (key: string) => { values.delete(key); },
    });
  });

  it('opens on the latest Round and the group before it when the folded graph is too wide', async () => {
    const events = growingRoundsHistory(12);
    render(renderGraph(events, events[0]));
    await waitFor(() => expect(flow.state.setViewportCalls).toHaveLength(1));
    const { viewport } = flow.state.setViewportCalls[0];
    expect(viewport.x).toBeLessThan(0);
    const frame = flow.state.nodes.find((node) => node.id === '__frame:round:Check-12')!;
    const frameLeft = frame.position.x * viewport.zoom + viewport.x;
    expect(frameLeft).toBeGreaterThan(0);
    expect(frameLeft).toBeLessThan(canvasSize.width);
    const previousGroup = flow.state.nodes.find((node) => node.id === '__group:round:Check-11')!;
    expect(previousGroup.position.x * viewport.zoom + viewport.x).toBeGreaterThanOrEqual(0);
  });

  it('fits the whole folded graph when it fits at a readable zoom', async () => {
    const events = sequentialStepHistory(81);
    render(renderGraph(events, events[0]));
    await waitFor(() => expect(flow.state.setViewportCalls).toHaveLength(1));
    const { viewport } = flow.state.setViewportCalls[0];
    expect(viewport.zoom).toBeGreaterThanOrEqual(0.6);
    expect(viewport.x).toBeGreaterThanOrEqual(0);
  });
});
