// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import React, {type ReactNode} from 'react';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import CodeBlock from '@theme/CodeBlock';
import TabItem from '@theme/TabItem';
import Tabs from '@theme/Tabs';
import hostData from '../data/dex-skill-hosts.json';
import styles from './DexSkillHostTabs.module.css';

export type DexSkillContext = keyof typeof hostData.invocations;

type HostContent = string | HostContent[] | {
  type: string;
  children?: HostContent[];
  style?: string;
  language?: string;
  start?: number;
};

function renderHostContent(content: HostContent, skill: DexSkillContext): ReactNode {
  if (typeof content === 'string') return content;
  if (Array.isArray(content)) {
    return content.map((child, index) => <React.Fragment key={index}>{renderHostContent(child, skill)}</React.Fragment>);
  }
  if (content.type === 'invocations') {
    const invocations = hostData.invocations[skill];
    return <CodeBlock language="text">{invocations[content.style as keyof typeof invocations]}</CodeBlock>;
  }
  const children = renderHostContent(content.children ?? [], skill);
  if (content.type === 'code') {
    return <CodeBlock language={content.language}>{children}</CodeBlock>;
  }
  return React.createElement(content.type, content.start ? {start: content.start} : {}, children);
}

export default function DexSkillHostTabs({skill}: {skill: DexSkillContext}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const locale = i18n.currentLocale === 'zh-Hans' ? 'zh-Hans' : 'en';
  const {introduction, hosts} = hostData.locales[locale];
  return (
    <>
      <p>{introduction}</p>
      <Tabs className={styles.tabs} defaultValue="codex-desktop" groupId="dex-skill-host" queryString="host">
        <TabItem label={hosts[0].label} value="codex-desktop">{renderHostContent(hosts[0].children, skill)}</TabItem>
        <TabItem label={hosts[1].label} value="codex-cli">{renderHostContent(hosts[1].children, skill)}</TabItem>
        <TabItem label={hosts[2].label} value="claude-desktop">{renderHostContent(hosts[2].children, skill)}</TabItem>
        <TabItem label={hosts[3].label} value="claude-cli">{renderHostContent(hosts[3].children, skill)}</TabItem>
        <TabItem label={hosts[4].label} value="cursor">{renderHostContent(hosts[4].children, skill)}</TabItem>
        <TabItem label={hosts[5].label} value="others">{renderHostContent(hosts[5].children, skill)}</TabItem>
      </Tabs>
    </>
  );
}
