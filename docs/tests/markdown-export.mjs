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

<DocsFlowDefinitionGraph graph={flowDefinition} />

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
    {id: 'decision:Begin:done', kind: 'decision', name: 'gracefulComplete', parentId: 'step:Begin', condition: 'not needs_wait',
      decision: {type: 'gracefulComplete'}},
    {id: 'decision:Wait:fail', kind: 'decision', name: 'forceFail', parentId: 'step:Wait', decision: {type: 'forceFail'}},
    {id: 'decision:Wait:empty', kind: 'decision', name: 'forceCompleteIfChannelsEmpty', parentId: 'step:Wait',
      decision: {type: 'forceCompleteIfChannelsEmpty'}},
    {id: 'decision:rpc', kind: 'decision', name: 'rpcResult', parentId: 'rpc:describe', decision: {type: 'rpcResult'}},
    {id: 'rpc:describe', kind: 'rpc', name: 'describe'},
  ],
  edges: [{id: 'edge:1', kind: 'transition', from: 'decision:Begin', to: 'step:Wait', label: 'go_to'}],
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
  await writeFile(join(siteDir, 'src/data/graph.json'), JSON.stringify(graph));
  await writeFile(join(outDir, 'img/example.png'), '');
  await writeFile(join(contentPath, 'index.mdx'), homeSource);
  await writeFile(join(contentPathLocalized, 'translated.mdx'), '# Translated\n');
  await writeFile(join(contentPath, 'translated.mdx'), '# English source\n');
  await writeFile(join(contentPath, 'older.mdx'), '# Earlier version\n');
  const metadata = (id, source, permalink, title) => ({id, source, permalink, title,
    description: `The ${title}\n  meta description.`, frontMatter: {description: 'Ignored front matter description.'}});
  const fallback = metadata('index', '@site/reference/index.mdx', '/zh-Hans/', 'Home');
  const translated = metadata('translated', '@site/i18n/zh-Hans/reference/translated.mdx', '/zh-Hans/translated/', 'Translated');
  const earlier = metadata('older', '@site/reference/older.mdx', '/zh-Hans/reference/go/1.32/', 'Go reference');
  const sidebars = {docs: [{type: 'category', label: '参考', link: {type: 'doc', id: 'translated'}, items: []}, {type: 'doc', id: 'index'}]};
  const options = {siteDir, outDir, baseUrl: '/zh-Hans/', siteConfig: {url: 'https://docs.example.test', title: 'Fixture', trailingSlash: true},
    i18n: {currentLocale: 'zh-Hans', defaultLocale: 'en', locales: ['en', 'zh-Hans']},
    routesPaths: ['/zh-Hans/', '/zh-Hans/translated/', '/zh-Hans/reference/go/1.32/', '/zh-Hans/cloud/'],
    allContent: {'docusaurus-plugin-content-docs': {
      primary: {loadedVersions: [{contentPath, contentPathLocalized, sidebars, docs: [fallback, translated]}]},
      'reference-go': {loadedVersions: [{contentPath, contentPathLocalized, docs: [earlier]},
        {contentPath, contentPathLocalized, docs: [{...earlier, permalink: '/zh-Hans/reference/go/1.33/'}]}]},
    }}};
  await exportMarkdown(options);
  const home = await readFile(join(outDir, 'index.md'), 'utf8');
  assert.match(home, /canonical_url: "https:\/\/docs.example.test\/zh-Hans\/"/);
  assert.match(home, /description: "The Home meta description."/);
  assert.match(home, /source: "docs\/reference\/index.mdx"/);
  assert.match(home, /locale: "zh-Hans"/);
  assert.match(home, /<a id="stable"><\/a>\n\n## Local heading/);
  assert.match(home, /> \*\*备注：Read first\*\*/);
  assert.match(home, /> \*\*重要\*\*\n>\n> Every admonition keyword is supported./);
  assert.match(home, /https:\/\/docs.example.test\/zh-Hans\/translated.md#stable/);
  assert.match(home, /https:\/\/docs.example.test\/zh-Hans\/reference\/go\/1.32.md/);
  assert.match(home, /!\[Example\]\(https:\/\/docs.example.test\/img\/example.png\)/);
  assert.match(home, /Jump to <a id="kept-anchor"><\/a>the anchor text here./);

  // SDK snippets follow the page tab order, and fences keep their own language.
  const sdkOrder = ['**Python**', '**Go**', '**Rust**'].map(label => home.indexOf(label));
  assert.deepEqual([...sdkOrder].sort((left, right) => left - right), sdkOrder, 'SDK snippets must follow the page tab order');
  assert.match(home, /```rust\nlet sample = 1;\n```/);
  assert.match(home, /```bash\ngo run .\/sample\n```/);
  assert.match(home, /```go\nconst sample = 1\n```/);
  assert.match(home, /https:\/\/github.com\/superdurable\/dex\/tree\/main\/examples\/go\/sample.go/);
  assert.match(home, /例子: \[examples\/go\/sample.go\]/);
  assert.match(home, /\*\*Go\*\*\n\n```go\nconst fromChild = 1\n```\n\n\*\*Java\*\*\n\n```java\nvar fromProp = 1;\n```\n\n例子: \[examples\/java\/Sample.java\]/);
  assert.doesNotMatch(home, /overridden/);

  // Flow graphs keep the start Step, wait conditions, terminal decisions, and decision conditions.
  assert.match(home, /\*\*Flow：SampleFlow\*\*\n\n起始 Step：`Begin`\n\n\*\*Step 列表\*\*/);
  assert.match(home, /- `Begin`：Starts the sample.\n  - `gracefulComplete` 条件 `not needs_wait`\n- `Wait`/);
  assert.match(home, /  - WaitFor `skipWaitImmediately` 条件 `is_ready\(\)`/);
  assert.match(home, /  - WaitFor `anyOf`：timer `one_day timer`, channel `approval.for 1` 条件 `not is_ready\(\)`/);
  assert.match(home, /  - `forceFail`\n  - `forceCompleteIfChannelsEmpty`\n/);
  assert.doesNotMatch(home, /`rpcResult`/);
  assert.match(home, /\*\*连接\*\*\n\n- `goTo` \(decision; Begin; 条件 `needs_wait`\) → `Wait` \(step\)：go_to\n/);
  assert.match(home, /例子: \[examples\/python\/sample_flow.py\]/);

  // Skill hosts sit one level below the surrounding section.
  assert.match(home, /## Install and use\n\n选择当前使用的 host/);
  assert.match(home, /\n### Codex Desktop\n\n#### Plugin/);
  assert.match(home, /\n##### /);
  assert.doesNotMatch(home, /\n## Codex Desktop/);
  assert.match(home, /```text\nhttps:\/\/github.com\/superdurable\/dex-skills\n```/);

  assert.match(await readFile(join(outDir, 'reference/go/1.32.md'), 'utf8'), /# Earlier version/);
  const index = await readFile(join(outDir, 'llms.txt'), 'utf8');
  assert.match(index, /^# Fixture\n\n> Dex 是 Durable Execution 平台。/);
  assert.equal((index.match(/^-/gm) ?? []).length, 4);
  assert.ok(index.indexOf('## 参考\n\n- [Translated]') < index.indexOf('## Home\n\n- [Home]'), 'llms.txt must follow sidebar order');
  assert.match(index, /## 其他页面\n\n- \[Go reference\].*1.32.md\): The Go reference meta description./);
  assert.doesNotMatch(index, /Ignored front matter description/);

  const english = {...options, baseUrl: '/', outDir: join(siteDir, 'build'), routesPaths: ['/', '/translated/'],
    i18n: {currentLocale: 'en', defaultLocale: 'en', locales: ['en', 'zh-Hans']},
    allContent: {'docusaurus-plugin-content-docs': {primary: {loadedVersions: [{contentPath, contentPathLocalized, sidebars,
      docs: [{...fallback, permalink: '/'}, {...translated, source: '@site/reference/translated.mdx', permalink: '/translated/'}]}]}}}};
  await writeFile(join(contentPath, 'index.mdx'), '# Home\n\n:::caution\n\nCareful.\n\n:::\n\n:::tip Short title\n\nTip.\n\n:::\n\n[Chinese](/zh-Hans/translated/)\n\n[Go API](/api/go/1.33/)\n');
  await exportMarkdown(english);
  const englishHome = await readFile(join(siteDir, 'build/index.md'), 'utf8');
  assert.match(englishHome, /> \*\*Caution\*\*/);
  assert.match(englishHome, /> \*\*Tip: Short title\*\*/);
  assert.match(englishHome, /\[Go API\]\(https:\/\/docs.example.test\/api\/go\/1.33\/\)/, 'API reference links skip the disk check');
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
    + 'prop SdkTabs, graphs, skill hosts, admonitions, descriptions, llms.txt sections, and fail-closed syntax.');
} finally {
  await rm(fixture, {recursive: true, force: true});
}
