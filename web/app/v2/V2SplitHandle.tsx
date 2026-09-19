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
export const LIST_WIDTH_DEFAULT = 512;
export const LIST_WIDTH_MIN = 352;
export const CANVAS_WIDTH_MIN = 280;
export const CASE_HEIGHT_MIN = 140;
export const LIST_REMAIN_MIN = 200;
export const CASE_HEIGHT_FRAC = 0.42;

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
}: {
  axis: 'column' | 'row';
  cssVariable: '--v2-list-w' | '--v2-case-h';
  targetRef: RefObject<HTMLElement | null>;
  measureRef: RefObject<HTMLElement | null>;
  value: number;
  ariaLabel: string;
  onCommit: (px: number) => void;
}) {
  const dragRef = useRef<{ startPointer: number; startSize: number } | null>(null);
  const liveRef = useRef(value);

  useEffect(() => {
    liveRef.current = value;
  }, [value]);

  const clampLive = useCallback((desired: number) => {
    const box = measureRef.current?.getBoundingClientRect();
    if (!box) return desired;
    return axis === 'column'
      ? clampListWidth(desired, box.width)
      : clampCaseHeight(desired, box.height);
  }, [axis, measureRef]);

  const writeLive = useCallback((next: number, handle: HTMLElement) => {
    liveRef.current = next;
    targetRef.current?.style.setProperty(cssVariable, `${Math.round(next)}px`);
    const box = measureRef.current?.getBoundingClientRect();
    if (!box) return;
    const floor = axis === 'column' ? LIST_WIDTH_MIN : CASE_HEIGHT_MIN;
    const ceiling = axis === 'column'
      ? Math.min(box.width * 0.6, box.width - CANVAS_WIDTH_MIN)
      : Math.max(floor, box.height - LIST_REMAIN_MIN);
    handle.dataset.atFloor = next <= floor ? 'true' : '';
    handle.dataset.atCeiling = next >= ceiling ? 'true' : '';
  }, [axis, cssVariable, measureRef, targetRef]);

  const onPointerDown = useCallback((event: PointerEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    const box = measureRef.current?.getBoundingClientRect();
    if (!box) return;
    const startSize = axis === 'column'
      ? clampListWidth(liveRef.current || box.width * 0.35, box.width)
      : clampCaseHeight(liveRef.current, box.height);
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
  }, [axis, measureRef]);

  const onPointerMove = useCallback((event: PointerEvent<HTMLDivElement>) => {
    const drag = dragRef.current;
    if (!drag) return;
    const delta = axis === 'column'
      ? event.clientX - drag.startPointer
      : drag.startPointer - event.clientY;
    writeLive(clampLive(drag.startSize + delta), event.currentTarget);
  }, [axis, clampLive, writeLive]);

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
      if (event.key === 'ArrowLeft') next = current - step;
      if (event.key === 'ArrowRight') next = current + step;
    } else {
      if (event.key === 'ArrowUp') next = current + step;
      if (event.key === 'ArrowDown') next = current - step;
    }
    if (next === null) return;
    event.preventDefault();
    onCommit(clampLive(next));
  }, [axis, clampLive, onCommit]);

  return (
    <div
      role="separator"
      aria-orientation={axis === 'column' ? 'vertical' : 'horizontal'}
      aria-label={ariaLabel}
      aria-valuenow={Math.round(value)}
      aria-valuemin={axis === 'column' ? LIST_WIDTH_MIN : CASE_HEIGHT_MIN}
      tabIndex={0}
      data-axis={axis}
      className="v2-split"
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={endDrag}
      onPointerCancel={endDrag}
      onKeyDown={onKeyDown}
    />
  );
}
