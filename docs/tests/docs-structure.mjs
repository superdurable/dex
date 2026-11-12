// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// Structure rules the docs build does not check on its own:
// 1. Docs pages never link to a README in the repository. User-facing content lives in the docs;
//    READMEs keep developer notes and link to the docs.
// 2. Every sidebar group header opens the group's overview page. That page is not listed again
//    under the header, and no item is labeled Overview.
// 3. A page's closing list of links is one section named Next (下一步 in Chinese), and it comes last.
// 4. The home page and Get Started link straight to the page that does each job: no ordered reading lists.
// 5. A group's overview page has the group's name as its title, so the header and the page agree.

import {readdirSync, readFileSync, statSync} from 'node:fs';
import {join, relative} from 'node:path';
import {fileURLToPath} from 'node:url';

const docsRoot = fileURLToPath(new URL('..', import.meta.url));
const contentRoots = [
  'content',
  'i18n/zh-Hans/docusaurus-plugin-content-docs/current',
  'reference',
];
// A link to a README file: a path ending in README.md, or a README path followed by an anchor or the link's end.
const readmeLink = /README\.md|\/README(?=[#)\s'"`])/i;
// Reference links into READMEs that still hold a maintained table. Move each table into the docs, then remove it here.
const pendingReadmeLinks = new Set([
  'reference/agents/install.mdx:agents/README.md#versions-and-compatibility',
  'reference/agents/tracing.mdx:agents/python/core/README.md#tracing',
  'reference/agents/tracing.mdx:agents/typescript/core/README.md#tracing',
  'reference/server/metrics.mdx:server-zd/README.md#metrics',
]);
// Snapshots of older reference versions are immutable history.
const versionedReferenceDir = /^reference\/[^/]+\/\d+\.\d+\.\d+(\/|$)/;

function mdxFiles(dir) {
  const files = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    const rel = relative(docsRoot, path);
    if (versionedReferenceDir.test(rel) || rel.startsWith('reference/sidebars')) continue;
    if (statSync(path).isDirectory()) files.push(...mdxFiles(path));
    else if (/\.mdx?$/.test(name)) files.push(path);
  }
  return files;
}

const failures = [];

for (const root of contentRoots) {
  for (const file of mdxFiles(join(docsRoot, root))) {
    const rel = relative(docsRoot, file);
    readFileSync(file, 'utf8').split('\n').forEach((line, index) => {
      if (!readmeLink.test(line)) return;
      const pending = [...pendingReadmeLinks].some((entry) => {
        const [path, target] = entry.split(':');
        return path === rel && line.includes(target);
      });
      if (!pending) {
        failures.push(`${rel}:${index + 1}: links to a repository README. Link a docs page, or an example's folder.`);
      }
    });
  }
}

// Section names that used to close pages. They are all Next now.
const oldClosingNames = /^## (Related|Related pages|Related evidence|Next steps|Where to go next|What's next|See also|接下来|相关|相关页面|相关内容|相关证据)( \{#[^}]+\})?\s*$/;
const nextHeading = /^## (Next|下一步)( \{#[^}]+\})?\s*$/;
for (const root of contentRoots.filter((item) => item !== 'reference')) {
  for (const file of mdxFiles(join(docsRoot, root))) {
    const rel = relative(docsRoot, file);
    const headings = [];
    let fence;
    readFileSync(file, 'utf8').split('\n').forEach((line, index) => {
      const fenceMatch = line.match(/^\s*(`{3,}|~{3,})/);
      if (fenceMatch) {
        if (!fence) fence = fenceMatch[1][0];
        else if (fenceMatch[1][0] === fence) fence = undefined;
        return;
      }
      if (fence || !line.startsWith('## ')) return;
      headings.push({line, number: index + 1});
      if (oldClosingNames.test(line)) {
        failures.push(`${rel}:${index + 1}: "${line.slice(3)}" closes a page. Name the closing section Next (下一步 in Chinese).`);
      }
    });
    const next = headings.findIndex(({line}) => nextHeading.test(line));
    if (next !== -1 && next !== headings.length - 1) {
      failures.push(`${rel}:${headings[next].number}: the Next section must be the page's last section.`);
    }
  }
}

// Ordered lists whose items start with a link are reading lists.
const routerPages = ['index.mdx', 'intro/get-started.mdx'];
for (const root of contentRoots.filter((item) => item !== 'reference')) {
  for (const page of routerPages) {
    const file = join(docsRoot, root, page);
    let source;
    try {
      source = readFileSync(file, 'utf8');
    } catch {
      continue;
    }
    source.split('\n').forEach((line, index) => {
      if (/^\s*\d+\.\s+\[/.test(line)) {
        failures.push(`${relative(docsRoot, file)}:${index + 1}: an ordered list of links. Link the one page that does the job; let its Next section give the order.`);
      }
    });
  }
}

const {default: sidebars} = await import(new URL('../sidebars.ts', import.meta.url));

function docId(item) {
  if (typeof item === 'string') return item;
  if (item && typeof item === 'object' && item.type === 'doc') return item.id;
  return undefined;
}

function checkCategories(items, trail) {
  for (const item of items) {
    if (item && typeof item === 'object' && item.label === 'Overview') {
      failures.push(`sidebars.ts: "${[...trail, 'Overview'].join(' > ')}" is labeled Overview. The group header opens the overview; keep each item's own title.`);
    }
    if (typeof item !== 'object' || item.type !== 'category') continue;
    const where = [...trail, item.label].join(' > ');
    if (!item.link || item.link.type !== 'doc') {
      failures.push(`sidebars.ts: "${where}" has no overview. Give the group header link: {type: 'doc', id: <its overview page>}.`);
    } else if ((item.items ?? []).some((child) => docId(child) === item.link.id)) {
      failures.push(`sidebars.ts: "${where}" lists its overview page (${item.link.id}) again under the header.`);
    }
    checkCategories(item.items ?? [], [...trail, item.label]);
  }
}

for (const [name, items] of Object.entries(sidebars)) checkCategories(items, [name]);

// Maps each doc ID to its title, read from the English source the way Docusaurus derives IDs.
function docTitles(root) {
  const titles = new Map();
  for (const file of mdxFiles(join(docsRoot, root))) {
    const rel = relative(join(docsRoot, root), file).split('\\').join('/');
    const source = readFileSync(file, 'utf8');
    const frontMatter = source.match(/^---\n([\s\S]*?)\n---/)?.[1] ?? '';
    const field = (key) => frontMatter.match(new RegExp(`^${key}:\\s*(.+)$`, 'm'))?.[1].trim().replace(/^["']|["']$/g, '');
    const dir = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/') + 1) : '';
    const id = `${dir}${field('id') ?? rel.slice(dir.length).replace(/\.mdx?$/, '')}`;
    const title = field('title') ?? source.match(/^# (.+)$/m)?.[1].trim();
    if (title) titles.set(id, title);
  }
  return titles;
}

const englishTitles = docTitles('content');
function checkOverviewTitles(items) {
  for (const item of items) {
    if (typeof item !== 'object' || item.type !== 'category') continue;
    if (item.link?.type === 'doc') {
      const title = englishTitles.get(item.link.id);
      if (title && title !== item.label) {
        failures.push(`sidebars.ts: the "${item.label}" group opens ${item.link.id}, titled "${title}". Title the overview page "${item.label}".`);
      }
    }
    checkOverviewTitles(item.items ?? []);
  }
}
for (const items of Object.values(sidebars)) checkOverviewTitles(items);

if (failures.length > 0) {
  console.error(failures.join('\n'));
  process.exit(1);
}
console.log('Docs structure check passed: no README links, every sidebar group header opens its overview, pages close with Next, the home page and Get Started have no reading lists, and each overview page carries the name of its group.');
