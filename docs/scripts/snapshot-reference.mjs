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
// Markdown destinations may be wrapped in angle brackets: [a](</path>) or [ref]: </path>.
const linkDestinationStart = '(?<=^|[\\s("\'\\[=]|\\(<|\\]:[ \\t]*<)';
const linkDestinationEndCharacter = '[\\s)"\'\\]#?>]';

export function referenceVersion(component, releaseVersion) {
  // The Dex app reference follows the manifest schema version, such as v1, not a release.
  if (component === 'dex-app') {
    const schema = releaseVersion.match(/^v?([1-9]\d*)$/);
    if (!schema) {
      throw new Error(`Invalid ${component} reference version: ${releaseVersion}`);
    }
    return `v${schema[1]}`;
  }
  const release = releaseVersion.replace(/^v/, '');
  if (component === 'server' && /^0\.1\.\d+$/.test(release)) {
    return release;
  }
  // The agents packages are before 1.0, so each of their 0.N minors is one version.
  const majorPattern = component === 'agents' ? '(0|[1-9]\\d*)' : '([1-9]\\d*)';
  const match = release.match(new RegExp(`^${majorPattern}\\.(\\d+)\\.(?:\\d+|x)$`));
  if (!match) {
    throw new Error(`Invalid ${component} reference version: ${releaseVersion}`);
  }
  return `${match[1]}.${match[2]}.x`;
}

export function compareVersions(first, second) {
  // A schema version such as v2 compares as 2.0.0.
  const versionParts = (version) => version.replace(/^v/, '').split('.').map((part) => part === 'x' ? 0 : Number(part));
  const firstParts = versionParts(first);
  const secondParts = versionParts(second);
  for (let index = 0; index < 3; index += 1) {
    if ((firstParts[index] ?? 0) !== (secondParts[index] ?? 0)) {
      return (firstParts[index] ?? 0) - (secondParts[index] ?? 0);
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
  await localizeSnapshotLinks(component, version, {siteDirectory});
  await pinServerCompatibilityVersion(component, version, {siteDirectory});
  // The site keeps every version; only an owner request removes one.
  const versions = [...new Set([version, ...previousVersions])]
    .sort((first, second) => compareVersions(second, first));
  await writeJSON(versionsPath, versions);
  await writeJSON(sharedVersionsPath, {...latestVersions, [component]: version});
  return {component, version, versions};
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
  const linkPattern = new RegExp(
    `${linkDestinationStart}${escapeRegExp(fromRoute)}(/?)(?=$|${linkDestinationEndCharacter})`, 'gm');
  const changedFiles = [];
  for (const entry of await readdir(siteDirectory, {withFileTypes: true})) {
    if (!entry.isDirectory() || !/^reference-[a-z-]+_versioned_(?:docs|sidebars)$/.test(entry.name)) {
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

/**
 * Points a snapshot's links to its own component at the snapshot's version, such as
 * /reference/server/workers to /reference/server/0.1.16/workers.
 *
 * @returns {Promise<number>} The number of rewritten links in the snapshot's docs and sidebar.
 */
/**
 * Pins each ServerZdCompatibility call in a new server snapshot to that snapshot's version.
 *
 * @returns {Promise<string[]>} The changed snapshot files, relative to the site directory.
 */
export async function pinServerCompatibilityVersion(component, version, {siteDirectory = docsDirectory} = {}) {
  if (component !== 'server') {
    return [];
  }
  const versionDirectory = snapshotDirectory(siteDirectory, component, version);
  const changedFiles = [];
  for (const source of (await Globby(['**/*.{md,mdx}'], {cwd: versionDirectory})).sort()) {
    const path = join(versionDirectory, source);
    const content = await readFile(path, 'utf8');
    const pinned = content.replace(/<ServerZdCompatibility\s*\/>/g, `<ServerZdCompatibility version="${version}" />`);
    if (pinned !== content) {
      await writeFile(path, pinned);
      changedFiles.push(relative(siteDirectory, path).split('\\').join('/'));
    }
  }
  return changedFiles;
}

export async function localizeSnapshotLinks(component, version, {siteDirectory = docsDirectory} = {}) {
  const componentRoute = `/reference/${component}`;
  const destinationEnd = `(?:$|/|${linkDestinationEndCharacter})`;
  // Links that already name a version stay unchanged, so reruns are idempotent.
  const linkPattern = new RegExp(`${linkDestinationStart}${escapeRegExp(componentRoute)}`
    + `(?!/(?:\\d+\\.\\d+\\.(?:\\d+|x)|v\\d+)${destinationEnd})(?=${destinationEnd})`, 'gm');
  const versionDirectory = snapshotDirectory(siteDirectory, component, version);
  const paths = [
    ...(await Globby(['**/*.{md,mdx}'], {cwd: versionDirectory})).map((source) => join(versionDirectory, source)),
    snapshotSidebarPath(siteDirectory, component, version),
  ];
  let rewrittenLinkCount = 0;
  for (const path of paths) {
    const content = await readFile(path, 'utf8');
    const rewritten = content.replace(linkPattern, () => {
      rewrittenLinkCount += 1;
      return `${componentRoute}/${version}`;
    });
    if (rewritten !== content) {
      await writeFile(path, rewritten);
    }
  }
  return rewrittenLinkCount;
}

/**
 * Applies localizeSnapshotLinks to every stored snapshot, including each hidden latest snapshot.
 *
 * @returns {Promise<Record<string, number>>} The number of rewritten links per component.
 */
export async function localizeAllSnapshotLinks({siteDirectory = docsDirectory} = {}) {
  const latestVersions = JSON.parse(await readFile(join(siteDirectory, 'reference/versions.json'), 'utf8'));
  const rewrittenLinkCounts = {};
  for (const component of Object.keys(latestVersions)) {
    rewrittenLinkCounts[component] = 0;
    for (const version of await readOptionalVersions(join(siteDirectory, `reference-${component}_versions.json`))) {
      rewrittenLinkCounts[component] += await localizeSnapshotLinks(component, version, {siteDirectory});
    }
  }
  return rewrittenLinkCounts;
}

function escapeRegExp(value) {
  return value.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
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
  await rm(snapshotSidebarPath(siteDirectory, component, version));
}

function snapshotSidebarPath(siteDirectory, component, version) {
  return join(siteDirectory, `reference-${component}_versioned_sidebars/version-${version}-sidebars.json`);
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
  } else if (commandArguments[0] === '--localize-links' && commandArguments.length === 1) {
    console.log(JSON.stringify(await localizeAllSnapshotLinks()));
  } else if (commandArguments.length === 2 && !commandArguments[0].startsWith('-')) {
    console.log(JSON.stringify(await snapshotReference(commandArguments[0], commandArguments[1])));
  } else {
    throw new Error('Usage: node scripts/snapshot-reference.mjs <component> <version>'
      + ' | --rewrite-links <from> <to> | --localize-links | --reference-version <component> <version>');
  }
}
