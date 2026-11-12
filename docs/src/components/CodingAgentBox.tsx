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
import Link from '@docusaurus/Link';
import CodeBlock from '@theme/CodeBlock';
import TabItem from '@theme/TabItem';
import Tabs from '@theme/Tabs';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {Sparkles} from 'lucide-react';

import hostData from '../data/coding-agent-hosts.json';

// Claude Code first. The commands match src/data/dex-skill-hosts.json.
const codingAgentHosts = hostData.hosts;

const copy = {
  en: {
    title: 'Do it with a coding agent',
    install: 'Install Dex Skills once:',
    other: 'Other hosts',
    prompt: 'Then paste this prompt:',
  },
  'zh-Hans': {
    title: '用编程 Agent 来做',
    install: '先安装一次 Dex Skills：',
    other: '其他 host',
    prompt: '然后粘贴这段提示词：',
  },
};

/** The copy-and-paste box for a coding agent: install Dex Skills, then a prompt for this page's task. */
export default function CodingAgentBox({children}: {children: ReactNode}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  return (
    <aside className="coding-agent-box" aria-label={text.title}>
      <p className="coding-agent-box__title">
        <span className="coding-agent-box__icon" aria-hidden="true">
          <Sparkles size={16} strokeWidth={2.25} />
        </span>
        {text.title}
      </p>
      <p className="coding-agent-box__label">
        {text.install}{' '}
        <Link to="/build-with-ai/dex-developer-skill#install-and-use-dex-skills">{text.other}</Link>
      </p>
      <Tabs groupId="coding-agent-host" className="coding-agent-box__tabs">
        {codingAgentHosts.map((host) => (
          <TabItem key={host.id} value={host.id} label={host.label}>
            <CodeBlock language="bash">{host.install}</CodeBlock>
          </TabItem>
        ))}
      </Tabs>
      <p className="coding-agent-box__label">{text.prompt}</p>
      {children}
    </aside>
  );
}
