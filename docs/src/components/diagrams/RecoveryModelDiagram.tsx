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
import {History, Play, RotateCcw, ScrollText, ShieldCheck, Zap} from 'lucide-react';

import {Arrow, ConceptDiagramFrame, DiagramIcon, Eyebrow} from '../conceptDiagram/ConceptDiagram';
import styles from './RecoveryModelDiagram.module.css';

type Lane = {
  name: string;
  record: string;
  recordDetail: string;
  resume: string;
  resumeDetail: string;
  rule: string;
};

const copy: Record<'en' | 'zh-Hans', {crash: string; temporal: Lane; dex: Lane}> = {
  en: {
    crash: 'A Worker stops mid-run',
    temporal: {
      name: 'TEMPORAL',
      record: 'Event History',
      recordDetail: 'every command and result',
      resume: 'Replay the function',
      resumeDetail: 'from the top, to the last await',
      rule: 'Workflow code is deterministic',
    },
    dex: {
      name: 'DEX',
      record: 'Step results + Attributes',
      recordDetail: 'saved with each decision',
      resume: 'Run the next Step',
      resumeDetail: 'finished Steps never run again',
      rule: 'Each effect is idempotent',
    },
  },
  'zh-Hans': {
    crash: 'Worker 运行中途停止',
    temporal: {
      name: 'TEMPORAL',
      record: 'Event History',
      recordDetail: '每条命令和结果',
      resume: '重放函数',
      resumeDetail: '从头执行到最后一个 await',
      rule: 'Workflow 代码必须确定',
    },
    dex: {
      name: 'DEX',
      record: 'Step 结果 + Attribute',
      recordDetail: '和每个决定一起保存',
      resume: '运行下一个 Step',
      resumeDetail: '完成的 Step 不再运行',
      rule: '每个外部效果必须幂等',
    },
  },
};

function LaneView({lane, tone}: {lane: Lane; tone: 'external' | 'accent'}): ReactNode {
  return (
    <div className={`${styles.lane} ${tone === 'external' ? styles.external : styles.dex}`}>
      <Eyebrow icon={tone === 'external' ? History : Play}>{lane.name}</Eyebrow>
      <div className={styles.box}>
        <DiagramIcon icon={ScrollText} size={15} />
        <span>
          <b>{lane.record}</b>
          <small>{lane.recordDetail}</small>
        </span>
      </div>
      <Arrow direction="down" tone={tone} isFlowing />
      <div className={styles.box}>
        <DiagramIcon icon={tone === 'external' ? RotateCcw : Play} size={15} />
        <span>
          <b>{lane.resume}</b>
          <small>{lane.resumeDetail}</small>
        </span>
      </div>
      <span className={styles.rule}>
        <DiagramIcon icon={ShieldCheck} size={13} />
        {lane.rule}
      </span>
    </div>
  );
}

/** How each engine continues a run after a Worker stops: replay the function, or run the next Step. */
export default function RecoveryModelDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <div className={styles.crash}>
        <DiagramIcon icon={Zap} size={14} />
        {text.crash}
      </div>
      <div className={styles.layout}>
        <LaneView lane={text.temporal} tone="external" />
        <LaneView lane={text.dex} tone="accent" />
      </div>
    </ConceptDiagramFrame>
  );
}
