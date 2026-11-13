// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from 'node:assert/strict';
import {mkdtemp, mkdir, readFile, writeFile, copyFile, rm} from 'node:fs/promises';
import {tmpdir} from 'node:os';
import {join, dirname} from 'node:path';
import {fileURLToPath} from 'node:url';
import {exportMarkdown} from '../plugins/markdown-export/export.mjs';

const docsRoot = dirname(dirname(fileURLToPath(import.meta.url)));
const fixture = await mkdtemp(join(tmpdir(), 'dex-markdown-export-'));
const fence = '```';
const homeSource = `---
title: Home
---

import flowDefinition from '@site/src/data/graph.json';

## Local heading{#stable}

:::note[Read first]

[Translated](./translated.mdx#stable) and [section](/reference/go/1.32/).

:::

:::important

Every admonition keyword is supported.

:::

<a href="/img/example.png">
<img src="/img/example.png" alt="Example" />
</a>

Jump to <a id="kept-anchor">the anchor text</a> here.

<div className="docs-home-sections">
  <DocsSection section="intro" icon="rocket" to="/translated" title="Card one" meta="10 min">
    First card.
  </DocsSection>
  <DocsSection section="concepts" to="/translated#stable" title="Card two">
    Second card.
  </DocsSection>
</div>

Pick [Go](/?sdk=go) or [Rust](/?sdk=rust#stable).

<SdkTabs examples={{go: 'examples/go/sample.go'}}>
<SdkSnippet lang="rust">

${fence}rust
let sample = 1;
${fence}

</SdkSnippet>
<SdkSnippet lang="go">

${fence}bash
go run ./sample
${fence}

${fence}
const sample = 1
${fence}

</SdkSnippet>
<SdkSnippet lang="python">

${fence}python
sample = 1
${fence}

</SdkSnippet>
</SdkTabs>

<SdkTabs java="var fromProp = 1;" go="const overridden = 1" examples={{java: 'examples/java/Sample.java'}}>
<SdkSnippet lang="go">

${fence}go
const fromChild = 1
${fence}

</SdkSnippet>
</SdkTabs>

<SdkTabs>
<SdkSnippet lang="go">

${fence}go
const goOnlyFirst = 1
${fence}

</SdkSnippet>
</SdkTabs>

The same sample as a session:

<SdkTabs>
<SdkSnippet lang="go">

${fence}go
const goOnlySecond = 1
${fence}

</SdkSnippet>
</SdkTabs>

<DocsFlowDefinitionGraph graph={flowDefinition} />

<DocsFlowDefinitionGraph graph={flowDefinition} markdownReference="/" markdownReferenceLabel="Graph home" />

<DexSystemDiagram markdownDescription="A labelled diagram." />

<CrashTimeline variant="quick-start" />

<FrameworkTabs>
<FrameworkTab framework="claude-agent-sdk">

Claude only text.

</FrameworkTab>
<FrameworkTab framework="mastra">

See [the agents setup](/agents#setup) and [this page's section](/#stable).

<SdkTabs>
<SdkSnippet lang="typescript">

${fence}ts
const mastraOnly = 1
${fence}

</SdkSnippet>
</SdkTabs>

</FrameworkTab>
</FrameworkTabs>

## Install and use

<DexSkillHostTabs skill="dex-sdk" />
`;
const graph = {
  source: {path: 'examples/python/sample_flow.py'},
  flow: {name: 'SampleFlow', startStepId: 'step:Begin'},
  nodes: [
    {id: 'step:Begin', kind: 'step', name: 'Begin', metadata: {explanation: 'Starts the sample.'}},
    {id: 'step:Wait', kind: 'step', name: 'Wait'},
    {id: 'wait:Wait:skip', kind: 'wait', name: 'skipWaitImmediately', parentId: 'step:Wait', condition: 'is_ready()',
      wait: {type: 'skipWaitImmediately', conditions: []}},
    {id: 'wait:Wait:any', kind: 'wait', name: 'anyOf', parentId: 'step:Wait', condition: 'not is_ready()',
      wait: {type: 'anyOf', conditions: [{kind: 'timer', label: 'one_day timer'}, {kind: 'channel', label: 'approval.for 1'}]}},
    {id: 'decision:Begin', kind: 'decision', name: 'goTo', parentId: 'step:Begin', condition: 'needs_wait', decision: {type: 'goTo'}},
    {id: 'decision:Begin:done', kind: 'decision', name: 'gracefulComplete', parentId: 'step:Begin', condition: 'not (context.has_timer_fired())',
      decision: {type: 'gracefulComplete'}},
    {id: 'decision:Wait:fail', kind: 'decision', name: 'forceFail', parentId: 'step:Wait', decision: {type: 'forceFail'}},
    {id: 'decision:Wait:empty', kind: 'decision', name: 'forceCompleteIfChannelsEmpty', parentId: 'step:Wait',
      decision: {type: 'forceCompleteIfChannelsEmpty'}},
    {id: 'decision:rpc', kind: 'decision', name: 'rpcResult', parentId: 'rpc:describe', decision: {type: 'rpcResult'}},
    {id: 'rpc:describe', kind: 'rpc', name: 'describe'},
    {id: 'resource:status', kind: 'attribute', name: 'status'},
  ],
  edges: [{id: 'edge:1', kind: 'transition', from: 'decision:Begin', to: 'step:Wait', label: 'go_to'},
    {id: 'edge:2', kind: 'resource_write', from: 'step:Begin', to: 'resource:status', label: 'set'},
    {id: 'edge:3', kind: 'resource_read', from: 'resource:status', to: 'rpc:describe', label: 'get'},
    {id: 'edge:4', kind: 'failure_transition', from: 'step:Wait', to: 'step:Begin', label: 'Execute failure'}],
};

try {
  const siteDir = join(fixture, 'docs');
  const outDir = join(siteDir, 'build', 'zh-Hans');
  const contentPath = join(siteDir, 'reference');
  const contentPathLocalized = join(siteDir, 'i18n', 'zh-Hans', 'reference');
  await mkdir(join(siteDir, 'src/data'), {recursive: true});
  await mkdir(join(outDir, 'img'), {recursive: true});
  await mkdir(contentPath, {recursive: true});
  await mkdir(contentPathLocalized, {recursive: true});
  await copyFile(join(docsRoot, 'src/data/dex-skill-hosts.json'), join(siteDir, 'src/data/dex-skill-hosts.json'));
  await copyFile(join(docsRoot, 'src/data/coding-agent-hosts.json'), join(siteDir, 'src/data/coding-agent-hosts.json'));
  await copyFile(join(docsRoot, 'src/data/crash-timeline.json'), join(siteDir, 'src/data/crash-timeline.json'));
  await writeFile(join(siteDir, 'src/data/graph.json'), JSON.stringify(graph));
  await writeFile(join(outDir, 'img/example.png'), '');
  await writeFile(join(contentPath, 'index.mdx'), homeSource);
  await writeFile(join(contentPathLocalized, 'translated.mdx'), '# Translated\n');
  await writeFile(join(contentPath, 'translated.mdx'), '# English source\n');
  await writeFile(join(contentPath, 'older.mdx'), '# Earlier version\n');
  await writeFile(join(contentPath, 'agents.mdx'), '# Agents\n\n<FrameworkTabs>\n<FrameworkTab framework="claude-agent-sdk">\n\nClaude agents.\n\n</FrameworkTab>\n<FrameworkTab framework="mastra">\n\n## Setup\n\nMastra agents.\n\n</FrameworkTab>\n</FrameworkTabs>\n');
  await writeFile(join(contentPath, 'moved.mdx'), '# Moved\n\nThis page moved. See [Home](/zh-Hans/).\n');
  const metadata = (id, source, permalink, title) => ({id, source, permalink, title,
    description: `The ${title}\n  meta description.`, frontMatter: {description: 'Ignored front matter description.'}});
  const fallback = metadata('index', '@site/reference/index.mdx', '/zh-Hans/', 'Home');
  const translated = metadata('translated', '@site/i18n/zh-Hans/reference/translated.mdx', '/zh-Hans/translated/', 'Translated');
  const earlier = metadata('older', '@site/reference/older.mdx', '/zh-Hans/reference/go/1.32/', 'Go reference');
  const agents = metadata('agents', '@site/reference/agents.mdx', '/zh-Hans/agents/', 'Agents');
  // Without a front matter description, Docusaurus keeps only the first source line.
  const moved = {id: 'moved', source: '@site/reference/moved.mdx', permalink: '/zh-Hans/moved/', title: 'Moved',
    description: 'This page moved.', frontMatter: {}};
  const sidebars = {docs: [{type: 'category', label: '参考', link: {type: 'doc', id: 'translated'}, items: []}, {type: 'doc', id: 'index'}]};
  const options = {siteDir, outDir, baseUrl: '/zh-Hans/', siteConfig: {url: 'https://docs.example.test', title: 'Fixture', trailingSlash: true},
    i18n: {currentLocale: 'zh-Hans', defaultLocale: 'en', locales: ['en', 'zh-Hans']},
    routesPaths: ['/zh-Hans/', '/zh-Hans/agents/', '/zh-Hans/translated/', '/zh-Hans/moved/', '/zh-Hans/reference/go/1.32/', '/zh-Hans/cloud/'],
    allContent: {'docusaurus-plugin-content-docs': {
      primary: {loadedVersions: [{isLast: true, contentPath, contentPathLocalized, sidebars, docs: [fallback, translated, moved, agents]}]},
      'reference-go': {loadedVersions: [{isLast: false, contentPath, contentPathLocalized, docs: [earlier]},
        {isLast: true, contentPath, contentPathLocalized, docs: [{...earlier, permalink: '/zh-Hans/reference/go/1.33/'}]}]},
    }}};
  await exportMarkdown(options);
  const home = await readFile(join(outDir, 'index.md'), 'utf8');
  // CrashTimeline exports its events as a numbered list in the page locale, without the animation.
  assert.match(home, /1\. 你启动 Flow，Dex Server 记录下来。\n2\. Worker 1 运行 Charge。Dex 保存它的结果。\n3\. Dex Server：/);
  assert.match(home, /Charge 只运行了一次，Ship 只运行了一次。/);
  assert.match(home, /canonical_url: "https:\/\/docs.example.test\/zh-Hans\/"/);
  assert.match(home, /description: "The Home meta description."/);
  assert.match(home, /source: "docs\/reference\/index.mdx"/);
  // The fallback home page has no Chinese translation, so its labels name English and the site locale.
  assert.match(home, /\nlocale: "en"\nsite_locale: "zh-Hans"\n/);
  assert.match(home, /\n> 本页内容是英文原文，中文译文还在准备中。\n/);
  const translatedPage = await readFile(join(outDir, 'translated.md'), 'utf8');
  assert.match(translatedPage, /\nlocale: "zh-Hans"\n/);
  assert.doesNotMatch(translatedPage, /site_locale|本页内容是英文原文/);
  assert.match(home, /<a id="stable"><\/a>\n\n## Local heading/);
  assert.match(home, /> \*\*备注：Read first\*\*/);
  assert.match(home, /> \*\*重要\*\*\n>\n> Every admonition keyword is supported./);
  assert.match(home, /https:\/\/docs.example.test\/zh-Hans\/translated.md#stable/);
  assert.match(home, /https:\/\/docs.example.test\/zh-Hans\/reference\/go\/1.32.md/);
  assert.match(home, /!\[Example\]\(https:\/\/docs.example.test\/img\/example.png\)/);
  assert.match(home, /Jump to <a id="kept-anchor"><\/a>the anchor text here./);
  // A card group exports as a list: title link, optional meta, then the description.
  assert.match(home, /\n- \[Card one\]\(https:\/\/docs\.example\.test\/zh-Hans\/translated\.md\) \(10 min\): First card\.\n- \[Card two\]\(https:\/\/docs\.example\.test\/zh-Hans\/translated\.md#stable\): Second card\.\n/);
  // A ?sdk= link points at that language's file.
  assert.match(home, /Pick \[Go\]\(https:\/\/docs\.example\.test\/zh-Hans\/index\.go\.md\) or \[Rust\]\(https:\/\/docs\.example\.test\/zh-Hans\/index\.rust\.md#stable\)\./);

  // A page with code in several SDKs gets one Markdown file per language. The default file shows the first
  // language in tab order and has no file of its own; a group without the file's language becomes a one-line note.
  assert.match(home, /\n本页代码为 Python。其他语言：\[Go\]\(https:\/\/docs\.example\.test\/zh-Hans\/index\.go\.md\) · \[Java\]\(https:\/\/docs\.example\.test\/zh-Hans\/index\.java\.md\) · \[Rust\]\(https:\/\/docs\.example\.test\/zh-Hans\/index\.rust\.md\)\n/);
  assert.match(home, /language: "python"/);
  assert.match(home, /```python\nsample = 1\n```/);
  assert.doesNotMatch(home, /```rust\nlet sample = 1;\n```/);
  assert.doesNotMatch(home, /const fromChild = 1/);
  assert.match(home, /这部分没有 Python 版本；见 \[Go 页面\]\(https:\/\/docs\.example\.test\/zh-Hans\/index\.go\.md\)。/);
  assert.doesNotMatch(home, /var fromProp/);
  // Back-to-back notes for the same page collapse into one, and a lead-in left between them goes too.
  assert.equal(home.match(/这部分没有 Python 版本/g).length, 1);
  assert.doesNotMatch(home, /The same sample as a session:/);
  // A page with framework tabs gets one file per framework; each file shows only its framework's content.
  assert.match(home, /\n本文件为 Claude Agent SDK 版本。其他框架：\[Mastra\]\(https:\/\/docs\.example\.test\/zh-Hans\/index\.mastra\.md\)\n/);
  assert.match(home, /Claude only text\./);
  assert.doesNotMatch(home, /mastraOnly/);
  const homeMastra = await readFile(join(outDir, 'index.mastra.md'), 'utf8');
  assert.match(homeMastra, /framework: "mastra"/);
  assert.doesNotMatch(homeMastra, /Claude only text/);
  assert.match(homeMastra, /本文件为 Mastra 版本。其他框架：\[Claude Agent SDK\]\(https:\/\/docs\.example\.test\/zh-Hans\/index\.md\)/);
  // A link in one framework's file keeps that framework, and a link to this page's own section stays in this file.
  assert.match(homeMastra, /\[the agents setup\]\(https:\/\/docs\.example\.test\/zh-Hans\/agents\.mastra\.md#setup\)/);
  assert.match(homeMastra, /\[this page's section\]\(https:\/\/docs\.example\.test\/zh-Hans\/index\.mastra\.md#stable\)/);
  // The default framework's file also answers to that framework's name.
  assert.equal(await readFile(join(outDir, 'index.claude-agent-sdk.md'), 'utf8'), home);
  assert.equal(await readFile(join(outDir, 'agents.claude-agent-sdk.md'), 'utf8'), await readFile(join(outDir, 'agents.md'), 'utf8'));
  // A framework with TypeScript tabs only gets TypeScript files only.
  assert.match(homeMastra, /```ts\nconst mastraOnly = 1\n```/);
  await assert.rejects(readFile(join(outDir, 'index.mastra.python.md'), 'utf8'));
  await assert.rejects(readFile(join(outDir, 'index.mastra.typescript.md'), 'utf8'));
  const homeGo = await readFile(join(outDir, 'index.go.md'), 'utf8');
  assert.match(homeGo, /language: "go"/);
  assert.match(homeGo, /本页代码为 Go。其他语言：\[Python\]\(https:\/\/docs\.example\.test\/zh-Hans\/index\.md\)/);
  assert.match(homeGo, /```bash\ngo run .\/sample\n```/);
  assert.match(homeGo, /```go\nconst sample = 1\n```/);
  assert.match(homeGo, /https:\/\/github.com\/superdurable\/dex\/tree\/main\/examples\/go\/sample.go/);
  assert.match(homeGo, /例子: \[examples\/go\/sample.go\]/);
  assert.match(homeGo, /```go\nconst fromChild = 1\n```/);
  assert.doesNotMatch(homeGo, /```python\nsample = 1/);
  assert.match(homeGo, /const goOnlyFirst = 1\n```\n\nThe same sample as a session:\n\n\*\*Go\*\*\n\n```go\nconst goOnlySecond = 1/);
  const homeJava = await readFile(join(outDir, 'index.java.md'), 'utf8');
  assert.match(homeJava, /\*\*Java\*\*\n\n```java\nvar fromProp = 1;\n```\n\n例子: \[examples\/java\/Sample.java\]/);
  const homeRust = await readFile(join(outDir, 'index.rust.md'), 'utf8');
  // The note in a language file links the other language's file, not the file's own language.
  assert.doesNotMatch(homeRust, /这部分没有 Rust 版本；见 \[[^\]]+\]\([^)]*index\.rust\.md\)/);
  assert.match(homeRust, /```rust\nlet sample = 1;\n```/);
  // index.python.md serves the same Markdown as index.md, so a reader can name any language.
  assert.equal(await readFile(join(outDir, 'index.python.md'), 'utf8'), home);
  // Every Markdown file points AI readers at llms.txt.
  assert.match(home, /^---\n[\s\S]*?\n---\n\n> 供 AI Agent 使用的全部页面索引：https:\/\/docs\.example\.test\/zh-Hans\/llms\.txt\n/);
  for (const file of [home, homeGo, homeJava, homeRust]) assert.doesNotMatch(file, /overridden/);

  // Flow graphs read as a numbered list of Steps, start Step first: each Step is one paragraph of plain sentences,
  // and code expressions appear only in parentheses.
  // A Step with an explanation reads as prose: the explanation, then where it goes next in words.
  assert.match(home, /\*\*Flow 图：SampleFlow\*\*\n\n\*\*Step 列表\*\*\n\n1\. `Begin`（起始 Step）：Starts the sample\. 下一步：`Wait`，或 Flow 完成。 写入 `status`。\n/);
  assert.match(home, /\n2\. `Wait`：不等待（条件 `is_ready\(\)`）；等待以下任一条件 Timer `one_day`、Channel `approval`（1 条消息）（条件 `not is_ready\(\)`）。 让 Flow 失败；Channel 为空时完成 Flow；Execute 失败时转到 `Begin`。\n/);
  assert.match(home, /\*\*RPC 列表\*\*\n\n- `describe`：读取 `status`。\n/);
  assert.doesNotMatch(home, /`rpcResult`/);
  // A page can point to the page that lists a graph instead of repeating it.
  assert.match(home, /Flow 图：`SampleFlow`。 \[Graph home\]\(https:\/\/docs\.example\.test\/zh-Hans\/index\.md\)列出了它的 Step。/);
  // Diagram text reads as a labelled quote, apart from the prose.
  assert.match(home, /> \*\*图示\*\*：A labelled diagram\./);

  assert.match(home, /例子: \[examples\/python\/sample_flow.py\]/);

  // Skill hosts sit one level below the surrounding section.
  assert.match(home, /## Install and use\n\n选择你使用的 host/);
  assert.match(home, /\n### Claude\n\n#### Claude Code CLI\n\n##### Plugin/);
  assert.match(home, /\n#### Codex Desktop\n/);
  assert.match(home, /\n###### /);
  assert.doesNotMatch(home, /\n## Claude\n/);
  assert.match(home, /```text\nhttps:\/\/github.com\/superdurable\/dex-skills\n```/);

  assert.match(await readFile(join(outDir, 'reference/go/1.32.md'), 'utf8'), /# Earlier version/);
  const index = await readFile(join(outDir, 'llms.txt'), 'utf8');
  assert.match(index, /\n含代码的页面有按语言划分的 Markdown。页面有 Python 代码时，\.md 文件显示 Python，\.python\.md 与它相同；[^\n]*llms-full\.go\.txt[^\n]*\n/);
  // llms-full.<language>.txt carries that language's code.
  assert.match(await readFile(join(outDir, 'llms-full.go.txt'), 'utf8'), /const sample = 1/);
  assert.match(index, /^# Fixture\n\n> Dex 是 Durable Execution 平台。/);
  // Archived versions keep their Markdown twin but stay out of llms.txt.
  assert.equal((index.match(/^-/gm) ?? []).length, 4);
  // A moved notice stays out of llms.txt.
  assert.doesNotMatch(index, /\[Moved\]/);
  assert.doesNotMatch(index, /1\.32\.md/);
  assert.ok(index.indexOf('## 参考\n\n- [Translated]') < index.indexOf('## Home\n\n- [Home]'), 'llms.txt must follow sidebar order');
  assert.match(index, /## 其他页面\n\n(?:- [^\n]*\n)*- \[Go reference\].*1.33.md\): The Go reference meta description./);
  assert.doesNotMatch(index, /Ignored front matter description/);
  // llms-full.txt holds each llms.txt page's default Markdown, in the same order, after its URL.
  const full = await readFile(join(outDir, 'llms-full.txt'), 'utf8');
  assert.match(full, /^# Fixture\n\n> Dex 是 Durable Execution 平台。/);
  assert.ok(full.indexOf('<!-- https://docs.example.test/zh-Hans/translated.md -->') < full.indexOf('<!-- https://docs.example.test/zh-Hans/index.md -->'),
    'llms-full.txt must follow llms.txt order');
  assert.doesNotMatch(full, /<!-- [^>]*1\.32\.md -->/, 'archived versions stay out of llms-full.txt');
  // llms-full.txt shows Python code, so llms-full.python.txt is the same file.
  assert.equal(await readFile(join(outDir, 'llms-full.python.txt'), 'utf8'), full);

  const english = {...options, baseUrl: '/', outDir: join(siteDir, 'build'), routesPaths: ['/', '/translated/'],
    i18n: {currentLocale: 'en', defaultLocale: 'en', locales: ['en', 'zh-Hans']},
    allContent: {'docusaurus-plugin-content-docs': {primary: {loadedVersions: [{isLast: true, contentPath, contentPathLocalized, sidebars,
      docs: [{...fallback, permalink: '/'}, {...translated, source: '@site/reference/translated.mdx', permalink: '/translated/'}]}]}}}};
  await writeFile(join(contentPath, 'index.mdx'), '# Home\n\n:::caution\n\nCareful.\n\n:::\n\n:::tip Short title\n\nTip.\n\n:::\n\n[Chinese](/zh-Hans/translated/)\n\n[Go API](/api/go/1.33/)\n\n[Java API](pathname:///api/java/latest/)\n');
  await exportMarkdown(english);
  const englishHome = await readFile(join(siteDir, 'build/index.md'), 'utf8');
  assert.match(englishHome, /> \*\*Caution\*\*/);
  assert.match(englishHome, /> \*\*Tip: Short title\*\*/);
  assert.match(englishHome, /\[Go API\]\(https:\/\/docs.example.test\/api\/go\/1.33\/\)/, 'API reference links skip the disk check');
  assert.match(englishHome, /\[Java API\]\(https:\/\/docs.example.test\/api\/java\/latest\/\)/, 'pathname links become absolute site URLs');
  assert.match(await readFile(join(siteDir, 'build/llms.txt'), 'utf8'), /^# Fixture\n\n> Dex is a Durable Execution platform./);

  const failures = [
    ['# Heading\n\n<UnrecognizedDiagram />\n', /index.mdx:3: Unknown JSX component <UnrecognizedDiagram>/],
    ['# Heading\n\n<DexSystemDiagram />\n', /index.mdx:3: DexSystemDiagram requires markdownDescription/],
    ['# Heading\n\nTotal {1 + 1}\n', /index.mdx:3: MDX expressions need an explicit Markdown expansion/],
    ['# Heading\n\n{/* hidden comment */}\n', /index.mdx:3: MDX expressions need an explicit Markdown expansion/],
    ['# Heading\n\nexport const value = 1;\n', /index.mdx:3: Only imports are supported in MDX modules/],
    ['# Heading\n\n[Missing](./missing.mdx)\n', /index.mdx:3: Cannot resolve source link .\/missing.mdx/],
    ['# Heading\n\n[Missing](/no-such-page/)\n', /index.mdx:3: Link target \/no-such-page\/ is not a route or static file/],
    ['# Heading\n\n[Missing](/apis/go/)\n', /index.mdx:3: Link target \/apis\/go\/ is not a route or static file/],
    ['# Heading\n\n[Missing](pathname:///apis/go/)\n', /index.mdx:3: Link target pathname:\/\/\/apis\/go\/ must start with pathname:\/\/\/api\//],
    ['# Heading\n\n<DexSkillHostTabs skill={skillName} />\n', /index.mdx:3: Only literal MDX attributes are supported/],
    ['# Heading\n\n<SdkTabs go={<code>x</code>} />\n', /index.mdx:3: Only literal MDX attributes are supported/],
    ['# Heading\n\n<SdkTabs>\n<SdkSnippet lang="cobol">\n\n```cobol\nDISPLAY 1\n```\n\n</SdkSnippet>\n</SdkTabs>\n',
      /index.mdx:4: Unknown SDK language: cobol/],
    ['# Heading\n\n<SdkTabs examples={{cobol: \'examples/x\'}} go="x" />\n', /index.mdx:3: Unknown SDK language in examples: cobol/],
    ['# Heading\n\n<SdkTabs>\nLoose text\n</SdkTabs>\n', /index.mdx:4: SdkTabs children must be SdkSnippet elements/],
    ['# Heading\n\n<DexSkillHostTabs skill="unknown-skill" />\n', /index.mdx:3: Unknown skill: unknown-skill/],
    ['# Heading\n\n:::custom\n\nBody\n\n:::\n', /index.mdx:3: Unsupported admonition :::custom/],
    ['# Heading\n\n:::note\n\nBody\n', /index.mdx:3: Admonition is not closed/],
    ['# Heading\n\n:::\n', /index.mdx:3: Closing ::: has no open admonition/],
    ['# Heading\n\n<a>no target</a>\n', /index.mdx:3: <a> requires a string href or id/],
  ];
  for (const [source, expected] of failures) {
    await writeFile(join(contentPath, 'index.mdx'), source);
    await assert.rejects(exportMarkdown(options), expected);
  }
  console.log('Markdown export integration passed: multiple instances, versioned routes, locale fallback, links, anchors, SDK order and fences, '
    + 'prop SdkTabs, framework files, graphs, skill hosts, admonitions, descriptions, llms.txt sections, and fail-closed syntax.');
} finally {
  await rm(fixture, {recursive: true, force: true});
}
