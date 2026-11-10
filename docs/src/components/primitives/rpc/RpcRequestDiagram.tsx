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
import clsx from 'clsx';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import type {LucideIcon} from 'lucide-react';
import {AppWindow, Cpu, Database, Laptop, Server} from 'lucide-react';

import {
  Arrow,
  ConceptDiagramFrame,
  DiagramIcon,
  Eyebrow,
  IconBadge,
  Tile,
  TileTitle,
  type ArrowDirection,
} from '../../conceptDiagram/ConceptDiagram';
import styles from './RpcRequestDiagram.module.css';

type Copy = {
  application: string;
  request: string;
  dispatch: string;
  result: string;
  response: string;
  client: string;
  server: string;
  worker: string;
};

const EN: Copy = {
  application: 'Application',
  request: 'Invoke RPC request',
  dispatch: 'Route request to Worker',
  result: 'Return result to Dex Server',
  response: 'Return output to Client',
  client: 'Client',
  server: 'Dex Server',
  worker: 'Worker',
};

const ZH: Copy = {
  application: 'Application',
  request: '发起 RPC 请求',
  dispatch: '将请求发送给 Worker',
  result: '将结果返回给 Dex Server',
  response: '将输出返回给 Client',
  client: 'Client',
  server: 'Dex Server',
  worker: 'Worker',
};

type Hop = {number: number; label: string; from: string; to: string; direction: ArrowDirection; isRequest: boolean};

function hops(copy: Copy): {clientRow: Hop[]; workerRow: Hop[]; ordered: Hop[]} {
  const request: Hop = {number: 1, label: copy.request, from: copy.client, to: copy.server, direction: 'right', isRequest: true};
  const dispatch: Hop = {number: 2, label: copy.dispatch, from: copy.server, to: copy.worker, direction: 'left', isRequest: true};
  const result: Hop = {number: 3, label: copy.result, from: copy.worker, to: copy.server, direction: 'right', isRequest: false};
  const response: Hop = {number: 4, label: copy.response, from: copy.server, to: copy.client, direction: 'left', isRequest: false};
  return {clientRow: [request, response], workerRow: [dispatch, result], ordered: [request, dispatch, result, response]};
}

function HopNumber({number}: {number: number}): ReactNode {
  return <span className={styles.hopNumber}>{number}</span>;
}

function HopArrow({hop}: {hop: Hop}): ReactNode {
  return (
    <div className={clsx(styles.hop, hop.direction === 'left' && styles.hopLeft)}>
      <span className={styles.hopLabel}>
        <HopNumber number={hop.number} />
        <span className={styles.hopText}>{hop.label}</span>
      </span>
      <Arrow direction={hop.direction} tone="accent" isFlowing={hop.isRequest} />
    </div>
  );
}

function RoleNode({icon, title, className}: {icon: LucideIcon; title: string; className: string}): ReactNode {
  return (
    <Tile className={clsx(styles.role, className)}>
      <IconBadge icon={icon} isSmall />
      <TileTitle>{title}</TileTitle>
    </Tile>
  );
}

export default function RpcRequestDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  const {clientRow, workerRow, ordered} = hops(copy);
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <div className={styles.layout}>
        <span className={styles.appBackground} />
        <span className={styles.appLabel}>
          <Eyebrow icon={AppWindow}>{copy.application}</Eyebrow>
        </span>
        <RoleNode icon={Laptop} title={copy.client} className={styles.client} />
        <RoleNode icon={Cpu} title={copy.worker} className={styles.worker} />
        <div className={clsx(styles.hops, styles.clientHops)}>
          {clientRow.map((hop) => (
            <HopArrow key={hop.number} hop={hop} />
          ))}
        </div>
        <div className={clsx(styles.hops, styles.workerHops)}>
          {workerRow.map((hop) => (
            <HopArrow key={hop.number} hop={hop} />
          ))}
        </div>
        <Tile tone="accent" className={styles.server}>
          <IconBadge icon={Server} tone="accent" isSolid />
          <TileTitle>{copy.server}</TileTitle>
          <span className={styles.serverStore}>
            <DiagramIcon icon={Database} size={22} />
          </span>
        </Tile>
        <ol className={styles.sequence}>
          {ordered.map((hop) => (
            <li key={hop.number}>
              <HopNumber number={hop.number} />
              <span className={styles.sequenceText}>
                <span className={styles.sequenceRoute}>
                  {hop.from} → {hop.to}
                </span>
                <span>{hop.label}</span>
              </span>
            </li>
          ))}
        </ol>
      </div>
    </ConceptDiagramFrame>
  );
}
