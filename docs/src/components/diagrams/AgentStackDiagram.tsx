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
import {AppWindow, Bot, Cpu, Laptop, Server} from 'lucide-react';

import {agentFrameworks} from '../agentFrameworks';
import {frameworkLogoMarkup} from '../frameworkLogos';
import {
  Arrow,
  ConceptDiagramFrame,
  DiagramIcon,
  Eyebrow,
  IconBadge,
  Tile,
  TileTitle,
} from '../conceptDiagram/ConceptDiagram';
import styles from './AgentStackDiagram.module.css';

const copy = {
  en: {
    app: 'YOUR APP',
    web: 'Web app or API',
    hosting: 'Dex Cloud / self-hosted',
    worker: 'YOUR AGENT WORKER',
    builtIn: 'Built-in loop',
    adapter: 'Dex Agents adapter',
    calls: 'sessions',
    work: 'pulls Steps',
  },
  'zh-Hans': {
    app: '你的应用',
    web: 'Web 应用或 API',
    hosting: 'Dex Cloud / 自托管',
    worker: '你的 Agent Worker',
    builtIn: '内置 Agent Loop',
    adapter: 'Dex Agents 适配器',
    calls: 'Session',
    work: '拉取 Step',
  },
};

/** Shows the three parts of a durable agent: your app, Dex Server, and your agent Worker. */
export default function AgentStackDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <div className={styles.layout}>
        <div className={styles.app}>
          <Eyebrow icon={AppWindow}>{text.app}</Eyebrow>
          <Tile>
            <TileTitle>{text.web}</TileTitle>
          </Tile>
          <Tile>
            <span className={styles.heading}>
              <IconBadge icon={Laptop} isSmall />
              <TileTitle>Client</TileTitle>
            </span>
          </Tile>
        </div>
        <div className={styles.toServer}>
          <Arrow direction="right" tone="accent" isFlowing label={text.calls} className={styles.wide} />
          <Arrow direction="down" tone="accent" isFlowing label={text.calls} className={styles.narrow} />
        </div>
        <Tile tone="accent" className={styles.server}>
          <IconBadge icon={Server} tone="accent" isSolid />
          <TileTitle>Dex Server</TileTitle>
          <span className={styles.hosting}>{text.hosting}</span>
        </Tile>
        <div className={styles.toWorker}>
          <Arrow direction="left" tone="accent" isFlowing label={text.work} className={styles.wide} />
          <Arrow direction="up" tone="accent" isFlowing label={text.work} className={styles.narrow} />
        </div>
        <div className={styles.worker}>
          <Eyebrow icon={Cpu}>{text.worker}</Eyebrow>
          <div className={styles.frameworks}>
            {agentFrameworks.filter(({id}) => id in frameworkLogoMarkup).map(({id, label}) => (
              <span key={id} className={styles.framework}>
                <svg viewBox="0 0 24 24" width={15} height={15} fill="currentColor" fillRule="evenodd" aria-hidden="true"
                  dangerouslySetInnerHTML={{__html: frameworkLogoMarkup[id as keyof typeof frameworkLogoMarkup]}} />
                {label}
              </span>
            ))}
            <span className={`${styles.framework} ${styles.dex}`}>
              <DiagramIcon icon={Bot} size={15} />
              {text.builtIn}
            </span>
          </div>
          <span className={styles.adapter}>{text.adapter}</span>
        </div>
      </div>
    </ConceptDiagramFrame>
  );
}
