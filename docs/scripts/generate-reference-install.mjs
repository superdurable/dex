// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// Rewrites the Install pin and the Supported versions table on each SDK reference landing page.
// Versions come from reference/versions.json, the reference-<sdk>_versions.json files, and the
// repository's sdk-<sdk>/vX.Y.Z release tags, so no version is typed by hand.
// Run it after a release snapshots the reference: node scripts/generate-reference-install.mjs
//
// The current page and the snapshot of the current line get both the pin and the table.
// Older snapshots keep their table as it was and only pin the newest patch of their own line.

import {execFileSync} from 'node:child_process';
import {existsSync, readdirSync, readFileSync, writeFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';

const docsRoot = fileURLToPath(new URL('..', import.meta.url));
const repositoryRoot = fileURLToPath(new URL('../..', import.meta.url));
const sdkComponents = ['go', 'python', 'java', 'typescript', 'rust'];

function readJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'));
}

// "1.45.x" becomes "1.45".
function minorLine(version) {
  return version.replace(/\.x$/, '');
}

const releaseTags = execFileSync('git', ['tag', '-l', 'sdk-*/v*'], {cwd: repositoryRoot, encoding: 'utf8'})
  .split('\n')
  .filter(Boolean);

// The newest released patch of one SDK line, from the sdk-<component>/vX.Y.Z tags.
function newestRelease(component, line) {
  const prefix = `sdk-${component}/v${line}.`;
  const patches = releaseTags
    .filter((tag) => tag.startsWith(prefix) && /^\d+$/.test(tag.slice(prefix.length)))
    .map((tag) => Number(tag.slice(prefix.length)));
  if (patches.length === 0) throw new Error(`no ${prefix}N release tag exists; fetch tags first (git fetch --tags)`);
  return `${line}.${Math.max(...patches)}`;
}

function requiredServerProtocol() {
  const source = readFileSync(`${repositoryRoot}sdk-go/dex/server_protocol.go`, 'utf8');
  const minimum = source.match(/minimumSupportedServerProtocolVersion uint32 = (\d+)/)?.[1];
  const firstRelease = source.match(/minimumSupportedServerZDRelease = "server-zd newer than v(\d+\.\d+\.\d+)"/)?.[1];
  if (!minimum || !firstRelease) throw new Error('sdk-go/dex/server_protocol.go no longer states the required server protocol');
  const [major, minor, patch] = firstRelease.split('.').map(Number);
  return {protocol: minimum, firstServer: `${major}.${minor}.${patch + 1}`};
}

// Each SDK's install pin, as its package manager spells it.
const pinRules = {
  python: [[/dex-python-sdk==\d+\.\d+\.\d+/g, (v) => `dex-python-sdk==${v}`]],
  typescript: [[/@superdurable\/dex@\d+\.\d+\.\d+/g, (v) => `@superdurable/dex@${v}`]],
  go: [[/sdk-go@v\d+\.\d+\.\d+/g, (v) => `sdk-go@v${v}`]],
  java: [[/io\.superdurable:dex-sdk:\d+\.\d+\.\d+/g, (v) => `io.superdurable:dex-sdk:${v}`]],
  rust: [
    [/dex-sdk = "=\d+\.\d+\.\d+"/g, (v) => `dex-sdk = "=${v}"`],
    [/\[SDK \d+\.\d+\.\d+ API reference\]\(https:\/\/docs\.rs\/dex-sdk\/\d+\.\d+\.\d+\/dex_sdk\/\)/g, (v) => `[SDK ${v} API reference](https://docs.rs/dex-sdk/${v}/dex_sdk/)`],
  ],
};

function supportedVersions(component, requirement) {
  const lines = readJson(`${docsRoot}reference-${component}_versions.json`).map(minorLine);
  return `| SDK minor | Server requirement |
| --- | --- |
| ${lines.join(', ')} | server-zd ${requirement.firstServer} or newer (protocol version ${requirement.protocol}) |

The [compatibility matrix](/reference/#compatibility-matrix) lists every component.
`;
}

function rewrite(path, component, line, withTable, requirement) {
  let page = readFileSync(path, 'utf8');
  const release = newestRelease(component, line);
  for (const [pattern, replacement] of pinRules[component]) {
    if (!pattern.test(page)) throw new Error(`${path} has no install pin matching ${pattern}`);
    page = page.replace(pattern, replacement(release));
  }
  if (withTable) {
    const start = page.indexOf('## Supported versions\n');
    const end = page.indexOf('\nMinor numbers follow', start);
    if (start === -1 || end === -1) throw new Error(`${path} lost its Supported versions section`);
    const heading = '## Supported versions\n\n';
    page = `${page.slice(0, start)}${heading}${supportedVersions(component, requirement)}${page.slice(end)}`;
  }
  writeFileSync(path, page);
  return release;
}

const versions = readJson(`${docsRoot}reference/versions.json`);
const requirement = requiredServerProtocol();
const written = [];
for (const component of sdkComponents) {
  const current = minorLine(versions[component]);
  written.push(`${component} ${rewrite(`${docsRoot}reference/${component}/index.mdx`, component, current, true, requirement)}`);
  const snapshots = `${docsRoot}reference-${component}_versioned_docs`;
  if (!existsSync(snapshots)) continue;
  for (const folder of readdirSync(snapshots)) {
    const line = folder.match(/^version-(\d+\.\d+)\.x$/)?.[1];
    const page = `${snapshots}/${folder}/index.mdx`;
    if (!line || !existsSync(page)) continue;
    rewrite(page, component, line, line === current, requirement);
  }
}
console.log(`Reference install pins written: ${written.join(', ')}.`);
