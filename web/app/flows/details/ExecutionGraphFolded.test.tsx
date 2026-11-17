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
  data: { label: ReactNode };
}

const flow = vi.hoisted(() => {
  const state = { nodes: [] as FakeNode[] };
  const instance = {
    getNode: () => undefined,
    getViewport: () => ({ x: 0, y: 0, zoom: 1 }),
    getZoom: () => 1,
    setViewport: () => Promise.resolve(true),
    setCenter: () => Promise.resolve(true),
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
        {nodes.map((node) => <div className="react-flow__node" data-id={node.id} key={node.id}>{node.data.label}</div>)}
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

describe('Folded Graph', () => {
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
    expect(shownNodeIDs()).toEqual(['__round-frame:Step-20', '__start__', '__round:Step-1', 'Step-20']);
    unmount();

    render(graph(loopEvents));
    expect(screen.getByRole('button', { name: 'Folded' }).getAttribute('aria-pressed')).toBe('true');
    expect(shownNodeIDs()).toContain('__round:Step-1');
  });

  it('falls back to off when storage is blocked', () => {
    stubStorage({ isBlocked: true });
    render(graph(loopEvents));
    expect(screen.getByRole('button', { name: 'Folded' }).getAttribute('aria-pressed')).toBe('false');
    fireEvent.click(screen.getByRole('button', { name: 'Folded' }));
    expect(screen.getByRole('button', { name: 'Folded' }).getAttribute('aria-pressed')).toBe('true');
  });

  it('opens a folded Round with Show Steps and folds it again with Hide Steps', () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    render(graph(loopEvents));
    fireEvent.click(screen.getByRole('button', { name: /Show Steps/ }));
    expect(shownNodeIDs()).toEqual(expect.arrayContaining(['Step-1', 'Step-2', 'Step-3', 'Step-18', 'Step-19', 'Step-20']));
    expect(screen.getByText('… 14 more')).toBeTruthy();
    fireEvent.click(screen.getByRole('button', { name: /Hide Steps/ }));
    expect(shownNodeIDs()).toContain('__round:Step-1');
  });

  it('opens the folded Round that holds the selected Step', async () => {
    storedValues.set(graphViewStorageKeys.isFolded, 'true');
    const selected = loopEvents.find((event) => event.eventId === 20)!;
    render(graph(loopEvents, selected));
    await waitFor(() => expect(shownNodeIDs()).toContain('Step-10'));
    expect(shownNodeIDs()).not.toContain('__round:Step-1');
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

describe('Graph mini map', () => {
  it('is shown by default, hides to a map button, and remembers the choice', () => {
    const { unmount } = render(graph(loopEvents));
    expect(screen.getByRole('img', { name: /mini map/ })).toBeTruthy();
    expect(screen.queryByRole('button', { name: /Mini Map/ })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Hide mini map' }));
    expect(screen.queryByRole('img', { name: /mini map/ })).toBeNull();
    expect(storedValues.get(graphViewStorageKeys.isMiniMapHidden)).toBe('true');
    unmount();

    render(graph(loopEvents));
    expect(screen.queryByRole('img', { name: /mini map/ })).toBeNull();
    fireEvent.click(screen.getByRole('button', { name: 'Show mini map' }));
    expect(screen.getByRole('img', { name: /mini map/ })).toBeTruthy();
    expect(storedValues.get(graphViewStorageKeys.isMiniMapHidden)).toBe('false');
  });
});
