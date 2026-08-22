// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';

type Copy = {
  label: string;
  run: string;
  otherRun: string;
  runID: string;
  otherRunID: string;
  stepExecution: string;
  waitFor: string;
  execute: string;
  waitDetail: string;
  executeDetail: string;
  publisherStep: string;
  publisherStepDetail: string;
  rpc: string;
  rpcDetail: string;
  channel: string;
  channelName: string;
  fifo: string;
  consumeOnce: string;
  waitsFor: string;
  publish: string;
  independent: string;
};

const EN: Copy = {
  label:
    'One Flow run owns a messages Channel. A Step waits on it, another Step and an RPC publish to it, and another Flow run has an independent FIFO Channel.',
  run: 'Flow run',
  otherRun: 'Another Flow run',
  runID: 'Run ID: run-a',
  otherRunID: 'Run ID: run-b',
  stepExecution: 'Step execution',
  waitFor: 'WaitFor',
  execute: 'Execute',
  waitDetail: 'messages.forOne()',
  executeDetail: 'first message is consumed once',
  publisherStep: 'Step execution',
  publisherStepDetail: 'publish messages',
  rpc: 'RPC',
  rpcDetail: 'publish messages',
  channel: 'CHANNEL',
  channelName: 'messages',
  fifo: 'FIFO queue',
  consumeOnce: 'each message is consumed once',
  waitsFor: 'waits for',
  publish: 'publish',
  independent: 'independent FIFO Channel',
};

const ZH: Copy = {
  label:
    '一个 Flow run 拥有 messages Channel。一个 Step 等待它，另一个 Step 和一个 RPC 往它发布消息；另一个 Flow run 有独立的 FIFO Channel。',
  run: 'Flow run',
  otherRun: '另一个 Flow run',
  runID: 'Run ID: run-a',
  otherRunID: 'Run ID: run-b',
  stepExecution: 'Step execution',
  waitFor: 'WaitFor',
  execute: 'Execute',
  waitDetail: 'messages.forOne()',
  executeDetail: '第一条消息只消费一次',
  publisherStep: 'Step execution',
  publisherStepDetail: '发布消息',
  rpc: 'RPC',
  rpcDetail: '发布消息',
  channel: 'CHANNEL',
  channelName: 'messages',
  fifo: 'FIFO 队列',
  consumeOnce: '每条消息只消费一次',
  waitsFor: '等待',
  publish: '发布',
  independent: '独立的 FIFO Channel',
};

const css = String.raw`
  .channel-run-diagram {
    position: relative;
    z-index: 1;
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    justify-items: stretch;
    gap: 1rem;
  }
  .channel-run-frame {
    padding: 0.85rem;
    border: 1px solid var(--line-strong);
    border-radius: 14px;
    background: color-mix(in srgb, var(--surface-solid) 84%, transparent);
  }
  .channel-run-frame-small {
    padding: 0.75rem;
  }
  .channel-run-heading {
    display: flex;
    margin-bottom: 0.8rem;
    align-items: baseline;
    justify-content: space-between;
    gap: 0.5rem;
  }
  .channel-run-heading b {
    font-size: 0.86rem;
  }
  .channel-run-heading span,
  .channel-run-node > span,
  .channel-run-queue span {
    color: var(--muted);
    font-size: 0.62rem;
    font-weight: 800;
    letter-spacing: 0.04em;
    text-transform: uppercase;
  }
  .channel-run-main {
    display: grid;
    grid-template-columns: minmax(7rem, 1fr) 3.75rem minmax(10rem, 1.2fr) 3.75rem minmax(7rem, 1fr);
    grid-template-rows: auto auto;
    align-items: center;
    row-gap: 0.8rem;
  }
  .channel-run-wait { grid-column: 1; grid-row: 1; }
  .channel-run-wait-arrow { grid-column: 2; grid-row: 1; }
  .channel-run-queue-main { grid-column: 3; grid-row: 1 / span 2; }
  .channel-run-rpc-arrow { grid-column: 4; grid-row: 1; }
  .channel-run-rpc { grid-column: 5; grid-row: 1; }
  .channel-run-step-publish { grid-column: 1; grid-row: 2; }
  .channel-run-step-arrow { grid-column: 2; grid-row: 2; }
  .channel-run-node {
    display: flex;
    min-height: 5rem;
    padding: 0.65rem;
    flex-direction: column;
    justify-content: center;
    gap: 0.25rem;
    border: 1px solid var(--line-strong);
    border-radius: 9px;
    background: var(--surface-solid);
  }
  .channel-run-node b,
  .channel-run-queue strong {
    font-size: 0.78rem;
  }
  .channel-run-node small,
  .channel-run-queue small {
    color: var(--muted);
    font-size: 0.68rem;
  }
  .channel-run-step {
    padding: 0;
    overflow: hidden;
  }
  .channel-run-step > span {
    padding: 0.65rem 0.65rem 0.25rem;
  }
  .channel-run-step > div {
    display: flex;
    padding: 0.5rem 0.65rem;
    flex-direction: column;
    gap: 0.15rem;
  }
  .channel-run-step > div + div {
    border-top: 1px solid var(--line);
    background: color-mix(in srgb, var(--cyan) 10%, var(--surface-solid));
  }
  .channel-run-queue {
    display: flex;
    min-height: 8.8rem;
    padding: 0.8rem;
    align-items: center;
    flex-direction: column;
    justify-content: center;
    gap: 0.35rem;
    border: 1px solid var(--line-strong);
    border-radius: 10px;
    background: color-mix(in srgb, var(--cyan) 13%, var(--surface-solid));
    text-align: center;
  }
  .channel-run-queue > div {
    display: flex;
    min-width: 0;
    flex-direction: column;
    gap: 0.16rem;
    align-items: center;
  }
  .channel-run-queue-icon {
    width: min(100%, 7.5rem);
    height: auto;
    flex: 0 0 auto;
    fill: none;
    stroke: var(--forest);
    stroke-linecap: round;
    stroke-linejoin: round;
    stroke-width: 2.5;
  }
  .channel-run-arrow {
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
  .channel-run-arrow i {
    position: relative;
    display: block;
    min-width: 1rem;
    height: 1px;
    flex: 1 1 auto;
    background: currentColor;
  }
  .channel-run-arrow-right i::after,
  .channel-run-arrow-left i::after {
    position: absolute;
    top: -3px;
    width: 0;
    height: 0;
    border-top: 3.5px solid transparent;
    border-bottom: 3.5px solid transparent;
    content: '';
  }
  .channel-run-arrow-right i::after {
    right: -1px;
    border-left: 5px solid currentColor;
  }
  .channel-run-arrow-left {
    flex-direction: row-reverse;
  }
  .channel-run-arrow-left i::after {
    left: -1px;
    border-right: 5px solid currentColor;
  }
  .channel-run-secondary {
    display: flex;
    width: min(100%, 17rem);
    min-height: 9rem;
    flex-direction: column;
    justify-content: center;
    justify-self: end;
  }
  .channel-run-queue-small {
    min-height: 6rem;
    padding: 0.65rem;
  }
  .channel-run-queue-small .channel-run-queue-icon {
    width: min(100%, 5rem);
  }
  @media (max-width: 760px) {
    .channel-run-main {
      grid-template-columns: minmax(7rem, 1fr) 3.25rem minmax(9rem, 1.1fr);
    }
    .channel-run-rpc-arrow { grid-column: 2; grid-row: 3; }
    .channel-run-rpc { grid-column: 1; grid-row: 3; }
    .channel-run-queue-main { grid-row: 1 / span 3; }
    .channel-run-arrow-left {
      flex-direction: row;
    }
    .channel-run-arrow-left i::after {
      right: -1px;
      left: auto;
      border-right: 0;
      border-left: 5px solid currentColor;
    }
  }
`;

function Arrow({direction, label}: {direction: 'left' | 'right'; label: string}): ReactNode {
  return (
    <div className={'channel-run-arrow channel-run-arrow-' + direction} aria-hidden="true">
      <span>{label}</span>
      <i />
    </div>
  );
}

function QueueIcon(): ReactNode {
  return (
    <svg className="channel-run-queue-icon" viewBox="0 0 180 88" aria-hidden="true">
      <ellipse cx="20" cy="44" rx="14" ry="31" />
      <path d="M20 13h124c16 0 29 14 29 31s-13 31-29 31H20" />
      <ellipse cx="144" cy="44" rx="14" ry="31" />
      <path d="M29 26h113M29 44h113M29 62h113" />
    </svg>
  );
}

function Queue({copy, small = false}: {copy: Copy; small?: boolean}): ReactNode {
  return (
    <div className={small ? 'channel-run-queue channel-run-queue-small' : 'channel-run-queue'}>
      <QueueIcon />
      <div>
        <span>{copy.channel}</span>
        <strong>{copy.channelName}</strong>
        <small>{small ? copy.independent : copy.fifo + ' · ' + copy.consumeOnce}</small>
      </div>
    </div>
  );
}

function StepWait({copy}: {copy: Copy}): ReactNode {
  return (
    <div className="channel-run-node channel-run-step">
      <span>{copy.stepExecution}</span>
      <div>
        <b>{copy.waitFor}</b>
        <small>{copy.waitDetail}</small>
      </div>
      <div>
        <b>{copy.execute}</b>
        <small>{copy.executeDetail}</small>
      </div>
    </div>
  );
}

function Publisher({title, detail}: {title: string; detail: string}): ReactNode {
  return (
    <div className="channel-run-node">
      <span>{title}</span>
      <b>{detail}</b>
      <small>messages</small>
    </div>
  );
}

export default function ChannelScopeDiagram(): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <div className="exec-diagram" role="img" aria-label={copy.label}>
      <style>{css}</style>
      <div className="channel-run-diagram">
        <section className="channel-run-frame">
          <div className="channel-run-heading">
            <b>{copy.run}</b>
            <span>{copy.runID}</span>
          </div>
          <div className="channel-run-main">
            <div className="channel-run-wait">
              <StepWait copy={copy} />
            </div>
            <div className="channel-run-wait-arrow">
              <Arrow direction="right" label={copy.waitsFor} />
            </div>
            <div className="channel-run-queue-main">
              <Queue copy={copy} />
            </div>
            <div className="channel-run-rpc-arrow">
              <Arrow direction="left" label={copy.publish} />
            </div>
            <div className="channel-run-rpc">
              <Publisher title={copy.rpc} detail={copy.rpcDetail} />
            </div>
            <div className="channel-run-step-publish">
              <Publisher title={copy.publisherStep} detail={copy.publisherStepDetail} />
            </div>
            <div className="channel-run-step-arrow">
              <Arrow direction="right" label={copy.publish} />
            </div>
          </div>
        </section>
        <section className="channel-run-frame channel-run-frame-small channel-run-secondary">
          <div className="channel-run-heading">
            <b>{copy.otherRun}</b>
            <span>{copy.otherRunID}</span>
          </div>
          <Queue copy={copy} small />
        </section>
      </div>
    </div>
  );
}
