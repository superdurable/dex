// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import type { ReactNode } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { sequentialStepHistory } from '@/lib/historyTestServer';
import type { FlowHistoryEvent } from '@/lib/types';
import { graphViewStorageKeys } from './graphViewPreferences';

interface FakeNode {
  id: string;
  position: { x: number; y: number };
  width?: number;
  height?: number;
  ariaLabel?: string;
  className?: string;
  data: { label: ReactNode };
}

const flow = vi.hoisted(() => {
  const state = {
    nodes: [] as FakeNode[],
    setViewportCalls: [] as Array<{ viewport: { x: number; y: number; zoom: number }; options?: { duration?: number } }>,
    setCenterCalls: [] as Array<{ x: number; y: number }>,
    viewport: { x: 0, y: 7, zoom: 0.8 },
  };
  const instance = {
    getNode: () => undefined,
    getViewport: () => state.viewport,
    getZoom: () => 0.8,
    setViewport: (viewport: { x: number; y: number; zoom: number }, options?: { duration?: number }) => {
      state.setViewportCalls.push({ viewport, options });
      return Promise.resolve(true);
    },
    setCenter: (x: number, y: number) => {
      state.setCenterCalls.push({ x, y });
      return Promise.resolve(true);
    },
  };
  return { state, instance };
});

// React Flow needs layout APIs that jsdom lacks, so a stand-in draws each node's label.
vi.mock('@xyflow/react', async (importOriginal) => {
  const actual = await importOriginal<typeof import('@xyflow/react')>();
  const { useEffect } = await import('react');
  function ReactFlow({ nodes, onInit, children }: { nodes: FakeNode[]; onInit: (instance: unknown) => void; children: ReactNode }) {
    flow.state.nodes = nodes;
    useEffect(() => { onInit(flow.instance); }, [onInit]);
    return (
      <div>
        {nodes.map((node) => (
          <div
            aria-label={node.ariaLabel}
            className={`react-flow__node ${node.className ?? ''}`}
            data-id={node.id}
            key={node.id}
            role={node.ariaLabel ? 'group' : undefined}
          >
            {node.data.label}
          </div>
        ))}
        {children}
      </div>
    );
  }
  return {
    ...actual,
    ReactFlow,
    Background: () => null,
    Handle: () => null,
    Controls: ({ children }: { children: ReactNode }) => <div>{children}</div>,
    ControlButton: ({ children, onClick, title }: { children: ReactNode; onClick: () => void; title: string }) => (
      <button aria-label={title} onClick={onClick} type="button">{children}</button>
    ),
    useViewport: () => ({ x: 0, y: 0, zoom: 1 }),
    useStore: (selector: (store: { width: number; height: number }) => number) => selector({ width: 1200, height: 320 }),
  };
});

const { ExecutionGraph } = await import('./ExecutionGraph');

let storedValues: Map<string, string>;

function stubStorage({ isBlocked = false } = {}) {
  storedValues = new Map();
  vi.stubGlobal('localStorage', {
    getItem: (key: string) => {
      if (isBlocked) throw new Error('blocked');
      return storedValues.get(key) ?? null;
    },
    setItem: (key: string, value: string) => {
      if (isBlocked) throw new Error('blocked');
      storedValues.set(key, value);
    },
    removeItem: (key: string) => { storedValues.delete(key); },
  });
}

beforeEach(() => {
  flow.state.nodes = [];
  flow.state.setViewportCalls = [];
  flow.state.setCenterCalls = [];
  flow.state.viewport = { x: 0, y: 7, zoom: 0.8 };
  stubStorage();
  vi.stubGlobal('ResizeObserver', class {
    observe() {}
    disconnect() {}
  });
});

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});

function graph(events: FlowHistoryEvent[], selectedEvent: FlowHistoryEvent | null = null) {
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

function shownNodeIDs(): string[] {
  return flow.state.nodes.map((node) => node.id);
}

// Step-1 … Step-20, the last one waiting: Folded merges Rounds 1–19 and keeps Round 20 open.
const loopEvents = sequentialStepHistory(40);
const foldedRangeID = '__group:rounds:Step-1';

/** A settled chain of Step executions, each returning the given Step decision. */
function decidedChainHistory(steps: Array<[stepType: string, stepDecision: unknown]>): FlowHistoryEvent[] {
  const events: FlowHistoryEvent[] = [{
    eventId: 1,
    eventTime: '2026-10-24T17:00:00Z',
    type: 'FlowStartedOrContinued',
    payload: { initialStart: { startStepType: steps[0][0] } },
  }];
  const counts = new Map<string, number>();
  let fromStepExecutionId = '__start__';
  for (const [stepType, stepDecision] of steps) {
    const count = (counts.get(stepType) ?? 0) + 1;
    counts.set(stepType, count);
    const context = { stepExecutionId: `${stepType}-${count}`, fromStepExecutionId, stepType };
    events.push({
      eventId: events.length + 1,
      eventTime: '2026-10-24T17:00:01Z',
      type: 'StepExecuteCompleted',
      payload: { context, output: { stepDecision } },
    });
    fromStepExecutionId = context.stepExecutionId;
  }
  return events;
}

describe('Folded Graph', () => {
  it('shows the Step decision of the execution a ×N node shows', () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    const toSync = { nextSteps: [{ stepType: 'SyncBatch' }] };
    const toCheckpoint = { nextSteps: [{ stepType: 'Checkpoint' }], cancelStepTypes: ['Poll'] };
    render(graph(decidedChainHistory([
      ...Array.from({ length: 8 }, () => ['SyncBatch', toSync] as [string, unknown]),
      ['SyncBatch', toCheckpoint],
      ['Checkpoint', toSync],
      ['SyncBatch', toCheckpoint],
      ['Checkpoint', toSync],
      ['SyncBatch', toCheckpoint],
      ['Checkpoint', { closeDecision: { closeDecisionType: 2 } }],
    ])));
    const repeated = screen.getByText('×9').closest('.graph-step-label')!;
    expect(repeated.querySelector('.graph-execute-decision-text')?.textContent).toBe('GoTo');
    expect(repeated.querySelector('.graph-execute-decision-cancel')?.textContent).toBe('CancelSteps×1');
    expect(repeated.querySelector('.graph-execute-decision-lines')?.getAttribute('title'))
      .toBe('GoTo → Checkpoint\nCancelSteps → Poll (entire Flow)');
  });


  it('is off by default and draws one node per Step execution', () => {
    render(graph(loopEvents));
    expect(screen.getByRole('button', { name: 'Folded' }).getAttribute('aria-pressed')).toBe('false');
    expect(shownNodeIDs().filter((id) => id.startsWith('Step-'))).toHaveLength(20);
  });

  it('folds repeated Rounds when turned on and remembers the choice', () => {
    const { unmount } = render(graph(loopEvents));
    fireEvent.click(screen.getByRole('button', { name: 'Folded' }));
    expect(storedValues.get(graphViewStorageKeys.isFolded)).toBe('true');
    expect(screen.getByText('Step ×19')).toBeTruthy();
    // The latest Round is open, so its frame draws behind Step-20.
    expect(shownNodeIDs()).toEqual(['__frame:round:Step-20', '__start__', foldedRangeID, 'Step-20']);
    unmount();

    render(graph(loopEvents));
    expect(screen.getByRole('button', { name: 'Folded' }).getAttribute('aria-pressed')).toBe('true');
    expect(shownNodeIDs()).toContain(foldedRangeID);
  });

  it('falls back to off when storage is blocked', () => {
    stubStorage({ isBlocked: true });
    render(graph(loopEvents));
    expect(screen.getByRole('button', { name: 'Folded' }).getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(screen.getByRole('button', { name: 'Folded' }));
    expect(screen.getByRole('button', { name: 'Folded' }).getAttribute('aria-pressed')).toBe('true');
  });

  it('opens a folded group with Show Steps and folds it again with Hide Steps', () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    render(graph(loopEvents));
    fireEvent.click(screen.getByRole('button', { name: /Show Steps/ }));
    expect(shownNodeIDs()).toEqual(expect.arrayContaining(['Step-1', 'Step-2', 'Step-3', 'Step-18', 'Step-19', 'Step-20']));
    expect(screen.getByText('… 14 more')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: /Hide Steps/ }));
    expect(shownNodeIDs()).toContain(foldedRangeID);
  });

  it('pans an opened group into view in both directions without changing the zoom', async () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    const canvas = { width: 1200, height: 320 };
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function width(this: HTMLElement) {
      return this.classList.contains('graph-canvas') ? canvas.width : 0;
    });
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function height(this: HTMLElement) {
      return this.classList.contains('graph-canvas') ? canvas.height : 0;
    });
    render(graph(loopEvents));
    // The reader dragged the graph far up and left.
    flow.state.viewport = { x: 5000, y: -10000, zoom: 0.8 };
    const callsBefore = flow.state.setViewportCalls.length;
    fireEvent.click(screen.getByRole('button', { name: /Show Steps/ }));
    await waitFor(() => expect(flow.state.setViewportCalls.length).toBeGreaterThan(callsBefore));
    const { viewport, options } = flow.state.setViewportCalls.at(-1)!;
    expect(viewport.zoom).toBe(0.8);
    expect(options?.duration).toBeGreaterThan(0);
    const frame = flow.state.nodes.find((node) => node.id === '__frame:rounds:Step-1')!;
    const frameTop = frame.position.y * viewport.zoom + viewport.y;
    const frameLeft = frame.position.x * viewport.zoom + viewport.x;
    expect(frameTop).toBeGreaterThanOrEqual(0);
    expect(frameTop).toBeLessThan(canvas.height);
    expect(frameLeft).toBeGreaterThanOrEqual(0);
    expect(frameLeft).toBeLessThan(canvas.width);
  });

  it('reopens a hidden group when another event of the same Step is selected', async () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    const waitFor10 = loopEvents.find((event) => event.eventId === 20)!;
    const execute10 = loopEvents.find((event) => event.eventId === 21)!;
    const { rerender } = render(graph(loopEvents, waitFor10));
    await waitFor(() => expect(shownNodeIDs()).toContain('Step-10'));
    fireEvent.click(screen.getByRole('button', { name: /Hide Steps/ }));
    expect(shownNodeIDs()).toContain(foldedRangeID);
    rerender(graph(loopEvents, execute10));
    await waitFor(() => expect(shownNodeIDs()).toContain('Step-10'));
  });

  it('draws a range of Rounds as a deck headed by the Round count', () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    const toCheck = { nextSteps: [{ stepType: 'Check' }] };
    const toSync = { nextSteps: [{ stepType: 'Sync' }] };
    render(graph(decidedChainHistory([
      ...Array.from({ length: 5 }, () => [['Sync', toCheck], ['Check', toSync]] as Array<[string, unknown]>).flat(),
      ['Sync', toCheck],
      ['Check', { closeDecision: { closeDecisionType: 2 } }],
    ])));
    const range = screen.getByRole('group', { name: '5 Rounds, Rounds 1–5, 10 Steps' });
    expect(range.classList.contains('node-round-range')).toBe(true);
    expect(range.querySelectorAll('.graph-round-deck-layer')).toHaveLength(2);
    expect(range.querySelector('.graph-round-deck-count')?.textContent).toBe('×5Rounds');
    expect(range.querySelector('.graph-round-deck-span')?.textContent).toBe('1–5 · 10 Steps');
    expect(range.querySelector('.graph-round-sequence')?.textContent).toBe('Sync → Check');
    const rangeNode = flow.state.nodes.find((node) => node.ariaLabel === '5 Rounds, Rounds 1–5, 10 Steps')!;
    // The deck's back cards take room up and right of the 280 by 136 card.
    expect({ width: rangeNode.width, height: rangeNode.height }).toEqual({ width: 296, height: 148 });

    fireEvent.click(screen.getByRole('button', { name: /Show Rounds/ }));
    const singleRound = screen.getByText('Round 1').closest('.react-flow__node')!;
    expect(singleRound.classList.contains('node-round-range')).toBe(false);
    expect(singleRound.querySelector('.graph-round-deck')).toBeNull();
    expect(singleRound.querySelector('.graph-round-count')?.textContent).toBe('2 Steps');
  });

  it('keeps the Step type title on the deck of a one-Step-type range', () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    render(graph(loopEvents));
    const range = screen.getByRole('group', { name: 'Step ×19, 19 Rounds, Rounds 1–19, 19 Steps' });
    expect(range.querySelector('.graph-round-deck-title')?.textContent).toBe('Step ×19');
    expect(range.querySelector('.graph-round-facts')?.textContent).toContain('Rounds 1–19');
  });

  it('opens the folded group that holds the selected Step', async () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    const selected = loopEvents.find((event) => event.eventId === 20)!;
    render(graph(loopEvents, selected));
    await waitFor(() => expect(shownNodeIDs()).toContain('Step-10'));
    expect(shownNodeIDs()).not.toContain(foldedRangeID);
  });
});

describe('Folded intro card', () => {
  it('shows on the first visit and stays dismissed', () => {
    vi.useFakeTimers();
    const { unmount } = render(graph(loopEvents));
    expect(screen.getByRole('note').textContent).toContain('groups repeated Rounds of Steps into one node');
    fireEvent.click(screen.getByRole('button', { name: 'Dismiss' }));
    expect(storedValues.get(graphViewStorageKeys.isFoldedIntroDismissed)).toBe('true');
    act(() => { vi.runAllTimers(); });
    expect(screen.queryByRole('note')).toBeNull();
    unmount();
    vi.useRealTimers();

    render(graph(loopEvents));
    expect(screen.queryByRole('note')).toBeNull();
  });

  it('is dismissed for good by turning Folded on', () => {
    render(graph(loopEvents));
    fireEvent.click(screen.getByRole('button', { name: 'Folded' }));
    expect(storedValues.get(graphViewStorageKeys.isFoldedIntroDismissed)).toBe('true');
  });
});

describe('Graph mini map keyboard', () => {
  it('is a focusable control whose arrow keys move the view', () => {
    render(graph(loopEvents));
    const miniMap = screen.getByRole('group', { name: /mini map/ });
    expect(miniMap.getAttribute('tabindex')).toBe('0');
    miniMap.focus();
    fireEvent.keyDown(miniMap, { key: 'ArrowRight' });
    // The view is 1200 wide at zoom 1, so ArrowRight moves its center half a view right.
    expect(flow.state.setCenterCalls.at(-1)).toEqual({ x: 1200, y: 160 });
  });
});

describe('Graph mini map', () => {
  it('is shown by default, hides to a map button, and remembers the choice', () => {
    const { unmount } = render(graph(loopEvents));
    expect(screen.getByRole('group', { name: /mini map/ })).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Mini Map/ })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Hide mini map' }));
    expect(screen.queryByRole('group', { name: /mini map/ })).toBeNull();
    expect(storedValues.get(graphViewStorageKeys.isMiniMapHidden)).toBe('true');
    unmount();

    render(graph(loopEvents));
    expect(screen.queryByRole('group', { name: /mini map/ })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Show mini map' }));
    expect(screen.getByRole('group', { name: /mini map/ })).toBeTruthy();
    expect(storedValues.get(graphViewStorageKeys.isMiniMapHidden)).toBe('false');
  });
});
