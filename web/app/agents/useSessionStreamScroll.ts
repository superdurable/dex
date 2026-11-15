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
  latestRef,
  isLoaded,
  followSignature,
  waitingCallIds,
}: {
  /** The session section, which holds the stream and the composer. */
  sectionRef: RefObject<HTMLElement | null>;
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

  // The scroll container varies, so a capturing listener hears whichever element scrolls.
  useEffect(() => {
    document.addEventListener('scroll', measureAtLatest, { capture: true, passive: true });
    window.addEventListener('resize', measureAtLatest);
    return () => {
      document.removeEventListener('scroll', measureAtLatest, { capture: true });
      window.removeEventListener('resize', measureAtLatest);
    };
  }, [measureAtLatest]);

  // Scroll margins keep scrolled-to content above the sticky composer.
  const updateComposerHeight = useCallback(() => {
    const section = sectionRef.current;
    const controls = section?.querySelector<HTMLElement>('.ses-controls');
    if (!section || !controls) return;
    const isSticky = getComputedStyle(controls).position === 'sticky';
    section.style.setProperty('--ses-composer-height', `${isSticky ? controls.offsetHeight : 0}px`);
  }, [sectionRef]);

  useEffect(() => {
    const controls = sectionRef.current?.querySelector<HTMLElement>('.ses-controls');
    if (!controls || typeof ResizeObserver === 'undefined') return;
    const observer = new ResizeObserver(updateComposerHeight);
    observer.observe(controls);
    return () => observer.disconnect();
  }, [sectionRef, isLoaded, updateComposerHeight]);

  // The composer turns sticky or static without changing size, which the observer misses.
  useLayoutEffect(updateComposerHeight);

  useLayoutEffect(() => {
    if (!isLoaded) return;
    const isFirstRead = shownSignatureRef.current === null;
    const hasNewItem = !isFirstRead && shownSignatureRef.current !== followSignature;
    shownSignatureRef.current = followSignature;
    if (hasNewItem && isAtLatestRef.current) latestRef.current?.scrollIntoView?.({ block: 'end' });
    measureAtLatest();
  }, [isLoaded, followSignature, latestRef, measureAtLatest]);

  // Runs after following, so a new waiting call's controls end up in view.
  const waitingKey = waitingCallIds.join('\n');
  useLayoutEffect(() => {
    if (!isLoaded) return;
    const seen = seenWaitingCallIdsRef.current;
    seenWaitingCallIdsRef.current = new Set(waitingCallIds);
    if (seen === null) return;
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
