// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

'use client';

import { useMemo, useRef } from 'react';
import { useStore, useViewport, type Node } from '@xyflow/react';
import type { StepGraphNode } from '@/lib/types';
import type { StepRoundGroup } from './graphFolding';

/** The node data the overview reads: what the node shows and its Step or Round. */
export interface GraphOverviewNodeData extends Record<string, unknown> {
  display: 'node' | 'group' | 'more' | 'frame';
  model?: StepGraphNode;
  round?: StepRoundGroup;
}

/** The narrowest viewport window, so it stays visible on a very wide graph. */
const minimumWindowPercent = 3;

/** Overview fills: Step status, folded Rounds in the accent; frames are left out. */
export function overviewNodeColor(data: GraphOverviewNodeData): string {
  if (data.display === 'more') return 'var(--ui-hover)';
  if (data.display === 'group') {
    return data.round && data.round.failedCount > 0 ? 'var(--tone-fail)' : 'var(--graph-folded-fill)';
  }
  switch (data.model?.status) {
    case 'Failed': return 'var(--tone-fail)';
    case 'Waiting':
    case 'Pending': return 'var(--tone-wait)';
    case 'Active': return 'var(--tone-accent)';
    case 'Completed': return 'color-mix(in srgb, var(--ui-ink-3) 45%, transparent)';
    default: return 'var(--ui-hover)';
  }
}

/** A whole-graph overview; width and height scale separately, so long graphs still fill it. */
export function GraphOverview({
  nodes,
  bounds,
  onCenter,
}: {
  nodes: Array<Node<GraphOverviewNodeData>>;
  bounds: { width: number; height: number };
  onCenter: (point: { x: number; y: number }) => void;
}) {
  const viewport = useViewport();
  const canvasWidth = useStore((store) => store.width);
  const canvasHeight = useStore((store) => store.height);
  const track = useRef<HTMLDivElement>(null);
  const isDragging = useRef(false);
  const width = Math.max(1, bounds.width);
  const height = Math.max(1, bounds.height);
  // Panning only moves the window, so the node shapes redraw only with the graph.
  const nodeShapes = useMemo(() => (
    <svg aria-hidden="true" preserveAspectRatio="none" viewBox={`0 0 ${width} ${height}`}>
      {nodes.map((node) => (node.data.display === 'frame' ? null : (
        <rect
          height={typeof node.style?.height === 'number' ? node.style.height : node.height ?? 0}
          key={node.id}
          style={{ fill: overviewNodeColor(node.data) }}
          width={typeof node.style?.width === 'number' ? node.style.width : node.width ?? 0}
          x={node.position.x}
          y={node.position.y}
        />
      )))}
    </svg>
  ), [height, nodes, width]);
  const shownLeft = -viewport.x / viewport.zoom;
  const shownTop = -viewport.y / viewport.zoom;
  const shownWidth = canvasWidth / viewport.zoom;
  const shownHeight = canvasHeight / viewport.zoom;
  const windowLeft = clampPercent((shownLeft / width) * 100);
  const windowTop = clampPercent((shownTop / height) * 100);
  const windowWidth = Math.max(
    minimumWindowPercent,
    clampPercent(((shownLeft + shownWidth) / width) * 100) - windowLeft,
  );
  const windowHeight = clampPercent(((shownTop + shownHeight) / height) * 100) - windowTop;

  const centerOnPointer = (clientX: number, clientY: number) => {
    const box = track.current?.getBoundingClientRect();
    if (!box || box.width <= 0 || box.height <= 0) return;
    onCenter({
      x: ((clientX - box.left) / box.width) * width,
      y: ((clientY - box.top) / box.height) * height,
    });
  };

  return (
    <div
      aria-label="Execution graph mini map; click to move the view"
      className="graph-overview"
      onPointerDown={(pointerEvent) => {
        isDragging.current = true;
        pointerEvent.currentTarget.setPointerCapture?.(pointerEvent.pointerId);
        centerOnPointer(pointerEvent.clientX, pointerEvent.clientY);
      }}
      onPointerMove={(pointerEvent) => {
        if (isDragging.current) centerOnPointer(pointerEvent.clientX, pointerEvent.clientY);
      }}
      onPointerUp={() => { isDragging.current = false; }}
      onPointerCancel={() => { isDragging.current = false; }}
      ref={track}
      role="img"
    >
      {nodeShapes}
      <span
        className="graph-overview-window"
        style={{
          left: `${windowLeft}%`,
          top: `${windowTop}%`,
          width: `${Math.min(windowWidth, 100 - windowLeft)}%`,
          height: `${Math.max(0, windowHeight)}%`,
        }}
      />
    </div>
  );
}

function clampPercent(value: number): number {
  if (!Number.isFinite(value)) return 0;
  return Math.min(100, Math.max(0, value));
}
