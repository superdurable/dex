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

if (failures.length > 0) {
  console.error(failures.join('\n'));
  process.exit(1);
}
console.log('Docs structure check passed: no README links, and every sidebar group header opens its overview.');
