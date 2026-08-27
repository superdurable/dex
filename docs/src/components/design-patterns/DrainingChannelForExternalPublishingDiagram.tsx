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

import {CompactCard, GraphArrow, StepCard} from '../primitives/step/flowGraphShared';

type Copy = {
  label: string;
  client: string;
  publish: string;
  alternativePublish: string;
  channel: string;
  queue: string;
  start: string;
  process: string;
  processMessage: string;
  conditionalComplete: string;
  empty: string;
  complete: string;
  notEmpty: string;
  fallback: string;
};

const EN: Copy = {
  label: 'Draining channel for external publishing: external publisher, Channel, ProcessMessage Step, and conditional completion',
  client: 'EXTERNAL PUBLISHER',
  publish: 'Client PublishToChannel',
  alternativePublish: 'or Flow RPC publish',
  channel: 'CHANNEL',
  queue: 'queueChannel',
  start: 'Start',
  process: 'ProcessMessage',
  processMessage: 'process one message',
  conditionalComplete: 'ForceCompleteIfChannelsEmpty',
  empty: 'empty',
  complete: 'complete',
  notEmpty: 'message queued',
  fallback: 'ProcessMessage',
};

const ZH: Copy = {
  label: '为外部发布排空 Channel：外部发布者、Channel、ProcessMessage Step 和条件完成',
  client: '外部发布者',
  publish: 'Client PublishToChannel',
  alternativePublish: '或 Flow RPC publish',
  channel: 'CHANNEL',
  queue: 'queueChannel',
  start: '开始',
  process: 'ProcessMessage',
  processMessage: '处理一条消息',
  conditionalComplete: 'ForceCompleteIfChannelsEmpty',
  empty: '为空',
  complete: '完成',
  notEmpty: '仍有消息',
  fallback: 'ProcessMessage',
};

export default function DrainingChannelForExternalPublishingDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <div className="draining-channel-graph">
        <div className="draining-channel-publisher">
          <CompactCard
            kicker={copy.client}
            title={copy.publish}
            detail={copy.alternativePublish}
            tone="source"
          />
          <GraphArrow label="append" />
          <CompactCard kicker={copy.channel} title={copy.queue} />
        </div>
        <div className="flow-graph draining-channel-flow">
          <CompactCard kicker="FLOW" title={copy.start} tone="source" />
          <GraphArrow />
          <StepCard
            name={copy.process}
            execute={copy.processMessage}
            waitFor={{channel: copy.queue}}
            tone="waiting"
          />
          <GraphArrow label={copy.conditionalComplete} />
          <div className="flow-graph-split">
            <div className="flow-graph-split-stem" aria-hidden="true" />
            <div className="flow-graph-split-bar" aria-hidden="true" />
            <div className="flow-graph-split-cards">
              <div className="flow-graph-split-branch">
                <GraphArrow label={copy.empty} />
                <CompactCard kicker="DONE" title={copy.complete} tone="done" />
              </div>
              <div className="flow-graph-split-branch">
                <GraphArrow label={copy.notEmpty} />
                <CompactCard kicker="FALLBACK" title={copy.fallback} tone="decision" />
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}
