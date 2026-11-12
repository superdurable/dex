/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import React, {useRef, useState, type ReactNode} from 'react';
import Link from '@docusaurus/Link';
import CodeBlock from '@theme/CodeBlock';
import TabItem from '@theme/TabItem';
import Tabs from '@theme/Tabs';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import {Check, ChevronDown, Copy, Sparkles} from 'lucide-react';

import hostData from '../data/coding-agent-hosts.json';

// Claude Code first. The commands match src/data/dex-skill-hosts.json.
const codingAgentHosts = hostData.hosts;

const copy = {
  en: {
    title: 'Do it with a coding agent',
    hint: 'Install Dex Skills, then paste a prompt.',
    copy: 'Copy prompt',
    copied: 'Copied',
    install: 'Install Dex Skills once:',
    other: 'Other hosts',
    prompt: 'Then paste this prompt:',
  },
  'zh-Hans': {
    title: '用编程 Agent 来做',
    hint: '安装 Dex Skills，然后粘贴提示词。',
    copy: '复制提示词',
    copied: '已复制',
    install: '先安装一次 Dex Skills：',
    other: '其他 host',
    prompt: '然后粘贴这段提示词：',
  },
};

/**
 * The copy-and-paste box for a coding agent. It stays one row tall: the row copies the page's prompt, and
 * opening it shows the Dex Skills install commands and the prompt.
 */
export default function CodingAgentBox({children}: {children: ReactNode}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  const bodyRef = useRef<HTMLDivElement>(null);
  const [isCopied, setIsCopied] = useState(false);
  const copyPrompt = (event: React.MouseEvent) => {
    event.preventDefault();
    const blocks = bodyRef.current?.querySelectorAll('pre');
    const lastBlock = blocks?.[blocks.length - 1];
    // A highlighted code block renders one span per line, so join them to keep the line breaks.
    const lines = lastBlock ? Array.from(lastBlock.querySelectorAll('.token-line'), (line) => line.textContent ?? '') : [];
    const prompt = lines.length > 0 ? lines.join('\n') : (lastBlock?.textContent ?? '');
    navigator.clipboard?.writeText(prompt).then(() => {
      setIsCopied(true);
      window.setTimeout(() => setIsCopied(false), 1600);
    }, () => undefined);
  };
  return (
    <details className="coding-agent-box">
      <summary className="coding-agent-box__summary">
        <span className="coding-agent-box__icon" aria-hidden="true">
          <Sparkles size={15} strokeWidth={2.25} />
        </span>
        <span className="coding-agent-box__title">{text.title}</span>
        <span className="coding-agent-box__hint">{text.hint}</span>
        <button type="button" className="coding-agent-box__copy" onClick={copyPrompt}>
          {isCopied ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
          {isCopied ? text.copied : text.copy}
        </button>
        <ChevronDown className="coding-agent-box__chevron" size={16} aria-hidden="true" />
      </summary>
      <div className="coding-agent-box__body" ref={bodyRef}>
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
      </div>
    </details>
  );
}
