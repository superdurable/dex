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
// 6. Every sidebar page that is not a group overview, the home page, Get Started, or the Glossary ends with Next.
// 7. Rules 2 and 5 also hold in each Developer reference component's sidebar. Its index page comes first, and the
//    shared sidebar hides it because the Developer Reference header already opens it.

import {existsSync, readdirSync, readFileSync, statSync} from 'node:fs';
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

// In Chinese, every sidebar group label is translated (i18n/zh-Hans/.../current.json), and its overview page
// carries that same title.
const chineseRoot = 'i18n/zh-Hans/docusaurus-plugin-content-docs/current';
const chineseLabels = JSON.parse(readFileSync(join(docsRoot, `${chineseRoot}.json`), 'utf8'));
const chineseTitles = docTitles(chineseRoot);
function checkChineseCategories(items, sidebarName) {
  for (const item of items) {
    if (typeof item !== 'object' || item.type !== 'category') continue;
    const label = chineseLabels[`sidebar.${sidebarName}.category.${item.label}`]?.message;
    if (!label || !/[一-鿿]/.test(label)) {
      failures.push(`${chineseRoot}.json: the "${item.label}" group has no Chinese label. Run npx docusaurus write-translations --locale zh-Hans and translate it.`);
    } else if (item.link?.type === 'doc') {
      const title = chineseTitles.get(item.link.id);
      if (title && title !== label) {
        failures.push(`${chineseRoot}: the "${label}" group opens ${item.link.id}, titled "${title}". Title the Chinese overview page "${label}".`);
      }
    }
    checkChineseCategories(item.items ?? [], sidebarName);
  }
}
for (const [name, items] of Object.entries(sidebars)) checkChineseCategories(items, name);

// The same rules hold in each Developer reference component's own sidebar.
for (const name of readdirSync(join(docsRoot, 'reference/sidebars')).filter((file) => file.endsWith('.ts'))) {
  const component = name.replace(/\.ts$/, '');
  const where = `reference/sidebars/${name}`;
  const {default: componentSidebars} = await import(new URL(`../reference/sidebars/${name}`, import.meta.url));
  const titles = docTitles(`reference/${component}`);
  const labels = new Map();
  for (const file of mdxFiles(join(docsRoot, 'reference', component))) {
    const rel = relative(join(docsRoot, 'reference', component), file).split('\\').join('/');
    const frontMatter = readFileSync(file, 'utf8').match(/^---\n([\s\S]*?)\n---/)?.[1] ?? '';
    const idField = frontMatter.match(/^id:\s*(.+)$/m)?.[1].trim();
    const dir = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/') + 1) : '';
    const id = `${dir}${idField ?? rel.slice(dir.length).replace(/\.mdx?$/, '')}`;
    const label = frontMatter.match(/^sidebar_label:\s*(.+)$/m)?.[1].trim().replace(/^["']|["']$/g, '');
    if (label) labels.set(id, label);
  }
  const check = (items, trail) => {
    for (const item of items) {
      const id = docId(item);
      if ((id && labels.get(id) === 'Overview') || (item && typeof item === 'object' && item.label === 'Overview')) {
        failures.push(`${where}: "${[...trail, id ?? item.label].join(' > ')}" is labeled Overview. Label it with the page's own title.`);
      }
      if (typeof item !== 'object' || item.type !== 'category') continue;
      const path = [...trail, item.label].join(' > ');
      if (!item.link || item.link.type !== 'doc') {
        failures.push(`${where}: "${path}" has no overview. Give the group header link: {type: 'doc', id: <its overview page>}.`);
      } else {
        if ((item.items ?? []).some((child) => docId(child) === item.link.id)) {
          failures.push(`${where}: "${path}" lists its overview page (${item.link.id}) again under the header.`);
        }
        const title = titles.get(item.link.id);
        if (title && title !== item.label) {
          failures.push(`${where}: the "${item.label}" group opens ${item.link.id}, titled "${title}". Title the overview page "${item.label}".`);
        }
      }
      check(item.items ?? [], [...trail, item.label]);
    }
  };
  for (const [sidebarName, items] of Object.entries(componentSidebars)) {
    // The component's index page stays first so it keeps a sidebar; the shared sidebar hides it, because the
    // Developer Reference header already opens it (site-smoke checks the rendered sidebar).
    const indexAt = items.findIndex((item) => docId(item) === 'index');
    if (indexAt > 1 || (indexAt === 1 && !(items[0]?.type === 'link'))) {
      failures.push(`${where}: index must be the first page, right after the back link, so the shared sidebar can hide it.`);
    }
    check(items, [component, sidebarName]);
  }
}


// Every page in the reading sequence ends with Next, so the order comes from one place.
const routerDocs = new Set(['index', 'intro/get-started', 'glossary']);
const sequenceDocs = [];
function collectSequence(items) {
  for (const item of items) {
    const id = docId(item);
    if (id) sequenceDocs.push(id);
    if (item && typeof item === 'object' && item.type === 'category') collectSequence(item.items ?? []);
  }
}
collectSequence(sidebars.docs);
const docFiles = new Map();
for (const root of ['content', 'i18n/zh-Hans/docusaurus-plugin-content-docs/current']) {
  for (const file of mdxFiles(join(docsRoot, root))) {
    const rel = relative(join(docsRoot, root), file).split('\\').join('/');
    const source = readFileSync(file, 'utf8');
    const frontMatter = source.match(/^---\n([\s\S]*?)\n---/)?.[1] ?? '';
    const idField = frontMatter.match(/^id:\s*(.+)$/m)?.[1].trim();
    const dir = rel.includes('/') ? rel.slice(0, rel.lastIndexOf('/') + 1) : '';
    const id = `${dir}${idField ?? rel.slice(dir.length).replace(/\.mdx?$/, '')}`;
    docFiles.set(`${root}:${id}`, {file, source});
  }
}
for (const id of sequenceDocs) {
  if (routerDocs.has(id)) continue;
  for (const root of ['content', 'i18n/zh-Hans/docusaurus-plugin-content-docs/current']) {
    const doc = docFiles.get(`${root}:${id}`);
    if (!doc) continue;
    const withoutCode = doc.source.replace(/^([ \t]*)(`{3,}|~{3,})[\s\S]*?^\1\2[ \t]*$/gm, '');
    if (!/^## (Next|下一步)(\s|$)/m.test(withoutCode)) {
      failures.push(`${relative(docsRoot, doc.file)}: ends without a Next section. Add "## Next" (下一步) with the one or two pages a reader opens next.`);
    }
  }
}

// The compatibility matrix is generated in every locale; a release that leaves one behind fails here.
{
  const versions = JSON.parse(readFileSync(join(docsRoot, 'reference/versions.json'), 'utf8'));
  const sdkLine = versions.go.replace(/\.x$/, '');
  const cliLine = versions.cli.replace(/\.x$/, '');
  const matrixPages = [
    {path: 'content/reference/index.mdx', start: '## Compatibility matrix', end: '## Release notes'},
    {path: 'i18n/zh-Hans/docusaurus-plugin-content-docs/current/reference/index.mdx', start: '## 兼容性矩阵', end: '## 发版说明'},
  ];
  for (const {path, start, end} of matrixPages) {
    const page = readFileSync(join(docsRoot, path), 'utf8');
    if (page.indexOf(start) === -1 || page.indexOf(end) === -1) {
      failures.push(`${path}: the compatibility matrix section (${start} … ${end}) is missing.`);
      continue;
    }
    const matrix = page.slice(page.indexOf(start), page.indexOf(end));
    if (!new RegExp(`SDK[^|]*\\| ${sdkLine.replace('.', '\\.')}[,| ]`).test(matrix) || !new RegExp(`^\\| ${cliLine.replace('.', '\\.')} \\|`, 'm').test(matrix)) {
      failures.push(`${path}: the compatibility matrix does not list SDK ${sdkLine} and dexcli ${cliLine}. Run node scripts/generate-reference-compatibility.mjs.`);
    }
  }
}

// Every SDK version pin in the guides and the current reference names the current SDK line, so a
// reader's install matches the matrix. Changelogs and the snapshots of older lines keep their history.
{
  const versions = JSON.parse(readFileSync(join(docsRoot, 'reference/versions.json'), 'utf8'));
  const sdkLine = versions.go.replace(/\.x$/, '');
  // pip ==, npm @, Go @v, cargo add @= or @, Cargo.toml "=X.Y.Z" or "X.Y.Z", the Maven/Gradle coordinate,
  // and a Maven <version> right after the dex-sdk artifactId.
  const pinPattern = /(?:dex-python-sdk==|sdk-go@v|@superdurable\/dex@|cargo add dex-sdk@=?|dex-sdk = "=?|io\.superdurable:dex-sdk:)(\d+\.\d+)\.\d+/g;
  const mavenVersion = /<version>(\d+\.\d+)\.\d+<\/version>/;
  // Lines that name an older SDK on purpose. Each entry needs its reason.
  const historicalPins = [
    // {file: 'reference/<sdk>/<page>.mdx', text: '<the pin>', reason: '<why this page names an older release>'},
  ];
  const allowed = (file, line) => historicalPins.some((entry) => file === entry.file && line.includes(entry.text));
  const roots = ['content', 'i18n/zh-Hans/docusaurus-plugin-content-docs/current'];
  for (const component of ['go', 'python', 'java', 'typescript', 'rust']) {
    roots.push(`reference/${component}`);
    const latestSnapshot = `reference-${component}_versioned_docs/version-${versions[component]}`;
    if (existsSync(join(docsRoot, latestSnapshot))) roots.push(latestSnapshot);
  }
  for (const root of roots) {
    for (const file of mdxFiles(join(docsRoot, root))) {
      const path = relative(docsRoot, file);
      if (/(^|\/)changelog\.mdx?$/.test(path)) continue;
      const lines = readFileSync(file, 'utf8').split('\n');
      lines.forEach((line, index) => {
        if (allowed(path, line)) return;
        const found = [...line.matchAll(pinPattern)].map(([pin, minor]) => [pin, minor]);
        const maven = line.match(mavenVersion);
        if (maven && /<artifactId>dex-sdk<\/artifactId>/.test(lines.slice(Math.max(0, index - 2), index).join('\n'))) {
          found.push([maven[0], maven[1]]);
        }
        for (const [pin, minor] of found) {
          if (minor !== sdkLine) {
            failures.push(`${path}:${index + 1}: ${pin} is not the current SDK line ${sdkLine} (reference/versions.json). Pin ${sdkLine}.x, or run node scripts/generate-reference-install.mjs for the reference landing pages.`);
          }
        }
      });
    }
  }
}

// Version notes name a full release, "Since 1.45.0" or "Changed in 1.45.0", so readers compare them
// with the version they run in one form.
{
  const roots = ['content', 'reference', 'i18n/zh-Hans/docusaurus-plugin-content-docs/current'];
  for (const root of roots) {
    if (!existsSync(join(docsRoot, root))) continue;
    for (const file of mdxFiles(join(docsRoot, root))) {
      const path = relative(docsRoot, file);
      if (/(^|\/)changelog\.mdx?$/.test(path)) continue;
      readFileSync(file, 'utf8').split('\n').forEach((line, index) => {
        for (const [note] of line.matchAll(/\b(?:Since|Changed in) \d+\.\d+(?![.\d])/g)) {
          failures.push(`${path}:${index + 1}: write "${note}.0", the full release, in version notes.`);
        }
      });
    }
  }
}

// The Dex Server image tag names the server the current dexcli bundles, and the Dex Web module pin
// names the current Dex Web line, so a copied manifest or install works with the current SDKs.
{
  const versions = JSON.parse(readFileSync(join(docsRoot, 'reference/versions.json'), 'utf8'));
  const webLine = versions.web.replace(/\.x$/, '');
  const roots = ['content', 'i18n/zh-Hans/docusaurus-plugin-content-docs/current', 'reference/server', 'reference/web', 'reference/cli'];
  for (const root of roots) {
    if (!existsSync(join(docsRoot, root))) continue;
    for (const file of mdxFiles(join(docsRoot, root))) {
      const path = relative(docsRoot, file);
      if (/(^|\/)changelog\.mdx?$/.test(path)) continue;
      readFileSync(file, 'utf8').split('\n').forEach((line, index) => {
        for (const [image, version] of line.matchAll(/dex-server-zd:(\d+\.\d+\.\d+)/g)) {
          if (version !== versions.server) {
            failures.push(`${path}:${index + 1}: ${image} is not the current server ${versions.server} (reference/versions.json). Run node scripts/generate-reference-install.mjs.`);
          }
        }
        for (const [pin, minor] of line.matchAll(/web\/embedded@v(\d+\.\d+)\.\d+/g)) {
          if (minor !== webLine) {
            failures.push(`${path}:${index + 1}: ${pin} is not the current Dex Web line ${webLine} (reference/versions.json). Run node scripts/generate-reference-install.mjs.`);
          }
        }
      });
    }
  }
}

// Section headings are statements, not questions ("Start here", not "First time here?").
for (const root of ['content', 'i18n/zh-Hans/docusaurus-plugin-content-docs/current']) {
  for (const file of mdxFiles(join(docsRoot, root))) {
    let fence;
    readFileSync(file, 'utf8').split('\n').forEach((line, index) => {
      const fenceMatch = line.match(/^\s*(`{3,}|~{3,})/);
      if (fenceMatch) fence = fence ? (fenceMatch[1][0] === fence ? undefined : fence) : fenceMatch[1][0];
      else if (!fence && /^#{2,6} .*[?？]\s*(\{#[^}]*\})?\s*$/.test(line)) {
        failures.push(`${relative(docsRoot, file)}:${index + 1}: "${line}" is a question. Write the heading as a statement.`);
      }
    });
  }
}

if (failures.length > 0) {
  console.error(failures.join('\n'));
  process.exit(1);
}
console.log('Docs structure check passed: no README links, every sidebar group header opens its overview, pages close with Next, the home page and Get Started have no reading lists, each overview page carries the name of its group, and SDK, server image, and Dex Web pins match the current lines.');
