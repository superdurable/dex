/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {type ReactNode, useEffect, useState} from 'react';
import Link from '@docusaurus/Link';
import CodeBlock from '@theme/CodeBlock';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {Sparkles} from 'lucide-react';

import hostData from '../data/coding-agent-hosts.json';
import {codingAgentCommand} from './codingAgentCommand';

// Claude Code first. The install commands match src/data/dex-skill-hosts.json.
const codingAgentHosts = hostData.hosts;
// The same key Docusaurus Tabs used for this group, so a reader's earlier choice carries over.
const hostStorageKey = 'docusaurus.tab.coding-agent-host';

const copy = {
  en: {
    title: 'Do it with a coding agent',
    hosts: 'Coding agent',
    install: 'Install Dex Skills',
    other: 'Other agents',
  },
  'zh-Hans': {
    title: '用编程 Agent 来做',
    hosts: '编程 Agent',
    install: '安装 Dex Skills',
    other: '其他 Agent',
  },
};

/**
 * Two rows: a header with the agent switch and an install link, then the command.
 * Only the Quick Start sets `withInstall`, so the Dex Skills install lines appear on one page.
 */
export default function CodingAgentBox({prompt, withInstall = false}: {prompt: string; withInstall?: boolean}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  const [hostId, setHostId] = useState(codingAgentHosts[0].id);
  useEffect(() => {
    try {
      const saved = window.localStorage.getItem(hostStorageKey);
      if (saved && codingAgentHosts.some((host) => host.id === saved)) setHostId(saved);
    } catch {
      // Storage can be unavailable; the first agent stays selected.
    }
  }, []);
  const chooseHost = (id: string) => {
    setHostId(id);
    try {
      window.localStorage.setItem(hostStorageKey, id);
    } catch {
      // The choice still applies to this page.
    }
  };
  const host = codingAgentHosts.find((item) => item.id === hostId) ?? codingAgentHosts[0];
  return (
    <aside className="coding-agent-box" aria-label={text.title}>
      <div className="coding-agent-box__header">
        <span className="coding-agent-box__icon" aria-hidden="true">
          <Sparkles size={13} strokeWidth={2.25} />
        </span>
        <span className="coding-agent-box__title">{text.title}</span>
        <span className="coding-agent-box__hosts" role="group" aria-label={text.hosts}>
          {codingAgentHosts.map((item) => (
            <button
              key={item.id}
              type="button"
              className="coding-agent-box__host"
              aria-pressed={item.id === host.id}
              onClick={() => chooseHost(item.id)}>
              {item.label}
            </button>
          ))}
        </span>
        <Link className="coding-agent-box__link" to="/build-with-ai/dex-developer-skill#install-and-use-dex-skills">
          {withInstall ? text.other : text.install}
        </Link>
      </div>
      <CodeBlock language="bash">{codingAgentCommand(host, prompt, withInstall)}</CodeBlock>
    </aside>
  );
}
