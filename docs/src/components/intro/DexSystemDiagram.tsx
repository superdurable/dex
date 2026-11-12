// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import clsx from 'clsx';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import type {LucideIcon} from 'lucide-react';
import {AppWindow, Cpu, Database, Laptop, Monitor, Package, Server, SquareTerminal, Wrench} from 'lucide-react';

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
import styles from './DexSystemDiagram.module.css';

type Copy = {
  app: string;
  client: string;
  worker: string;
  toServer: string;
  fromServer: string;
  persist: string;
  operate: string;
  cli: string;
  web: string;
};

const EN: Copy = {
  app: 'APPLICATION',
  client: 'Start and interact with Flow instances.',
  worker: 'Hosts the Flow. Runs Step and RPC implementations.',
  toServer: 'startFlow / invokeRPC',
  fromServer: 'invoke Step / RPC',
  persist: 'Persists Flow instances and execution.',
  operate: 'OPERATE',
  cli: 'Run Dex locally; manage Flows.',
  web: 'Inspect and act on Flows.',
};

const ZH: Copy = {
  app: '应用',
  client: '启动 Flow 实例，并与之交互。',
  worker: '托管 Flow，运行 Step 和 RPC 的实现。',
  toServer: 'startFlow / invokeRPC',
  fromServer: 'invoke Step / RPC',
  persist: '持久化 Flow 实例及其执行。',
  operate: '运维',
  cli: '本机运行 Dex，管理 Flow。',
  web: '查看和操作 Flow。',
};

function RoleNode({
  icon,
  title,
  detail,
  className,
}: {
  icon: LucideIcon;
  title: string;
  detail: string;
  className: string;
}): ReactNode {
  return (
    <Tile className={clsx(styles.role, className)}>
      <span className={styles.roleHeading}>
        <IconBadge icon={icon} isSmall />
        <TileTitle>{title}</TileTitle>
      </span>
      <Detail>{detail}</Detail>
    </Tile>
  );
}

/** Renders a vertical arrow for the stacked layout and a horizontal one for the wide layout. */
function SystemArrow({
  label,
  isToServer,
  className,
}: {
  label: string;
  isToServer: boolean;
  className: string;
}): ReactNode {
  return (
    <div className={clsx(styles.call, className)}>
      <Arrow direction={isToServer ? 'down' : 'up'} tone="accent" isFlowing label={label} className={styles.callNarrow} />
      <Arrow direction={isToServer ? 'right' : 'left'} tone="accent" isFlowing label={label} className={styles.callWide} />
    </div>
  );
}

/** Shows the application and Dex Server; withOperatorTools adds dexcli and Dex Web. */
export default function DexSystemDiagram({
  markdownDescription,
  withOperatorTools = false,
}: {
  markdownDescription: string;
  withOperatorTools?: boolean;
}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const copy = i18n.currentLocale === 'zh-Hans' ? ZH : EN;
  return (
    <ConceptDiagramFrame label={markdownDescription}>
      <div className={clsx(styles.layout, withOperatorTools && styles.withTools)}>
        <span className={styles.appBackground} />
        <span className={styles.appLabel}>
          <Eyebrow icon={AppWindow}>{copy.app}</Eyebrow>
        </span>
        <RoleNode icon={Laptop} title="Client" detail={copy.client} className={styles.client} />
        <RoleNode icon={Cpu} title="Worker" detail={copy.worker} className={styles.worker} />
        <Tile tone="accent" className={styles.sdk}>
          <IconBadge icon={Package} tone="accent" isSmall />
          <span className={styles.sdkText}>
            <Eyebrow>SDK</Eyebrow>
            <TileTitle tone="accent">Dex SDK</TileTitle>
          </span>
        </Tile>
        <SystemArrow label={copy.toServer} isToServer className={styles.toServer} />
        <SystemArrow label={copy.fromServer} isToServer={false} className={styles.fromServer} />
        <Tile tone="accent" className={styles.server}>
          <IconBadge icon={Server} tone="accent" isSolid />
          <TileTitle>Dex Server</TileTitle>
          <span className={styles.serverStore}>
            <DiagramIcon icon={Database} size={18} />
            <Detail>{copy.persist}</Detail>
          </span>
        </Tile>
        {withOperatorTools ? (
          <div className={styles.tools}>
            <span className={styles.toolsLabel}>
              <Eyebrow icon={Wrench}>{copy.operate}</Eyebrow>
            </span>
            <RoleNode icon={SquareTerminal} title="dexcli" detail={copy.cli} className={styles.tool} />
            <RoleNode icon={Monitor} title="Dex Web" detail={copy.web} className={styles.tool} />
          </div>
        ) : null}
      </div>
    </ConceptDiagramFrame>
  );
}
