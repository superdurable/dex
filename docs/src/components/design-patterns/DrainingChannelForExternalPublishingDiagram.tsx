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
  flow: string;
  flowName: string;
  channel: string;
  queue: string;
  rpc: string;
  exampleRPC: string;
  rpcDetail: string;
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
  label: 'Draining external Channel publishing: a Flow, a one-way Channel, a two-way RPC, the ProcessMessage Step, and conditional completion',
  flow: 'FLOW',
  flowName: 'DrainingChannelFlow',
  channel: 'CHANNEL',
  queue: 'queueChannel',
  rpc: 'RPC',
  exampleRPC: 'exampleRPC',
  rpcDetail: 'String ⇄ String',
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
  label: '排空外部 Channel 发布：Flow、单向 Channel、双向 RPC、ProcessMessage Step 和条件完成',
  flow: 'FLOW',
  flowName: 'DrainingChannelFlow',
  channel: 'CHANNEL',
  queue: 'queueChannel',
  rpc: 'RPC',
  exampleRPC: 'exampleRPC',
  rpcDetail: 'String ⇄ String',
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
        <div className="draining-channel-flow">
          <div className="draining-channel-flow-title">
            <span>{copy.flow}</span>
            <b>{copy.flowName}</b>
          </div>
          <div className="draining-channel-connector draining-channel-connector-channel">
            <span>{copy.channel}</span>
            <b>{copy.queue}</b>
          </div>
          <div className="draining-channel-connector draining-channel-connector-rpc">
            <span>{copy.rpc}</span>
            <b>{copy.exampleRPC}</b>
            <small>{copy.rpcDetail}</small>
          </div>
          <div className="flow-graph draining-channel-flow-content">
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
    </div>
  );
}
