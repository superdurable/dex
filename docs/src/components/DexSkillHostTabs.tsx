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

// Must match the content types expanded by plugins/markdown-export/export.mjs.
const HOST_CONTAINER_TYPES = new Set(['p', 'ol', 'ul', 'li', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6']);

function renderHostContent(content: HostContent, skill: DexSkillContext): ReactNode {
  if (typeof content === 'string') return content;
  if (Array.isArray(content)) {
    return content.map((child, index) => <React.Fragment key={index}>{renderHostContent(child, skill)}</React.Fragment>);
  }
  if (content.type === 'invocations') {
    const invocations = hostData.invocations[skill];
    return <CodeBlock language="text">{invocations[content.style as keyof typeof invocations]}</CodeBlock>;
  }
  if (content.type === 'code') {
    // CodeBlock highlights and adds the copy button only for string children.
    return <CodeBlock language={content.language}>{hostCodeText(content.children ?? [])}</CodeBlock>;
  }
  if (!HOST_CONTAINER_TYPES.has(content.type)) {
    throw new Error(`Unsupported skill host content type: ${content.type}`);
  }
  const children = renderHostContent(content.children ?? [], skill);
  return React.createElement(content.type, content.start ? {start: content.start} : {}, children);
}

function hostCodeText(children: HostContent[]): string {
  return children.map((child) => {
    if (typeof child !== 'string') throw new Error('Skill host code must contain only text');
    return child;
  }).join('');
}

export default function DexSkillHostTabs({skill}: {skill: DexSkillContext}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const locale = i18n.currentLocale === 'zh-Hans' ? 'zh-Hans' : 'en';
  const {introduction, hosts} = hostData.locales[locale];
  return (
    <>
      <p>{introduction}</p>
      <Tabs className={styles.tabs} defaultValue={hosts[0].id} groupId="dex-skill-host" queryString="host">
        {hosts.map((host) => (
          <TabItem key={host.id} label={host.label} value={host.id}>
            {renderHostContent(host.children as HostContent, skill)}
          </TabItem>
        ))}
      </Tabs>
    </>
  );
}
