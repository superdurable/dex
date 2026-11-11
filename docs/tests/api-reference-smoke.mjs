// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from 'node:assert/strict';
import {readFile, readdir, stat} from 'node:fs/promises';
import {dirname, join, relative, resolve, sep} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {
  bannerClassName, latestSegment, minorOf, siteOrigin, versionMarkerName,
} from '../scripts/assemble-api-reference.mjs';

const docsRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export const apiVersions = JSON.parse(await readFile(join(docsRoot, 'api-reference-versions.json'), 'utf8'));
export const apiEntryPaths = new Set(Object.entries(apiVersions).flatMap(([language, versions]) => [
  `/api/${language}/${latestSegment}/`,
  ...versions.map((version) => `/api/${language}/${minorOf(version)}/`),
]));
const pagesLimitBytes = 1_000_000_000;

/**
 * Reports whether a path is under a minor version of a hosted SDK's API reference, current or retired.
 *
 * Archived reference pages are frozen and may link to a retired minor; the 404 page redirects those.
 *
 * @returns {boolean} True for /api/<manifest language>/<major.minor>/ and any page below it.
 */
export function isApiReferenceVersionPath(pathname) {
  const match = /^\/api\/([^/]+)\/\d+\.\d+\//.exec(pathname);
  return Boolean(match && Object.hasOwn(apiVersions, match[1]));
}

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  await auditApiReferenceIndexPages();
  await auditPythonGenerationLocks();
  await auditApiReferences(resolve(process.argv[2] ?? join(docsRoot, 'build', 'api')), process.argv.includes('--all'));
}

/**
 * Audits an assembled /api/ tree: layout, indexing policy, canonicals, banners, local links, anchors, and size.
 *
 * @returns {Promise<{totalBytes: number, bytesByVersion: object}>} The output size, in total and per version tree.
 */
export async function auditApiReferences(apiRoot, isAllRequired = false) {
  const files = await collectFiles(apiRoot);
  const outputPaths = new Set(files.map((filePath) => relative(apiRoot, filePath).split(sep).join('/')));
  await auditLayout(apiRoot, outputPaths, isAllRequired);
  const htmlFiles = files.filter((filePath) => filePath.endsWith('.html'));
  assert.ok(htmlFiles.length > 0, 'API reference output must contain HTML');
  const htmlByPath = new Map(await Promise.all(htmlFiles.map(async (filePath) => [
    `/api/${relative(apiRoot, filePath).split(sep).join('/')}`, await readFile(filePath, 'utf8'),
  ])));
  for (const [pathname, html] of htmlByPath) {
    auditPageMetadata(pathname, html);
    auditPageLinks(pathname, html, htmlByPath, outputPaths, isAllRequired);
  }
  let totalBytes = 0;
  const bytesByVersion = {};
  for (const filePath of files) {
    const byteCount = (await stat(filePath)).size;
    totalBytes += byteCount;
    const versionPath = relative(apiRoot, filePath).split(sep).slice(0, 2).join('/');
    bytesByVersion[versionPath] = (bytesByVersion[versionPath] ?? 0) + byteCount;
  }
  assert.ok(totalBytes < pagesLimitBytes, 'API output exceeds the GitHub Pages 1 GB site limit');
  console.log(JSON.stringify({apiHtmlPages: htmlFiles.length, bytesByVersion, totalBytes, pagesLimitBytes}, null, 2));
  return {totalBytes, bytesByVersion};
}

/**
 * Checks that each SDK reference index links to /latest/ first and then to exactly the manifest's versions.
 */
export async function auditApiReferenceIndexPages() {
  for (const language of [...Object.keys(apiVersions), 'rust']) {
    const source = await readFile(join(docsRoot, 'reference', language, 'index.mdx'), 'utf8');
    assert.doesNotMatch(source, /javadoc\.io/, `reference/${language}/index.mdx links to javadoc.io`);
    assert.doesNotMatch(source, /https:\/\/docs\.superdurable\.io\/api\//,
      `reference/${language}/index.mdx must use pathname:///api/ links for API pages`);
    const releases = apiVersions[language];
    if (!releases) continue;
    const links = [...source.matchAll(/\[([^\]]+)\]\(pathname:\/\/\/api\/([^/)]+)\/([^/)]+)\/\)/g)]
      .map(([, label, linkLanguage, segment]) => ({label, linkLanguage, segment}));
    assert.ok(links.every(({linkLanguage}) => linkLanguage === language), `reference/${language}/index.mdx links to another language`);
    assert.equal(links[0]?.segment, latestSegment, `reference/${language}/index.mdx must link to /api/${language}/latest/ first`);
    assert.match(source, new RegExp(`\\(pathname:///api/${language}/latest/\\)[^\\n]*${releases[0].replaceAll('.', '\\.')}`),
      `reference/${language}/index.mdx must name ${releases[0]} as the latest release`);
    assert.deepEqual(links.slice(1).map(({label, segment}) => `${label}@${segment}`),
      releases.map((version) => `${version}@${minorOf(version)}`),
      `reference/${language}/index.mdx versioned links must match api-reference-versions.json`);
  }
  console.log('API reference index pages match api-reference-versions.json.');
}

/**
 * Checks that every manifest Python SDK version, and nothing else, has a hashed generation lock.
 */
export async function auditPythonGenerationLocks() {
  const lockDirectory = join(docsRoot, 'scripts', 'api-reference-tools', 'python');
  assert.deepEqual((await readdir(lockDirectory)).sort(), apiVersions.python.map((version) => `${version}.txt`).sort(),
    'Python locks must match api-reference-versions.json; run scripts/api-reference-tools/lock-python-dependencies.sh');
  for (const version of apiVersions.python) {
    const lock = await readFile(join(lockDirectory, `${version}.txt`), 'utf8');
    const lines = lock.split('\n');
    const requirementIndexes = lines.flatMap((line, index) => /^[A-Za-z0-9]/.test(line) ? [index] : []);
    assert.ok(requirementIndexes.some((index) => lines[index] === `dex-python-sdk==${version} \\`),
      `python/${version}.txt must pin dex-python-sdk ${version}`);
    for (const index of requirementIndexes) {
      assert.match(lines[index], /^[A-Za-z0-9._-]+==\S+ \\$/, `python/${version}.txt: unpinned requirement ${lines[index]}`);
      assert.match(lines[index + 1] ?? '', /^ {4}--hash=sha256:[0-9a-f]{64}/, `python/${version}.txt: unhashed requirement ${lines[index]}`);
    }
  }
}

async function auditLayout(apiRoot, outputPaths, isAllRequired) {
  for (const language of await readdir(apiRoot)) {
    assert.ok(apiVersions[language], `Unexpected API language directory: ${language}`);
    const minors = apiVersions[language].map(minorOf);
    for (const segment of await readdir(join(apiRoot, language))) {
      assert.ok(segment === latestSegment || minors.includes(segment), `Retired or unknown API version directory: ${language}/${segment}`);
    }
    const latestMinor = minors[0];
    if (outputPaths.has(`${language}/${latestMinor}/index.html`) || isAllRequired) {
      const filesUnder = (prefix) => [...outputPaths].filter((path) => path.startsWith(prefix))
        .map((path) => path.slice(prefix.length)).sort();
      const latestFiles = filesUnder(`${language}/${latestSegment}/`);
      assert.ok(latestFiles.includes('index.html'), `Missing /api/${language}/latest/`);
      assert.deepEqual(latestFiles, filesUnder(`${language}/${latestMinor}/`),
        `/api/${language}/latest/ must copy /api/${language}/${latestMinor}/`);
    }
  }
  if (isAllRequired) {
    for (const entryPath of apiEntryPaths) {
      assert.ok(outputPaths.has(`${entryPath.slice('/api/'.length)}index.html`), `Missing API version ${entryPath}`);
    }
  }
}

function auditPageMetadata(pathname, html) {
  const [, , language, segment] = pathname.split('/');
  const isLatestTree = segment === latestSegment;
  const version = isLatestTree ? apiVersions[language][0] : apiVersions[language].find((release) => minorOf(release) === segment);
  assert.ok(version, `Unexpected API version: ${pathname}`);
  assert.ok(html.includes(`<meta name="${versionMarkerName}" content="${language}@${version}">`), `${pathname}: wrong version marker`);
  const robots = [...html.matchAll(/<meta\b[^>]*name=["']robots["'][^>]*content=["']([^"']+)["']/gi)].map((match) => match[1]);
  assert.deepEqual(robots, [isLatestTree ? 'index,follow' : 'noindex,follow'], `${pathname}: wrong indexing policy`);
  const canonicals = [...html.matchAll(/<link\b[^>]*rel=["']canonical["'][^>]*href=["']([^"']+)["']/gi)].map((match) => match[1]);
  assert.deepEqual(canonicals, [`${siteOrigin}${pathname.replace(/\/index\.html$/, '/')}`], `${pathname}: canonical must reference itself`);
  assert.equal(html.split(`class="${bannerClassName}"`).length - 1, 1, `${pathname}: needs exactly one version banner`);
  assert.doesNotMatch(html, /hreflang=|name="generator" content="Docusaurus/i);
}

function auditPageLinks(pathname, html, htmlByPath, outputPaths, isAllRequired) {
  const [, , language] = pathname.split('/');
  for (const match of html.matchAll(/<(?:a|link|script|img)\b[^>]*\b(?:href|src)=["']([^"']+)["']/g)) {
    const href = match[1].replaceAll('&amp;', '&');
    const target = new URL(href, `${siteOrigin}${pathname}`);
    if (target.origin !== siteOrigin || !['http:', 'https:'].includes(target.protocol)) continue;
    if (target.pathname === `/reference/${language}/`) continue;
    if (target.pathname === '/reference/server/configuration' && target.hash === '#history-offload') continue;
    if (target.pathname === '/reference/server/changelog' && !target.hash) continue;
    assert.ok(target.pathname.startsWith('/api/'), `${pathname}: unexpected local target ${href}`);
    const targetPath = decodeURIComponent(target.pathname).replace(/\/$/, '/index.html');
    const outputPath = targetPath.slice('/api/'.length);
    if (!outputPaths.has(outputPath) && !isAllRequired && apiEntryPaths.has(target.pathname)) continue;
    assert.ok(outputPaths.has(outputPath), `${pathname}: missing API file ${href}`);
    if (target.hash && htmlByPath.has(targetPath)) {
      const rawAnchor = target.hash.slice(1);
      const decodedAnchor = decodeURIComponent(rawAnchor);
      const targetHtml = htmlByPath.get(targetPath);
      const hasAnchor = [rawAnchor, decodedAnchor, escapeHtmlAttribute(decodedAnchor)]
        .some((anchor) => targetHtml.includes(`id="${anchor}"`) || targetHtml.includes(`name="${anchor}"`));
      assert.ok(hasAnchor, `${pathname}: missing anchor ${href}`);
    }
  }
}

function escapeHtmlAttribute(value) {
  return value.replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;').replaceAll('>', '&gt;');
}

async function collectFiles(directory) {
  const files = [];
  for (const entry of await readdir(directory, {withFileTypes: true})) {
    const entryPath = join(directory, entry.name);
    files.push(...(entry.isDirectory() ? await collectFiles(entryPath) : [entryPath]));
  }
  return files;
}
