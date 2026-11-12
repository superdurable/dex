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
import {AppWindow, Brain, Workflow, Wrench} from 'lucide-react';

import {
  Arrow,
  Chip,
  ConceptDiagramFrame,
  Detail,
  Eyebrow,
  IconBadge,
  Tile,
  TileTitle,
} from '../conceptDiagram/ConceptDiagram';
import styles from './SessionTurnDiagram.module.css';

const copy = {
  en: {
    app: 'YOUR APP',
    appTitle: 'Client',
    flow: 'SESSION FLOW · ONE PER SESSION',
    loop: "YOUR FRAMEWORK'S AGENT LOOP",
    model: 'Model call',
    tool: 'Tool call',
    approval: 'Approval',
    note: 'Each chip is a Step that Dex records.',
    message: 'message',
    calls: 'calls',
    models: 'Models',
    tools: 'Tools',
  },
  'zh-Hans': {
    app: '你的应用',
    appTitle: 'Client',
    flow: 'SESSION FLOW · 每个 Session 一个',
    loop: '你的框架的 Agent Loop',
    model: '模型调用',
    tool: 'Tool Call',
    approval: '审批',
    note: '每个标签都是 Dex 记录的一个 Step。',
    message: '消息',
    calls: '调用',
    models: '模型',
    tools: 'Tool',
  },
};

function StepChip({label, step}: {label: string; step: string}): ReactNode {
  return (
    <span className={styles.step}>
      <Chip tone="accent" isSolid>{label}</Chip>
      <code className={styles.stepName}>{step}</code>
    </span>
  );
}

/** Shows one agent turn inside a session Flow: each model call, tool call, and approval is a Step. */
export default function SessionTurnDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <div className={styles.layout}>
        <div className={styles.app}>
          <Eyebrow icon={AppWindow}>{text.app}</Eyebrow>
          <Tile>
            <TileTitle>{text.appTitle}</TileTitle>
          </Tile>
        </div>
        <div className={styles.in}>
          <Arrow direction="right" tone="accent" isFlowing label={text.message} className={styles.wide} />
          <Arrow direction="down" tone="accent" isFlowing label={text.message} className={styles.narrow} />
        </div>
        <Tile tone="accent" className={styles.flow}>
          <Eyebrow icon={Workflow}>{text.flow}</Eyebrow>
          <div className={styles.loop}>
            <Eyebrow>{text.loop}</Eyebrow>
            <div className={styles.steps}>
              <StepChip label={text.model} step="RunModel" />
              <span className={styles.next} aria-hidden="true">→</span>
              <StepChip label={text.tool} step="CallTool" />
              <span className={styles.next} aria-hidden="true">→</span>
              <StepChip label={text.approval} step="AwaitToolApproval" />
              <span className={styles.next} aria-hidden="true">→</span>
              <StepChip label={text.model} step="RunModel" />
            </div>
          </div>
          <Detail>{text.note}</Detail>
        </Tile>
        <div className={styles.out}>
          <Arrow direction="right" tone="accent" isFlowing label={text.calls} className={styles.wide} />
          <Arrow direction="down" tone="accent" isFlowing label={text.calls} className={styles.narrow} />
        </div>
        <div className={styles.external}>
          <Tile>
            <span className={styles.heading}>
              <IconBadge icon={Brain} isSmall />
              <TileTitle>{text.models}</TileTitle>
            </span>
          </Tile>
          <Tile>
            <span className={styles.heading}>
              <IconBadge icon={Wrench} isSmall />
              <TileTitle>{text.tools}</TileTitle>
            </span>
          </Tile>
        </div>
      </div>
    </ConceptDiagramFrame>
  );
}
