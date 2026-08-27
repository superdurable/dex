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

import {ChannelIcon, CompactCard, GraphArrow, GraphLoopBack, StepCard} from '../primitives/step/flowGraphShared';

type Copy = {
  label: string;
  controls: string;
  client: string;
  clientDetail: string;
  trigger: string;
  skip: string;
  definition: string;
  flow: string;
  start: string;
  startExecute: string;
  schedule: string;
  scheduleExecute: string;
  run: string;
  runExecute: string;
  complete: string;
  completeDetail: string;
};

const EN: Copy = {
  label: 'Cron schedule Flow definitions and control Channels',
  controls: 'External control',
  client: 'Client',
  clientDetail: 'publishes an occurrence decision',
  trigger: 'Trigger',
  skip: 'Skip',
  definition: 'Flow definition',
  flow: 'CronScheduleFlow',
  start: 'Start',
  startExecute: 'validate input → WaitForSchedule',
  schedule: 'WaitForSchedule',
  scheduleExecute: 'Skip: next interval; timer / Trigger: Run + next interval',
  run: 'Run',
  runExecute: 'record scheduled work',
  complete: 'complete',
  completeDetail: 'after the final occurrence',
};

const ZH: Copy = {
  label: 'Cron schedule Flow definition 与控制 Channel',
  controls: '外部控制',
  client: 'Client',
  clientDetail: '发布本次 occurrence 的决策',
  trigger: 'Trigger',
  skip: 'Skip',
  definition: 'Flow definition',
  flow: 'CronScheduleFlow',
  start: 'Start',
  startExecute: '校验 input → WaitForSchedule',
  schedule: 'WaitForSchedule',
  scheduleExecute: 'Skip：下一间隔；timer / Trigger：Run + 下一间隔',
  run: 'Run',
  runExecute: '记录 scheduled work',
  complete: 'complete',
  completeDetail: '最后一次 occurrence 后',
};

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

export default function CronScheduleDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <div className="flow-graph">
        <div className="flow-graph-panel">
          <div className="flow-graph-panel-title">{copy.controls}</div>
          <div className="flow-graph flow-graph-panel-body">
            <CompactCard kicker="CLIENT" title={copy.client} detail={copy.clientDetail} tone="source" />
            <GraphArrow />
            <div className="flow-graph-split flow-graph-split-compact">
              <div className="flow-graph-split-stem" aria-hidden="true" />
              <div className="flow-graph-split-bar" aria-hidden="true" />
              <div className="flow-graph-split-cards">
                <div className="flow-graph-split-branch">
                  <GraphArrow />
                  <ChannelCard name={copy.trigger} />
                </div>
                <div className="flow-graph-split-branch">
                  <GraphArrow />
                  <ChannelCard name={copy.skip} />
                </div>
              </div>
            </div>
          </div>
        </div>
        <div className="flow-graph-panel">
          <div className="flow-graph-panel-title">{copy.definition}</div>
          <div className="flow-graph flow-graph-panel-body">
            <CompactCard kicker="FLOW" title={copy.flow} tone="source" />
            <GraphArrow />
            <div className="flow-graph-loop-def">
              <div className="flow-graph-loop-def-steps">
                <StepCard name={copy.start} executeOnly execute={copy.startExecute} />
                <GraphArrow />
                <StepCard
                  name={copy.schedule}
                  execute={copy.scheduleExecute}
                  waitFor={{timer: 'interval', channel: `${copy.trigger} + ${copy.skip}`}}
                  tone="waiting"
                />
                <GraphArrow />
                <StepCard name={copy.run} executeOnly execute={copy.runExecute} />
              </div>
              <GraphLoopBack />
            </div>
            <GraphArrow />
            <CompactCard kicker="DONE" title={copy.complete} detail={copy.completeDetail} tone="done" />
          </div>
        </div>
      </div>
    </div>
  );
}
