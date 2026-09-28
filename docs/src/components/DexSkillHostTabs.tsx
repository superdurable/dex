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
import styles from './DexSkillHostTabs.module.css';

export type DexSkillContext =
  | 'all'
  | 'dex-app-builder'
  | 'dex-sdk'
  | 'dex-connector-contributor';

type DexSkillHostTabsProps = {
  skill: DexSkillContext;
};

type InvocationStyle = 'natural' | 'codex-plugin' | 'codex' | 'claude-plugin' | 'slash';

const SKILL_NAMES = [
  'dex-app-builder',
  'dex-sdk',
  'dex-connector-contributor',
] as const;

type DexSkillName = (typeof SKILL_NAMES)[number];

const PROMPTS: Record<DexSkillName, string> = {
  'dex-app-builder': 'Design and build an event-registration application.',
  'dex-sdk': 'Diagnose why this Flow retries after the payment Step completes.',
  'dex-connector-contributor': 'Add <XYZ> to Dex official connector library',
};

function selectedSkillNames(skill: DexSkillContext): readonly DexSkillName[] {
  return skill === 'all' ? SKILL_NAMES : [skill];
}

function invocationLine(skillName: DexSkillName, style: InvocationStyle): string {
  const prompt = PROMPTS[skillName];
  if (style === 'codex-plugin') {
    return `@Dex ${prompt}`;
  }
  if (style === 'codex') {
    return `$${skillName} ${prompt}`;
  }
  if (style === 'claude-plugin') {
    return `/superdurable-dex:${skillName} ${prompt}`;
  }
  if (style === 'slash') {
    return `/${skillName} ${prompt}`;
  }
  return prompt;
}

function InvocationExamples({
  skill,
  style,
}: {
  skill: DexSkillContext;
  style: InvocationStyle;
}): ReactNode {
  const examples = selectedSkillNames(skill)
    .map((skillName) => invocationLine(skillName, style))
    .join('\n');
  return <CodeBlock language="text">{examples}</CodeBlock>;
}

function StandaloneSkills({
  agent,
  invocationStyle,
  isChinese,
  skill,
}: {
  agent: 'codex' | 'claude-code' | 'cursor';
  invocationStyle: InvocationStyle;
  isChinese: boolean;
  skill: DexSkillContext;
}): ReactNode {
  return (
    <>
      <h3>Standalone Skills</h3>
      <p>
        {isChinese
          ? '如果选择该方式，请不要再安装 Plugin。三个 Skills 必须一起安装。'
          : 'Do not also install the Plugin when using this path. Install all three Skills together.'}
      </p>
      <h4>{isChinese ? '安装' : 'Install'}</h4>
      <CodeBlock language="bash">
        {`npx skills add superdurable/dex-skills --skill '*' --agent ${agent} --global`}
      </CodeBlock>
      <h4>{isChinese ? '使用' : 'Use'}</h4>
      <InvocationExamples skill={skill} style={invocationStyle} />
      <h4>{isChinese ? '更新' : 'Update'}</h4>
      <CodeBlock language="bash">
        {'npx skills update dex-app-builder dex-sdk dex-connector-contributor --global'}
      </CodeBlock>
      <p>
        {isChinese
          ? '如果已安装的 skills CLI 不支持 update，请重新运行上面的安装命令。'
          : 'If the installed skills CLI does not support update, rerun the installation command above.'}
      </p>
    </>
  );
}

function CodexDesktop({
  isChinese,
  skill,
}: {
  isChinese: boolean;
  skill: DexSkillContext;
}): ReactNode {
  return (
    <>
      <h3>{isChinese ? 'Plugin（推荐）' : 'Plugin (recommended)'}</h3>
      <h4>{isChinese ? '安装' : 'Install'}</h4>
      <p>
        {isChinese
          ? '此路径不需要单独安装 Codex CLI。'
          : 'This path does not require a separate Codex CLI installation.'}
      </p>
      <ol>
        <li>
          {isChinese
            ? '打开 plugin browser，选择添加菜单，然后点击 Add a marketplace。'
            : 'Open the plugin browser, open the add menu, and select Add a marketplace.'}
        </li>
        <li>
          {isChinese
            ? 'Source 使用下面的 repository，Git ref 使用 main，Sparse paths 保持为空。'
            : 'Use the repository below as Source, main as Git ref, and leave Sparse paths empty.'}
        </li>
      </ol>
      <CodeBlock language="text">
        {'https://github.com/superdurable/dex-skills'}
      </CodeBlock>
      <ol start={3}>
        <li>
          {isChinese
            ? '添加 marketplace，打开 Super Durable，选择 Dex 并安装。'
            : 'Add the marketplace, open Super Durable, select Dex, and install it.'}
        </li>
        <li>
          {isChinese
            ? '打开 repository 或空 project，然后创建新 task。'
            : 'Open a repository or empty project, then start a new task.'}
        </li>
      </ol>
      <h4>{isChinese ? '使用' : 'Use'}</h4>
      <p>
        {isChinese
          ? '输入 @，从 picker 中选择 Dex，再输入请求。只输入普通 @Dex 文本不会创建 Plugin binding。'
          : 'Type @, select Dex from the picker, then enter the request. Plain @Dex text does not create a Plugin binding.'}
      </p>
      <InvocationExamples skill={skill} style="codex-plugin" />
      <h4>{isChinese ? '更新' : 'Update'}</h4>
      <p>
        {isChinese
          ? '在 plugin browser 的 marketplace 管理页面找到 Super Durable，点击 Upgrade。完成后重启 Desktop app，并创建新 task。'
          : 'Find Super Durable in the plugin browser marketplace manager and select Upgrade. Restart the desktop app and start a new task.'}
      </p>
      <StandaloneSkills
        agent="codex"
        invocationStyle="codex"
        isChinese={isChinese}
        skill={skill}
      />
    </>
  );
}

function CodexCLI({
  isChinese,
  skill,
}: {
  isChinese: boolean;
  skill: DexSkillContext;
}): ReactNode {
  return (
    <>
      <h3>{isChinese ? 'Plugin（推荐）' : 'Plugin (recommended)'}</h3>
      <h4>{isChinese ? '安装' : 'Install'}</h4>
      <p>
        {isChinese
          ? '先确认 shell 的 PATH 可以找到 Codex CLI，然后添加 marketplace 和 Plugin。'
          : 'First confirm that Codex CLI is available on PATH, then add the marketplace and Plugin.'}
      </p>
      <CodeBlock language="bash">
        {`codex --version
codex plugin marketplace add superdurable/dex-skills
codex plugin add superdurable-dex@superdurable`}
      </CodeBlock>
      <h4>{isChinese ? '使用' : 'Use'}</h4>
      <p>
        {isChinese
          ? '启动新的 interactive Codex session，输入 @，并从 picker 中选择 Dex。初始 codex prompt 或 codex exec 中的普通 @Dex 文本不是结构化 Plugin binding。'
          : 'Start a new interactive Codex session, type @, and select Dex from the picker. Plain @Dex text in the initial codex prompt or codex exec is not a structured Plugin binding.'}
      </p>
      <InvocationExamples skill={skill} style="codex-plugin" />
      <h4>{isChinese ? '更新' : 'Update'}</h4>
      <CodeBlock language="bash">
        {`codex plugin marketplace upgrade superdurable
codex`}
      </CodeBlock>
      <p>
        {isChinese
          ? '在新 session 中打开 /plugins，确认 Dex 已启用。'
          : 'Open /plugins in the new session and confirm that Dex is enabled.'}
      </p>
      <StandaloneSkills
        agent="codex"
        invocationStyle="codex"
        isChinese={isChinese}
        skill={skill}
      />
      <p>
        {isChinese
          ? '需要确定性非交互调用时，请使用 standalone Skill 路径。'
          : 'Use the standalone Skill path for deterministic non-interactive invocation.'}
      </p>
    </>
  );
}

function ClaudeDesktop({
  isChinese,
  skill,
}: {
  isChinese: boolean;
  skill: DexSkillContext;
}): ReactNode {
  return (
    <>
      <h3>Plugin</h3>
      <h4>{isChinese ? '安装' : 'Install'}</h4>
      <ol>
        <li>
          {isChinese
            ? '打开 Customize，进入 Plugins tab。'
            : 'Open Customize and select the Plugins tab.'}
        </li>
        <li>
          {isChinese
            ? '选择 Add → Add marketplace → Add from a repository。'
            : 'Select Add → Add marketplace → Add from a repository.'}
        </li>
        <li>
          {isChinese
            ? '输入下面的 repository，然后打开 Super Durable marketplace 并安装 Dex。'
            : 'Enter the repository below, then open the Super Durable marketplace and install Dex.'}
        </li>
      </ol>
      <CodeBlock language="text">
        {'https://github.com/superdurable/dex-skills'}
      </CodeBlock>
      <h4>{isChinese ? '使用' : 'Use'}</h4>
      <p>
        {isChinese
          ? '使用自然语言请求或 / picker。'
          : 'Use a natural-language request or the / picker.'}
      </p>
      <InvocationExamples skill={skill} style="claude-plugin" />
      <h4>{isChinese ? '更新' : 'Update'}</h4>
      <p>
        {isChinese
          ? 'Claude 没有公开的 per-user upgrade command。organization-managed marketplace 由 owner 在 Organization settings → Plugins & skills → Marketplaces → Update 中更新。'
          : 'Claude does not document a per-user upgrade command. For an organization-managed marketplace, an owner uses Organization settings → Plugins & skills → Marketplaces → Update.'}
      </p>
      <h3>Standalone Skills</h3>
      <p>
        {isChinese
          ? 'Dex 不为 Claude Web 或 Desktop 分发 standalone ZIP Skills。请使用 Plugin。'
          : 'Dex does not distribute standalone ZIP Skills for Claude web or desktop. Use the Plugin path.'}
      </p>
    </>
  );
}

function ClaudeCodeCLI({
  isChinese,
  skill,
}: {
  isChinese: boolean;
  skill: DexSkillContext;
}): ReactNode {
  return (
    <>
      <h3>{isChinese ? 'Plugin（推荐）' : 'Plugin (recommended)'}</h3>
      <h4>{isChinese ? '安装' : 'Install'}</h4>
      <p>
        {isChinese
          ? '在 shell 中运行以下命令。也可以在 interactive Claude Code session 中使用相同命令的 /plugin 版本。'
          : 'Run these commands in a shell. You can also use their /plugin forms inside an interactive Claude Code session.'}
      </p>
      <CodeBlock language="bash">
        {`claude plugin marketplace add superdurable/dex-skills
claude plugin install superdurable-dex@superdurable`}
      </CodeBlock>
      <p>
        {isChinese
          ? '安装后启动新的 Claude Code session。Claude Code 2.1.273 或更高版本也可以同步同一账号的 Desktop Plugin。'
          : 'Start a new Claude Code session after installation. Claude Code 2.1.273 or later can also sync the desktop Plugin for the same account.'}
      </p>
      <h4>{isChinese ? '使用' : 'Use'}</h4>
      <InvocationExamples skill={skill} style="claude-plugin" />
      <h4>{isChinese ? '更新' : 'Update'}</h4>
      <CodeBlock language="bash">
        {'claude plugin update superdurable-dex@superdurable'}
      </CodeBlock>
      <p>
        {isChinese
          ? '也可以在 interactive session 中使用 /plugin marketplace update superdurable。更新后启动新 session。'
          : 'You can instead use /plugin marketplace update superdurable in an interactive session. Start a new session afterward.'}
      </p>
      <StandaloneSkills
        agent="claude-code"
        invocationStyle="slash"
        isChinese={isChinese}
        skill={skill}
      />
    </>
  );
}

function Cursor({
  isChinese,
  skill,
}: {
  isChinese: boolean;
  skill: DexSkillContext;
}): ReactNode {
  return (
    <>
      <h3>{isChinese ? 'Plugin（推荐）' : 'Plugin (recommended)'}</h3>
      <h4>{isChinese ? '安装' : 'Install'}</h4>
      <ol>
        <li>
          {isChinese
            ? '在 Cursor Desktop 中打开 project，然后打开侧栏中的 Customize。'
            : 'Open a project in Cursor Desktop, then open Customize in the sidebar.'}
        </li>
        <li>
          {isChinese
            ? '选择 Plugins → Add → From GitHub Repository，并输入下面的 repository。'
            : 'Select Plugins → Add → From GitHub Repository and enter the repository below.'}
        </li>
      </ol>
      <CodeBlock language="text">{'github.com/superdurable/dex-skills'}</CodeBlock>
      <ol start={3}>
        <li>
          {isChinese
            ? '打开 Dex，点击 Install，然后选择 user 或 project scope。'
            : 'Open Dex, select Install, and choose a user or project scope.'}
        </li>
      </ol>
      <p>
        {isChinese
          ? 'user-scope Desktop 安装也可用于 Cursor CLI。或者启动 cursor-agent，输入 /plugin，在 Marketplace tab 中安装 Dex。'
          : 'A user-scope desktop installation is also available to Cursor CLI. Alternatively, start cursor-agent, enter /plugin, and install Dex from the Marketplace tab.'}
      </p>
      <h4>{isChinese ? '使用' : 'Use'}</h4>
      <InvocationExamples skill={skill} style="slash" />
      <h4>{isChinese ? '更新' : 'Update'}</h4>
      <p>
        {isChinese
          ? '对于 GitHub-imported team marketplace，管理员点击 Refresh 或启用 Auto Refresh，然后用户 reload Cursor window。'
          : 'For a GitHub-imported team marketplace, an administrator selects Refresh or enables Auto Refresh, then users reload the Cursor window.'}
      </p>
      <StandaloneSkills
        agent="cursor"
        invocationStyle="slash"
        isChinese={isChinese}
        skill={skill}
      />
      <p>
        {isChinese
          ? 'Cursor 的 Plugin 和 standalone 安装使用相同短 slash command，只有安装来源不同。'
          : 'Cursor uses the same short slash command for Plugin and standalone installs; only the installation source differs.'}
      </p>
    </>
  );
}

function OtherClients({
  isChinese,
  skill,
}: {
  isChinese: boolean;
  skill: DexSkillContext;
}): ReactNode {
  return (
    <>
      <h3>Plugin</h3>
      <p>
        {isChinese
          ? 'Dex 没有为未单列的 host 记录通用 marketplace Plugin 流程。'
          : 'Dex does not document one universal marketplace Plugin flow for unlisted hosts.'}
      </p>
      <h3>Standalone Skills</h3>
      <p>
        {isChinese
          ? '未单列的 Agent Skills client 使用 standalone 路径。'
          : 'Use standalone Skills with other Agent Skills clients.'}
      </p>
      <h4>{isChinese ? '安装' : 'Install'}</h4>
      <p>
        {isChinese
          ? '交互选择检测到的兼容 agent，或者显式传入该 host 的 agent id。'
          : 'Select a detected compatible agent interactively, or pass the host agent id explicitly.'}
      </p>
      <CodeBlock language="bash">
        {`npx skills add superdurable/dex-skills --skill '*' --global
npx skills add superdurable/dex-skills --skill '*' --agent <agent-id> --global`}
      </CodeBlock>
      <h4>{isChinese ? '使用' : 'Use'}</h4>
      <p>
        {isChinese
          ? '使用 host 的 Skill picker、调用 prefix 或匹配的自然语言请求。'
          : 'Use the host Skill picker, invocation prefix, or a matching natural-language request.'}
      </p>
      <InvocationExamples skill={skill} style="natural" />
      <h4>{isChinese ? '更新' : 'Update'}</h4>
      <CodeBlock language="bash">
        {'npx skills update dex-app-builder dex-sdk dex-connector-contributor --global'}
      </CodeBlock>
    </>
  );
}

export default function DexSkillHostTabs({skill}: DexSkillHostTabsProps): ReactNode {
  const {i18n} = useDocusaurusContext();
  const isChinese = i18n.currentLocale === 'zh-Hans';

  return (
    <>
      <p>
        {isChinese
          ? '选择当前使用的 host。一个 host 只能选择 Plugin 或 standalone Skills 中的一种安装路径。'
          : 'Choose your current host. On one host, use either the Plugin or standalone Skills, not both.'}
      </p>
      <Tabs
        className={styles.tabs}
        defaultValue="codex-desktop"
        groupId="dex-skill-host"
        queryString="host"
      >
        <TabItem label="Codex Desktop" value="codex-desktop">
          <CodexDesktop isChinese={isChinese} skill={skill} />
        </TabItem>
        <TabItem label="Codex CLI" value="codex-cli">
          <CodexCLI isChinese={isChinese} skill={skill} />
        </TabItem>
        <TabItem label="Claude Desktop" value="claude-desktop">
          <ClaudeDesktop isChinese={isChinese} skill={skill} />
        </TabItem>
        <TabItem label="Claude Code CLI" value="claude-cli">
          <ClaudeCodeCLI isChinese={isChinese} skill={skill} />
        </TabItem>
        <TabItem label="Cursor" value="cursor">
          <Cursor isChinese={isChinese} skill={skill} />
        </TabItem>
        <TabItem label={isChinese ? '其他客户端' : 'Other clients'} value="others">
          <OtherClients isChinese={isChinese} skill={skill} />
        </TabItem>
      </Tabs>
    </>
  );
}
