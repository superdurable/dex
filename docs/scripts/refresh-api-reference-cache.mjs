// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// Keeps docs/.cache/api-reference current for one version scope, regenerating only entries whose stamp is stale.

import {execFile, spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {appendFile, readdir, readFile, rm, stat, writeFile} from 'node:fs/promises';
import {dirname, join, resolve} from 'node:path';
import {fileURLToPath, pathToFileURL} from 'node:url';
import {promisify} from 'node:util';

const executeFile = promisify(execFile);
const docsRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const repositoryRoot = dirname(docsRoot);
const buildScriptPath = join(docsRoot, 'scripts', 'build-api-reference.mjs');
const stampSuffix = '.stamp.json';
const minorDirectoryPattern = /^\d+\.\d+$/;
const manifestLanguages = Object.keys(JSON.parse(await readFile(join(docsRoot, 'api-reference-versions.json'), 'utf8')));
const usage = `Usage: node scripts/refresh-api-reference-cache.mjs <plan|refresh> <latest|all>

  plan     Fetches missing SDK release tags and prints the cache key hash for the scope.
  refresh  Regenerates stale trees for the scope and removes every other tree, so
           "refresh latest" prunes non-latest trees. It does not fetch tags; run plan first.

DEX_API_TOOL_HASH is required: the hash of the API reference tools.
DEX_API_OUTPUT optionally changes the cache root (default .cache/api-reference).
In GitHub Actions, refresh writes changed-tree-count (generated plus removed trees) to GITHUB_OUTPUT.`;
// Maven Central artifacts are immutable, so the version identifies the Javadoc.
const javaRevision = 'maven-central';

if (process.argv[1] && pathToFileURL(resolve(process.argv[1])).href === import.meta.url) {
  const [command, scope] = process.argv.slice(2);
  if (!['plan', 'refresh'].includes(command) || !['latest', 'all'].includes(scope)) {
    throw new Error(usage);
  }
  const toolHash = process.env.DEX_API_TOOL_HASH;
  if (!toolHash) throw new Error(`DEX_API_TOOL_HASH is not set.\n\n${usage}`);
  const cacheRoot = resolve(process.env.DEX_API_OUTPUT ?? join(docsRoot, '.cache', 'api-reference'));
  const entries = await readMatrixEntries(scope);
  if (command === 'plan') {
    await fetchReleaseTags(entries);
    console.log(entriesHash(await resolveRevisions(entries), toolHash));
  } else {
    let changedTreeCount = 0;
    const generate = async (entry) => {
      await generateWithBuildScript(entry);
      changedTreeCount += 1;
    };
    try {
      const outcome = await refreshApiReferenceCache({cacheRoot, toolHash, entries: await resolveRevisions(entries), generate});
      changedTreeCount += outcome.removed.length;
    } finally {
      // Written even on failure, so CI can save the trees finished before it.
      if (process.env.GITHUB_OUTPUT) await appendFile(process.env.GITHUB_OUTPUT, `changed-tree-count=${changedTreeCount}\n`);
    }
  }
}

/**
 * Regenerates each entry whose stamp differs, then removes every tree outside the entry list.
 *
 * @returns {Promise<{generated: string[], reused: string[], removed: string[]}>} "<language>/<minor>" names per outcome.
 */
export async function refreshApiReferenceCache({cacheRoot, toolHash, entries, generate}) {
  const outcome = {generated: [], reused: [], removed: []};
  for (const entry of entries) {
    const name = `${entry.language}/${entry.minor}`;
    const treeRoot = join(cacheRoot, entry.language, entry.minor);
    const stampPath = treeRoot + stampSuffix;
    const expectedStamp = stampContent(entry, toolHash);
    if (await readOptionalFile(stampPath) === expectedStamp && await isFile(join(treeRoot, 'build-metadata.json'))) {
      console.log(`Reusing cached ${entry.language} ${entry.version} (${entry.revision})`);
      outcome.reused.push(name);
      continue;
    }
    await rm(stampPath, {force: true});
    await generate(entry);
    await writeFile(stampPath, expectedStamp);
    outcome.generated.push(name);
  }
  outcome.removed = await removeTreesOutside(cacheRoot, entries);
  console.log(`API reference cache: ${outcome.generated.length} generated, ${outcome.reused.length} reused, ${outcome.removed.length} removed`);
  return outcome;
}

/**
 * Hashes every (language, version, revision) entry with the tool hash for the cache key.
 *
 * @returns {string} A hex SHA-256 that changes whenever any entry or the tools change.
 */
export function entriesHash(entries, toolHash) {
  const hash = createHash('sha256');
  for (const entry of entries) hash.update(stampContent(entry, toolHash));
  return hash.digest('hex');
}

function stampContent({language, version, revision}, toolHash) {
  return JSON.stringify({language, version, revision, toolHash}) + '\n';
}

async function removeTreesOutside(cacheRoot, entries) {
  const keptNames = new Set(entries.map((entry) => `${entry.language}/${entry.minor}`));
  const removed = [];
  for (const language of await directoryNames(cacheRoot)) {
    if (!manifestLanguages.includes(language)) continue;
    const languageRoot = join(cacheRoot, language);
    for (const fileName of await readdir(languageRoot)) {
      const minor = fileName.endsWith(stampSuffix) ? fileName.slice(0, -stampSuffix.length) : fileName;
      if (!minorDirectoryPattern.test(minor) || keptNames.has(`${language}/${minor}`)) continue;
      await rm(join(languageRoot, fileName), {recursive: true, force: true});
      if (!fileName.endsWith(stampSuffix)) removed.push(`${language}/${minor}`);
    }
  }
  return removed.sort();
}

async function readMatrixEntries(scope) {
  const {stdout} = await executeFile(process.execPath, [buildScriptPath, '--matrix', scope], {cwd: docsRoot});
  return JSON.parse(stdout).include;
}

async function fetchReleaseTags(entries) {
  const missingTags = [];
  for (const {language, version} of entries.filter((entry) => entry.language !== 'java')) {
    const tag = `sdk-${language}/v${version}`;
    if (!(await isTagCommitPresent(`refs/tags/${tag}`))) missingTags.push(tag);
  }
  if (missingTags.length === 0) return;
  const {stdout: shallowFlag} = await executeFile('git', ['rev-parse', '--is-shallow-repository'], {cwd: repositoryRoot});
  // Only a shallow checkout, as in CI, fetches shallowly; a full clone must stay full.
  const depthArguments = shallowFlag.trim() === 'true' ? ['--depth=1'] : [];
  // The plan command's stdout carries only the hash, so Git output goes to stderr.
  await runChildProcess('git', ['fetch', '--no-tags', ...depthArguments, 'origin', ...missingTags.map((tag) => `refs/tags/${tag}:refs/tags/${tag}`)],
    repositoryRoot, ['ignore', 2, 2]);
}

async function isTagCommitPresent(reference) {
  try {
    await executeFile('git', ['rev-parse', '--verify', '--quiet', `${reference}^{commit}`], {cwd: repositoryRoot});
    return true;
  } catch (error) {
    if (error.code === 1) return false;
    throw error;
  }
}

async function resolveRevisions(entries) {
  return await Promise.all(entries.map(async (entry) => {
    if (entry.language === 'java') return {...entry, revision: javaRevision};
    const tag = `sdk-${entry.language}/v${entry.version}`;
    const {stdout} = await executeFile('git', ['rev-parse', `${tag}^{commit}`], {cwd: repositoryRoot});
    return {...entry, revision: stdout.trim()};
  }));
}

async function generateWithBuildScript({language, version}) {
  await runChildProcess(process.execPath, [buildScriptPath, language, `sdk-${language}/v${version}`], docsRoot);
}

function runChildProcess(command, commandArguments, cwd, stdio = 'inherit') {
  return new Promise((resolvePromise, rejectPromise) => {
    const child = spawn(command, commandArguments, {cwd, stdio});
    child.on('error', rejectPromise);
    child.on('exit', (code, signal) => {
      if (code === 0) resolvePromise();
      else rejectPromise(new Error(`${command} ${commandArguments.join(' ')} failed with ${signal ?? `exit code ${code}`}`));
    });
  });
}

async function directoryNames(root) {
  try {
    return (await readdir(root, {withFileTypes: true})).filter((entry) => entry.isDirectory()).map((entry) => entry.name);
  } catch (error) {
    if (error.code === 'ENOENT') return [];
    throw error;
  }
}

async function readOptionalFile(path) {
  try {
    return await readFile(path, 'utf8');
  } catch (error) {
    if (error.code === 'ENOENT') return undefined;
    throw error;
  }
}

async function isFile(path) {
  try {
    return (await stat(path)).isFile();
  } catch (error) {
    if (error.code === 'ENOENT') return false;
    throw error;
  }
}
