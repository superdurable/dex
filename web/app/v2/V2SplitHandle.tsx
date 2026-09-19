// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useRef, type PointerEvent, type KeyboardEvent, type RefObject } from 'react';

export const LIST_WIDTH_KEY = 'dex-web.v2-list-w';
export const CASE_HEIGHT_KEY = 'dex-web.v2-case-h';
export const PANEL_WIDTH_KEY = 'dex-web.v2-panel-w';
export const LIST_WIDTH_DEFAULT = 512;
export const LIST_WIDTH_MIN = 352;
export const CANVAS_WIDTH_MIN = 280;
export const CASE_HEIGHT_MIN = 140;
export const LIST_REMAIN_MIN = 200;
export const CASE_HEIGHT_FRAC = 0.42;
export const PANEL_WIDTH_DEFAULT = 360;
export const PANEL_WIDTH_MIN = 280;
export const PANEL_CANVAS_REMAIN_MIN = 240;
export const DEF_HEIGHT_KEY = 'dex-web.v2-def-h';
export const DEF_HEIGHT_DEFAULT = 220;
export const DEF_HEIGHT_MIN = 96;
export const EXEC_REMAIN_MIN = 180;

export function readStoredPixels(key: string): number {
  try {
    const raw = window.localStorage.getItem(key);
    const value = raw === null ? Number.NaN : Number(raw);
    return Number.isFinite(value) && value > 0 ? value : Number.NaN;
  } catch {
    return Number.NaN;
  }
}

export function writeStoredPixels(key: string, value: number): void {
  try {
    window.localStorage.setItem(key, String(Math.round(value)));
  } catch {
    // Private mode can throw; a pane size is not worth taking the app down.
  }
}

export function clampListWidth(desired: number, containerWidth: number): number {
  const container = finiteSize(containerWidth);
  const floor = LIST_WIDTH_MIN;
  const ceiling = Math.min(container * 0.6, container - CANVAS_WIDTH_MIN);
  if (ceiling <= floor) return Math.max(160, ceiling);
  const want = Number.isFinite(desired) && desired > 0 ? desired : LIST_WIDTH_DEFAULT;
  return Math.min(Math.max(want, floor), ceiling);
}

export function clampCaseHeight(desired: number, paneHeight: number): number {
  const pane = finiteSize(paneHeight);
  const floor = CASE_HEIGHT_MIN;
  const ceiling = Math.max(floor, pane - LIST_REMAIN_MIN);
  const want = Number.isFinite(desired) && desired > 0 ? desired : pane * CASE_HEIGHT_FRAC;
  return Math.min(Math.max(want, floor), ceiling);
}

export function clampPanelWidth(desired: number, canvasWidth: number): number {
  const container = finiteSize(canvasWidth);
  const floor = PANEL_WIDTH_MIN;
  const ceiling = Math.max(floor, Math.min(container * 0.7, container - PANEL_CANVAS_REMAIN_MIN));
  const want = Number.isFinite(desired) && desired > 0 ? desired : PANEL_WIDTH_DEFAULT;
  return Math.min(Math.max(want, floor), ceiling);
}

export function clampDefHeight(desired: number, bodyHeight: number): number {
  const body = finiteSize(bodyHeight);
  const floor = DEF_HEIGHT_MIN;
  const ceiling = Math.max(floor, body - EXEC_REMAIN_MIN);
  const want = Number.isFinite(desired) && desired > 0 ? desired : Math.min(DEF_HEIGHT_DEFAULT, ceiling);
  return Math.min(Math.max(want, floor), ceiling);
}

function finiteSize(value: number): number {
  if (!Number.isFinite(value)) return 0;
  return Math.max(0, value);
}

export function V2SplitHandle({
  axis,
  cssVariable,
  targetRef,
  measureRef,
  value,
  ariaLabel,
  onCommit,
  invert = false,
  edge = 'end',
}: {
  axis: 'column' | 'row';
  cssVariable: '--v2-list-w' | '--v2-case-h' | '--v2-panel-w' | '--v2-def-h';
  targetRef: RefObject<HTMLElement | null>;
  measureRef: RefObject<HTMLElement | null>;
  value: number;
  ariaLabel: string;
  onCommit: (px: number) => void;
  /** When true, dragging toward the start of the axis grows the measured size. */
  invert?: boolean;
  edge?: 'start' | 'end' | 'between';
}) {
  const dragRef = useRef<{ startPointer: number; startSize: number } | null>(null);
  const liveRef = useRef(value);

  useEffect(() => {
    liveRef.current = value;
  }, [value]);

  const clampLive = useCallback((desired: number) => {
    const box = measureRef.current?.getBoundingClientRect();
    if (!box) return desired;
    if (cssVariable === '--v2-panel-w') return clampPanelWidth(desired, box.width);
    if (cssVariable === '--v2-def-h') return clampDefHeight(desired, box.height);
    return axis === 'column'
      ? clampListWidth(desired, box.width)
      : clampCaseHeight(desired, box.height);
  }, [axis, cssVariable, measureRef]);

  const writeLive = useCallback((next: number, handle: HTMLElement) => {
    liveRef.current = next;
    targetRef.current?.style.setProperty(cssVariable, `${Math.round(next)}px`);
    const box = measureRef.current?.getBoundingClientRect();
    if (!box) return;
    let floor = LIST_WIDTH_MIN;
    let ceiling = box.width;
    if (cssVariable === '--v2-panel-w') {
      floor = PANEL_WIDTH_MIN;
      ceiling = Math.max(floor, Math.min(box.width * 0.7, box.width - PANEL_CANVAS_REMAIN_MIN));
    } else if (cssVariable === '--v2-def-h') {
      floor = DEF_HEIGHT_MIN;
      ceiling = Math.max(floor, box.height - EXEC_REMAIN_MIN);
    } else if (axis === 'column') {
      floor = LIST_WIDTH_MIN;
      ceiling = Math.min(box.width * 0.6, box.width - CANVAS_WIDTH_MIN);
    } else {
      floor = CASE_HEIGHT_MIN;
      ceiling = Math.max(floor, box.height - LIST_REMAIN_MIN);
    }
    handle.dataset.atFloor = next <= floor ? 'true' : '';
    handle.dataset.atCeiling = next >= ceiling ? 'true' : '';
  }, [axis, cssVariable, measureRef, targetRef]);

  const onPointerDown = useCallback((event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    const box = measureRef.current?.getBoundingClientRect();
    if (!box) return;
    const fallback = cssVariable === '--v2-panel-w'
      ? PANEL_WIDTH_DEFAULT
      : cssVariable === '--v2-def-h'
        ? Math.min(DEF_HEIGHT_DEFAULT, Math.max(DEF_HEIGHT_MIN, box.height - EXEC_REMAIN_MIN))
        : axis === 'column'
          ? box.width * 0.35
          : box.height * CASE_HEIGHT_FRAC;
    const startSize = clampLive(liveRef.current || fallback);
    liveRef.current = startSize;
    dragRef.current = {
      startPointer: axis === 'column' ? event.clientX : event.clientY,
      startSize,
    };
    document.body.style.cursor = axis === 'column' ? 'col-resize' : 'row-resize';
    document.body.style.userSelect = 'none';
    try {
      event.currentTarget.setPointerCapture(event.pointerId);
    } catch {
      // Pointer already released; pointermove on this handle still fires while captured elsewhere.
    }
  }, [axis, clampLive, cssVariable, measureRef]);

  const onPointerMove = useCallback((event: PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag) return;
    const rawDelta = axis === 'column'
      ? event.clientX - drag.startPointer
      : drag.startPointer - event.clientY;
    const delta = invert ? -rawDelta : rawDelta;
    writeLive(clampLive(drag.startSize + delta), event.currentTarget);
  }, [axis, clampLive, invert, writeLive]);

  const endDrag = useCallback((event: PointerEvent<HTMLDivElement>) => {
    if (!dragRef.current) return;
    dragRef.current = null;
    document.body.style.cursor = '';
    document.body.style.userSelect = '';
    delete event.currentTarget.dataset.atFloor;
    delete event.currentTarget.dataset.atCeiling;
    try {
      event.currentTarget.releasePointerCapture(event.pointerId);
    } catch {
      // Capture was already released with the pointer.
    }
    onCommit(liveRef.current);
  }, [onCommit]);

  const onKeyDown = useCallback((event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.shiftKey ? 40 : 8;
    const current = liveRef.current > 0 ? liveRef.current : clampLive(0);
    let next: number | null = null;
    if (axis === 'column') {
      if (event.key === 'ArrowLeft') next = invert ? current + step : current - step;
      if (event.key === 'ArrowRight') next = invert ? current - step : current + step;
    } else {
      if (event.key === 'ArrowUp') next = invert ? current - step : current + step;
      if (event.key === 'ArrowDown') next = invert ? current + step : current - step;
    }
    if (next === null) return;
    event.preventDefault();
    onCommit(clampLive(next));
  }, [axis, clampLive, invert, onCommit]);

  const valueMin = cssVariable === '--v2-panel-w'
    ? PANEL_WIDTH_MIN
    : cssVariable === '--v2-def-h'
      ? DEF_HEIGHT_MIN
      : axis === 'column'
        ? LIST_WIDTH_MIN
        : CASE_HEIGHT_MIN;

  return (
    <div
      role="separator"
      aria-orientation={axis === 'column' ? 'vertical' : 'horizontal'}
      aria-label={ariaLabel}
      aria-valuenow={Math.round(value)}
      aria-valuemin={valueMin}
      tabIndex={0}
      data-axis={axis}
      data-edge={edge}
      className="v2-split"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onKeyDown={onKeyDown}
    />
  );
}
