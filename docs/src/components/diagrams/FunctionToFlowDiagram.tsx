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
import {Clock, FileCode2, MessageSquare, RotateCcw, Tag, Workflow, Zap} from 'lucide-react';

import {
  Arrow,
  Chip,
  ConceptDiagramFrame,
  DiagramIcon,
  Detail,
  Eyebrow,
  Tile,
  TileTitle,
} from '../conceptDiagram/ConceptDiagram';
import styles from './FunctionToFlowDiagram.module.css';

const copy = {
  en: {
    before: 'TODAY · ONE FUNCTION',
    lines: [
      'charge the buyer',
      'save status "charged"',
      'poll for approval, remind after 24 h',
      'ship the item',
      'save status "shipped"',
      'refund if shipping keeps failing',
    ],
    crash: 'a crash here leaves the order half done',
    after: 'AS A FLOW · ONE PER ORDER',
    split: 'split into Steps',
    charge: 'Calls the payment API once.',
    ship: 'Waits for approval or a 24-hour Timer, then ships.',
    refund: 'Runs when Ship keeps failing.',
    wait: 'approval message',
    timer: '24 h Timer',
    status: 'order-status Attribute',
    approve: 'approve RPC',
    note: 'Dex saves each Step, so a crash repeats at most the Step that was running.',
  },
  'zh-Hans': {
    before: '现在 · 一个函数',
    lines: [
      '向买家扣款',
      '保存状态 “charged”',
      '轮询审批，24 小时后提醒',
      '发货',
      '保存状态 “shipped”',
      '发货一直失败就退款',
    ],
    crash: '在这里崩溃，订单就停在半路',
    after: '改成 Flow · 每个订单一个',
    split: '拆成 Step',
    charge: '只调用一次支付 API。',
    ship: '等待审批或 24 小时 Timer，然后发货。',
    refund: 'Ship 一直失败时运行。',
    wait: '审批消息',
    timer: '24 小时 Timer',
    status: 'order-status Attribute',
    approve: '审批 RPC',
    note: 'Dex 保存每个 Step，所以崩溃最多重复正在运行的那个 Step。',
  },
};

/** Shows one order function before and after it becomes a Flow of Steps. */
export default function FunctionToFlowDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <div className={styles.layout}>
        <div className={styles.before}>
          <Eyebrow icon={FileCode2}>{text.before}</Eyebrow>
          <ol className={styles.lines}>
            {text.lines.map((line, index) => (
              <React.Fragment key={line}>
                <li className={styles.line}>{line}</li>
                {index === 1 ? (
                  <li className={styles.crash} aria-hidden="true">
                    <DiagramIcon icon={Zap} size={13} />
                    {text.crash}
                  </li>
                ) : null}
              </React.Fragment>
            ))}
          </ol>
        </div>
        <div className={styles.split}>
          <Arrow direction="right" tone="accent" isFlowing label={text.split} className={styles.wide} />
          <Arrow direction="down" tone="accent" isFlowing label={text.split} className={styles.narrow} />
        </div>
        <Tile tone="accent" className={styles.after}>
          <Eyebrow icon={Workflow}>{text.after}</Eyebrow>
          <div className={styles.steps}>
            <Tile className={styles.step}>
              <TileTitle>Charge</TileTitle>
              <Detail>{text.charge}</Detail>
            </Tile>
            <span className={styles.next} aria-hidden="true">→</span>
            <Tile className={styles.step}>
              <TileTitle>Ship</TileTitle>
              <Detail>{text.ship}</Detail>
              <span className={styles.waits}>
                <Chip icon={MessageSquare}>{text.wait}</Chip>
                <Chip icon={Clock}>{text.timer}</Chip>
              </span>
            </Tile>
          </div>
          <div className={styles.failure}>
            <span className={styles.route}>
              <DiagramIcon icon={RotateCcw} size={13} />
            </span>
            <Tile className={styles.step}>
              <TileTitle>Refund</TileTitle>
              <Detail>{text.refund}</Detail>
            </Tile>
          </div>
          <span className={styles.state}>
            <Chip icon={Tag}>{text.status}</Chip>
            <Chip icon={MessageSquare}>{text.approve}</Chip>
          </span>
          <Detail>{text.note}</Detail>
        </Tile>
      </div>
    </ConceptDiagramFrame>
  );
}
