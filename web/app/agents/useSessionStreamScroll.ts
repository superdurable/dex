// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { useCallback, useEffect, useLayoutEffect, useRef, useState, type RefObject } from 'react';

/** How far above the composer the end of the stream may sit and still count as read to the end. */
const AT_LATEST_TOLERANCE_PX = 32;

/** Follows new stream items while the reader is at the end, and shows a new waiting call's controls. */
export function useSessionStreamScroll({
  sectionRef,
  streamRef,
  latestRef,
  isLoaded,
  followSignature,
  waitingCallIds,
}: {
  /** The session section, which holds the stream and the composer. */
  sectionRef: RefObject<HTMLElement | null>;
  /** The transcript; its size changes when the reader opens a long message, note, or call. */
  streamRef: RefObject<HTMLElement | null>;
  /** An empty element at the end of the stream. */
  latestRef: RefObject<HTMLElement | null>;
  /** False until the first session read; nothing scrolls by itself on that first read. */
  isLoaded: boolean;
  /** Changes when the newest item changes, but not when earlier messages load above it. */
  followSignature: string;
  /** Tool call IDs that wait for an approval or an answer, in stream order. */
  waitingCallIds: readonly string[];
}): { isAtLatest: boolean; jumpToLatest: () => void; showWaitingCall: (toolCallId: string) => void } {
  const [isAtLatest, setAtLatest] = useState(true);
  const isAtLatestRef = useRef(true);
  /** Whether the reader was at the end before this commit's new items arrived. */
  const wasFollowingRef = useRef(false);
  const shownSignatureRef = useRef<string | null>(null);
  const seenWaitingCallIdsRef = useRef<Set<string> | null>(null);

  const measureAtLatest = useCallback(() => {
    const section = sectionRef.current;
    const latest = latestRef.current;
    if (!section || !latest) return;
    const controls = section.querySelector('.ses-controls');
    const viewBottom = Math.min(
      scrollContainerBottom(section),
      controls ? controls.getBoundingClientRect().top : Number.POSITIVE_INFINITY,
    );
    const isNowAtLatest = latest.getBoundingClientRect().top <= viewBottom + AT_LATEST_TOLERANCE_PX;
    isAtLatestRef.current = isNowAtLatest;
    setAtLatest(isNowAtLatest);
  }, [sectionRef, latestRef]);
  const scheduleMeasureAtLatest = useFrameCoalesced(measureAtLatest);

  // Scroll margins keep scrolled-to content above the sticky composer.
  const updateComposerHeight = useCallback(() => {
    const section = sectionRef.current;
    const controls = section?.querySelector<HTMLElement>('.ses-controls');
    if (!section || !controls) return;
    const isSticky = getComputedStyle(controls).position === 'sticky';
    section.style.setProperty('--ses-composer-height', `${isSticky ? controls.offsetHeight : 0}px`);
  }, [sectionRef]);
  const scheduleComposerHeight = useFrameCoalesced(updateComposerHeight);

  // The scroll container varies, so a capturing listener hears whichever element scrolls.
  useEffect(() => {
    document.addEventListener('scroll', scheduleMeasureAtLatest, { capture: true, passive: true });
    window.addEventListener('resize', scheduleMeasureAtLatest);
    return () => {
      document.removeEventListener('scroll', scheduleMeasureAtLatest, { capture: true });
      window.removeEventListener('resize', scheduleMeasureAtLatest);
    };
  }, [scheduleMeasureAtLatest]);

  // Opening a long message, note, or call moves the end without a scroll event.
  useEffect(() => {
    const stream = streamRef.current;
    const controls = sectionRef.current?.querySelector<HTMLElement>('.ses-controls');
    if (!stream || !controls || typeof ResizeObserver === 'undefined') return;
    const streamObserver = new ResizeObserver(scheduleMeasureAtLatest);
    streamObserver.observe(stream);
    const composerObserver = new ResizeObserver(scheduleComposerHeight);
    composerObserver.observe(controls);
    return () => {
      streamObserver.disconnect();
      composerObserver.disconnect();
    };
  }, [sectionRef, streamRef, isLoaded, scheduleMeasureAtLatest, scheduleComposerHeight]);

  // The composer turns sticky or static without changing size, which the observer misses.
  useEffect(scheduleComposerHeight);

  // Runs first on every commit, before following measures the new end.
  useLayoutEffect(() => {
    wasFollowingRef.current = isAtLatestRef.current;
  });

  useLayoutEffect(() => {
    if (!isLoaded) return;
    const isFirstRead = shownSignatureRef.current === null;
    const hasNewItem = !isFirstRead && shownSignatureRef.current !== followSignature;
    shownSignatureRef.current = followSignature;
    if (hasNewItem && isAtLatestRef.current && !isEditingWaitingCall()) latestRef.current?.scrollIntoView?.({ block: 'end' });
    measureAtLatest();
  }, [isLoaded, followSignature, latestRef, measureAtLatest]);

  // Runs after following. Only a reader who follows the end is moved; others have the header chip.
  const waitingKey = waitingCallIds.join('\n');
  useLayoutEffect(() => {
    if (!isLoaded) return;
    const seen = seenWaitingCallIdsRef.current;
    seenWaitingCallIdsRef.current = new Set(waitingCallIds);
    if (seen === null || !wasFollowingRef.current || isEditingWaitingCall()) return;
    const firstNew = waitingCallIds.find((toolCallId) => !seen.has(toolCallId));
    if (firstNew !== undefined) waitingCallControls(sectionRef.current, firstNew)?.scrollIntoView?.({ block: 'nearest' });
    // waitingKey stands for waitingCallIds, whose array identity changes on every read.
  }, [isLoaded, waitingKey, sectionRef]);

  const jumpToLatest = useCallback(() => {
    latestRef.current?.scrollIntoView?.({ block: 'end' });
    measureAtLatest();
  }, [latestRef, measureAtLatest]);

  const showWaitingCall = useCallback((toolCallId: string) => {
    const controls = waitingCallControls(sectionRef.current, toolCallId);
    if (!controls) return;
    controls.scrollIntoView?.({ block: 'nearest' });
    controls.querySelector<HTMLElement>('button:not(:disabled), textarea:not(:disabled)')?.focus({ preventScroll: true });
  }, [sectionRef]);

  return { isAtLatest, jumpToLatest, showWaitingCall };
}

/** Returns a stable function that runs the latest `work` at most once per animation frame. */
function useFrameCoalesced(work: () => void): () => void {
  const workRef = useRef(work);
  workRef.current = work;
  const frameRef = useRef<number | null>(null);
  useEffect(() => () => {
    if (frameRef.current !== null) cancelAnimationFrame(frameRef.current);
  }, []);
  return useCallback(() => {
    if (frameRef.current !== null) return;
    frameRef.current = requestAnimationFrame(() => {
      frameRef.current = null;
      workRef.current();
    });
  }, []);
}

/** True while focus is inside an approval or question card; a new waiting call must not move the view. */
function isEditingWaitingCall(): boolean {
  return document.activeElement?.closest('.atc > .agc') != null;
}

/** The approve, reject, or answer controls of one call card, or the card when it has none. */
function waitingCallControls(section: HTMLElement | null, toolCallId: string): HTMLElement | null {
  if (!section) return null;
  const card = [...section.querySelectorAll<HTMLElement>('.atc')]
    .find((candidate) => candidate.dataset.toolCallId === toolCallId);
  return card?.querySelector<HTMLElement>(':scope > .agc') ?? card ?? null;
}

/** The bottom edge, on screen, of the nearest element that scrolls the session, or of the window. */
function scrollContainerBottom(section: HTMLElement): number {
  for (let element = section.parentElement; element; element = element.parentElement) {
    const overflowY = getComputedStyle(element).overflowY;
    if ((overflowY === 'auto' || overflowY === 'scroll') && element.scrollHeight > element.clientHeight) {
      return element.getBoundingClientRect().bottom;
    }
  }
  return window.innerHeight;
}
