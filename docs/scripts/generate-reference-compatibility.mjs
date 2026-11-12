// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// Rewrites the Developer reference compatibility matrix from the repository and its GitHub releases.
// Run it after a release snapshots the reference: node scripts/generate-reference-compatibility.mjs

import {execFile} from 'node:child_process';
import {readFileSync, writeFileSync} from 'node:fs';
import {fileURLToPath} from 'node:url';
import {promisify} from 'node:util';

const executeFile = promisify(execFile);
const docsRoot = fileURLToPath(new URL('..', import.meta.url));
const repositoryRoot = fileURLToPath(new URL('../..', import.meta.url));
const repository = 'superdurable/dex';
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
const sdkComponents = ['go', 'python', 'java', 'typescript', 'rust'];

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

function serverCurrentProtocol() {
  const source = readFileSync(`${repositoryRoot}server-zd/crates/dexzd-api/src/flow_service.rs`, 'utf8');
  const current = source.match(/pub const CURRENT_PROTOCOL_VERSION: u32 = (\d+);/)?.[1];
  if (!current) throw new Error('server-zd no longer states CURRENT_PROTOCOL_VERSION in flow_service.rs');
  return current;
}

async function gh(args) {
  const {stdout} = await executeFile('gh', args, {maxBuffer: 16 * 1024 * 1024});
  return stdout;
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

const sdkNames = {
  en: {all: 'Go, Python, Java, TypeScript, and Rust SDKs', go: 'Go SDK', python: 'Python SDK', java: 'Java SDK', typescript: 'TypeScript SDK', rust: 'Rust SDK'},
  'zh-Hans': {all: 'Go、Python、Java、TypeScript 和 Rust SDK', go: 'Go SDK', python: 'Python SDK', java: 'Java SDK', typescript: 'TypeScript SDK', rust: 'Rust SDK'},
};

function needsText(locale, requirement) {
  return locale === 'en'
    ? `${requirement.firstServer} or newer (protocol version ${requirement.protocol})`
    : `${requirement.firstServer} 或更新（协议版本 ${requirement.protocol}）`;
}

function sdkRows(locale, requirement) {
  const lines = sdkComponents.map((component) => retainedLines(component).join(', '));
  const needs = needsText(locale, requirement);
  const names = sdkNames[locale];
  if (new Set(lines).size === 1) {
    return [`| ${names.all} | ${lines[0]} | ${needs} |`];
  }
  return sdkComponents.map((component, index) => `| ${names[component]} | ${lines[index]} | ${needs} |`);
}

const requirement = requiredServerProtocol();
const currentProtocol = serverCurrentProtocol();
const webLines = retainedLines('web').join(', ');
const bundled = await bundledServers();

function section(locale) {
  const sdk = sdkRows(locale, requirement).join('\n');
  const needs = needsText(locale, requirement);
  const dexcli = bundled.map(({cli, server}) => `| ${cli} | ${server} |`).join('\n');
  if (locale === 'en') {
    return `## Compatibility matrix

Workers and Clients negotiate a protocol version with the server. server-zd
${requirement.firstServer} and newer provide protocol version ${currentProtocol}.

| Component | Version | server-zd it needs |
| --- | --- | --- |
${sdk}
| Dex Web | ${webLines} | ${needs} |

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

Worker 和 Client 会和 server 协商协议版本。server-zd ${requirement.firstServer} 及更新版本提供协议版本 ${currentProtocol}。

| 组件 | 版本 | 需要的 server-zd |
| --- | --- | --- |
${sdk}
| Dex Web | ${webLines} | ${needs} |

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
console.log(`Compatibility matrix written in ${pages.length} locales: SDK lines ${retainedLines('go').join(', ')}, ${bundled.length} dexcli lines.`);
