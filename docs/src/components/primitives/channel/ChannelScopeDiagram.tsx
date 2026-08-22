// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

import {CompactCard, GraphArrow} from '../step/flowGraphShared';

type Copy = {
  label: string;
  executionA: string;
  executionB: string;
  publish: string;
  publishers: string;
  publisherDetail: string;
  queued: string;
  fifo: string;
  firstMessage: string;
  secondMessage: string;
  consumed: string;
  consumedDetail: string;
  append: string;
  consume: string;
  noCross: string;
  separateQueue: string;
};

const EN: Copy = {
  label:
    'A Channel is a FIFO queue scoped to one Flow execution. The first queued message is consumed once by a waiting Step and never crosses to another Flow execution.',
  executionA: 'Flow execution A',
  executionB: 'Flow execution B',
  publish: 'PUBLISH MESSAGES',
  publishers: 'RPC · Step · Client',
  publisherDetail: 'each appends to this queue',
  queued: 'Approval Channel',
  fifo: 'FIRST-IN, FIRST-OUT',
  firstMessage: '1. approved',
  secondMessage: '2. approved',
  consumed: 'Waiting Step',
  consumedDetail: 'message 1 consumed and removed',
  append: 'append in arrival order',
  consume: 'consume the first message once',
  noCross: 'no messages cross execution boundaries',
  separateQueue: 'separate Approval Channel queue',
};

const ZH: Copy = {
  label:
    'Channel 是只属于一个 Flow execution 的 FIFO 队列。第一个排队消息由 waiting Step 消费一次，不会跨到另一个 Flow execution。',
  executionA: 'Flow execution A',
  executionB: 'Flow execution B',
  publish: '发布消息',
  publishers: 'RPC · Step · Client',
  publisherDetail: '都会追加到这条队列',
  queued: 'Approval Channel',
  fifo: 'FIRST-IN, FIRST-OUT',
  firstMessage: '1. approved',
  secondMessage: '2. approved',
  consumed: 'Waiting Step',
  consumedDetail: '消息 1 被消费并移除',
  append: '按到达顺序追加',
  consume: '消费第一条消息一次',
  noCross: '消息不会跨 execution 边界',
  separateQueue: '独立的 Approval Channel 队列',
};

function Queue({copy}: {copy: Copy}): ReactNode {
  return (
    <div className="flow-graph-card flow-graph-card-waiting">
      <div className="flow-graph-heading">
        <span>{copy.fifo}</span>
        <b>{copy.queued}</b>
      </div>
      <div className="flow-graph-methods flow-graph-methods-execute-only">
        <div className="flow-graph-method">
          <strong>{copy.firstMessage}</strong>
          <strong>{copy.secondMessage}</strong>
        </div>
      </div>
    </div>
  );
}

export default function ChannelScopeDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <div className="flow-graph">
        <CompactCard kicker="FLOW" title={copy.executionA} tone="source" />
        <GraphArrow label={copy.append} />
        <CompactCard
          kicker={copy.publish}
          title={copy.publishers}
          detail={copy.publisherDetail}
          tone="source"
        />
        <GraphArrow />
        <Queue copy={copy} />
        <GraphArrow label={copy.consume} />
        <CompactCard
          kicker="WAITFOR"
          title={copy.consumed}
          detail={copy.consumedDetail}
          tone="done"
        />
        <GraphArrow label={copy.noCross} />
        <CompactCard kicker="FLOW" title={copy.executionB} detail={copy.separateQueue} tone="source" />
      </div>
    </div>
  );
}
