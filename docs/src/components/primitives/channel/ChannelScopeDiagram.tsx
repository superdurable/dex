// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

import {StepCard} from '../step/flowGraphShared';

type Copy = {
  label: string;
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
  label:
    'A Flow execution owns a requests_queue Channel. ExampleStepA and ExampleRpc publish messages to it. ExampleStepB waits for and consumes messages from it.',
  execution: 'Flow execution',
  publisherStep: 'ExampleStepA',
  publisherRPC: 'ExampleRpc',
  consumerStep: 'ExampleStepB',
  waitFor: 'requests_queue.for_one()',
  execute: 'process request',
  channel: 'CHANNEL',
  channelName: 'requests_queue',
  fifo: 'FIFO queue',
  consumeOnce: 'each message is consumed once',
  publish: 'publish messages',
  consume: 'consume',
};

const ZH: Copy = {
  label:
    '一个 Flow execution 拥有 requests_queue Channel。ExampleStepA 和 ExampleRpc 往它发布消息。ExampleStepB 等待并从中消费消息。',
  execution: 'Flow execution',
  publisherStep: 'ExampleStepA',
  publisherRPC: 'ExampleRpc',
  consumerStep: 'ExampleStepB',
  waitFor: 'requests_queue.for_one()',
  execute: '处理请求',
  channel: 'CHANNEL',
  channelName: 'requests_queue',
  fifo: 'FIFO 队列',
  consumeOnce: '每条消息只消费一次',
  publish: '发布消息',
  consume: '消费',
};

const css = String.raw`
  .channel-execution-frame {
    position: relative;
    z-index: 1;
    padding: 0.85rem;
    border: 1px solid var(--line-strong);
    border-radius: 14px;
    background: color-mix(in srgb, var(--surface-solid) 84%, transparent);
  }
  .channel-execution-heading {
    margin-bottom: 0.8rem;
    color: var(--text);
    font-size: 0.86rem;
  }
  .channel-execution-main {
    display: grid;
    grid-template-columns: minmax(8.5rem, 1fr) 4.5rem minmax(11rem, 1.15fr) 4.5rem minmax(10.5rem, 1fr);
    grid-template-rows: auto auto;
    align-items: center;
    row-gap: 0.8rem;
  }
  .channel-execution-publisher-step { grid-column: 1; grid-row: 1; }
  .channel-execution-publisher-rpc { grid-column: 1; grid-row: 2; }
  .channel-execution-step-arrow { grid-column: 2; grid-row: 1; }
  .channel-execution-rpc-arrow { grid-column: 2; grid-row: 2; }
  .channel-execution-queue { grid-column: 3; grid-row: 1 / span 2; }
  .channel-execution-consume-arrow { grid-column: 4; grid-row: 1 / span 2; }
  .channel-execution-consumer { grid-column: 5; grid-row: 1 / span 2; }
  .channel-execution-node {
    display: flex;
    min-height: 4.8rem;
    padding: 0.7rem;
    flex-direction: column;
    justify-content: center;
    gap: 0.28rem;
    border: 1px solid var(--line-strong);
    border-radius: 9px;
    background: var(--surface-solid);
  }
  .channel-execution-node span,
  .channel-execution-queue span {
    color: var(--muted);
    font-size: 0.62rem;
    font-weight: 800;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .channel-execution-node b,
  .channel-execution-queue strong {
    font-size: 0.8rem;
  }
  .channel-execution-queue-box {
    display: flex;
    min-height: 10.4rem;
    padding: 0.8rem;
    align-items: center;
    flex-direction: column;
    justify-content: center;
    gap: 0.45rem;
    border: 1px solid var(--line-strong);
    border-radius: 10px;
    background: color-mix(in srgb, var(--cyan) 13%, var(--surface-solid));
    text-align: center;
  }
  .channel-execution-queue-box > div {
    display: flex;
    min-width: 0;
    flex-direction: column;
    align-items: center;
    gap: 0.16rem;
  }
  .channel-execution-queue-box small {
    color: var(--muted);
    font-size: 0.68rem;
  }
  .channel-execution-database {
    width: min(100%, 7.5rem);
    height: auto;
    fill: var(--surface-solid);
    stroke: var(--forest);
    stroke-linecap: round;
    stroke-linejoin: round;
    stroke-width: 2;
  }
  .channel-execution-arrow {
    display: flex;
    min-width: 0;
    align-items: center;
    gap: 0.2rem;
    color: var(--muted);
    font-size: 0.62rem;
    font-weight: 700;
    line-height: 1.1;
    text-align: center;
  }
  .channel-execution-arrow i {
    position: relative;
    display: block;
    min-width: 1rem;
    height: 1px;
    flex: 1 1 auto;
    background: currentColor;
  }
  .channel-execution-arrow i::after {
    position: absolute;
    top: -3px;
    right: -1px;
    width: 0;
    height: 0;
    border-top: 3.5px solid transparent;
    border-bottom: 3.5px solid transparent;
    border-left: 5px solid currentColor;
    content: '';
  }
  .channel-execution-consumer .flow-graph-card {
    min-width: 0;
  }
  @media (max-width: 760px) {
    .channel-execution-main {
      grid-template-columns: minmax(8rem, 1fr) 3.8rem minmax(10rem, 1.1fr);
      grid-template-rows: auto auto auto;
    }
    .channel-execution-queue { grid-column: 3; grid-row: 1 / span 3; }
    .channel-execution-consume-arrow { grid-column: 2; grid-row: 3; }
    .channel-execution-consumer { grid-column: 1; grid-row: 3; }
    .channel-execution-consume-arrow .channel-execution-arrow {
      flex-direction: row-reverse;
    }
    .channel-execution-consume-arrow .channel-execution-arrow i::after {
      right: auto;
      left: -1px;
      border-right: 5px solid currentColor;
      border-left: 0;
    }
  }
`;

function Arrow({label}: {label: string}): ReactNode {
  return (
    <div className="channel-execution-arrow" aria-hidden="true">
      <span>{label}</span>
      <i />
    </div>
  );
}

function Publisher({kicker, name}: {kicker: string; name: string}): ReactNode {
  return (
    <div className="channel-execution-node">
      <span>{kicker}</span>
      <b>{name}</b>
    </div>
  );
}

function DatabaseIcon(): ReactNode {
  return (
    <svg className="channel-execution-database" viewBox="0 0 180 88" aria-hidden="true">
      <path d="M29 25c0-7 10-12 22-12h78c12 0 22 5 22 12v38c0 7-10 12-22 12H51c-12 0-22-5-22-12V25Z" />
      <ellipse cx="51" cy="44" rx="22" ry="31" />
      <path d="M29 57c0 7 10 12 22 12h78c12 0 22-5 22-12" fill="none" />
    </svg>
  );
}

function Queue({copy}: {copy: Copy}): ReactNode {
  return (
    <div className="channel-execution-queue-box">
      <DatabaseIcon />
      <div>
        <span>{copy.channel}</span>
        <strong>{copy.channelName}</strong>
        <small>{copy.fifo + ' · ' + copy.consumeOnce}</small>
      </div>
    </div>
  );
}

export default function ChannelScopeDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <style>{css}</style>
      <section className="channel-execution-frame">
        <b className="channel-execution-heading">{copy.execution}</b>
        <div className="channel-execution-main">
          <div className="channel-execution-publisher-step">
            <Publisher kicker="STEP" name={copy.publisherStep} />
          </div>
          <div className="channel-execution-publisher-rpc">
            <Publisher kicker="RPC" name={copy.publisherRPC} />
          </div>
          <div className="channel-execution-step-arrow">
            <Arrow label={copy.publish} />
          </div>
          <div className="channel-execution-rpc-arrow">
            <Arrow label={copy.publish} />
          </div>
          <div className="channel-execution-queue">
            <Queue copy={copy} />
          </div>
          <div className="channel-execution-consume-arrow">
            <Arrow label={copy.consume} />
          </div>
          <div className="channel-execution-consumer">
            <StepCard
              name={copy.consumerStep}
              execute={copy.execute}
              waitFor={{channel: copy.waitFor}}
              tone="waiting"
            />
          </div>
        </div>
      </section>
    </div>
  );
}
