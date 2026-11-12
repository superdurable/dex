// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import assert from 'node:assert/strict';
import {access, readFile, readdir} from 'node:fs/promises';
import {dirname, join, relative, sep} from 'node:path';
import {fileURLToPath} from 'node:url';
import {unified} from 'unified';
import remarkParse from 'remark-parse';
import remarkMdx from 'remark-mdx';
import remarkFrontmatter from 'remark-frontmatter';
import remarkGfm from 'remark-gfm';
import {frameworkMarkdownPath, frameworkOrder, languageMarkdownPath, prepareSource, sdkOrder} from '../plugins/markdown-export/export.mjs';
import {markdownPath} from '../src/markdownPath.mjs';
import './markdown-export.mjs';
import {readReferenceDocuments, compareVersions} from '../scripts/snapshot-reference.mjs';
import {
  apiEntryPaths, auditApiReferenceIndexPages, auditApiReferences, auditPythonGenerationLocks, isApiReferenceVersionPath,
} from './api-reference-smoke.mjs';
import './api-reference-redirect.mjs';

const siteOrigin = 'https://docs.superdurable.io';

// The generated API pages are built at deploy time, so a page below a retained version cannot be checked here.
function isRetainedApiReferencePath(pathname) {
  return [...apiEntryPaths].some((entryPath) => pathname.startsWith(entryPath));
}
const root = join(dirname(fileURLToPath(import.meta.url)), '..', 'build');
const docsRoot = join(root, '..');
const redirectsPath = join(root, '..', 'redirects.json');
const latestReferenceVersions = JSON.parse(await readFile(join(docsRoot, 'reference/versions.json'), 'utf8'));
const referenceVersions = new Map();
for (const [component, latest] of Object.entries(latestReferenceVersions)) {
  let snapshots;
  try {
    snapshots = JSON.parse(await readFile(join(docsRoot, `reference-${component}_versions.json`), 'utf8'));
  } catch (error) {
    if (error.code !== 'ENOENT') throw error;
    snapshots = [];
  }
  const archived = snapshots.filter((version) => version !== latest);
  assert.ok(archived.length < 5, `${component} must publish at most five versions including current`);
  assert.deepEqual(snapshots, [...new Set(snapshots)].sort((first, second) => compareVersions(second, first)));
  referenceVersions.set(component, ['current', ...archived]);
  for (const version of ['current', ...archived]) {
    const sourceDirectory = version === 'current' ? join(docsRoot, 'reference', component)
      : join(docsRoot, `reference-${component}_versioned_docs/version-${version}`);
    for (const document of await readReferenceDocuments(sourceDirectory)) {
      for (const localePrefix of ['', 'zh-Hans/']) {
        const versionPrefix = version === 'current' ? '' : `${version}/`;
        await access(join(root, `${localePrefix}reference/${component}/${versionPrefix}${document.slug}`, 'index.html'));
      }
    }
  }
}
const redirectRules = JSON.parse(await readFile(redirectsPath, 'utf8'));
const expectedRedirects = new Map(
  redirectRules.map(({from, to}) => [
    routeWithTrailingSlash(from),
    new URL(routeWithTrailingSlash(to), siteOrigin).href,
  ]),
);

const home = await readFile(join(root, 'index.html'), 'utf8');
const cloud = await readFile(join(root, 'cloud', 'index.html'), 'utf8');
const cron = await readFile(join(root, 'design-patterns', 'durable-timer', 'cron', 'index.html'), 'utf8');
const whyDex = await readFile(join(root, 'intro', 'what-is-dex', 'index.html'), 'utf8');
const components = await readFile(join(root, 'components', 'index.html'), 'utf8');
const referenceLanding = await readFile(join(root, 'reference', 'index.html'), 'utf8');
const zhReferenceLanding = await readFile(join(root, 'zh-Hans', 'reference', 'index.html'), 'utf8');
const serverReference = await readFile(join(root, 'reference', 'server', 'workers', 'index.html'), 'utf8');
const zhServerReference = await readFile(join(root, 'zh-Hans', 'reference', 'server', 'workers', 'index.html'), 'utf8');
const serverTransition = await readFile(join(root, 'production', 'server-zd-operations', 'index.html'), 'utf8');
const zhPullWorkersTransition = await readFile(
  join(root, 'zh-Hans', 'production', 'pull-workers', 'index.html'),
  'utf8',
);
const dexSkills = await readFile(join(root, 'build-with-ai', 'dex-developer-skill', 'index.html'), 'utf8');
const dexAppBuilder = await readFile(
  join(root, 'build-with-ai', 'dex-developer-skill', 'dex-app-builder', 'index.html'),
  'utf8',
);
const dexSdk = await readFile(join(root, 'build-with-ai', 'dex-developer-skill', 'dex-sdk', 'index.html'), 'utf8');
const dexConnectorContributor = await readFile(
  join(root, 'build-with-ai', 'dex-developer-skill', 'dex-connector-contributor', 'index.html'),
  'utf8',
);
const connector = await readFile(join(root, 'concepts', 'connector', 'index.html'), 'utf8');
const zhCron = await readFile(join(root, 'zh-Hans', 'design-patterns', 'durable-timer', 'cron', 'index.html'), 'utf8');
const zhDexSkills = await readFile(join(root, 'zh-Hans', 'build-with-ai', 'dex-developer-skill', 'index.html'), 'utf8');
const zhDexAppBuilder = await readFile(
  join(root, 'zh-Hans', 'build-with-ai', 'dex-developer-skill', 'dex-app-builder', 'index.html'),
  'utf8',
);
const zhDexSdk = await readFile(
  join(root, 'zh-Hans', 'build-with-ai', 'dex-developer-skill', 'dex-sdk', 'index.html'),
  'utf8',
);
const zhDexConnectorContributor = await readFile(
  join(root, 'zh-Hans', 'build-with-ai', 'dex-developer-skill', 'dex-connector-contributor', 'index.html'),
  'utf8',
);
const zhConnector = await readFile(join(root, 'zh-Hans', 'concepts', 'connector', 'index.html'), 'utf8');
const zhWhyDex = await readFile(join(root, 'zh-Hans', 'intro', 'what-is-dex', 'index.html'), 'utf8');
const zhSubflow = await readFile(join(root, 'zh-Hans', 'concepts', 'primitives', 'subflow', 'index.html'), 'utf8');
const dexSkillHostTabsSource = await readFile(join(docsRoot, 'src', 'components', 'DexSkillHostTabs.tsx'), 'utf8');
const dexSkillHostData = JSON.parse(await readFile(join(docsRoot, 'src', 'data', 'dex-skill-hosts.json'), 'utf8'));
const dexSkillSourcePaths = [
  join(docsRoot, 'content', 'build-with-ai', 'dex-developer-skill.mdx'),
  join(docsRoot, 'content', 'build-with-ai', 'dex-developer-skill', 'dex-app-builder.mdx'),
  join(docsRoot, 'content', 'build-with-ai', 'dex-developer-skill', 'dex-sdk.mdx'),
  join(docsRoot, 'content', 'build-with-ai', 'dex-developer-skill', 'dex-connector-contributor.mdx'),
  join(
    docsRoot,
    'i18n',
    'zh-Hans',
    'docusaurus-plugin-content-docs',
    'current',
    'build-with-ai',
    'dex-developer-skill.mdx',
  ),
  join(
    docsRoot,
    'i18n',
    'zh-Hans',
    'docusaurus-plugin-content-docs',
    'current',
    'build-with-ai',
    'dex-developer-skill',
    'dex-app-builder.mdx',
  ),
  join(
    docsRoot,
    'i18n',
    'zh-Hans',
    'docusaurus-plugin-content-docs',
    'current',
    'build-with-ai',
    'dex-developer-skill',
    'dex-sdk.mdx',
  ),
  join(
    docsRoot,
    'i18n',
    'zh-Hans',
    'docusaurus-plugin-content-docs',
    'current',
    'build-with-ai',
    'dex-developer-skill',
    'dex-connector-contributor.mdx',
  ),
];
const dexSkillSources = await Promise.all(dexSkillSourcePaths.map((filePath) => readFile(filePath, 'utf8')));

assert.doesNotMatch(home, /Toggle color theme/);
assert.doesNotMatch(home, /github-star-link|github-star-stat|Book a call|Book a demo/);
assert.doesNotMatch(home, /fonts\.googleapis\.com/);

// Row 1: the wordmark (to superdurable.io), search, icon-only links, and the Cloud sign-up button. Row 2: four section tabs.
const headerIconLinks = [
  ['Discord', 'https://discord.gg/V9cMHXts98'],
  ['GitHub', 'https://github.com/superdurable/dex'],
];
const signUpCloud = '<a class="sd-button sd-button-primary sd-button-small sd-signup" href="https://dex.superdurable.io/register">Sign up Cloud</a>';
const header = home.match(/<header class="[^"]*\bsd-header\b[\s\S]*?<\/header>/)?.[0] ?? '';
assert.doesNotMatch(header, /target=|superdurable\.ai/);
assert.doesNotMatch(header, /Dex Connectors|>Blog<|>About<|Log in|dex-cloud/);
assert.match(header, /<a class="sd-wordmark" aria-label="Super Durable home" href="https:\/\/superdurable\.io\/">[\s\S]*?<span>Super Durable<\/span><\/a>/);
assert.match(header, /<div class="sd-header-search"><div class="navbar__search[^"]*"[^>]*>[\s\S]*?<input placeholder="Search…"/);
const desktopActions = header.match(/<div class="sd-header-actions">[\s\S]*?<\/div><details class="sd-mobile-menu">/)?.[0] ?? '';
assert.match(desktopActions, /^<div class="sd-header-actions"><a class="sd-icon-link"/);
assert.doesNotMatch(desktopActions, /sd-locale/);
assert.match(desktopActions, /<button type="button" class="clean-btn sd-icon-link sd-theme-toggle" aria-label="Switch between light and dark"/);
assert.ok(desktopActions.includes(`</button>${signUpCloud}</div>`), 'the Cloud sign-up button ends the desktop actions');
assert.deepEqual(
  [...desktopActions.matchAll(/<a class="sd-icon-link" href="([^"]*)" aria-label="([^"]*)" title="\2">/g)].map((match) => [match[2], match[1]]),
  headerIconLinks,
);
const mobileMenu = header.match(/<details class="sd-mobile-menu">[\s\S]*?<\/details>/)?.[0] ?? '';
assert.match(mobileMenu, /<summary>Menu<\/summary>/);
assert.deepEqual(
  [...mobileMenu.matchAll(/<a class="sd-mobile-icon-link" href="([^"]*)">(?:<svg[\s\S]*?<\/svg>)([^<]*)<\/a>/g)].map((match) => [match[2], match[1]]),
  headerIconLinks,
);
assert.ok(mobileMenu.includes(signUpCloud), 'the mobile menu has the Cloud sign-up button');
assert.doesNotMatch(mobileMenu, /sd-locale/);
const headerTabs = header.match(/<nav class="sd-tabs" aria-label="Docs sections">[\s\S]*?<\/details><\/div><\/nav>/)?.[0] ?? '';
assert.deepEqual(
  [...headerTabs.matchAll(/<a class="sd-tab[^"]*"[^>]*? href="([^"]*)"[^>]*>(?:<svg[\s\S]*?<\/svg>)([^<]*)<\/a>/g)].map((match) => [match[2], match[1]]),
  [['Docs', '/'], ['Quickstart', '/quick-start/'], ['AI Agents', '/agent-frameworks/'], ['Examples', '/use-cases/']],
);
assert.match(headerTabs, /<a class="sd-tab sd-tab--active" aria-current="page" href="\/">/);
// The language switch sits at the end of the tab row and names the current language.
assert.match(headerTabs, /<\/div><details class="sd-locale"><summary class="sd-locale-summary" aria-label="Language: English">[\s\S]*?<span>English<\/span>/);

const footer = home.match(/<footer class="theme-layout-footer footer[\s\S]*?<\/footer>/)?.[0] ?? '';
assert.match(footer, /company-footer-brand[\s\S]*?>Super Durable</);
assert.match(footer, /company-footer-bottom/);
assert.match(footer, />Products</);
assert.match(footer, />Developers</);
assert.match(footer, />Company</);
assert.match(footer, />Legal</);
assert.match(footer, /href="https:\/\/superdurable\.io\/dex-cloud\/"[^>]*>Dex Cloud</);
assert.match(footer, /href="https:\/\/github\.com\/superdurable\/dex"[^>]*>GitHub</);
assert.doesNotMatch(footer, /Dex Engine|Sign up/);
assert.match(footer, /href="\/quick-start\/"/);
assert.match(footer, /href="https:\/\/superdurable\.io\/about\/#contact"/);
assert.match(footer, /Super Durable, Inc\./);
assert.doesNotMatch(footer, /superdurable\.ai|target="_blank"/);
assert.doesNotMatch(footer, /Process-centric apps|Simple, reliable|>Team<|Book a/);
assert.match(home, /English/);
assert.match(home, /简体中文/);
assert.match(home, /Build with AI/);
// Group headers that have no page of their own open their first page.
assert.match(home, /href="\/get-started\/"[^>]*>(?:(?!<\/a>)[\s\S])*Get Started<\/a>/);
assert.match(home, /href="\/evaluate\/"[^>]*>(?:(?!<\/a>)[\s\S])*Evaluate Dex<\/a>/);
assert.match(home, /href="\/build-with-ai\/dex-developer-skill\/"[^>]*>(?:(?!<\/a>)[\s\S])*Build with AI<\/a>/);

// The sidebar comes before the page content, so the first match of each label is its sidebar group.
const sidebarGroupOrder = [
  'Get Started',
  'Build with AI',
  'Durable Agents',
  'Concepts',
  'Design Patterns',
  'Examples',
  'Run Dex',
  'Evaluate Dex',
  'Developer Reference',
  'Glossary',
];
// The header's tab row repeats some labels, so the search starts after the header.
const homeAfterHeader = home.slice(home.indexOf('</header>'));
let previousGroupPosition = -1;
for (const label of sidebarGroupOrder) {
  const position = homeAfterHeader.indexOf(`>${label}<`);
  assert.ok(position > previousGroupPosition, `Sidebar group out of order or missing: ${label}`);
  previousGroupPosition = position;
}
for (const retiredLabel of ['Agent Skills', 'Agent Frameworks', 'Product Examples', 'Intro and Quick Start']) {
  assert.ok(!home.includes(`>${retiredLabel}<`), `Retired sidebar label still present: ${retiredLabel}`);
}

const zhHome = await readFile(join(root, 'zh-Hans', 'index.html'), 'utf8');
for (const page of [home, zhHome, cron, zhCron]) {
  assert.match(page, /<a class="sd-icon-link" href="https:\/\/github\.com\/superdurable\/dex"/);
  assert.match(page, /<nav class="sd-tabs"/);
}
assert.match(zhHome, /English/);
assert.match(zhHome, /简体中文/);
assert.doesNotMatch(zhHome, /superdurable\.ai/);
for (const label of ['Dex 文档', '菜单', '文档', '快速开始', 'AI 智能体', '示例', '产品', '开发者', '公司', '引擎对比', '联系我们']) {
  assert.ok(zhHome.includes(label), `Missing Chinese shell label: ${label}`);
}
assert.match(zhHome, /href="\/zh-Hans\/quick-start\/"/);

assert.match(cloud, /Dex Cloud \/ BYOC/);
assert.match(cloud, /Coming Soon/);
assert.match(cloud, /Explore Dex OSS Docs/);
assert.match(cloud, />Managed Dex Engine <span/);
assert.doesNotMatch(cloud, /View Dex BYOC/);
assert.match(cloud, /https:\/\/superdurable\.ai\/dex-engine\//);
assert.match(cron, /docs-flow-canvas-placeholder/);
assert.match(cron, /CronScheduleFlow/);
assert.match(zhCron, /docs-flow-canvas-placeholder/);
assert.match(zhCron, /CronScheduleFlow/);
assert.match(whyDex, /docs-flow-canvas-placeholder/);
assert.match(whyDex, /OrderProcessingFlow/);
assert.match(zhWhyDex, /docs-flow-canvas-placeholder/);
assert.match(zhWhyDex, /OrderProcessingFlow/);
assert.match(components, /rel="canonical" href="https:\/\/docs\.superdurable\.io\/components\/"/);
assert.match(referenceLanding, /rel="canonical" href="https:\/\/docs\.superdurable\.io\/reference\/"/);
assert.match(referenceLanding, /id="compatibility-matrix"/);
assert.match(zhReferenceLanding, /rel="canonical" href="https:\/\/docs\.superdurable\.io\/zh-Hans\/reference\/"/);
assert.match(zhReferenceLanding, /id="compatibility-matrix"/);
// The Developer reference is English only, so zh-Hans serves the English text at its own URL.
assert.match(serverReference, /rel="canonical" href="https:\/\/docs\.superdurable\.io\/reference\/server\/workers\/"/);
assert.match(
  zhServerReference,
  /rel="canonical" href="https:\/\/docs\.superdurable\.io\/zh-Hans\/reference\/server\/workers\/"/,
);
assert.match(zhServerReference, /id="step-task-queue"/);
assert.match(zhServerReference, /Workers and the task queue/);
// Transition pages keep every old section id so inbound anchors still resolve.
// The inventory lists the ids of the pages before they moved.
const transitionAnchors = JSON.parse(await readFile(join(docsRoot, 'tests', 'transition-anchors.json'), 'utf8'));
for (const [route, ids] of Object.entries(transitionAnchors)) {
  const html = await readFile(join(root, ...route.split('/').filter(Boolean), 'index.html'), 'utf8');
  assert.equal(metaRefreshTarget(html), undefined, `${route} must stay a transition page, not a redirect`);
  for (const id of ids) {
    assert.ok(html.includes(`id="${id}"`), `${route} lost the old anchor #${id}`);
  }
}
// Old Primitives URLs keep every section id at the page they now resolve to.
const movedPrimitiveAnchors = JSON.parse(
  await readFile(join(docsRoot, 'tests', 'moved-primitive-anchors.json'), 'utf8'),
);
for (const [route, ids] of Object.entries(movedPrimitiveAnchors)) {
  const redirectTarget = expectedRedirects.get(route);
  const finalRoute = redirectTarget ? new URL(redirectTarget).pathname : route;
  const html = await readFile(join(root, ...finalRoute.split('/').filter(Boolean), 'index.html'), 'utf8');
  for (const id of ids) {
    assert.ok(html.includes(`id="${id}"`), `${route}#${id} does not resolve at ${finalRoute}`);
  }
}
assert.match(serverTransition, /href="\/reference\/server\/configuration\/#history-window"/);
assert.match(zhPullWorkersTransition, /id="readiness"/);
assert.match(zhPullWorkersTransition, /id="enable-pull-mode"/);
assert.match(dexSkills, /Build with AI/);
assert.match(dexSkills, /superdurable\/dex-skills/);
assert.match(dexSkills, /dex-sdk/);
assert.match(dexSkills, /dex-app-builder/);
assert.match(dexSkills, /dex-connector-contributor/);
assert.match(dexSkills, /primary skill/i);
assert.match(dexSkills, /Requires a coding workspace/);
assert.match(dexSkills, /writable repository or project, file-editing tools, and command execution/);
assert.match(dexSkills, /https:\/\/github\.com\/superdurable\/dex-skills/);

// The full install lives once, on the Build with AI overview. Each skill page links to it and shows its own invocation.
assertDexSkillHostTabs(dexSkills, 'Other clients', false);
assertDexSkillHostTabs(zhDexSkills, '其他客户端', true);
for (const [page, skill] of [[dexAppBuilder, 'dex-app-builder'], [dexSdk, 'dex-sdk'],
  [dexConnectorContributor, 'dex-connector-contributor'], [zhDexAppBuilder, 'dex-app-builder'], [zhDexSdk, 'dex-sdk'],
  [zhDexConnectorContributor, 'dex-connector-contributor']]) {
  assert.equal((page.match(/role="tab"/g) ?? []).length, 0, `${skill} page repeats the install tabs`);
  assert.match(page, /href="\/(?:zh-Hans\/)?build-with-ai\/dex-developer-skill\/#install-and-use-dex-skills"/);
  assert.match(htmlText(page), new RegExp(`\\$${skill} `));
  assert.match(htmlText(page), new RegExp(`/superdurable-dex:${skill} `));
}

assert.match(dexSkillHostTabsSource, /defaultValue=\{hosts\[0\]\.id\}/);
assert.match(dexSkillHostTabsSource, /groupId="dex-skill-host"/);
assert.match(dexSkillHostTabsSource, /queryString="host"/);
assert.match(dexSkillHostTabsSource, /<TabItem key=\{host\.id\} label=\{host\.label\} value=\{host\.id\}>/);
for (const {hosts} of Object.values(dexSkillHostData.locales)) {
  assert.deepEqual(hosts.map((host) => host.id), ['codex-desktop', 'codex-cli', 'claude-desktop', 'claude-cli', 'cursor', 'others']);
}
assert.doesNotMatch(dexSkillHostTabsSource, /\blazy(?:=|\s)/);
for (const source of dexSkillSources) {
  // Only the overview carries the install tabs; skill pages link to it.
  if (/^id: dex-developer-skill$/m.test(source)) assert.match(source, /<DexSkillHostTabs skill="all" \/>/);
  else assert.doesNotMatch(source, /<DexSkillHostTabs/);
  assert.doesNotMatch(source, /^## (?:Install for|安装到) (?:Codex|Claude|Cursor)$/m);
  assert.doesNotMatch(source, /\[(?:installation overview|安装总览)\]/i);
}

const dexSkillsText = htmlText(dexSkills);
assert.match(dexSkillsText, /\$dex-app-builder Design and build an event-registration application\./);
assert.match(dexSkillsText, /\$dex-sdk Diagnose why this Flow retries after the payment Step completes\./);
assert.match(dexSkillsText, /\$dex-connector-contributor Add <XYZ> to Dex official connector library/);
assert.match(dexSkillsText, /\/superdurable-dex:dex-connector-contributor Add <XYZ>/);
assert.doesNotMatch(dexSkillsText, /Add this Trigger to the official Slack Connector/);

assert.match(htmlText(dexAppBuilder), /\$dex-app-builder Design and build an event-registration application\./);
assert.match(htmlText(dexSdk), /\$dex-sdk Diagnose why this Flow retries after the payment Step completes\./);
assert.match(
  htmlText(dexConnectorContributor),
  /\$dex-connector-contributor Add <XYZ> to Dex official connector library/,
);
for (const page of [dexAppBuilder, zhDexAppBuilder]) {
  assertSkillInvocationExamples(page, 'dex-app-builder', 'Design and build an event-registration application.');
}
for (const page of [dexSdk, zhDexSdk]) {
  assertSkillInvocationExamples(page, 'dex-sdk', 'Diagnose why this Flow retries after the payment Step completes.');
}
for (const page of [dexConnectorContributor, zhDexConnectorContributor]) {
  assertSkillInvocationExamples(page, 'dex-connector-contributor', 'Add <XYZ> to Dex official connector library');
}
assert.ok(dexSkills.indexOf('dex-app-builder') < dexSkills.indexOf('dex-sdk'));
assert.match(dexSkills, /dex-developer-skill\/dex-app-builder/);
assert.match(dexSkills, /dex-developer-skill\/dex-sdk/);
assert.match(dexSkills, /dex-developer-skill\/dex-connector-contributor/);
assert.match(dexAppBuilder, /primary skill/i);
assert.match(dexAppBuilder, /Choose the application surface/);
assert.match(dexAppBuilder, /call external providers only from <code>Execute<\/code>/);
assert.match(dexSdk, /Version and source authority/);
assert.match(dexSdk, /Python, Go, Java, TypeScript, and Rust/);
assert.match(dexSdk, /the skill only reads by default/);
assert.match(dexConnectorContributor, /Repository and fork workflow/);
assert.match(dexConnectorContributor, /Update <code>connector.yaml<\/code> before writing provider code/);
assert.match(dexConnectorContributor, /not ready for production until the Connector is released/);
assert.match(dexConnectorContributor, /needs a Go implementation/);
assert.match(dexConnectorContributor, /https:\/\/github\.com\/superdurable\/dex-connectors-library\/blob\/main\/AGENTS\.md/);
assert.match(zhDexSkills, /使用 AI 构建/);
assert.match(zhDexSkills, /主要 skill/);
assert.match(zhDexSkills, /需要代码工作区/);
assert.match(zhDexSkills, /写入 repository 或项目目录/);
assert.ok(zhDexSkills.indexOf('dex-app-builder') < zhDexSkills.indexOf('dex-sdk'));
assert.match(zhDexAppBuilder, /主要 skill/);
assert.match(zhDexAppBuilder, /选择应用界面/);
assert.match(zhDexSdk, /版本与来源/);
assert.match(zhDexSdk, /skill 默认只读/);
assert.match(zhDexConnectorContributor, /Repository 与 Fork 工作流/);
assert.match(zhDexConnectorContributor, /在 Connector 以确切版本\s*发布之前，应用不能进入生产/);
assert.match(zhDexConnectorContributor, /必须有 Go 实现/);
assert.match(zhDexConnectorContributor, /https:\/\/github\.com\/superdurable\/dex-connectors-library\/blob\/main\/AGENTS\.md/);
assert.match(connector, /ready-made Steps/);
assert.match(connector, /Connectors support Go, Python, and TypeScript/);
assert.match(connector, /https:\/\/github\.com\/superdurable\/dex-connectors-library\/issues\/new/);
assert.match(connector, /rel="canonical" href="https:\/\/docs\.superdurable\.io\/concepts\/connector\/"/);
assert.match(zhConnector, /现成 Step/);
assert.match(zhConnector, /Connector 支持 Go、Python 和 TypeScript/);
assert.match(zhConnector, /https:\/\/github\.com\/superdurable\/dex-connectors-library\/issues\/new/);
assert.match(zhConnector, /rel="canonical" href="https:\/\/docs\.superdurable\.io\/zh-Hans\/concepts\/connector\/"/);
assert.match(zhSubflow, /rel="canonical" href="https:\/\/docs\.superdurable\.io\/zh-Hans\/concepts\/primitives\/subflow\/"/);

await Promise.all([
  access(join(root, 'intro', 'compared-to-database', 'index.html')),
  access(join(root, 'intro', 'what-is-dex', 'index.html')),
  access(join(root, 'quick-start', 'index.html')),
  access(join(root, 'concepts', 'index.html')),
  access(join(root, 'primitives', 'connector', 'index.html')),
  access(join(root, 'concepts', 'step', 'index.html')),
  access(join(root, 'reference', 'cli', 'index.html')),
  access(join(root, 'reference', 'web', 'flow-visualization', 'index.html')),
  access(join(root, 'reference', 'go', 'workers', 'index.html')),
  access(join(root, 'reference', 'agents', 'workers', 'index.html')),
  access(join(root, 'zh-Hans', 'components', 'application', 'index.html')),
  access(join(root, 'zh-Hans', 'reference', 'rust', 'index.html')),
  access(join(root, 'build-with-ai', 'dex-developer-skill', 'index.html')),
  access(join(root, 'build-with-ai', 'dex-developer-skill', 'dex-app-builder', 'index.html')),
  access(join(root, 'build-with-ai', 'dex-developer-skill', 'dex-sdk', 'index.html')),
  access(join(root, 'build-with-ai', 'dex-developer-skill', 'dex-connector-contributor', 'index.html')),
  access(join(root, 'zh-Hans', 'intro', 'compared-to-database', 'index.html')),
  access(join(root, 'zh-Hans', 'intro', 'what-is-dex', 'index.html')),
  access(join(root, 'zh-Hans', 'quick-start', 'index.html')),
  access(join(root, 'zh-Hans', 'primitives', 'connector', 'index.html')),
  access(join(root, 'zh-Hans', 'build-with-ai', 'dex-developer-skill', 'index.html')),
  access(join(root, 'zh-Hans', 'build-with-ai', 'dex-developer-skill', 'dex-app-builder', 'index.html')),
  access(join(root, 'zh-Hans', 'build-with-ai', 'dex-developer-skill', 'dex-sdk', 'index.html')),
  access(join(root, 'zh-Hans', 'build-with-ai', 'dex-developer-skill', 'dex-connector-contributor', 'index.html')),
]);

const outputFiles = await collectOutputFiles(root);
const outputFilePaths = new Set(outputFiles.map((filePath) => relativeOutputPath(filePath)));
const htmlIndexFiles = outputFiles.filter((filePath) =>
  filePath.endsWith(`${sep}index.html`) && !relativeOutputPath(filePath).startsWith('api/'));
await auditApiReferenceIndexPages();
await auditPythonGenerationLocks();
if (outputFiles.some((filePath) => relativeOutputPath(filePath).startsWith('api/'))) {
  await auditApiReferences(join(root, 'api'));
}
const canonicalPages = new Map();
const archivedCanonicalUrls = new Set();
const actualRedirects = new Map();

for (const filePath of htmlIndexFiles) {
  const route = routeFromIndexFile(filePath);
  const html = await readFile(filePath, 'utf8');
  const refreshTarget = metaRefreshTarget(html);
  const canonicalLinks = linkTargets(html, 'canonical');

  assert.equal(canonicalLinks.length, 1, `${route} must have exactly one canonical URL`);

  if (refreshTarget !== undefined) {
    const expectedTarget = expectedRedirects.get(route);
    assert.ok(expectedTarget, `${route} is an undeclared redirect`);
    assert.equal(new URL(refreshTarget, siteOrigin).href, expectedTarget);
    assert.equal(new URL(canonicalLinks[0], siteOrigin).href, expectedTarget);
    actualRedirects.set(route, expectedTarget);
    continue;
  }

  const expectedCanonical = new URL(route, siteOrigin).href;
  assert.equal(canonicalLinks[0], expectedCanonical, `${route} has a non-self-referencing canonical URL`);
  assert.deepEqual(
    alternateLanguageLinks(html),
    expectedAlternateLanguageLinks(route),
    `${route} has incorrect alternate-language URLs`,
  );
  const referenceMatch = route.match(/^\/(?:zh-Hans\/)?reference\/(server|cli|web|go|python|java|typescript|rust|agents)\/(?:(\d+\.\d+\.(?:x|\d+))\/)?/);
  const isArchivedReference = Boolean(referenceMatch?.[2]);
  const selectorMarkers = html.match(/data-reference-version-selector=/g) ?? [];
  assert.equal(selectorMarkers.length, referenceMatch ? 1 : 0, `${route} has an incorrect sidebar version selector`);
  if (referenceMatch) {
    const [, component, version] = referenceMatch;
    const sidebarHtml = html.match(/<nav aria-label="[^"]*" class="menu thin-scrollbar[\s\S]*?<\/nav>/)?.[0] ?? '';
    assert.match(sidebarHtml, />Design Patterns</, `${route} must show the main docs sidebar`);
    assert.match(sidebarHtml, /menu__link--active[^>]*aria-current="page"|aria-current="page"[^>]*menu__link--active/,
      `${route} must highlight its page in the sidebar`);
    const select = html.match(/<select\b[^>]*>[\s\S]*?<\/select>/)?.[0];
    assert.ok(select, `${route} needs a native version select`);
    const versionNames = [...select.matchAll(/<option[^>]*value="([^"]+)"/g)].map((match) => match[1]);
    assert.deepEqual(versionNames, referenceVersions.get(component), `${route} must list its own versions newest first`);
    if (version) assert.ok(referenceVersions.get(component).includes(version), `${route} has an undeclared version`);
    assert.ok(html.includes(latestReferenceVersions[component]), `${route} needs the latest component version`);
    if (isArchivedReference) {
      archivedCanonicalUrls.add(expectedCanonical);
      assert.ok(select.includes(`value="${version}" selected=""`), `${route} must select its own version`);
      assert.match(html, /data-reference-version-banner/);
      const robotsMeta = htmlTags(html, 'meta').find((tag) => attributeValue(tag, 'name') === 'robots');
      assert.equal(attributeValue(robotsMeta, 'content'), 'noindex, nofollow');
    } else {
      assert.match(select, /value="current" selected=""/);
      assert.doesNotMatch(html, /data-reference-version-banner/);
    }
  }
  assert.equal(hasNoIndexOrNoFollow(html), isArchivedReference, `${route} has incorrect indexing directives`);
  assert.equal(canonicalPages.has(expectedCanonical), false, `${route} duplicates canonical ${expectedCanonical}`);
  canonicalPages.set(expectedCanonical, {html, route});
}

assert.deepEqual(
  [...actualRedirects.entries()].sort(),
  [...expectedRedirects.entries()].sort(),
  'the production build must contain every declared redirect and no undeclared redirects',
);

for (const [canonical, {html, route}] of canonicalPages) {
  for (const href of anchorTargets(html)) {
    const target = new URL(href, canonical);
    if (target.origin !== siteOrigin) {
      continue;
    }
    const isRoute = canonicalPages.has(new URL(target.pathname, siteOrigin).href) ||
      outputFilePaths.has(decodeURIComponent(target.pathname).replace(/^\//, '')) ||
      isRetainedApiReferencePath(target.pathname);
    // Archived snapshots are frozen, so their links may reach a moved page through its declared redirect.
    if (archivedCanonicalUrls.has(canonical)) {
      const redirectTarget = expectedRedirects.get(routeWithTrailingSlash(target.pathname));
      assert.ok(isRoute || canonicalPages.has(redirectTarget) || isApiReferenceVersionPath(target.pathname),
        `${route} links to missing route ${target.pathname}`);
      continue;
    }
    assert.equal(expectedRedirects.has(target.pathname), false, `${route} links to redirect ${target.pathname}`);
    assert.ok(isRoute, `${route} links to missing route ${target.pathname}`);
  }
}

const sitemapFiles = outputFiles.filter((filePath) => filePath.endsWith(`${sep}sitemap.xml`));
const sitemapUrls = (
  await Promise.all(
    sitemapFiles.map(async (filePath) => {
      const sitemap = await readFile(filePath, 'utf8');
      return [...sitemap.matchAll(/<loc>([^<]+)<\/loc>/g)].map((match) => match[1]);
    }),
  )
).flat();
assert.equal(new Set(sitemapUrls).size, sitemapUrls.length, 'sitemap URLs must be unique');
assert.deepEqual(
  [...sitemapUrls].sort(),
  [...canonicalPages.keys()].filter((url) => !archivedCanonicalUrls.has(url)).sort(),
  'sitemap must contain current canonical URLs and exclude archived references, redirects, and duplicate variants',
);

const markdownParser = unified().use(remarkParse).use(remarkFrontmatter).use(remarkGfm);
const sourceParser = unified().use(remarkParse).use(remarkMdx).use(remarkFrontmatter).use(remarkGfm);
let markdownPages = 0;
const indexableMarkdownUrls = new Set();
for (const [canonical, {html, route}] of canonicalPages) {
  if (!html.includes('theme-doc-markdown')) continue;
  const locale = route.startsWith('/zh-Hans/') ? 'zh-Hans' : 'en';
  const pathname = markdownPath(route, locale === 'zh-Hans' ? '/zh-Hans/' : '/');
  const markdown = await readFile(join(root, pathname.slice(1)), 'utf8');
  const metadata = Object.fromEntries([...markdown.matchAll(/^(title|description|canonical_url|source|locale): (.+)$/gm)]
    .map(([, key, value]) => [key, JSON.parse(value)]));
  assert.equal(metadata.canonical_url, canonical, `${pathname} has incorrect HTML canonical`);
  assert.equal(metadata.locale, locale);
  assert.ok(metadata.title && metadata.source, `${pathname} is missing source metadata`);
  const descriptionTag = htmlTags(html, 'meta').find((tag) => attributeValue(tag, 'name') === 'description');
  const metaDescription = htmlText(attributeValue(descriptionTag ?? '', 'content') ?? '').replace(/\s+/g, ' ').trim();
  assert.equal(metadata.description ?? '', metaDescription, `${pathname} description must match the HTML meta description`);
  const markdownLinkLabel = locale === 'zh-Hans' ? '查看 Markdown（在新标签页中打开）' : 'View as Markdown (opens in a new tab)';
  const markdownLinks = [...html.matchAll(/<a [^>]*target="_blank" rel="noopener" aria-label="([^"]+)"[^>]*>/g)]
    .filter(([tag]) => tag.includes(`href="${pathname}"`));
  assert.deepEqual(markdownLinks.map(([, label]) => label), [markdownLinkLabel], `${pathname} needs one localized robot link`);
  assert.doesNotMatch(markdownLinks[0][0], /\stitle=/, `${pathname} robot link must not repeat its label in title`);
  assert.ok(html.indexOf('<h1') >= 0 && html.indexOf('<h1') < markdownLinks[0].index, `${pathname} robot link must follow the h1`);
  const auditMarkdownFile = (filePath, text) => {
    const blocks = [];
    visitMarkdown(markdownParser.parse(text), node => {
      if (node.type === 'code') blocks.push(node);
      if (node.type === 'html') assert.match(node.value, /^(?:<a id="[^"<>]+">(?:<\/a>)?|<\/a>)$/, `${filePath} contains leftover JSX/HTML`);
      if (node.type === 'text') assert.doesNotMatch(node.value, /^\s*:::/m, `${filePath} contains a leftover admonition fence`);
      if (['link', 'definition', 'image'].includes(node.type)) {
        assert.match(node.url, /^(?:https?:\/\/|mailto:)/, `${filePath} contains non-absolute URL ${node.url}`);
        const target = new URL(node.url);
        if (target.origin === siteOrigin) {
          assert.ok(canonicalPages.has(new URL(target.pathname, siteOrigin).href) ||
            outputFilePaths.has(decodeURIComponent(target.pathname).slice(1)) || isRetainedApiReferencePath(target.pathname) ||
            (archivedCanonicalUrls.has(canonical) && isApiReferenceVersionPath(target.pathname)),
            `${filePath} links to missing ${target.pathname}`);
        }
      }
    });
    return blocks;
  };
  const codeBlocks = auditMarkdownFile(pathname, markdown);
  const source = await readFile(join(docsRoot, '..', metadata.source), 'utf8');
  const sourceTree = sourceParser.parse(prepareSource(source, {headingIds: new Map(), locale, sourcePath: metadata.source}));
  const groups = [];
  // SdkTabs inside a FrameworkTab belong to that framework's Markdown files only.
  const markFramework = (node, framework) => {
    const own = node.name === 'FrameworkTab' ? node.attributes.find(attribute => attribute.name === 'framework')?.value : framework;
    if (own) node.framework = own;
    node.children?.forEach(child => markFramework(child, own));
  };
  markFramework(sourceTree, undefined);
  visitMarkdown(sourceTree, node => {
    if (node.name !== 'SdkTabs' && !(node.name === 'SdkSnippet' && !node.isInsideSdkTabs)) return;
    const snippets = node.name === 'SdkSnippet' ? [node] : node.children.filter(child => child.name === 'SdkSnippet');
    snippets.forEach(snippet => { snippet.isInsideSdkTabs = node.name === 'SdkTabs'; });
    groups.push(Object.assign(new Map(snippets.map(snippet => {
      const language = snippet.attributes.find(attribute => attribute.name === 'lang')?.value;
      return [language, snippet.children.filter(child => child.type === 'code')
        .map(block => ({lang: block.lang || language, value: block.value}))];
    })), {framework: node.framework}));
  });
  const assertBlocksInOrder = (filePath, blocks, expectedBlocks) => {
    let codeCursor = 0;
    for (const expected of expectedBlocks) {
      while (codeCursor < blocks.length && !(blocks[codeCursor].lang === expected.lang && blocks[codeCursor].value === expected.value)) codeCursor++;
      assert.ok(codeCursor < blocks.length, `${filePath} lost or reordered the ${expected.lang} SDK snippet`);
      codeCursor++;
    }
  };
  // Prop-only SdkTabs have no SdkSnippet children here, so the exporter's own test covers them.
  const pageFrameworks = frameworkOrder.filter(framework => groups.some(group => group.framework === framework));
  for (const framework of pageFrameworks.length > 0 ? pageFrameworks : [undefined]) {
    const isDefaultFramework = framework === undefined || framework === pageFrameworks[0];
    const frameworkGroups = groups.filter(group => !group.framework || group.framework === framework);
    // A framework's files follow the languages of its own tabs, as the exporter does.
    const ownGroups = frameworkGroups.filter(group => framework && group.framework === framework);
    const languageGroups = ownGroups.some(group => group.size > 0) ? ownGroups : frameworkGroups;
    const pageLanguages = sdkOrder.filter(language => languageGroups.some(group => group.has(language)));
    const filePath = language => {
      const first = language === undefined || language === pageLanguages[0];
      if (isDefaultFramework) return first ? pathname : languageMarkdownPath(pathname, language);
      return first ? frameworkMarkdownPath(pathname, framework) : frameworkMarkdownPath(pathname, framework, language);
    };
    const readFileFor = async language => {
      if (filePath(language) === pathname) return {text: markdown, blocks: codeBlocks};
      const text = await readFile(join(root, filePath(language).slice(1)), 'utf8');
      assert.equal(JSON.parse(text.match(/^canonical_url: (.+)$/m)?.[1] ?? 'null'), canonical,
        `${filePath(language)} has incorrect HTML canonical`);
      if (framework) assert.match(text, new RegExp(`^framework: "${framework}"$`, 'm'), `${filePath(language)} must name its framework`);
      return {text, blocks: auditMarkdownFile(filePath(language), text)};
    };
    // A group without the file's language becomes a one-line note instead of code.
    const blocksFor = language => frameworkGroups.flatMap(group => group.get(language) ?? []);
    if (pageLanguages.length > 1) {
      // The first language is the framework's own file; every other language gets its own file.
      for (const language of pageLanguages) {
        const {text, blocks} = await readFileFor(language);
        assert.match(text, new RegExp(`^language: "${language}"$`, 'm'), `${filePath(language)} must name its language`);
        assertBlocksInOrder(filePath(language), blocks, blocksFor(language));
      }
    } else {
      const {blocks} = await readFileFor(pageLanguages[0]);
      const expected = pageLanguages.length === 1 ? blocksFor(pageLanguages[0])
        : frameworkGroups.flatMap(group => [...group.values()].flat());
      assertBlocksInOrder(filePath(pageLanguages[0]), blocks, expected);
    }
  }
  markdownPages++;
  // A page that only says it moved stays out of llms.txt.
  const isMovedNotice = /\n# [^\n]+\n\n(?:This page moved|本页已经?移)/.test(markdown);
  if (!archivedCanonicalUrls.has(canonical) && !isMovedNotice) indexableMarkdownUrls.add(new URL(pathname, siteOrigin).href);
}
const indexedMarkdown = new Set();
for (const localeRoot of ['', 'zh-Hans/']) {
  const index = await readFile(join(root, localeRoot, 'llms.txt'), 'utf8');
  for (const [, href] of index.matchAll(/\]\((https?:[^)]+\.md)\)/g)) {
    const target = new URL(href);
    assert.equal(target.origin, siteOrigin);
    assert.ok(outputFilePaths.has(decodeURIComponent(target.pathname).slice(1)), `llms.txt links to missing ${href}`);
  }
  // Every llms-full file llms.txt names exists, and each page with framework files answers to its default
  // framework's name too, so a reader can build any file name the index describes.
  for (const [name] of index.matchAll(/llms-full(?:\.[a-z]+)?\.txt/g)) {
    assert.ok(outputFilePaths.has(`${localeRoot}${name}`), `llms.txt names missing ${localeRoot}${name}`);
  }
  for (const [, href] of index.matchAll(/^- \[[^\n]*?\]\((https?:[^)]+\.md)\)[^\n]*(?:By framework: |按框架：)/gm)) {
    const filePath = decodeURIComponent(new URL(href).pathname).slice(1);
    const framework = (await readFile(join(root, filePath), 'utf8')).match(/^framework: "([^"]+)"$/m)?.[1];
    assert.ok(framework, `${filePath} must name its framework`);
    const alias = filePath.replace(/\.md$/, `.${framework}.md`);
    assert.ok(outputFilePaths.has(alias), `${filePath} needs its default-framework alias ${alias}`);
  }
  // Each entry's first link is the page; per-framework links after it point at that page's other files.
  for (const [, href] of index.matchAll(/^- \[[^\n]*?\]\((https?:[^)]+\.md)\)/gm)) {
    assert.equal(indexedMarkdown.has(href), false, `llms.txt duplicates ${href}`);
    indexedMarkdown.add(href);
  }
}
assert.deepEqual([...indexedMarkdown].sort(), [...indexableMarkdownUrls].sort(),
  'llms.txt indexes must cover every indexable docs page exactly once and omit archived versions');
assert.ok(sitemapUrls.every(href => !new URL(href).pathname.endsWith('.md')), 'Markdown twins must stay outside sitemaps');
// A concept page's Go file links the Go reference, and the note about the Python reference stays in the Python file.
{
  const stepGo = await readFile(join(root, 'concepts/step.go.md'), 'utf8');
  assert.match(stepGo, /\]\(https:\/\/docs\.superdurable\.io\/reference\/go\/step\.md\)/);
  assert.doesNotMatch(stepGo, /reference\/python\/step\.md|The links below open the Python reference/);
  const stepPython = await readFile(join(root, 'concepts/step.md'), 'utf8');
  assert.match(stepPython, /The links below open the Python reference/);
  assert.equal(await readFile(join(root, 'concepts/step.python.md'), 'utf8'), stepPython);
  assert.match(stepPython, /^---\n[\s\S]*?\n---\n\n> Index of every page for AI agents: https:\/\/docs\.superdurable\.io\/llms\.txt\n/);
}
console.log(`Markdown audit passed for ${markdownPages} docs pages in both locales, SDK snippets, and llms.txt indexes.`);

const robots = await readFile(join(root, 'robots.txt'), 'utf8');
assert.match(robots, /^User-agent: \*$/m);
assert.match(robots, /^Allow: \/$/m);
assert.doesNotMatch(robots, /^Disallow:\s*\/$/m);
assert.match(robots, /^Sitemap: https:\/\/docs\.superdurable\.io\/sitemap\.xml$/m);
assert.match(robots, /^Sitemap: https:\/\/docs\.superdurable\.io\/zh-Hans\/sitemap\.xml$/m);

console.log(
  `Docs shell and SEO audit passed for ${canonicalPages.size} canonical routes and ${actualRedirects.size} redirects.`,
);

function assertDexSkillHostTabs(html, otherClientsLabel, isChinese) {
  const tabLabels = [...html.matchAll(/role="tab"[^>]*>([^<]+)<\/li>/g)].map((match) => match[1]);
  assert.deepEqual(tabLabels, [
    'Codex Desktop',
    'Codex CLI',
    'Claude Desktop',
    'Claude Code CLI',
    'Cursor',
    otherClientsLabel,
  ]);
  assert.match(html, /role="tab"[^>]*aria-selected="true"[^>]*>Codex Desktop<\/li>/);
  assert.equal((html.match(/role="tabpanel"/g) ?? []).length, 6);
  // Only string CodeBlock children render the theme-code-block container with its copy button.
  const codeBlocks = (html.match(/class="[^"]*\btheme-code-block\b/g) ?? []).length;
  assert.ok(codeBlocks >= 8, 'skill host code blocks must render as highlighted CodeBlocks');
  assert.equal((html.match(/<pre\b/g) ?? []).length, codeBlocks, 'every skill host code block needs CodeBlock highlighting and a copy button');

  const text = htmlText(html);
  assert.match(text, /@Dex /);
  assert.match(text, /\/superdurable-dex:(?:dex-app-builder|dex-sdk|dex-connector-contributor) /);
  assert.match(text, /codex plugin marketplace add superdurable\/dex-skills/);
  assert.match(text, /codex plugin add superdurable-dex@superdurable/);
  assert.match(text, /npx skills add superdurable\/dex-skills --skill '\*' --agent codex --global/);
  assert.match(text, /claude plugin marketplace add superdurable\/dex-skills/);
  assert.match(text, /claude plugin install superdurable-dex@superdurable/);
  assert.match(text, /npx skills add superdurable\/dex-skills --skill '\*' --agent claude-code --global/);
  assert.match(text, /cursor-agent/);
  assert.match(text, /npx skills add superdurable\/dex-skills --skill '\*' --agent cursor --global/);
  assert.match(text, /npx skills add superdurable\/dex-skills --skill '\*' --global/);
  assert.match(text, /npx skills add superdurable\/dex-skills --skill '\*' --agent <agent-id> --global/);
  assert.match(text, /npx skills update dex-app-builder dex-sdk dex-connector-contributor --global/);
  assert.match(text, isChinese ? /Plugin（推荐）/ : /Plugin \(recommended\)/);
  assert.match(text, /Standalone Skills/);
  assert.match(text, isChinese ? /安装.*使用.*更新/s : /Install.*Use.*Update/s);
}

function assertSkillInvocationExamples(html, skillName, prompt) {
  const text = htmlText(html);
  // Skill pages show the Codex and Claude Code Plugin invocations; the overview lists every host.
  assert.ok(text.includes(`$${skillName} ${prompt}`));
  assert.ok(text.includes(`/superdurable-dex:${skillName} ${prompt}`));
  for (const otherPrompt of [
    'Design and build an event-registration application.',
    'Diagnose why this Flow retries after the payment Step completes.',
    'Add <XYZ> to Dex official connector library',
  ]) {
    if (otherPrompt !== prompt) {
      assert.equal(text.includes(otherPrompt), false);
    }
  }
}

function headerLinks(html) {
  return [...html.matchAll(/<a\b[^>]*\bhref="([^"]*)"[^>]*>([^<]*)<\/a>/g)].map((match) => [match[2], match[1]]);
}

function htmlText(html) {
  return html
    .replace(/<[^>]*>/g, '')
    .replaceAll('&lt;', '<')
    .replaceAll('&gt;', '>')
    .replaceAll('&#x27;', "'")
    .replaceAll('&quot;', '"')
    .replaceAll('&amp;', '&');
}

async function collectOutputFiles(directory) {
  const entries = await readdir(directory, {withFileTypes: true});
  const files = await Promise.all(
    entries.map((entry) => {
      const entryPath = join(directory, entry.name);
      return entry.isDirectory() ? collectOutputFiles(entryPath) : [entryPath];
    }),
  );
  return files.flat();
}

function relativeOutputPath(filePath) {
  return relative(root, filePath).split(sep).join('/');
}

function routeFromIndexFile(filePath) {
  const outputPath = relativeOutputPath(filePath);
  if (outputPath === 'index.html') {
    return '/';
  }
  return `/${outputPath.slice(0, -'index.html'.length)}`;
}

function routeWithTrailingSlash(pathname) {
  const route = new URL(pathname, siteOrigin).pathname.replace(/\/+$/, '');
  return route === '' ? '/' : `${route}/`;
}

function htmlTags(html, tagName) {
  return html.match(new RegExp(`<${tagName}\\b[^>]*>`, 'gi')) ?? [];
}

function attributeValue(tag, attributeName) {
  const match = tag.match(new RegExp(`\\b${attributeName}=(['"])(.*?)\\1`, 'i'));
  return match?.[2];
}

function linkTargets(html, relationship) {
  return htmlTags(html, 'link')
    .filter((tag) => attributeValue(tag, 'rel')?.split(/\s+/).includes(relationship))
    .map((tag) => attributeValue(tag, 'href'))
    .filter((href) => href !== undefined);
}

function anchorTargets(html) {
  return htmlTags(html, 'a')
    .map((tag) => attributeValue(tag, 'href'))
    .filter((href) => href !== undefined && !/^(?:mailto:|tel:|javascript:)/i.test(href));
}

function alternateLanguageLinks(html) {
  const entries = htmlTags(html, 'link')
    .filter((tag) => attributeValue(tag, 'rel')?.split(/\s+/).includes('alternate'))
    .map((tag) => [attributeValue(tag, 'hreflang'), attributeValue(tag, 'href')]);
  assert.equal(
    new Set(entries.map(([language]) => language)).size,
    entries.length,
    'alternate-language declarations must be unique',
  );
  return Object.fromEntries(entries);
}

function expectedAlternateLanguageLinks(route) {
  const unlocalizedRoute = route.startsWith('/zh-Hans/')
    ? route.slice('/zh-Hans'.length)
    : route;
  const englishUrl = new URL(unlocalizedRoute, siteOrigin).href;
  const chineseUrl = new URL(`/zh-Hans${unlocalizedRoute}`, siteOrigin).href;
  return {
    en: englishUrl,
    'zh-Hans': chineseUrl,
    'x-default': englishUrl,
  };
}

function metaRefreshTarget(html) {
  const refreshTag = htmlTags(html, 'meta').find(
    (tag) => attributeValue(tag, 'http-equiv')?.toLowerCase() === 'refresh',
  );
  if (refreshTag === undefined) {
    return undefined;
  }
  const content = attributeValue(refreshTag, 'content') ?? '';
  return content.match(/^\s*0\s*;\s*url=(.+)\s*$/i)?.[1];
}

function hasNoIndexOrNoFollow(html) {
  return htmlTags(html, 'meta').some((tag) => {
    if (attributeValue(tag, 'name')?.toLowerCase() !== 'robots') {
      return false;
    }
    return /(?:^|[,\s])(noindex|nofollow)(?:$|[,\s])/i.test(attributeValue(tag, 'content') ?? '');
  });
}

function visitMarkdown(node, visitor) {
  visitor(node);
  node.children?.forEach(child => visitMarkdown(child, visitor));
}
