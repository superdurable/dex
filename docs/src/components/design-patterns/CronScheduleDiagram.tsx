/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

import {ChannelIcon, CompactCard, StepCard} from '../primitives/step/flowGraphShared';

type Copy = {
  label: string;
  client: string;
  clientDetail: string;
  publishes: string;
  trigger: string;
  skip: string;
  flow: string;
  start: string;
  startExecute: string;
  schedule: string;
  run: string;
  runExecute: string;
  goTo: string;
  goToMulti: string;
  timerOrTrigger: string;
  skipTransition: string;
};

const EN: Copy = {
  label: 'CronScheduleFlow with boundary-crossing Channel publishing and StepDecision movements',
  client: 'Client',
  clientDetail: 'publishes an occurrence decision',
  publishes: 'publish',
  trigger: 'Trigger',
  skip: 'Skip',
  flow: 'CronScheduleFlow',
  start: 'Start',
  startExecute: 'validate input',
  schedule: 'WaitForSchedule',
  run: 'Run',
  runExecute: 'record scheduled work',
  goTo: 'GoTo',
  goToMulti: 'GoToMulti',
  timerOrTrigger: 'Timer / Trigger',
  skipTransition: 'Skip',
};

const ZH: Copy = {
  label: 'CronScheduleFlow：跨越边界的 Channel 发布与 StepDecision 跳转',
  client: 'Client',
  clientDetail: '发布本次 occurrence 的决策',
  publishes: '发布',
  trigger: 'Trigger',
  skip: 'Skip',
  flow: 'CronScheduleFlow',
  start: 'Start',
  startExecute: '校验 input',
  schedule: 'WaitForSchedule',
  run: 'Run',
  runExecute: '记录 scheduled work',
  goTo: 'GoTo',
  goToMulti: 'GoToMulti',
  timerOrTrigger: 'Timer / Trigger',
  skipTransition: 'Skip',
};

const css = `
  .cron-schedule-graph {
    display: grid;
    width: 100%;
    grid-template-columns: minmax(8rem, 0.45fr) minmax(0, 1.55fr);
    align-items: center;
    gap: 1.1rem;
  }
  .cron-schedule-client {
    display: flex;
    min-width: 0;
    flex-direction: column;
    align-items: center;
    gap: 0.7rem;
  }
  .cron-schedule-client .flow-graph-card { width: min(100%, 10rem); }
  .cron-publish-arrows {
    width: 100%;
    min-width: 6rem;
    height: 7rem;
    overflow: visible;
  }
  .cron-publish-arrows path { fill: none; stroke: #3b876c; stroke-linecap: round; stroke-width: 2.5; }
  .cron-publish-arrows text { fill: #3b876c; font-size: 11px; font-weight: 700; text-anchor: middle; }
  .cron-flow-boundary {
    position: relative;
    min-width: 0;
    padding: 1rem 1rem 1.35rem 5.9rem;
    border: 2px solid #7ca79a;
    border-radius: 18px;
    background: rgba(244, 250, 247, 0.64);
  }
  .cron-flow-boundary-title {
    display: flex;
    margin-bottom: 1rem;
    flex-direction: column;
    gap: 0.15rem;
    color: #395e54;
  }
  .cron-flow-boundary-title span { font-size: 0.62rem; font-weight: 800; letter-spacing: 0.06em; }
  .cron-flow-boundary-title b { font-size: 1rem; }
  .cron-flow-channels {
    position: absolute;
    top: 4.6rem;
    left: 0;
    display: flex;
    width: 7.6rem;
    flex-direction: column;
    gap: 0.75rem;
    transform: translateX(-46%);
  }
  .cron-flow-channels .flow-graph-card {
    width: 100%;
    border-color: #3b876c;
    background: #edf8f2;
  }
  .cron-flow-channels .flow-graph-card b { display: flex; align-items: center; gap: 0.28rem; }
  .cron-step-stack {
    display: flex;
    min-width: 0;
    flex-direction: column;
    align-items: center;
  }
  .cron-step-stack > .flow-graph-card { width: min(100%, 22.5rem); }
  .cron-transition {
    display: flex;
    min-height: 2.8rem;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 0.12rem;
  }
  .cron-transition span {
    font-size: 0.62rem;
    font-weight: 800;
    line-height: 1.1;
    text-align: center;
  }
  .cron-transition svg { width: 18px; height: 28px; }
  .cron-transition path:first-child { fill: none; stroke: currentColor; stroke-linecap: round; stroke-width: 1.8; }
  .cron-transition path:last-child { fill: currentColor; stroke: none; }
  .cron-transition-default { color: #496d94; }
  .cron-transition-parallel { color: #6377be; }
  .cron-schedule-branching {
    display: grid;
    position: relative;
    width: 100%;
    max-width: 28rem;
    grid-template-columns: minmax(0, 1fr) minmax(0, 1fr);
    align-items: start;
    gap: 0.5rem;
  }
  .cron-schedule-branching::before {
    position: absolute;
    top: 0;
    left: 50%;
    width: 2px;
    height: 0.9rem;
    background: #6377be;
    content: '';
  }
  .cron-schedule-branching::after {
    position: absolute;
    top: 0.9rem;
    left: 16%;
    width: 68%;
    height: 2px;
    background: #6377be;
    content: '';
  }
  .cron-schedule-run-branch {
    display: flex;
    padding-top: 0.9rem;
    flex-direction: column;
    align-items: center;
  }
  .cron-schedule-run-branch .flow-graph-card { width: min(100%, 13rem); }
  .cron-self-loops {
    position: relative;
    min-height: 13.2rem;
    padding-top: 0.6rem;
  }
  .cron-self-loop {
    position: absolute;
    right: 0;
    left: 0;
    display: flex;
    align-items: center;
    gap: 0.38rem;
  }
  .cron-self-loop span {
    width: 4.6rem;
    font-size: 0.62rem;
    font-weight: 800;
    line-height: 1.12;
    text-align: right;
  }
  .cron-self-loop svg { width: 3.3rem; height: 5.4rem; overflow: visible; }
  .cron-self-loop path:first-child { fill: none; stroke: currentColor; stroke-linecap: round; stroke-linejoin: round; stroke-width: 2.1; }
  .cron-self-loop path:last-child { fill: currentColor; stroke: none; }
  .cron-self-loop-parallel { top: 0.35rem; color: #6377be; }
  .cron-self-loop-skip { top: 6.45rem; color: #b87836; }
  [data-theme='dark'] .cron-flow-boundary { border-color: #6d9789; background: rgba(25, 47, 39, 0.55); }
  [data-theme='dark'] .cron-flow-boundary-title { color: #b6d3c8; }
  [data-theme='dark'] .cron-flow-channels .flow-graph-card { border-color: #559b80; background: #173b2d; }
  [data-theme='dark'] .cron-publish-arrows path { stroke: #72bfa0; }
  [data-theme='dark'] .cron-publish-arrows text { fill: #72bfa0; }
  @media (max-width: 720px) {
    .cron-schedule-graph { grid-template-columns: 1fr; }
    .cron-schedule-client { flex-direction: row; justify-content: center; }
    .cron-schedule-client .flow-graph-card { width: min(10rem, 100%); }
    .cron-publish-arrows { width: 6.5rem; height: 4rem; transform: rotate(90deg); }
    .cron-flow-boundary { padding-left: 4.8rem; }
    .cron-flow-channels { width: 6.3rem; transform: translateX(-38%); }
  }
`;

function ChannelCard({name}: {name: string}): ReactNode {
  return (
    <div className="flow-graph-card flow-graph-card-compact">
      <span>CHANNEL</span>
      <b>
        <ChannelIcon /> {name}
      </b>
    </div>
  );
}

function Transition({label, tone}: {label: string; tone: 'default' | 'parallel'}): ReactNode {
  return (
    <div className={`cron-transition cron-transition-${tone}`}>
      <span>{label}</span>
      <svg viewBox="0 0 18 28" aria-hidden="true">
        <path d="M9 1v17" />
        <path d="M3.2 16.2 9 26.2 14.8 16.2z" />
      </svg>
    </div>
  );
}

function SelfLoop({label, tone}: {label: string; tone: 'parallel' | 'skip'}): ReactNode {
  return (
    <div className={`cron-self-loop cron-self-loop-${tone}`}>
      <span>{label}</span>
      <svg viewBox="0 0 58 92" aria-hidden="true">
        <path d="M2 70h22c13 0 22-9 22-22V25c0-11-8-19-19-19H2" />
        <path d="M11 1 2 6l9 5z" />
      </svg>
    </div>
  );
}

export default function CronScheduleDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <style>{css}</style>
      <div className="cron-schedule-graph">
        <div className="cron-schedule-client">
          <CompactCard kicker="CLIENT" title={copy.client} detail={copy.clientDetail} tone="source" />
          <svg className="cron-publish-arrows" viewBox="0 0 180 120" aria-hidden="true">
            <defs>
              <marker id="cron-channel-arrowhead" markerHeight="8" markerWidth="8" orient="auto" refX="7" refY="4">
                <path d="M1 1 7 4 1 7z" fill="#3b876c" />
              </marker>
            </defs>
            <path d="M10 60H58M58 60V25H168" markerEnd="url(#cron-channel-arrowhead)" />
            <path d="M58 60v35h110" markerEnd="url(#cron-channel-arrowhead)" />
            <text x="88" y="54">{copy.publishes}</text>
          </svg>
        </div>
        <section className="cron-flow-boundary">
          <div className="cron-flow-boundary-title">
            <span>FLOW</span>
            <b>{copy.flow}</b>
          </div>
          <div className="cron-flow-channels">
            <ChannelCard name={copy.trigger} />
            <ChannelCard name={copy.skip} />
          </div>
          <div className="cron-step-stack">
            <StepCard name={copy.start} executeOnly execute={copy.startExecute} />
            <Transition label={copy.goTo} tone="default" />
            <StepCard
              name={copy.schedule}
              waitFor={{timer: 'interval', channel: `${copy.trigger} + ${copy.skip}`}}
              tone="waiting"
            />
            <div className="cron-schedule-branching">
              <div className="cron-schedule-run-branch">
                <Transition label={`${copy.goToMulti} · ${copy.timerOrTrigger}`} tone="parallel" />
                <StepCard name={copy.run} executeOnly execute={copy.runExecute} />
              </div>
              <div className="cron-self-loops">
                <SelfLoop label={`${copy.goToMulti} · ${copy.timerOrTrigger}`} tone="parallel" />
                <SelfLoop label={`${copy.goTo} · ${copy.skipTransition}`} tone="skip" />
              </div>
            </div>
          </div>
        </section>
      </div>
    </div>
  );
}
