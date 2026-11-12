// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode, useEffect, useRef, useState} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {CircleCheck, CirclePlay, CircleStop, Hourglass, Play, RotateCcw, Zap} from 'lucide-react';

import timelines from '@site/src/data/crash-timeline.json';
import styles from './CrashTimeline.module.css';

type ActorKey = 'first' | 'server' | 'second';
type EventKind = 'step' | 'wait' | 'stop' | 'start' | 'fire' | 'done';
type TimelineEvent = {actor: ActorKey; kind: EventKind; text: string};
type TimelineCopy = {
  label: string;
  actors: Record<ActorKey, string>;
  events: TimelineEvent[];
  summary: string;
  replay: string;
};
export type CrashTimelineVariant = keyof typeof timelines;

const STEP_MS = 1400;
const kindIcons = {step: Play, wait: Hourglass, stop: CircleStop, start: CirclePlay, fire: Zap, done: CircleCheck};
const actorOrder: ActorKey[] = ['first', 'server', 'second'];

function usePrefersReducedMotion(): boolean {
  const [isReduced, setIsReduced] = useState(false);
  useEffect(() => {
    const query = window.matchMedia('(prefers-reduced-motion: reduce)');
    setIsReduced(query.matches);
    const onChange = (): void => setIsReduced(query.matches);
    query.addEventListener('change', onChange);
    return () => query.removeEventListener('change', onChange);
  }, []);
  return isReduced;
}

/** Index of the last shown event; it advances once the figure scrolls into view. */
function useRevealedCount(total: number, isReduced: boolean, runId: number): [number, React.RefObject<HTMLElement>] {
  const ref = useRef<HTMLElement>(null);
  const [revealed, setRevealed] = useState(total);
  useEffect(() => {
    if (isReduced || typeof IntersectionObserver === 'undefined' || !ref.current) {
      setRevealed(total);
      return undefined;
    }
    setRevealed(0);
    let timer: number | undefined;
    const observer = new IntersectionObserver((entries) => {
      if (!entries.some((entry) => entry.isIntersecting)) return;
      observer.disconnect();
      timer = window.setInterval(() => {
        setRevealed((count) => {
          if (count + 1 >= total) window.clearInterval(timer);
          return Math.min(count + 1, total);
        });
      }, STEP_MS);
    }, {threshold: 0.4});
    observer.observe(ref.current);
    return () => {
      observer.disconnect();
      window.clearInterval(timer);
    };
  }, [total, isReduced, runId]);
  return [revealed, ref];
}

function actorState(actor: ActorKey, events: TimelineEvent[], revealed: number): 'idle' | 'on' | 'off' {
  const shown = events.slice(0, revealed).filter((event) => event.actor === actor);
  if (actor === 'server') return 'on';
  if (shown.some((event) => event.kind === 'stop')) return 'off';
  return shown.length > 0 ? 'on' : 'idle';
}

/**
 * Animates what happens when a Worker stops in the middle of a Flow or an agent session.
 * The Markdown export writes the same events as a numbered list.
 */
export default function CrashTimeline({variant}: {variant: CrashTimelineVariant}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const byLocale = timelines[variant] as Record<string, TimelineCopy>;
  const copy = byLocale[i18n.currentLocale] ?? byLocale.en;
  const isReduced = usePrefersReducedMotion();
  const [runId, setRunId] = useState(0);
  const [revealed, ref] = useRevealedCount(copy.events.length, isReduced, runId);
  const isDone = revealed >= copy.events.length;
  return (
    <figure ref={ref} className={styles.timeline} aria-label={copy.label}>
      <div className={styles.actors} aria-hidden="true">
        {actorOrder.map((actor) => (
          <span key={actor} className={`${styles.actor} ${styles[`actor_${actorState(actor, copy.events, revealed)}`]}`}>
            <span className={styles.dot} />
            {copy.actors[actor]}
          </span>
        ))}
      </div>
      <ol className={styles.events}>
        {copy.events.map((event, index) => {
          const Icon = kindIcons[event.kind];
          const isShown = index < revealed;
          return (
            <li
              key={event.text}
              className={`${styles.event} ${styles[`kind_${event.kind}`]} ${styles[`lane_${event.actor}`]}${isShown ? ` ${styles.shown}` : ''}`}>
              <span className={styles.lane}>{copy.actors[event.actor]}</span>
              <span className={styles.body}>
                <Icon className={styles.icon} aria-hidden="true" />
                <span>{event.text}</span>
              </span>
            </li>
          );
        })}
      </ol>
      <figcaption className={`${styles.summary}${isDone ? ` ${styles.shown}` : ''}`}>
        <CircleCheck className={styles.icon} aria-hidden="true" />
        <span className={styles.summaryText}>{copy.summary}</span>
        {!isReduced ? (
          <button type="button" className={styles.replay} onClick={() => setRunId((id) => id + 1)} disabled={!isDone}>
            <RotateCcw className={styles.icon} aria-hidden="true" />
            {copy.replay}
          </button>
        ) : null}
      </figcaption>
    </figure>
  );
}
