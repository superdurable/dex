// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import clsx from 'clsx';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import type {LucideIcon} from 'lucide-react';
import {ArrowRight, Footprints, Mail, MessagesSquare, Plug, Workflow} from 'lucide-react';

import {
  Arrow,
  Code,
  ConceptDiagramFrame,
  DiagramIcon,
  Eyebrow,
  IconBadge,
  Tile,
  TileTitle,
} from '../../conceptDiagram/ConceptDiagram';
import styles from './ChannelScopeDiagram.module.css';

type Copy = {
  execution: string;
  publisherStep: string;
  publisherRPC: string;
  consumerStep: string;
  waitFor: string;
  execute: string;
  channel: string;
  channelName: string;
  fifo: string;
  consumeOnce: string;
  publish: string;
  consume: string;
};

const EN: Copy = {
  execution: 'Flow execution',
  publisherStep: 'ExampleStepA',
  publisherRPC: 'ExampleRpc',
  consumerStep: 'ExampleStepB',
  waitFor: 'requests_queue.for_one()',
  execute: 'process request',
  channel: 'CHANNEL',
  channelName: 'requests_queue',
  fifo: 'FIFO queue',
  consumeOnce: 'one wait consumes each message',
  publish: 'publish messages',
  consume: 'consume',
};

const ZH: Copy = {
  execution: 'Flow execution',
  publisherStep: 'ExampleStepA',
  publisherRPC: 'ExampleRpc',
  consumerStep: 'ExampleStepB',
  waitFor: 'requests_queue.for_one()',
  execute: '处理请求',
  channel: 'CHANNEL',
  channelName: 'requests_queue',
  fifo: 'FIFO 队列',
  consumeOnce: '每条消息由一个 wait 消费',
  publish: '发布消息',
  consume: '消费',
};

function Publisher({icon, kicker, name}: {icon: LucideIcon; kicker: string; name: string}): ReactNode {
  return (
    <Tile className={styles.publisher}>
      <IconBadge icon={icon} isSmall />
      <span className={styles.publisherText}>
        <Eyebrow>{kicker}</Eyebrow>
        <TileTitle>{name}</TileTitle>
      </span>
    </Tile>
  );
}

/** Four queued messages; the oldest one, at the head, leaves first. */
function QueueStrip(): ReactNode {
  return (
    <span className={styles.queue} aria-hidden="true">
      <span className={styles.queueDirection}>
        <DiagramIcon icon={ArrowRight} size={16} />
      </span>
      {[0, 1, 2, 3].map((position) => (
        <span key={position} className={clsx(styles.queueMessage, position === 3 && styles.queueHead)}>
          <DiagramIcon icon={Mail} size={14} />
        </span>
      ))}
    </span>
  );
}

export default function ChannelScopeDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <section className={styles.scope}>
        <span className={styles.scopeLabel}>
          <DiagramIcon icon={Workflow} size={15} />
          {copy.execution}
        </span>
        <div className={styles.publishers}>
          <Publisher icon={Footprints} kicker="STEP" name={copy.publisherStep} />
          <Publisher icon={Plug} kicker="RPC" name={copy.publisherRPC} />
        </div>
        <div className={styles.publishArrows}>
          <Arrow direction="down" />
          <span className={styles.publishLabel}>{copy.publish}</span>
          <Arrow direction="down" />
        </div>
        <Tile tone="accent" className={styles.channel}>
          <span className={styles.channelHeading}>
            <IconBadge icon={MessagesSquare} tone="accent" isSolid />
            <span className={styles.channelName}>
              <Eyebrow>{copy.channel}</Eyebrow>
              <TileTitle tone="accent">{copy.channelName}</TileTitle>
            </span>
          </span>
          <QueueStrip />
          <span className={styles.channelFacts}>
            <b>{copy.fifo}</b>
            <span aria-hidden="true">·</span>
            <span>{copy.consumeOnce}</span>
          </span>
        </Tile>
        <Arrow direction="down" tone="accent" isFlowing label={copy.consume} className={styles.consumeArrow} />
        <Tile className={styles.consumer}>
          <Eyebrow name={copy.consumerStep}>STEP</Eyebrow>
          <div className={styles.methods}>
            <div className={styles.method}>
              <span className={styles.methodNameWait}>WaitFor</span>
              <Code tone="wait" icon={MessagesSquare}>
                {copy.waitFor}
              </Code>
            </div>
            <div className={styles.method}>
              <span className={styles.methodName}>Execute</span>
              <span className={styles.methodBody}>{copy.execute}</span>
            </div>
          </div>
        </Tile>
      </section>
    </ConceptDiagramFrame>
  );
}
