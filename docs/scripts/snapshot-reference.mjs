// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {spawnSync} from 'node:child_process';
import {readFile, readdir, rm, writeFile} from 'node:fs/promises';
import {basename, dirname, extname, join, relative} from 'node:path';
import {fileURLToPath} from 'node:url';
import {GlobExcludeDefault, Globby} from '@docusaurus/utils';
import matter from 'gray-matter';
import slugModule from '@docusaurus/plugin-content-docs/lib/slug.js';
import {DefaultNumberPrefixParser, stripPathNumberPrefixes} from '@docusaurus/plugin-content-docs/lib/numberPrefix.js';

const getSlug = slugModule.default;
const docsDirectory = fileURLToPath(new URL('../', import.meta.url));
const retainedVersionCount = 5;

export function referenceVersion(component, releaseVersion) {
  const release = releaseVersion.replace(/^v/, '');
  if (component === 'server' && /^0\.1\.\d+$/.test(release)) {
    return release;
  }
  const match = release.match(/^([1-9]\d*)\.(\d+)\.(?:\d+|x)$/);
  if (!match) {
    throw new Error(`Invalid ${component} reference version: ${releaseVersion}`);
  }
  return `${match[1]}.${match[2]}.x`;
}

export function compareVersions(first, second) {
  const firstParts = first.split('.').map((part) => part === 'x' ? 0 : Number(part));
  const secondParts = second.split('.').map((part) => part === 'x' ? 0 : Number(part));
  for (let index = 0; index < 3; index += 1) {
    if (firstParts[index] !== secondParts[index]) {
      return firstParts[index] - secondParts[index];
    }
  }
  return 0;
}

export async function snapshotReference(component, releaseVersion, {
  siteDirectory = docsDirectory,
  createSnapshot = runDocusaurusVersion,
} = {}) {
  const sharedVersionsPath = join(siteDirectory, 'reference/versions.json');
  const latestVersions = JSON.parse(await readFile(sharedVersionsPath, 'utf8'));
  if (!Object.hasOwn(latestVersions, component)) {
    throw new Error(`Unknown reference component: ${component}`);
  }
  const version = referenceVersion(component, releaseVersion);
  const latestVersion = latestVersions[component];
  const versionOrder = compareVersions(version, latestVersion);
  if (versionOrder < 0) {
    throw new Error(`Cannot replace latest ${latestVersion} with older ${version}`);
  }
  const versionsPath = join(siteDirectory, `reference-${component}_versions.json`);
  let previousVersions = await readOptionalVersions(versionsPath);
  if (versionOrder > 0 && !previousVersions.includes(latestVersion)) {
    throw new Error(`Cannot snapshot ${component} ${version}: the hidden snapshot of the latest ${latestVersion} is missing. `
      + `Create it first from the ${latestVersion} reference content with `
      + `"node scripts/snapshot-reference.mjs ${component} ${latestVersion}" (see docs/README.md).`);
  }
  if (previousVersions.includes(version)) {
    if (versionOrder > 0) {
      throw new Error(`Snapshot ${component} ${version} exists but is newer than the latest ${latestVersion}`);
    }
    // A patch release refreshes the hidden latest snapshot with the current content.
    await removeSnapshot(siteDirectory, component, version);
    previousVersions = previousVersions.filter((previousVersion) => previousVersion !== version);
    await writeJSON(versionsPath, previousVersions);
  }
  await createSnapshot(siteDirectory, component, version);
  const allVersions = [...new Set([version, ...previousVersions])]
    .sort((first, second) => compareVersions(second, first));
  const retainedVersions = allVersions.slice(0, retainedVersionCount);
  const droppedVersions = allVersions.slice(retainedVersionCount);
  const latestDocuments = await readReferenceDocuments(join(siteDirectory, 'reference', component));
  const replacements = new Map();
  for (const droppedVersion of droppedVersions) {
    const droppedDocuments = await readReferenceDocuments(snapshotDirectory(siteDirectory, component, droppedVersion));
    for (const redirect of droppedVersionRedirects(component, droppedVersion, droppedDocuments, latestDocuments)) {
      replacements.set(redirect.from, redirect.to);
    }
  }
  const redirectsPath = join(siteDirectory, 'redirects.json');
  const redirects = JSON.parse(await readFile(redirectsPath, 'utf8'));
  const nextRedirects = retargetRedirects(redirects, replacements);
  await writeJSON(redirectsPath, nextRedirects);
  await writeJSON(versionsPath, retainedVersions);
  await writeJSON(sharedVersionsPath, {...latestVersions, [component]: version});
  for (const droppedVersion of droppedVersions) {
    await removeSnapshot(siteDirectory, component, droppedVersion);
  }
  return {component, version, retainedVersions, droppedVersions};
}

/**
 * Rewrites links to a moved page in every retained reference snapshot.
 *
 * @returns {Promise<string[]>} The changed snapshot files, relative to the site directory.
 */
export async function rewriteSnapshotLinks(fromPath, toPath, {siteDirectory = docsDirectory} = {}) {
  for (const path of [fromPath, toPath]) {
    if (!/^\/[^\s#?]*$/.test(path)) {
      throw new Error(`Link rewrites need site paths without anchors or queries: ${path}`);
    }
  }
  const fromRoute = fromPath.replace(/\/+$/, '');
  if (!fromRoute) {
    throw new Error('Cannot rewrite links to the site root');
  }
  const toRoute = toPath.replace(/\/+$/, '');
  // Markdown destinations may be wrapped in angle brackets: [a](</path>) or [ref]: </path>.
  const linkPattern = new RegExp(
    `(?<=^|[\\s("'\\[=]|\\(<|\\]:[ \\t]*<)${escapeRegExp(fromRoute)}(/?)(?=$|[\\s)"'\\]#?>])`, 'gm');
  const changedFiles = [];
  for (const entry of await readdir(siteDirectory, {withFileTypes: true})) {
    if (!entry.isDirectory() || !/^reference-[a-z]+_versioned_(?:docs|sidebars)$/.test(entry.name)) {
      continue;
    }
    for (const source of await Globby(['**/*.{md,mdx,json}'], {cwd: join(siteDirectory, entry.name)})) {
      const path = join(siteDirectory, entry.name, source);
      const content = await readFile(path, 'utf8');
      const rewritten = content.replace(linkPattern, (_match, trailingSlash) => `${toRoute}${trailingSlash || (toRoute ? '' : '/')}`);
      if (rewritten !== content) {
        await writeFile(path, rewritten);
        changedFiles.push(relative(siteDirectory, path).split('\\').join('/'));
      }
    }
  }
  return changedFiles.sort();
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
}

export function droppedVersionRedirects(component, version, droppedDocuments, latestDocuments) {
  const latestById = new Map(latestDocuments.map((document) => [document.id, document]));
  const redirects = [];
  for (const localePrefix of ['', '/zh-Hans']) {
    const componentRoute = `${localePrefix}/reference/${component}`;
    for (const document of droppedDocuments) {
      const latestDocument = latestById.get(document.id);
      redirects.push({
        from: `${componentRoute}/${version}${document.slug}`,
        to: `${componentRoute}${latestDocument?.slug ?? '/'}`,
      });
    }
  }
  return redirects;
}

export function retargetRedirects(redirects, replacements) {
  const redirectsBySource = new Map([...redirects.map(({from, to}) => [from, to]), ...replacements]);
  const destinations = new Map([...redirectsBySource].map(([from, to]) => [redirectKey(from), to]));
  return [...redirectsBySource].map(([from, destination]) => {
    const visited = new Set([redirectKey(from)]);
    while (destinations.has(redirectKey(destination))) {
      if (visited.has(redirectKey(destination))) {
        throw new Error(`Redirect cycle at ${destination}`);
      }
      visited.add(redirectKey(destination));
      destination = destinations.get(redirectKey(destination));
    }
    return {from, to: destination};
  });
}

function redirectKey(path) {
  return `${path.replace(/\/+$/, '')}/`;
}

export async function readReferenceDocuments(directory) {
  const documents = [];
  // Match the Docusaurus docs plugin's default include and exclude patterns.
  const sources = await Globby(['**/*.{md,mdx}'], {cwd: directory, ignore: GlobExcludeDefault});
  for (const source of sources.sort()) {
    const {data: frontMatter} = matter(await readFile(join(directory, source), 'utf8'));
    const sourceDirName = dirname(source);
    const filename = basename(source, extname(source));
    const shouldParseNumberPrefixes = frontMatter.parse_number_prefixes !== false;
    const baseID = frontMatter.id ?? (shouldParseNumberPrefixes
      ? DefaultNumberPrefixParser(filename).filename : filename);
    const directoryId = shouldParseNumberPrefixes
      ? stripPathNumberPrefixes(sourceDirName, DefaultNumberPrefixParser) : sourceDirName;
    const slug = getSlug({baseID, source, sourceDirName,
      frontMatterSlug: frontMatter.slug, stripDirNumberPrefixes: shouldParseNumberPrefixes});
    documents.push({id: [directoryId === '.' ? '' : directoryId, baseID].filter(Boolean).join('/'),
      slug: `${slug.replace(/\/+$/, '')}/`});
  }
  return documents;
}

async function readOptionalVersions(path) {
  try {
    return JSON.parse(await readFile(path, 'utf8'));
  } catch (error) {
    if (error.code === 'ENOENT') {
      return [];
    }
    throw error;
  }
}

function snapshotDirectory(siteDirectory, component, version) {
  return join(siteDirectory, `reference-${component}_versioned_docs/version-${version}`);
}

async function removeSnapshot(siteDirectory, component, version) {
  await rm(snapshotDirectory(siteDirectory, component, version), {recursive: true});
  await rm(join(siteDirectory, `reference-${component}_versioned_sidebars/version-${version}-sidebars.json`));
}

function runDocusaurusVersion(siteDirectory, component, version) {
  const result = spawnSync(process.execPath, [join(docsDirectory, 'node_modules/@docusaurus/core/bin/docusaurus.mjs'),
    `docs:version:reference-${component}`, version], {cwd: siteDirectory, stdio: 'inherit'});
  if (result.error) {
    throw result.error;
  }
  if (result.status !== 0) {
    throw new Error(`Docusaurus version snapshot failed (${result.status})`);
  }
}

async function writeJSON(path, value) {
  await writeFile(path, `${JSON.stringify(value, null, 2)}\n`);
}

if (process.argv[1] && fileURLToPath(import.meta.url) === fileURLToPath(new URL(process.argv[1], 'file:///'))) {
  const commandArguments = process.argv.slice(2);
  if (commandArguments[0] === '--reference-version' && commandArguments.length === 3) {
    console.log(referenceVersion(commandArguments[1], commandArguments[2]));
  } else if (commandArguments[0] === '--rewrite-links' && commandArguments.length === 3) {
    console.log(JSON.stringify(await rewriteSnapshotLinks(commandArguments[1], commandArguments[2])));
  } else if (commandArguments.length === 2 && !commandArguments[0].startsWith('-')) {
    console.log(JSON.stringify(await snapshotReference(commandArguments[0], commandArguments[1])));
  } else {
    throw new Error('Usage: node scripts/snapshot-reference.mjs <component> <version>'
      + ' | --rewrite-links <from> <to> | --reference-version <component> <version>');
  }
}
