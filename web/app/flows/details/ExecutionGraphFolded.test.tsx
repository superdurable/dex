// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// @vitest-environment jsdom

import { readFileSync } from 'node:fs';
import type { CSSProperties, ReactNode } from 'react';
import { act, cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { sequentialStepHistory } from '@/lib/historyTestServer';
import type { FlowHistoryEvent } from '@/lib/types';
import { graphViewStorageKeys } from './graphViewPreferences';

interface FakeNode {
  id: string;
  type?: string;
  handles?: Array<{ type: string; position: string; x: number; y: number; width?: number; height?: number }>;
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
  function ReactFlow({ nodes, nodeTypes, onInit, children }: {
    nodes: FakeNode[];
    nodeTypes: Record<string, (props: { data: FakeNode['data'] }) => ReactNode>;
    onInit: (instance: unknown) => void;
    children: ReactNode;
  }) {
    flow.state.nodes = nodes;
    useEffect(() => { onInit(flow.instance); }, [onInit]);
    return (
      <div>
        {nodes.map((node) => {
          const NodeComponent = node.type ? nodeTypes[node.type] : undefined;
          return (
            <div
              aria-label={node.ariaLabel}
              className={`react-flow__node ${node.className ?? ''}`}
              data-id={node.id}
              key={node.id}
              role={node.ariaLabel ? 'group' : undefined}
            >
              {NodeComponent ? <NodeComponent data={node.data} /> : node.data.label}
            </div>
          );
        })}
        {children}
      </div>
    );
  }
  return {
    ...actual,
    ReactFlow,
    Background: () => null,
    Handle: ({ position, style, type }: { position: string; style?: CSSProperties; type: string }) => (
      <i className="test-handle" data-position={position} data-type={type} style={style} />
    ),
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

function graph(events: FlowHistoryEvent[], selectedEvent: FlowHistoryEvent | null = null, direction: 'lr' | 'tb' = 'lr') {
  return (
    <MemoryRouter>
      <ExecutionGraph
        direction={direction}
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

// Six Sync → Check Rounds: Folded merges Rounds 1–5 into one range and keeps Round 6 open.
const twoStepTypeRoundsHistory = decidedChainHistory([
  ...Array.from({ length: 5 }, () => [
    ['Sync', { nextSteps: [{ stepType: 'Check' }] }],
    ['Check', { nextSteps: [{ stepType: 'Sync' }] }],
  ] as Array<[string, unknown]>).flat(),
  ['Sync', { nextSteps: [{ stepType: 'Check' }] }],
  ['Check', { closeDecision: { closeDecisionType: 2 } }],
]);

const toSyncBatch = { nextSteps: [{ stepType: 'SyncBatch' }] };
const gracefulComplete = { closeDecision: { closeDecisionType: 2 } };

/** SyncBatch executions deciding GoTo, then a two-Round Checkpoint tail; the first ones fold into ×N. */
function syncBatchRunHistory(count: number): FlowHistoryEvent[] {
  return decidedChainHistory([
    ...Array.from({ length: count - 1 }, () => ['SyncBatch', toSyncBatch] as [string, unknown]),
    ['SyncBatch', { nextSteps: [{ stepType: 'Checkpoint' }] }],
    ['Checkpoint', { nextSteps: [{ stepType: 'Checkpoint' }] }],
    ['Checkpoint', gracefulComplete],
  ]);
}

function stepNodeElement(id: string): HTMLElement {
  return document.querySelector<HTMLElement>(`.react-flow__node[data-id="${id}"]`)!;
}

describe('Folded Graph', () => {
  it('folds only executions that return the same Step decision', () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    const toCheckpoint = { nextSteps: [{ stepType: 'Checkpoint' }], cancelStepTypes: ['Poll'] };
    render(graph(decidedChainHistory([
      ...Array.from({ length: 8 }, () => ['SyncBatch', toSyncBatch] as [string, unknown]),
      ['SyncBatch', toCheckpoint],
      ['Checkpoint', toSyncBatch],
      ['SyncBatch', toCheckpoint],
      ['Checkpoint', toSyncBatch],
      ['SyncBatch', toCheckpoint],
      ['Checkpoint', gracefulComplete],
    ])));
    const repeated = screen.getByText('×8').closest('.graph-step-label')!;
    expect(repeated.querySelector('.graph-execute-decision-text')?.textContent).toBe('GoTo');
    expect(repeated.querySelector('.graph-execute-decision-cancel')).toBeNull();
    // The ninth one also cancels Steps, so it stands alone.
    const canceling = stepNodeElement('SyncBatch-9');
    expect(canceling.querySelector('.graph-step-repeat')).toBeNull();
    expect(canceling.querySelector('.graph-execute-decision-cancel')?.textContent).toBe('CancelSteps×1');
    expect(canceling.querySelector('.graph-execute-decision-lines')?.getAttribute('title'))
      .toBe('GoTo → Checkpoint\nCancelSteps → Poll (entire Flow)');
  });

  it('opens a ×N run in place with its badge and folds it again with Hide', async () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    const canvas = { width: 1200, height: 320 };
    vi.spyOn(HTMLElement.prototype, 'clientWidth', 'get').mockImplementation(function width(this: HTMLElement) {
      return this.classList.contains('graph-canvas') ? canvas.width : 0;
    });
    vi.spyOn(HTMLElement.prototype, 'clientHeight', 'get').mockImplementation(function height(this: HTMLElement) {
      return this.classList.contains('graph-canvas') ? canvas.height : 0;
    });
    render(graph(syncBatchRunHistory(4)));
    const show = screen.getByRole('button', { name: 'Show 4 SyncBatch executions' });
    expect(show.getAttribute('aria-expanded')).toBe('false');
    expect(show.tabIndex).toBe(0);
    flow.state.viewport = { x: 5000, y: -10000, zoom: 0.8 };
    const callsBefore = flow.state.setViewportCalls.length;
    fireEvent.click(show);
    expect(shownNodeIDs()).toEqual(expect.arrayContaining([
      '__frame:run:SyncBatch-1', 'SyncBatch-1', 'SyncBatch-2', 'SyncBatch-3', 'SyncBatch-4',
    ]));
    // The opened run pans into view at the same zoom.
    await waitFor(() => expect(flow.state.setViewportCalls.length).toBeGreaterThan(callsBefore));
    expect(flow.state.setViewportCalls.at(-1)?.viewport.zoom).toBe(0.8);
    const hide = screen.getByRole('button', { name: 'Hide 4 SyncBatch executions' });
    expect(hide.getAttribute('aria-expanded')).toBe('true');
    expect(hide.textContent).toBe('Hide');
    fireEvent.click(hide);
    expect(shownNodeIDs()).not.toContain('__frame:run:SyncBatch-1');
    expect(screen.getByRole('button', { name: 'Show 4 SyncBatch executions' })).toBeTruthy();
  });

  it('elides the middle of a long opened run', () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    render(graph(syncBatchRunHistory(12)));
    fireEvent.click(screen.getByRole('button', { name: 'Show 12 SyncBatch executions' }));
    expect(shownNodeIDs()).toEqual(expect.arrayContaining(['SyncBatch-1', 'SyncBatch-2', 'SyncBatch-3', 'SyncBatch-11', 'SyncBatch-12']));
    expect(shownNodeIDs()).not.toContain('SyncBatch-6');
    expect(screen.getByText('… 7 more')).toBeTruthy();
  });

  it('keeps an opened run open through a Live update', () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    const { rerender } = render(graph(syncBatchRunHistory(4)));
    fireEvent.click(screen.getByRole('button', { name: 'Show 4 SyncBatch executions' }));
    rerender(graph(syncBatchRunHistory(5)));
    expect(shownNodeIDs()).toEqual(expect.arrayContaining(['__frame:run:SyncBatch-1', 'SyncBatch-1', 'SyncBatch-5']));
    expect(screen.getByRole('button', { name: 'Hide 5 SyncBatch executions' })).toBeTruthy();
  });

  it('opens the ×N run that holds an event selected outside the Graph', async () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    const events = syncBatchRunHistory(4);
    const { rerender } = render(graph(events));
    expect(shownNodeIDs()).not.toContain('SyncBatch-2');
    const execute2 = events.find((event) => (event.payload.context as Record<string, unknown> | undefined)?.stepExecutionId === 'SyncBatch-2')!;
    rerender(graph(events, execute2));
    await waitFor(() => expect(shownNodeIDs()).toContain('__frame:run:SyncBatch-1'));
    expect(shownNodeIDs()).toEqual(expect.arrayContaining(['SyncBatch-1', 'SyncBatch-2', 'SyncBatch-3', 'SyncBatch-4']));
  });

  it('keeps a ×N run folded when its shown execution is selected in the Graph', () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    const events = syncBatchRunHistory(4);
    const { rerender } = render(graph(events));
    const execute4 = events.find((event) => (event.payload.context as Record<string, unknown> | undefined)?.stepExecutionId === 'SyncBatch-4')!;
    fireEvent.click(stepNodeElement('SyncBatch-4').querySelector('.graph-method-execute')!);
    rerender(graph(events, execute4));
    expect(shownNodeIDs()).not.toContain('__frame:run:SyncBatch-1');
    expect(screen.getByRole('button', { name: 'Show 4 SyncBatch executions' })).toBeTruthy();
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
    render(graph(twoStepTypeRoundsHistory));
    const range = screen.getByRole('group', { name: '5 Rounds, Rounds 1–5, 10 Steps' });
    expect(range.classList.contains('node-round-range')).toBe(true);
    expect(range.querySelectorAll('.graph-round-deck-layer')).toHaveLength(2);
    expect(range.querySelector('.graph-round-deck-count')?.textContent).toBe('×5Rounds');
    expect(range.querySelector('.graph-round-deck-span')?.textContent).toBe('1–5 · 10 Steps');
    expect(range.querySelector('.graph-round-sequence')?.textContent).toBe('Sync → Check');
    const rangeNode = flow.state.nodes.find((node) => node.ariaLabel === '5 Rounds, Rounds 1–5, 10 Steps')!;
    // The 280 by 136 card keeps 16 by 12 of room on each side: the back cards use the top right.
    expect({ width: rangeNode.width, height: rangeNode.height }).toEqual({ width: 312, height: 160 });

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
    // A long span keeps its full text in the tooltip when the facts line shortens it.
    expect(range.querySelector('.graph-round-facts span[title="Rounds 1–19"]')).not.toBeNull();
  });

  it('shortens long facts inside the deck card and keeps the open button whole', () => {
    // The deck does not clip, so the facts line must fit by itself, as for "Rounds 10001–20000" and "123h 59m".
    const designCss = readFileSync('app/v1-design.css', 'utf8');
    const ruleBody = (selector: string) => {
      const start = designCss.indexOf(`${selector} {`);
      expect(start).toBeGreaterThanOrEqual(0);
      return designCss.slice(start, designCss.indexOf('}', start));
    };
    const factText = ruleBody('.v1-page .graph-round-deck-card .graph-round-facts > span');
    expect(factText).toContain('min-width: 0');
    expect(factText).toContain('text-overflow: ellipsis');
    expect(ruleBody('.v1-page .graph-round-action')).toContain('flex: none');
    expect(ruleBody('.v1-page .graph-round-facts')).toContain('min-width: 0');
  });

  it.each([
    // The 280 by 136 top card sits at (16, 12) in a 312 by 160 node; edges meet its side midpoints.
    ['lr', { target: { left: 16, top: 80 }, source: { left: 296, top: 80 } }],
    ['tb', { target: { left: 156, top: 12 }, source: { left: 156, top: 148 } }],
  ] as const)('meets %s edges at the deck top card, not the stacked cards', (direction, expected) => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    render(graph(twoStepTypeRoundsHistory, null, direction));
    const range = screen.getByRole('group', { name: '5 Rounds, Rounds 1–5, 10 Steps' });
    const handleAt = (type: string) => {
      const handle = range.querySelector<HTMLElement>(`.test-handle[data-type="${type}"]`)!;
      expect(handle.style.transform).toBe('translate(-50%, -50%)');
      return { left: Number.parseFloat(handle.style.left), top: Number.parseFloat(handle.style.top) };
    };
    expect({ target: handleAt('target'), source: handleAt('source') }).toEqual(expected);

    // Before React Flow measures the DOM Handles, edges use these boxes on the same side midpoints.
    const rangeNode = flow.state.nodes.find((node) => node.ariaLabel === '5 Rounds, Rounds 1–5, 10 Steps')!;
    expect(rangeNode.handles?.map(({ x, y, width, height }) => ({ x, y, width, height })))
      .toEqual([{ x: 16, y: 12, width: 280, height: 136 }, { x: 16, y: 12, width: 280, height: 136 }]);

    fireEvent.click(screen.getByRole('button', { name: /Show Rounds/ }));
    const singleRound = screen.getByText('Round 1').closest('.react-flow__node')!;
    singleRound.querySelectorAll<HTMLElement>('.test-handle').forEach((handle) => expect(handle.style.left).toBe(''));
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
