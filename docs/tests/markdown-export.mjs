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
try {
  const siteDir = join(fixture, 'docs');
  const outDir = join(siteDir, 'build', 'zh-Hans');
  const contentPath = join(siteDir, 'reference');
  const contentPathLocalized = join(siteDir, 'i18n', 'zh-Hans', 'reference');
  await mkdir(join(siteDir, 'src/data'), {recursive: true});
  await mkdir(contentPath, {recursive: true});
  await mkdir(contentPathLocalized, {recursive: true});
  await copyFile(join(docsRoot, 'src/data/dex-skill-hosts.json'), join(siteDir, 'src/data/dex-skill-hosts.json'));
  const homeSource = `---\ntitle: Home\n---\n\n## Local heading{#stable}\n\n:::note Read first\n\n[Translated](./translated.mdx#stable) and [section](/reference/go/1.32/).\n\n:::\n\n<a href="/img/example.png">\n<img src="/img/example.png" alt="Example" />\n</a>\n\n<SdkTabs examples={{go: 'examples/go/sample.go'}}>\n<SdkSnippet lang="rust">\n\n\`\`\`rust\nlet sample = 1;\n\`\`\`\n\n</SdkSnippet>\n<SdkSnippet lang="go">\n\n\`\`\`go\nconst sample = 1\n\`\`\`\n\n</SdkSnippet>\n</SdkTabs>\n`;
  await writeFile(join(contentPath, 'index.mdx'), homeSource);
  await writeFile(join(contentPathLocalized, 'translated.mdx'), '# Translated\n');
  await writeFile(join(contentPath, 'translated.mdx'), '# English source\n');
  await writeFile(join(contentPath, 'older.mdx'), '# Earlier version\n');
  const metadata = (source, permalink, title) => ({source, permalink, title, frontMatter: {description: 'A fixture description.'}});
  const fallback = metadata('@site/reference/index.mdx', '/zh-Hans/', 'Home');
  const translated = metadata('@site/i18n/zh-Hans/reference/translated.mdx', '/zh-Hans/translated/', 'Translated');
  const earlier = metadata('@site/reference/older.mdx', '/zh-Hans/reference/go/1.32/', 'Go reference');
  const options = {siteDir, outDir, baseUrl: '/zh-Hans/', siteConfig: {url: 'https://docs.example.test', title: 'Fixture'},
    i18n: {currentLocale: 'zh-Hans'}, allContent: {'docusaurus-plugin-content-docs': {
      primary: {loadedVersions: [{contentPath, contentPathLocalized, docs: [fallback, translated]}]},
      'reference-go': {loadedVersions: [{contentPath, contentPathLocalized, docs: [earlier]},
        {contentPath, contentPathLocalized, docs: [{...earlier, permalink: '/zh-Hans/reference/go/1.33/'}]}]},
    }}};
  await exportMarkdown(options);
  const home = await readFile(join(outDir, 'index.md'), 'utf8');
  assert.match(home, /canonical_url: "https:\/\/docs.example.test\/zh-Hans\/"/);
  assert.match(home, /source: "docs\/reference\/index.mdx"/);
  assert.match(home, /locale: "zh-Hans"/);
  assert.match(home, /<a id="stable"><\/a>\n\n## Local heading/);
  assert.match(home, /> \*\*Note: Read first\*\*/);
  assert.match(home, /https:\/\/docs.example.test\/zh-Hans\/translated.md#stable/);
  assert.match(home, /https:\/\/docs.example.test\/zh-Hans\/reference\/go\/1.32.md/);
  assert.match(home, /!\[Example\]\(https:\/\/docs.example.test\/img\/example.png\)/);
  assert.ok(home.indexOf('**Rust**') < home.indexOf('**Go**'));
  assert.match(home, /```rust\nlet sample = 1;\n```/);
  assert.match(home, /```go\nconst sample = 1\n```/);
  assert.match(home, /https:\/\/github.com\/superdurable\/dex\/tree\/main\/examples\/go\/sample.go/);
  assert.match(await readFile(join(outDir, 'reference/go/1.32.md'), 'utf8'), /# Earlier version/);
  const index = await readFile(join(outDir, 'llms.txt'), 'utf8');
  assert.equal((index.match(/^-/gm) ?? []).length, 4);
  assert.match(index, /\[Go reference\].*1.32.md\): A fixture description/);
  await writeFile(join(contentPath, 'index.mdx'), '# Heading\n\n<UnrecognizedDiagram />\n');
  await assert.rejects(exportMarkdown(options), /index.mdx:3: Unknown JSX component <UnrecognizedDiagram>/);
  await writeFile(join(contentPath, 'index.mdx'), '# Heading\n\n<DexSystemDiagram />\n');
  await assert.rejects(exportMarkdown(options), /index.mdx:3: DexSystemDiagram requires markdownDescription/);
  console.log('Markdown export integration passed: multiple instances, versioned routes, locale fallback, links, anchors, SDKs, and fail-closed JSX.');
} finally {
  await rm(fixture, {recursive: true, force: true});
}
