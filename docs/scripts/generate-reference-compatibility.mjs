// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// Rewrites the Developer reference compatibility matrix from the repository and its GitHub releases.
// Run it after a release snapshots the reference: node scripts/generate-reference-compatibility.mjs
// The per-server table itself renders from static/compatibility/server-zd.json at build time.

import {execFile} from 'node:child_process';
import {readFileSync, writeFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';

const executeFile = promisify(execFile);
const docsRoot = fileURLToPath(new URL('..', import.meta.url));
const repositoryRoot = fileURLToPath(new URL('../..', import.meta.url));
const repository = 'superdurable/dex';
const compatibilityUrl = 'https://docs.superdurable.io/compatibility/server-zd.json';
const compatibilitySchemaUrl = 'https://docs.superdurable.io/compatibility/server-zd.schema.json';
// Each locale's landing page, with the headings that bound its matrix section.
const pages = [
  {locale: 'en', path: `${docsRoot}content/reference/index.mdx`, start: '## Compatibility matrix', end: '## Release notes'},
  {
    locale: 'zh-Hans',
    path: `${docsRoot}i18n/zh-Hans/docusaurus-plugin-content-docs/current/reference/index.mdx`,
    start: '## 兼容性矩阵',
    end: '## 发版说明',
  },
];

function readJson(path) {
  return JSON.parse(readFileSync(path, 'utf8'));
}

// "1.45.x" becomes "1.45".
function minorLine(version) {
  return version.replace(/\.x$/, '');
}

function retainedLines(component) {
  return readJson(`${docsRoot}reference-${component}_versions.json`).map(minorLine);
}

function requiredServerProtocol() {
  const source = readFileSync(`${repositoryRoot}sdk-go/dex/server_protocol.go`, 'utf8');
  const minimum = source.match(/minimumSupportedServerProtocolVersion uint32 = (\d+)/)?.[1];
  const firstRelease = source.match(/minimumSupportedServerZDRelease = "server-zd newer than v(\d+\.\d+\.\d+)"/)?.[1];
  if (!minimum || !firstRelease) throw new Error('sdk-go/dex/server_protocol.go no longer states the required server protocol');
  const [major, minor, patch] = firstRelease.split('.').map(Number);
  return {protocol: minimum, firstServer: `${major}.${minor}.${patch + 1}`};
}

async function gh(args) {
  const {stdout} = await executeFile('gh', args, {maxBuffer: 16 * 1024 * 1024});
  return stdout;
}

function compareVersions(left, right) {
  const leftParts = left.split('.').map(Number);
  const rightParts = right.split('.').map(Number);
  for (let index = 0; index < 3; index += 1) {
    if (leftParts[index] !== rightParts[index]) return leftParts[index] - rightParts[index];
  }
  return 0;
}

// Released server-zd versions, oldest first.
async function serverReleases() {
  return (await gh(['release', 'list', '--repo', repository, '--limit', '1000', '--json', 'tagName', '--jq', '.[].tagName']))
    .split('\n')
    .map((tag) => tag.match(/^server-zd\/v(\d+\.\d+\.\d+)$/)?.[1])
    .filter(Boolean)
    .sort(compareVersions);
}

// The current protocol of one server-zd release, or 0 when that release does not state one.
async function releasedServerProtocol(version) {
  const path = 'server-zd/crates/dexzd-api/src/flow_service.rs';
  let content;
  try {
    content = await gh(['api', `repos/${repository}/contents/${path}?ref=server-zd/v${version}`, '--jq', '.content']);
  } catch (error) {
    if (String(error.stderr ?? error.message).includes('404')) return 0;
    throw error;
  }
  const source = Buffer.from(content, 'base64').toString('utf8');
  return Number(source.match(/pub const CURRENT_PROTOCOL_VERSION: u32 = (\d+);/)?.[1] ?? 0);
}

// The first released server-zd version that provides the protocol, or null before any release does.
// Protocol versions only grow from one release to the next, so a binary search finds it.
async function firstServerProviding(protocol, releases) {
  let low = 0;
  let high = releases.length;
  while (low < high) {
    const middle = Math.floor((low + high) / 2);
    if ((await releasedServerProtocol(releases[middle])) >= protocol) high = middle;
    else low = middle + 1;
  }
  return low < releases.length ? releases[low] : null;
}

// Each retained dexcli line, with the server-zd release its newest patch bundles.
async function bundledServers() {
  const tags = (await gh(['release', 'list', '--repo', repository, '--limit', '1000', '--json', 'tagName', '--jq', '.[].tagName']))
    .split('\n')
    .filter((tag) => /^cli-v\d+\.\d+\.\d+$/.test(tag));
  const rows = [];
  for (const line of retainedLines('cli')) {
    const newest = tags
      .filter((tag) => tag.startsWith(`cli-v${line}.`))
      .sort((left, right) => Number(right.split('.').at(-1)) - Number(left.split('.').at(-1)))[0];
    if (!newest) throw new Error(`no cli-v${line}.N release exists`);
    const content = await gh(['api', `repos/${repository}/contents/cli/SERVER_ZD_RELEASE?ref=${newest}`, '--jq', '.content']);
    const release = Buffer.from(content, 'base64').toString('utf8').trim();
    const server = release.match(/^server-zd\/v(\d+\.\d+\.\d+)$/)?.[1];
    if (!server) throw new Error(`${newest} bundles an unexpected server-zd release: ${release}`);
    rows.push({cli: line, server});
  }
  return rows;
}

const requirement = requiredServerProtocol();
const releases = await serverReleases();
const firstMinimumServer = await firstServerProviding(Number(requirement.protocol), releases);
if (firstMinimumServer !== requirement.firstServer) {
  throw new Error(
    `sdk-go/dex/server_protocol.go names server-zd ${requirement.firstServer} for protocol ${requirement.protocol}, but releases first provide it in ${firstMinimumServer}`,
  );
}
const webLines = retainedLines('web').join(', ');
const bundled = await bundledServers();

function section(locale) {
  const dexcli = bundled.map(({cli, server}) => `| ${cli} | ${server} |`).join('\n');
  if (locale === 'en') {
    return `## Compatibility matrix

Each server-zd release accepts a range of protocol versions. Each client
release needs a minimum protocol version, and some need a newer server-zd
release. A newer server keeps accepting older clients until it raises its
minimum protocol version.

Each row is one supported server-zd release. Each cell holds the oldest and the
newest release of a client that works with that server. A release between them
works too, if its registry has it. The Dex Agents packages and the Agents CLI
are listed per registry, because a release is sometimes published to one
registry only.

<ServerZdCompatibilityMatrix />

Each row has the anchor **server-zd-MAJOR-MINOR-PATCH**, such as
**server-zd-0-1-31**, so a link can open the row of one server-zd release.
Each server-zd reference version shows its own row on its
[Upgrades and compatibility](/reference/server/upgrades#compatible-client-releases)
page.

Programs read the same data from
[server-zd.json](${compatibilityUrl}).
Its [JSON Schema](${compatibilitySchemaUrl})
describes every field and the compatibility rule.

Dex Web ${webLines} needs server-zd ${requirement.firstServer} or newer (protocol
version ${requirement.protocol}).

Upgrade the server first, then the SDKs. See
[Upgrades and compatibility](/reference/server/upgrades).

Each \`dexcli\` release bundles one server-zd release, which **dexcli dev**
runs:

| dexcli | Bundled server-zd |
| --- | --- |
${dexcli}

`;
  }
  return `## 兼容性矩阵 {#compatibility-matrix}

每个 server-zd 版本接受一个协议版本范围。每个客户端版本需要一个最低协议版本，有些还需要更新的 server-zd 版本。较新的 server 会一直接受较旧的客户端，直到它提高最低协议版本。

每一行是一个受支持的 server-zd 版本。每个单元格给出能和这个 server 一起使用的某个客户端的最旧和最新版本。两者之间的版本只要已发布到对应的 registry，也可以使用。Dex Agents 包和 Agents CLI 按 registry 分开列出，因为一个版本可能只发布到一个 registry。

<ServerZdCompatibilityMatrix />

每一行都有锚点 **server-zd-MAJOR-MINOR-PATCH**，例如 **server-zd-0-1-31**，链接可以直接打开某个 server-zd 版本的那一行。每个 server-zd 参考版本在它的 [Upgrades and compatibility](/reference/server/upgrades#compatible-client-releases) 页面上显示自己的那一行。

程序可以从 [server-zd.json](${compatibilityUrl}) 读取同样的数据。它的 [JSON Schema](${compatibilitySchemaUrl}) 说明每个字段和兼容规则。

Dex Web ${webLines} 需要 server-zd ${requirement.firstServer} 或更新（协议版本 ${requirement.protocol}）。

先升级 server，再升级 SDK。见 [Upgrades and compatibility](/reference/server/upgrades)。

每个 \`dexcli\` 版本打包一个 server-zd 版本，**dexcli dev** 运行的就是它：

| dexcli | 打包的 server-zd |
| --- | --- |
${dexcli}

`;
}

for (const {locale, path, start: startHeading, end: endHeading} of pages) {
  const page = readFileSync(path, 'utf8');
  const start = page.indexOf(startHeading);
  const end = page.indexOf(endHeading);
  if (start === -1 || end === -1 || end < start) throw new Error(`${path} lost its ${startHeading} or ${endHeading} heading`);
  writeFileSync(path, `${page.slice(0, start)}${section(locale)}${page.slice(end)}`);
}
console.log(`Compatibility matrix written in ${pages.length} locales: ${bundled.length} dexcli lines.`);
