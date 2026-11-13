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
import turnStyles from './SessionTurnDiagram.module.css';
import styles from './AgentTermMapDiagram.module.css';

const copy = {
  en: {
    app: 'YOUR APP',
    run: 'Run the agent',
    flow: 'THREAD → SESSION FLOW',
    loop: 'AGENT LOOP → STEPS',
    model: 'LLM call',
    tool: 'Tool call',
    approval: 'Human in the loop',
    results: 'Tool results',
    history: 'Chat history',
    stream: 'Streaming',
    note: 'Chip: your framework. Below: Dex.',
    message: 'message',
    calls: 'calls',
    models: 'Models',
    tools: 'Tools',
  },
  'zh-Hans': {
    app: '你的应用',
    run: '运行 Agent',
    flow: 'Thread → Session Flow',
    loop: 'Agent Loop → Step',
    model: 'LLM 调用',
    tool: 'Tool Call',
    approval: '人工介入',
    results: 'Tool 结果',
    history: '聊天记录',
    stream: '流式输出',
    note: '标签：你的框架。下方：Dex。',
    message: '消息',
    calls: '调用',
    models: '模型',
    tools: 'Tool',
  },
};

function TermPair({term, dex, isSolid = true}: {term: string; dex: string; isSolid?: boolean}): ReactNode {
  return (
    <span className={styles.pair}>
      <Chip tone="accent" isSolid={isSolid}>{term}</Chip>
      <code className={styles.dexName}>{dex}</code>
    </span>
  );
}

/** Maps the agent-framework terms a builder knows onto the Dex session Flow, its Steps, and its state. */
export default function AgentTermMapDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <div className={turnStyles.layout}>
        <div className={turnStyles.app}>
          <Eyebrow icon={AppWindow}>{text.app}</Eyebrow>
          <Tile>
            <TileTitle>Client</TileTitle>
            <span className={styles.appPair}>
              <Chip>{text.run}</Chip>
              <code className={styles.dexName}>send_user_message</code>
            </span>
          </Tile>
        </div>
        <div className={turnStyles.in}>
          <Arrow direction="right" tone="accent" isFlowing label={text.message} className={turnStyles.wide} />
          <Arrow direction="down" tone="accent" isFlowing label={text.message} className={turnStyles.narrow} />
        </div>
        <Tile tone="accent" className={turnStyles.flow}>
          <Eyebrow icon={Workflow}>{text.flow}</Eyebrow>
          <div className={turnStyles.loop}>
            <Eyebrow>{text.loop}</Eyebrow>
            <div className={turnStyles.steps}>
              <TermPair term={text.model} dex="RunModel" />
              <span className={turnStyles.next} aria-hidden="true">→</span>
              <TermPair term={text.tool} dex="CallTool" />
              <span className={turnStyles.next} aria-hidden="true">→</span>
              <TermPair term={text.approval} dex="AwaitToolApproval" />
              <span className={turnStyles.next} aria-hidden="true">→</span>
              <TermPair term={text.results} dex="ResumeTurn" />
            </div>
          </div>
          <div className={styles.state}>
            <TermPair term={text.history} dex="Attributes" isSolid={false} />
            <TermPair term={text.stream} dex="AgentEvents Stream" isSolid={false} />
          </div>
          <Detail>{text.note}</Detail>
        </Tile>
        <div className={turnStyles.out}>
          <Arrow direction="right" tone="external" isFlowing label={text.calls} className={turnStyles.wide} />
          <Arrow direction="down" tone="external" isFlowing label={text.calls} className={turnStyles.narrow} />
        </div>
        <div className={turnStyles.external}>
          <Tile tone="external">
            <span className={turnStyles.heading}>
              <IconBadge icon={Brain} tone="external" isSmall />
              <TileTitle>{text.models}</TileTitle>
            </span>
          </Tile>
          <Tile tone="external">
            <span className={turnStyles.heading}>
              <IconBadge icon={Wrench} tone="external" isSmall />
              <TileTitle>{text.tools}</TileTitle>
            </span>
          </Tile>
        </div>
      </div>
    </ConceptDiagramFrame>
  );
}
