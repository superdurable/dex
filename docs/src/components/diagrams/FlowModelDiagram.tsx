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
import {AppWindow, Laptop, Workflow} from 'lucide-react';

import {
  Arrow,
  Chip,
  ChipRow,
  ConceptDiagramFrame,
  Detail,
  Eyebrow,
  IconBadge,
  Tile,
  TileTitle,
} from '../conceptDiagram/ConceptDiagram';
import styles from './FlowModelDiagram.module.css';

const copy = {
  en: {
    app: 'YOUR APPLICATION',
    flow: 'ONE FLOW · ONE FLOW ID',
    waits: 'Waits on Channels, Timers, SubFlows.',
    start: 'StartFlow / RPC',
    read: 'GetAttributes / wait',
    stream: 'Stream (live)',
  },
  'zh-Hans': {
    app: '你的应用',
    flow: '一个 Flow · 一个 Flow ID',
    waits: '等待 Channel、Timer、SubFlow。',
    start: 'StartFlow / RPC',
    read: 'GetAttributes / 等待',
    stream: 'Stream（实时）',
  },
};

/** Shows a Client driving one Flow: calls in, state and live updates out. */
export default function FlowModelDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <div className={styles.layout}>
        <div className={styles.app}>
          <Eyebrow icon={AppWindow}>{text.app}</Eyebrow>
          <Tile className={styles.client}>
            <span className={styles.heading}>
              <IconBadge icon={Laptop} isSmall />
              <TileTitle>Client</TileTitle>
            </span>
          </Tile>
        </div>
        <div className={styles.calls}>
          <Arrow direction="right" tone="accent" isFlowing label={text.start} className={styles.wide} />
          <Arrow direction="left" tone="accent" isFlowing label={text.read} className={styles.wide} />
          <Arrow direction="left" tone="accent" isFlowing label={text.stream} className={styles.wide} />
          <Arrow direction="down" tone="accent" isFlowing label={text.start} className={styles.narrow} />
          <Arrow direction="up" tone="accent" isFlowing label={`${text.read} · ${text.stream}`} className={styles.narrow} />
        </div>
        <Tile tone="accent" className={styles.flow}>
          <Eyebrow icon={Workflow}>{text.flow}</Eyebrow>
          <ChipRow>
            <Chip tone="accent" isSolid>Step</Chip>
            <span className={styles.next} aria-hidden="true">→</span>
            <Chip tone="accent" isSolid>Step</Chip>
            <span className={styles.next} aria-hidden="true">→</span>
            <Chip tone="accent" isSolid>Step</Chip>
          </ChipRow>
          <Detail>{text.waits}</Detail>
          <ChipRow>
            <Chip>Attributes</Chip>
            <Chip>Channels</Chip>
            <Chip>Streams</Chip>
          </ChipRow>
        </Tile>
      </div>
    </ConceptDiagramFrame>
  );
}
