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
import {codingAgentCommand} from './codingAgentCommand';

// Claude Code first. The install commands match src/data/dex-skill-hosts.json.
const codingAgentHosts = hostData.hosts;

const copy = {
  en: {
    title: 'Do it with a coding agent',
    noteWithInstall: 'Installs Dex Skills, then starts the agent on this page.',
    note: 'Starts the agent on this page. Needs Dex Skills, installed once.',
    other: 'Other agents',
    install: 'Install Dex Skills',
  },
  'zh-Hans': {
    title: '用编程 Agent 来做',
    noteWithInstall: '先安装 Dex Skills，再让 Agent 按这一页来做。',
    note: '让 Agent 按这一页来做。需要先安装一次 Dex Skills。',
    other: '其他 Agent',
    install: '安装 Dex Skills',
  },
};

/**
 * One shell block per coding agent that starts the agent with this page's short prompt.
 * Only the Quick Start sets `withInstall`, so the Dex Skills install lines appear on one page.
 */
export default function CodingAgentBox({prompt, withInstall = false}: {prompt: string; withInstall?: boolean}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  return (
    <aside className="coding-agent-box" aria-label={text.title}>
      <div className="coding-agent-box__header">
        <span className="coding-agent-box__icon" aria-hidden="true">
          <Sparkles size={15} strokeWidth={2.25} />
        </span>
        <span className="coding-agent-box__title">{text.title}</span>
      </div>
      <p className="coding-agent-box__label">
        {withInstall ? text.noteWithInstall : text.note}{' '}
        <Link to="/build-with-ai/dex-developer-skill#install-and-use-dex-skills">
          {withInstall ? text.other : text.install}
        </Link>
      </p>
      <Tabs groupId="coding-agent-host" className="coding-agent-box__tabs">
        {codingAgentHosts.map((host) => (
          <TabItem key={host.id} value={host.id} label={host.label}>
            <CodeBlock language="bash">{codingAgentCommand(host, prompt, withInstall)}</CodeBlock>
          </TabItem>
        ))}
      </Tabs>
    </aside>
  );
}
