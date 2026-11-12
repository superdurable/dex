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
    copy: 'Copy prompt',
    copied: 'Copied',
    more: 'Show full prompt and setup',
    less: 'Show less',
    install: 'Install Dex Skills once:',
    other: 'Other hosts',
    prompt: 'Then paste this prompt:',
  },
  'zh-Hans': {
    title: '用编程 Agent 来做',
    copy: '复制提示词',
    copied: '已复制',
    more: '显示完整提示词和安装步骤',
    less: '收起',
    install: '先安装一次 Dex Skills：',
    other: '其他 host',
    prompt: '然后粘贴这段提示词：',
  },
};

/**
 * The copy-and-paste box for a coding agent. Closed, it shows the start of the page's prompt and a copy button;
 * open, it adds the Dex Skills install commands and the full prompt.
 */
export default function CodingAgentBox({children}: {children: ReactNode}): ReactNode {
  const {i18n} = useDocusaurusContext();
  const text = i18n.currentLocale === 'zh-Hans' ? copy['zh-Hans'] : copy.en;
  const promptRef = useRef<HTMLDivElement>(null);
  const [isOpen, setIsOpen] = useState(false);
  const [isCopied, setIsCopied] = useState(false);
  const copyPrompt = () => {
    const block = promptRef.current?.querySelector('pre');
    // A highlighted code block renders one span per line, so join them to keep the line breaks.
    const lines = block ? Array.from(block.querySelectorAll('.token-line'), (line) => line.textContent ?? '') : [];
    const prompt = lines.length > 0 ? lines.join('\n') : (block?.textContent ?? '');
    navigator.clipboard?.writeText(prompt).then(() => {
      setIsCopied(true);
      window.setTimeout(() => setIsCopied(false), 1600);
    }, () => undefined);
  };
  return (
    <aside className={isOpen ? 'coding-agent-box coding-agent-box--open' : 'coding-agent-box'} aria-label={text.title}>
      <div className="coding-agent-box__header">
        <span className="coding-agent-box__icon" aria-hidden="true">
          <Sparkles size={15} strokeWidth={2.25} />
        </span>
        <span className="coding-agent-box__title">{text.title}</span>
        <button type="button" className="coding-agent-box__copy" onClick={copyPrompt}>
          {isCopied ? <Check size={14} aria-hidden="true" /> : <Copy size={14} aria-hidden="true" />}
          {isCopied ? text.copied : text.copy}
        </button>
      </div>
      {isOpen ? (
        <>
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
        </>
      ) : null}
      <div className="coding-agent-box__prompt" ref={promptRef}>
        {children}
      </div>
      <button type="button" className="coding-agent-box__toggle" aria-expanded={isOpen} onClick={() => setIsOpen(!isOpen)}>
        {isOpen ? text.less : text.more}
        <ChevronDown className="coding-agent-box__chevron" size={15} aria-hidden="true" />
      </button>
    </aside>
  );
}
