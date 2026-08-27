/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     https://superdurable.io/licenses/sdsl-1.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

import {CompactCard, GraphArrow, StepCard} from '../primitives/step/flowGraphShared';

type Copy = {
  label: string;
  producer: string;
  consumer: string;
  start: string;
  initialize: string;
  startBranches: string;
  process: string;
  publishDocument: string;
  afterLastDocument: string;
  finalize: string;
  publishFinal: string;
  channel: string;
  documentCommands: string;
  upsert: string;
  waitForChannel: string;
  upsertDocument: string;
  finalCommand: string;
  continueDraining: string;
  complete: string;
};

const EN: Copy = {
  label: 'Draining Channels for Internal Publishing Flow',
  producer: 'Producer branch',
  consumer: 'Consumer branch',
  start: 'Start',
  initialize: 'Init',
  startBranches: 'Start producer and consumer',
  process: 'ProcessData',
  publishDocument: 'Publish document; process; loop',
  afterLastDocument: 'after the last document',
  finalize: 'Finalize',
  publishFinal: 'Publish final command; complete',
  channel: 'upsertMongoData',
  documentCommands: 'document commands',
  upsert: 'UpsertMongoRecord',
  waitForChannel: 'upsertMongoData',
  upsertDocument: 'Upsert one document',
  finalCommand: 'Final command?',
  continueDraining: 'no: wait for the next document',
  complete: 'yes: complete',
};

const ZH: Copy = {
  label: 'Flow：用内部发布排空 Channel',
  producer: '生产者分支',
  consumer: '消费者分支',
  start: '开始',
  initialize: 'Init',
  startBranches: '启动生产者和消费者',
  process: 'ProcessData',
  publishDocument: '发布文档、处理、循环',
  afterLastDocument: '最后一条文档之后',
  finalize: 'Finalize',
  publishFinal: '发布终止命令并完成',
  channel: 'upsertMongoData',
  documentCommands: '文档命令',
  upsert: 'UpsertMongoRecord',
  waitForChannel: 'upsertMongoData',
  upsertDocument: '写入一个文档',
  finalCommand: '终止命令？',
  continueDraining: '否：等待下一条文档',
  complete: '是：完成',
};

function Panel({title, children}: {title: string; children: ReactNode}): ReactNode {
  return (
    <div className="flow-graph-panel">
      <div className="flow-graph-panel-title">{title}</div>
      <div className="flow-graph flow-graph-panel-body">{children}</div>
    </div>
  );
}

export default function DrainingChannelsForInternalPublishingDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;

  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <div className="flow-graph">
        <CompactCard kicker="FLOW" title={copy.start} tone="source" />
        <GraphArrow />
        <StepCard name={copy.initialize} executeOnly execute={copy.startBranches} />
      </div>
      <div className="flow-graph-grid">
        <Panel title={copy.producer}>
          <StepCard name={copy.process} executeOnly execute={copy.publishDocument} />
          <GraphArrow label={copy.afterLastDocument} />
          <StepCard name={copy.finalize} executeOnly execute={copy.publishFinal} />
          <GraphArrow />
          <CompactCard kicker="CHANNEL" title={copy.channel} detail={copy.documentCommands} tone="source" />
        </Panel>
        <Panel title={copy.consumer}>
          <StepCard
            name={copy.upsert}
            execute={copy.upsertDocument}
            waitFor={{channel: copy.waitForChannel}}
            tone="waiting"
          />
          <GraphArrow />
          <CompactCard kicker="DECISION" title={copy.finalCommand} tone="decision" />
          <div className="flow-graph-split flow-graph-split-compact">
            <div className="flow-graph-split-stem" aria-hidden="true" />
            <div className="flow-graph-split-bar" aria-hidden="true" />
            <div className="flow-graph-split-cards">
              <div className="flow-graph-split-branch">
                <GraphArrow />
                <CompactCard kicker="LOOP" title={copy.continueDraining} detail={copy.upsert} />
              </div>
              <div className="flow-graph-split-branch">
                <GraphArrow />
                <CompactCard kicker="DONE" title={copy.complete} tone="done" />
              </div>
            </div>
          </div>
        </Panel>
      </div>
    </div>
  );
}
