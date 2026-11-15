// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// Builds the compatibility tables from static/compatibility/server-zd.json.
// The site components and the Markdown exporter share these tables.

const copies = {
  en: {
    serverZd: 'server-zd',
    protocol: 'Protocol',
    component: 'Component',
    oldest: 'Oldest',
    newest: 'Newest',
    none: 'none',
    sdk: (version) => `SDK ${version}`,
    protocolSentence: (version, protocol) =>
      `server-zd ${version} accepts protocol versions ${protocol.min} to ${protocol.max}. `
      + 'These client releases work with it. A release between the oldest and the newest works too, if its registry has it.',
    notCovered: (version, oldest) =>
      `The compatibility data covers server-zd ${oldest} and newer, so it no longer lists server-zd ${version}. `
      + 'Upgrade the server to read the current ranges.',
  },
  'zh-Hans': {
    serverZd: 'server-zd',
    protocol: '协议',
    component: '组件',
    oldest: '最旧',
    newest: '最新',
    none: '无',
    sdk: (version) => `SDK ${version}`,
    protocolSentence: (version, protocol) =>
      `server-zd ${version} 接受协议版本 ${protocol.min} 到 ${protocol.max}。`
      + '下面这些客户端版本可以和它一起使用。最旧和最新之间的版本，只要已发布到对应的 registry，也可以使用。',
    notCovered: (version, oldest) =>
      `兼容性数据只覆盖 server-zd ${oldest} 及更新版本，不再列出 server-zd ${version}。升级 server 后可以查看当前的范围。`,
  },
};

// Each client column: its label and how to read its range from one server entry.
const clients = [
  {label: 'Go SDK', range: (server) => server.sdks.go},
  {label: 'Python SDK', range: (server) => server.sdks.python},
  {label: 'TypeScript SDK', range: (server) => server.sdks.typescript},
  {label: 'Java SDK', range: (server) => server.sdks.java},
  {label: 'Rust SDK', range: (server) => server.sdks.rust},
  {label: 'dexcli', range: (server) => server.dexcli},
  {label: 'Dex Agents (npm)', range: (server) => server.agents.npm},
  {label: 'Dex Agents (PyPI)', range: (server) => server.agents.pypi},
  {label: 'Agents CLI (npm)', range: (server) => server.agentsCli.npm},
  {label: 'Agents CLI (PyPI)', range: (server) => server.agentsCli.pypi},
];

const commandRegistryLabels = {npm: 'npm', pypi: 'PyPI'};

// One client per Agents CLI command package, labeled by its published name.
function commandClients(server) {
  return Object.entries(commandRegistryLabels).flatMap(([registry, registryLabel]) =>
    Object.keys(server.agentsCliCommands?.[registry] ?? {}).map((name) => ({
      label: `${name} (${registryLabel})`,
      range: (entry) => entry.agentsCliCommands[registry][name],
    })),
  );
}

function copyFor(locale) {
  return copies[locale] ?? copies.en;
}

/**
 * Returns the anchor of a server-zd release's row in the Compatibility matrix.
 *
 * @param {string} version A server-zd release, such as `0.1.31`.
 * @returns {string} The anchor, such as `server-zd-0-1-31`.
 */
export function serverZdAnchor(version) {
  return `server-zd-${version.split('.').join('-')}`;
}

function releaseText(release) {
  return typeof release === 'string' ? release : release.version;
}

function rangeText(range, copy) {
  if (range === null) return copy.none;
  const oldest = releaseText(range.oldest);
  const newest = releaseText(range.newest);
  return oldest === newest ? oldest : `${oldest} – ${newest}`;
}

function endText(release, copy) {
  return typeof release === 'string' ? release : `${release.version} (${copy.sdk(release.pinnedSdk)})`;
}

/**
 * Returns the Compatibility matrix: one row per server-zd release, newest first.
 *
 * @param {{servers: object[]}} document The parsed server-zd.json.
 * @param {string} locale The site locale.
 * @returns {{header: string[], rows: {anchor: string, cells: string[]}[]}} The table, with each row's anchor.
 */
export function compatibilityMatrixTable(document, locale) {
  const copy = copyFor(locale);
  return {
    header: [copy.serverZd, copy.protocol, ...clients.map((client) => client.label)],
    rows: document.servers.map((server) => ({
      anchor: serverZdAnchor(server.serverZd),
      cells: [
        server.serverZd,
        `${server.protocolVersion.min}–${server.protocolVersion.max}`,
        ...clients.map((client) => rangeText(client.range(server), copy)),
      ],
    })),
  };
}

/**
 * Returns the compatible client releases of one server-zd release, then each Agents CLI command package.
 *
 * @param {{servers: object[]}} document The parsed server-zd.json.
 * @param {string} version The server-zd release.
 * @param {string} locale The site locale.
 * @returns {{summary: string, header: string[] | null, rows: string[][]}} A sentence and the table, or only a
 *   sentence with a null header when the data no longer covers the release.
 */
export function serverCompatibilityTable(document, version, locale) {
  const copy = copyFor(locale);
  const server = document.servers.find((entry) => entry.serverZd === version);
  if (!server) {
    const oldest = document.servers.at(-1)?.serverZd ?? version;
    return {summary: copy.notCovered(version, oldest), header: null, rows: []};
  }
  return {
    summary: copy.protocolSentence(version, server.protocolVersion),
    header: [copy.component, copy.oldest, copy.newest],
    rows: [...clients, ...commandClients(server)].map((client) => {
      const range = client.range(server);
      if (range === null) return [client.label, copy.none, copy.none];
      return [client.label, endText(range.oldest, copy), endText(range.newest, copy)];
    }),
  };
}
