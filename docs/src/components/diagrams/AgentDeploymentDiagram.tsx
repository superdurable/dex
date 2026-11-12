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
import {Cpu, Database, Globe, Server, Users} from 'lucide-react';

import {
  Arrow,
  ConceptDiagramFrame,
  DiagramIcon,
  Detail,
  Eyebrow,
  IconBadge,
  Tile,
  TileTitle,
} from '../conceptDiagram/ConceptDiagram';
import styles from './AgentDeploymentDiagram.module.css';

const copy = {
  en: {
    users: 'USERS',
    web: 'Web app replicas',
    webDetail: 'Hold a Client.',
    worker: 'Worker processes',
    workerDetail: 'Run the session Flow.',
    server: 'Session state in object storage.',
    rpc: 'RPCs, reads',
    pull: 'pull tasks (outbound only)',
  },
  'zh-Hans': {
    users: '用户',
    web: 'Web 应用副本',
    webDetail: '持有 Client。',
    worker: 'Worker 进程',
    workerDetail: '运行 Session Flow。',
    server: 'Session 状态保存在对象存储中。',
    rpc: 'RPC、读取',
    pull: '拉取任务（只出站）',
  },
};

/** Shows the three kinds of process behind durable agent sessions. */
export default function AgentDeploymentDiagram({markdownDescription}: {markdownDescription: string}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <div className={styles.layout}>
        <Tile className={styles.web}>
          <Eyebrow icon={Users}>{text.users}</Eyebrow>
          <span className={styles.heading}>
            <IconBadge icon={Globe} isSmall />
            <TileTitle>{text.web}</TileTitle>
          </span>
          <Detail>{text.webDetail}</Detail>
        </Tile>
        <div className={styles.toServer}>
          <Arrow direction="right" tone="accent" isFlowing label={text.rpc} className={styles.wide} />
          <Arrow direction="down" tone="accent" isFlowing label={text.rpc} className={styles.narrow} />
        </div>
        <Tile tone="accent" className={styles.server}>
          <IconBadge icon={Server} tone="accent" isSolid />
          <TileTitle>Dex Server</TileTitle>
          <span className={styles.store}>
            <DiagramIcon icon={Database} size={18} />
            <Detail>{text.server}</Detail>
          </span>
        </Tile>
        <div className={styles.fromWorker}>
          <Arrow direction="left" tone="accent" isFlowing label={text.pull} className={styles.wide} />
          <Arrow direction="up" tone="accent" isFlowing label={text.pull} className={styles.narrow} />
        </div>
        <Tile className={styles.worker}>
          <span className={styles.heading}>
            <IconBadge icon={Cpu} isSmall />
            <TileTitle>{text.worker}</TileTitle>
          </span>
          <Detail>{text.workerDetail}</Detail>
        </Tile>
      </div>
    </ConceptDiagramFrame>
  );
}
