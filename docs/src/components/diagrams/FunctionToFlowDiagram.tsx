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
import {Clock, FileCode2, Workflow, Zap} from 'lucide-react';

import {Arrow, ConceptDiagramFrame, DiagramIcon, Eyebrow} from '../conceptDiagram/ConceptDiagram';
import styles from './FunctionToFlowDiagram.module.css';

const copy = {
  en: {
    before: 'TODAY · ONE FUNCTION',
    lines: ['charge()', 'wait for approval', 'ship()', 'refund()'],
    crash: 'crash: half done',
    after: 'AS A FLOW',
    split: 'split into Steps',
    wait: 'approval or 24 h',
    done: 'Done',
    fails: 'fails',
  },
  'zh-Hans': {
    before: '现在 · 一个函数',
    lines: ['charge()', '等待审批', 'ship()', 'refund()'],
    crash: '崩溃：停在半路',
    after: '改成 Flow',
    split: '拆成 Step',
    wait: '审批或 24 小时',
    done: '完成',
    fails: '失败时',
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
                {index === 0 ? (
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
        <div className={styles.after}>
          <Eyebrow icon={Workflow}>{text.after}</Eyebrow>
          <div className={styles.graph}>
            <span className={`${styles.step} ${styles.charge}`}>Charge</span>
            <span className={`${styles.edge} ${styles.e1}`} aria-hidden="true">→</span>
            <span className={`${styles.step} ${styles.ship}`}>
              Ship
              <small className={styles.wait}>
                <DiagramIcon icon={Clock} size={12} />
                {text.wait}
              </small>
            </span>
            <span className={`${styles.edge} ${styles.e2}`} aria-hidden="true">→</span>
            <span className={`${styles.end} ${styles.done}`}>{text.done}</span>
            <span className={styles.down} aria-hidden="true">↓ {text.fails}</span>
            <span className={`${styles.step} ${styles.recovery}`}>Refund</span>
          </div>
        </div>
      </div>
    </ConceptDiagramFrame>
  );
}
