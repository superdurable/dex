// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import assert from 'node:assert/strict';
import {readFile, readdir, stat} from 'node:fs/promises';
import {dirname, join, relative, resolve, sep} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';

const docsRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export const apiVersions = JSON.parse(await readFile(join(docsRoot, 'api-reference-versions.json'), 'utf8'));
const siteOrigin = 'https://docs.superdurable.io';
export const apiEntryPaths = new Set(Object.entries(apiVersions).flatMap(([language, versions]) =>
  versions.map((version) => `/api/${language}/${version.split('.').slice(0, 2).join('.')}/`)));

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  await auditApiReferences(resolve(process.argv[2] ?? join(docsRoot, '.cache', 'api-reference')), process.argv.includes('--all'));
}

export async function auditApiReferences(apiRoot, requireAll = false) {
  const files = await collectFiles(apiRoot);
  const outputPaths = new Set(files.map((filePath) => relative(apiRoot, filePath).split(sep).join('/')));
  const htmlFiles = files.filter((filePath) => filePath.endsWith('.html'));
  assert.ok(htmlFiles.length > 0, 'API reference output must contain HTML');
  const htmlByPath = new Map(await Promise.all(htmlFiles.map(async (filePath) => [
    `/api/${relative(apiRoot, filePath).split(sep).join('/')}`, await readFile(filePath, 'utf8'),
  ])));
  for (const [pathname, html] of htmlByPath) {
    const [, , language, minor] = pathname.split('/');
    const version = apiVersions[language]?.find((release) => release.startsWith(`${minor}.`));
    assert.ok(version, `Unexpected API version: ${pathname}`);
    assert.match(html, new RegExp(`<meta name="dex-api-reference" content="${language}@${version.replaceAll('.', '\\.')}"`));
    const expectedRobots = version === apiVersions[language][0] ? 'index,follow' : 'noindex,follow';
    assert.ok(html.includes(`<meta name="robots" content="${expectedRobots}">`), `${pathname}: wrong indexing policy`);
    const canonicals = [...html.matchAll(/<link\b[^>]*rel="canonical"[^>]*href="([^"]+)"/g)].map((match) => match[1]);
    assert.deepEqual(canonicals, [`${siteOrigin}${pathname.replace(/index\.html$/, '')}`], `${pathname}: wrong canonical`);
    assert.doesNotMatch(html, /hreflang=|name="generator" content="Docusaurus/i);
    for (const match of html.matchAll(/<(?:a|link|script|img)\b[^>]*\b(?:href|src)=["']([^"']+)["']/g)) {
      const href = match[1].replaceAll('&amp;', '&');
      const target = new URL(href, `${siteOrigin}${pathname}`);
      if (target.origin !== siteOrigin || !['http:', 'https:'].includes(target.protocol)) continue;
      if (target.pathname === `/reference/${language}/`) continue;
      assert.ok(target.pathname.startsWith('/api/'), `${pathname}: unexpected local target ${href}`);
      const targetPath = decodeURIComponent(target.pathname).replace(/\/$/, '/index.html');
      const outputPath = targetPath.slice('/api/'.length);
      if (!outputPaths.has(outputPath) && !requireAll && apiEntryPaths.has(target.pathname)) continue;
      assert.ok(outputPaths.has(outputPath), `${pathname}: missing API file ${href}`);
      if (target.hash && htmlByPath.has(targetPath)) {
        const anchor = decodeURIComponent(target.hash.slice(1)).replaceAll('&', '&amp;').replaceAll('"', '&quot;');
        const targetHtml = htmlByPath.get(targetPath);
        assert.ok(targetHtml.includes(`id="${anchor}"`) || targetHtml.includes(`name="${anchor}"`), `${pathname}: missing anchor ${href}`);
      }
    }
  }
  if (requireAll) {
    for (const entryPath of apiEntryPaths) {
      assert.ok(outputPaths.has(`${entryPath.slice('/api/'.length)}index.html`), `Missing API version ${entryPath}`);
    }
  }
  let totalBytes = 0;
  const bytesByVersion = {};
  for (const filePath of files) {
    const byteCount = (await stat(filePath)).size;
    totalBytes += byteCount;
    const versionPath = relative(apiRoot, filePath).split(sep).slice(0, 2).join('/');
    bytesByVersion[versionPath] = (bytesByVersion[versionPath] ?? 0) + byteCount;
  }
  assert.ok(totalBytes < 1_000_000_000, 'API output exceeds the GitHub Pages 1 GB site limit');
  console.log(JSON.stringify({apiHtmlPages: htmlFiles.length, bytesByVersion, totalBytes, pagesLimitBytes: 1_000_000_000}, null, 2));
  return {totalBytes, bytesByVersion};
}

async function collectFiles(directory) {
  const files = [];
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const entryPath = join(directory, entry.name);
    files.push(...(entry.isDirectory() ? await collectFiles(entryPath) : [entryPath]));
  }
  return files;
}
