// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode, useEffect, useRef, useState} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {CircleCheck, CirclePlay, CircleStop, Flag, FlagOff, Hourglass, Play, RotateCcw, Zap} from 'lucide-react';

import timelines from '@site/src/data/crash-timeline.json';
import styles from './CrashTimeline.module.css';

type ActorKey = 'first' | 'server' | 'second';
type EventKind = 'flow' | 'step' | 'wait' | 'stop' | 'start' | 'fire' | 'done';
/** History events are the ones Dex Web numbers; Worker stops and starts are drawn between them. */
type TimelineEvent = {actor: ActorKey; kind: EventKind; title: string; status?: string; history: boolean; text: string};
type TimelineCopy = {
  label: string;
  run: string;
  running: string;
  completed: string;
  actors: Record<ActorKey, string>;
  events: TimelineEvent[];
  summary: string;
  replay: string;
};
export type CrashTimelineVariant = keyof typeof timelines;

const STEP_MS = 1200;
const kindIcons = {flow: Flag, step: Play, wait: Hourglass, stop: CircleStop, start: CirclePlay, fire: Zap, done: FlagOff};
const workerOrder: ActorKey[] = ['first', 'second'];

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

/** Index of the last shown event; it advances once the figure scrolls into view, and only once. */
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

function workerState(actor: ActorKey, events: TimelineEvent[], revealed: number): 'idle' | 'on' | 'off' {
  const shown = events.slice(0, revealed).filter((event) => event.actor === actor);
  if (shown.some((event) => event.kind === 'stop')) return 'off';
  return shown.length > 0 ? 'on' : 'idle';
}

/**
 * A Flow run drawn the way Dex Web shows one: a run header with its status, then the numbered history,
 * with the Worker stopping and starting between events. It plays once when it scrolls into view.
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
  let historyNumber = 0;
  return (
    <figure ref={ref} className={styles.run} aria-label={copy.label}>
      <div className={styles.header}>
        <span className={styles.runId}>{copy.run}</span>
        <span className={`${styles.status}${isDone ? ` ${styles.statusDone}` : ''}`}>
          {isDone ? <CircleCheck className={styles.icon} aria-hidden="true" /> : <span className={styles.pulse} />}
          {isDone ? copy.completed : copy.running}
        </span>
      </div>
      <div className={styles.workers} aria-hidden="true">
        <span className={`${styles.chip} ${styles.chipServer}`}>
          <span className={styles.dot} />
          {copy.actors.server}
        </span>
        {workerOrder.map((actor) => (
          <span key={actor} className={`${styles.chip} ${styles[`chip_${workerState(actor, copy.events, revealed)}`]}`}>
            <span className={styles.dot} />
            {copy.actors[actor]}
          </span>
        ))}
      </div>
      <ol className={styles.events}>
        {copy.events.map((event, index) => {
          const Icon = kindIcons[event.kind];
          const isShown = index < revealed;
          if (event.history) historyNumber += 1;
          return (
            <li
              key={`${event.kind}-${event.title}`}
              title={event.text}
              className={`${styles.event} ${styles[`kind_${event.kind}`]} ${event.history ? styles.history : styles.between}${isShown ? ` ${styles.shown}` : ''}`}>
              <span className={styles.number}>{event.history ? `#${historyNumber}` : ''}</span>
              <Icon className={styles.icon} aria-hidden="true" />
              <span className={styles.title}>{event.title}</span>
              {event.status ? <span className={styles.badge}>{event.status}</span> : <span />}
              <span className={styles.actor}>{copy.actors[event.actor]}</span>
              <span className={styles.srOnly}>{event.text}</span>
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
