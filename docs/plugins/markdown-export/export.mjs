// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {existsSync} from 'node:fs';
import {readFile, mkdir, writeFile} from 'node:fs/promises';
import {dirname, join, relative, resolve} from 'node:path';
import {unified} from 'unified';
import remarkParse from 'remark-parse';
import remarkMdx from 'remark-mdx';
import remarkFrontmatter from 'remark-frontmatter';
import remarkGfm from 'remark-gfm';
import remarkStringify from 'remark-stringify';
import {toString as mdastToString} from 'mdast-util-to-string';
import {markdownPath} from '../../src/markdownPath.mjs';

// Matches the tab order of SdkTabs.tsx.
export const sdkOrder = ['python', 'go', 'java', 'typescript', 'rust'];
const sdkLabels = {python: 'Python', go: 'Go', java: 'Java', typescript: 'TypeScript', rust: 'Rust'};
const diagrams = new Set(['DexSystemDiagram', 'ExecutionApproachDiagram', 'StepWaitExecuteDiagram',
  'StepDecisionMovementsDiagram', 'RpcRequestDiagram', 'ChannelScopeDiagram', 'ArchitectureComparison',
  'RequirementChangeComparison']);
// Docusaurus default admonition keywords.
const admonitionKeywords = ['note', 'tip', 'info', 'warning', 'danger', 'caution', 'important', 'secondary', 'success'];
const localeCopies = {
  en: {
    separator: ': ',
    example: 'Example',
    flow: 'Flow',
    steps: 'Steps',
    when: 'when',
    graph: {
      rpcs: 'RPCs', startMarker: ' (start Step)', timeoutMarker: ' (timeout handler)', listJoiner: ', ',
      goesTo: 'Goes to', cancels: 'cancels', cancelsJoiner: ' and ', startsSubFlow: 'Starts SubFlow',
      onFailure: label => (label?.endsWith(' failure') ? `If ${label.slice(0, -' failure'.length)} fails, goes to` : 'On failure, goes to'),
      resource: {resource_write: 'Writes', resource_read: 'Reads', resource_publish: 'Publishes to', resource_lock: 'Locks'},
      decisions: {gracefulComplete: 'Completes the Flow', forceComplete: 'Completes the Flow at once',
        forceCompleteIfChannelsEmpty: 'Completes the Flow if its Channels are empty', forceFail: 'Fails the Flow',
        deadEnd: 'Ends this branch'},
      waits: {until: 'Waits for', anyOf: 'Waits for any of', allOf: 'Waits for all of', skipWaitImmediately: 'Does not wait',
        failure: 'Waits after a failure'},
      conditionKinds: {channel: 'Channel', timer: 'Timer', subflow: 'SubFlow'},
    },
    summary: 'Dex is a Durable Execution platform. Each entry links to the Markdown export of a documentation page.',
    otherPages: 'Other pages',
    admonitions: {note: 'Note', tip: 'Tip', info: 'Info', warning: 'Warning', danger: 'Danger', caution: 'Caution',
      important: 'Important', secondary: 'Secondary', success: 'Success'},
  },
  'zh-Hans': {
    separator: '：',
    example: '例子',
    flow: 'Flow',
    steps: 'Step 列表',
    when: '条件',
    graph: {
      rpcs: 'RPC 列表', startMarker: '（起始 Step）', timeoutMarker: '（超时处理）', listJoiner: '、',
      goesTo: '转到', cancels: '取消', cancelsJoiner: '，并', startsSubFlow: '启动 SubFlow',
      onFailure: label => (label?.endsWith(' failure') ? `${label.slice(0, -' failure'.length)} 失败时转到` : '失败时转到'),
      resource: {resource_write: '写入', resource_read: '读取', resource_publish: '发布到', resource_lock: '锁定'},
      decisions: {gracefulComplete: '完成 Flow', forceComplete: '立即完成 Flow',
        forceCompleteIfChannelsEmpty: 'Channel 为空时完成 Flow', forceFail: '让 Flow 失败', deadEnd: '结束这个分支'},
      waits: {until: '等待', anyOf: '等待以下任一条件', allOf: '等待以下全部条件', skipWaitImmediately: '不等待',
        failure: '失败后等待'},
      conditionKinds: {channel: 'Channel', timer: 'Timer', subflow: 'SubFlow'},
    },
    summary: 'Dex 是 Durable Execution 平台。每个条目链接到一个文档页面的 Markdown 版本。',
    otherPages: '其他页面',
    admonitions: {note: '备注', tip: '提示', info: '信息', warning: '注意', danger: '危险', caution: '警告',
      important: '重要', secondary: '补充', success: '成功'},
  },
};
// Must match the content types rendered by DexSkillHostTabs.tsx.
const hostContainerTypes = new Set(['p', 'ol', 'ul', 'li', 'h1', 'h2', 'h3', 'h4', 'h5', 'h6']);
// Output written outside this build's postBuild, such as generated API reference, may not exist yet.
export const unverifiedLinkPrefixes = ['/api/'];
const pathnameProtocol = 'pathname://';
const phrasingTypes = new Set(['text', 'emphasis', 'strong', 'delete', 'inlineCode', 'break', 'image', 'link', 'html']);
const parser = unified().use(remarkParse).use(remarkMdx).use(remarkFrontmatter).use(remarkGfm);
const writer = unified().use(remarkStringify, {bullet: '-', fences: true, handlers: {text: writeText}}).use(remarkGfm);

/**
 * Returns the export labels for a locale.
 *
 * @returns {object} The labels for the locale, or the English labels for an unknown locale.
 */
export function localeCopy(locale) {
  return localeCopies[locale] ?? localeCopies.en;
}

export async function exportMarkdown({allContent, siteDir, outDir, baseUrl, siteConfig, i18n, routesPaths}) {
  const versions = [];
  for (const instance of Object.values(allContent['docusaurus-plugin-content-docs'] ?? {})) {
    for (const version of instance.loadedVersions) {
      const sourceRoutes = new Map();
      for (const page of version.docs) {
        const sourcePath = resolveSiteSource(siteDir, page.source);
        const contentRoot = isWithin(version.contentPathLocalized, sourcePath) ? version.contentPathLocalized : version.contentPath;
        const sourceRelative = relative(contentRoot, sourcePath);
        for (const root of [version.contentPath, version.contentPathLocalized]) {
          sourceRoutes.set(resolve(root, sourceRelative), page.permalink);
        }
      }
      versions.push({version, pages: version.docs.map(metadata => ({...metadata, sourceRoutes}))});
    }
  }
  const pages = versions.flatMap(item => item.pages);
  const locale = i18n.currentLocale;
  const copy = localeCopy(locale);
  const routes = new Map(pages.map(page => [routeKey(page.permalink), page.permalink]));
  const knownRoutes = new Set((routesPaths ?? []).map(routeKey));
  const otherLocaleBases = (i18n.locales ?? []).filter(item => item !== locale && item !== i18n.defaultLocale)
    .map(item => `/${item}/`);
  const hostData = JSON.parse(await readFile(join(siteDir, 'src/data/dex-skill-hosts.json'), 'utf8'));
  const entries = new Map();
  const writtenPaths = new Set();
  const movedPages = new Set();
  for (const page of pages) {
    const sourcePath = resolveSiteSource(siteDir, page.source);
    const source = await readFile(sourcePath, 'utf8');
    const context = {page, siteDir, sourcePath, routes, knownRoutes, otherLocaleBases, outDir, baseUrl, siteUrl: siteConfig.url,
      canonicalUrl: new URL(siteConfig.trailingSlash ? `${page.permalink.replace(/\/+$/, '')}/` : page.permalink, siteConfig.url).href,
      locale, copy, hostData, imports: new Map(), headingIds: new Map(), headingDepth: 1};
    const prepared = prepareSource(source, {headingIds: context.headingIds, locale, sourcePath});
    let tree;
    try {
      tree = parser.parse(prepared);
    } catch (error) {
      throw new Error(`${sourcePath}:${error.line ?? 1}: ${error.message}`, {cause: error});
    }
    collectImports(tree, context);
    tree.children = await transformChildren(tree.children, context);
    const leadText = firstParagraphText(tree);
    const description = singleLine(page.description);
    const frontMatter = {title: page.title,
      ...(description ? {description} : {}),
      canonical_url: context.canonicalUrl,
      source: relative(resolve(siteDir, '..'), sourcePath).split('\\').join('/'),
      locale};
    const yaml = Object.entries(frontMatter).map(([key, value]) => `${key}: ${JSON.stringify(value)}`).join('\n');
    const markdown = `---\n${yaml}\n---\n\n${tree.children.some(node => node.type === 'heading' && node.depth === 1) ? '' : `# ${page.title}\n\n`}${writer.stringify(tree)}`;
    const pathname = markdownPath(page.permalink, baseUrl);
    if (writtenPaths.has(pathname)) throw new Error(`Duplicate Markdown output: ${pathname}`);
    writtenPaths.add(pathname);
    await writeOutput(outDir, baseUrl, pathname, markdown);
    if (movedNoticePattern.test(leadText)) movedPages.add(page);
    entries.set(page, `- [${escapeLabel(page.title)}](${new URL(pathname, siteConfig.url).href})${description ? `: ${description}` : ''}`);
  }
  // Archived versions keep Markdown twins but stay out of llms.txt, like the sitemap.
  const sections = llmsSections(versions.filter(item => item.version.isLast), copy, movedPages);
  const index = [`# ${siteConfig.title}`, `> ${copy.summary}`,
    ...sections.map(section => `## ${section.title}\n\n${section.pages.map(page => entries.get(page)).join('\n')}`)];
  await writeOutput(outDir, baseUrl, `${baseUrl}llms.txt`, `${index.join('\n\n')}\n`);
  console.log(`[markdown-export] Exported ${pages.length} pages (${locale}) from ${versions.length} docs version(s).`);
}

function llmsSections(versions, copy, movedPages = new Set()) {
  const sections = [];
  const listed = new Set();
  for (const {version, pages} of versions) {
    const pagesById = new Map(pages.map(page => [page.id, page]));
    for (const sidebar of Object.values(version.sidebars ?? {})) {
      for (const item of sidebar) {
        const sectionPages = sidebarDocIds(item).map(id => pagesById.get(id))
          .filter(page => page && !listed.has(page));
        if (!sectionPages.length) continue;
        sectionPages.forEach(page => listed.add(page));
        sections.push({title: item.type === 'category' ? item.label : sectionPages[0].title, pages: sectionPages});
      }
    }
  }
  // A page that only says where its content moved helps no reader of llms.txt.
  const unlisted = versions.flatMap(item => item.pages).filter(page => !listed.has(page) && !movedPages.has(page));
  if (unlisted.length) sections.push({title: copy.otherPages, pages: unlisted});
  return sections;
}

function sidebarDocIds(item) {
  if (item.type === 'doc' || item.type === 'ref') return [item.id];
  if (item.type !== 'category') return [];
  return [...(item.link?.type === 'doc' ? [item.link.id] : []), ...item.items.flatMap(sidebarDocIds)];
}

async function writeOutput(outDir, baseUrl, pathname, content) {
  if (!pathname.startsWith(baseUrl)) throw new Error(`Markdown route outside locale base URL: ${pathname}`);
  const output = join(outDir, decodeURIComponent(pathname.slice(baseUrl.length)));
  await mkdir(dirname(output), {recursive: true});
  await writeFile(output, content);
}

function resolveSiteSource(siteDir, source) {
  return source.startsWith('@site/') ? resolve(siteDir, source.slice(6)) : resolve(siteDir, source);
}

/**
 * Rewrites Docusaurus-only line syntax that remark-mdx cannot parse.
 *
 * @returns {string} The source with heading IDs removed and admonitions turned into labelled blockquotes.
 * @throws {Error} With the source file and line for an unsupported, stray, or unclosed admonition.
 */
export function prepareSource(source, {headingIds, locale, sourcePath}) {
  const copy = localeCopy(locale);
  let fence;
  const admonitions = [];
  const lines = [];
  for (const [index, sourceLine] of source.split('\n').entries()) {
    let line = sourceLine;
    const lineNumber = index + 1;
    const fenceMatch = line.match(/^\s*(`{3,}|~{3,})/);
    if (fenceMatch) {
      if (!fence) fence = fenceMatch[1];
      else if (fenceMatch[1][0] === fence[0] && fenceMatch[1].length >= fence.length) fence = undefined;
    }
    if (!fence && !fenceMatch) {
      const headingMatch = line.match(/^#{1,6}\s.*\{#([^}]+)\}\s*$/);
      if (headingMatch) {
        headingIds.set(lineNumber, headingMatch[1]);
        line = line.replace(/\s*\{#[^}]+\}\s*$/, '');
      }
      const directive = line.match(/^\s*:{3,}(.*)$/);
      if (directive && !directive[1].trim()) {
        if (!admonitions.length) throw sourceError(sourcePath, lineNumber, 'Closing ::: has no open admonition');
        admonitions.pop();
        lines.push('> '.repeat(admonitions.length));
        continue;
      }
      if (directive) {
        const open = directive[1].match(/^([A-Za-z][\w-]*)(?:\[([^\]]*)\])?(?:\s+(.*?))?\s*$/);
        if (!open || !admonitionKeywords.includes(open[1])) {
          throw sourceError(sourcePath, lineNumber, `Unsupported admonition :::${directive[1].trim()}`);
        }
        const title = open[2] ?? open[3];
        admonitions.push(lineNumber);
        lines.push(`${'> '.repeat(admonitions.length)}**${copy.admonitions[open[1]]}${title ? `${copy.separator}${title}` : ''}**`);
        continue;
      }
    }
    lines.push(`${'> '.repeat(admonitions.length)}${line}`);
  }
  if (admonitions.length) throw sourceError(sourcePath, admonitions.at(-1), 'Admonition is not closed');
  return lines.join('\n');
}

function collectImports(tree, context) {
  for (const node of tree.children) {
    if (node.type !== 'mdxjsEsm') continue;
    for (const statement of node.data.estree.body) {
      if (statement.type !== 'ImportDeclaration') fail(context, node, 'Only imports are supported in MDX modules');
      for (const specifier of statement.specifiers) {
        context.imports.set(specifier.local.name, statement.source.value);
      }
    }
  }
}

async function transformChildren(children, context) {
  const result = [];
  for (const node of children) result.push(...await transformNode(node, context));
  return result;
}

async function transformNode(node, context) {
  if (node.type === 'yaml' || node.type === 'mdxjsEsm') return [];
  if (node.type === 'mdxJsxFlowElement' || node.type === 'mdxJsxTextElement') return expandJsx(node, context);
  if (node.type === 'mdxFlowExpression' || node.type === 'mdxTextExpression') {
    fail(context, node, 'MDX expressions need an explicit Markdown expansion');
  }
  if (['link', 'definition'].includes(node.type)) node.url = rewriteLink(node.url, context, node);
  // fold="..." only changes how the site shows a code block.
  if (node.type === 'code' && node.meta) node.meta = node.meta.replace(/\s*\bfold=(["'])[^"']*\1/, '').trim() || null;
  if (node.type === 'image') node.url = new URL(node.url, context.canonicalUrl).href;
  if (node.children) node.children = await transformChildren(node.children, context);
  if (node.type !== 'heading') return [node];
  context.headingDepth = node.depth;
  const headingId = context.headingIds.get(node.position?.start.line);
  return headingId ? [anchor(headingId), node] : [node];
}

async function expandJsx(node, context) {
  const name = node.name;
  if (name === 'SdkTabs') return expandSdkTabs(node, context);
  if (name === 'SdkSnippet') {
    const snippet = await snippetContent(node, context);
    return snippetMarkdown(snippet.language, {...snippet, example: attribute(node, 'example', context)}, context);
  }
  if (diagrams.has(name)) {
    const description = attribute(node, 'markdownDescription', context);
    if (typeof description !== 'string' || !description.trim()) fail(context, node, `${name} requires markdownDescription`);
    return [paragraph(description)];
  }
  if (name === 'DocsFlowDefinitionGraph') return expandGraph(node, context);
  if (name === 'DexSkillHostTabs') return expandHostTabs(node, context);
  if (name === 'DocsSection') return expandDocsSection(node, context);
  if (name === 'FrameworkTabs') return expandFrameworkTabs(node, context);
  if (name === 'CrashTimeline') return expandCrashTimeline(node, context);
  if (name === 'img') {
    const image = {type: 'image', ...imageProperties(node, context)};
    return node.type === 'mdxJsxTextElement' ? [image] : [{type: 'paragraph', children: [image]}];
  }
  if (name === 'br') return [{type: 'break'}];
  if (['div', 'span', 'p', 'figure', 'figcaption'].includes(name)) return transformChildren(node.children, context);
  if (['strong', 'b', 'em', 'i'].includes(name)) return [{type: ['strong', 'b'].includes(name) ? 'strong' : 'emphasis',
    children: await transformChildren(node.children, context)}];
  if (name === 'a') return expandAnchorElement(node, context);
  fail(context, node, `Unknown JSX component <${name}>`);
}

// Mirrors CrashTimeline.tsx without the animation: the events as a numbered list, then the summary.
async function expandCrashTimeline(node, context) {
  const variant = attribute(node, 'variant', context);
  const timelines = JSON.parse(await readFile(join(context.siteDir, 'src/data/crash-timeline.json'), 'utf8'));
  const byLocale = timelines[variant];
  if (!byLocale) fail(context, node, `CrashTimeline has unknown variant ${JSON.stringify(variant)}`);
  const copy = byLocale[context.locale] ?? byLocale.en;
  const separator = localeCopy(context.locale).separator;
  const eventText = event => {
    const actor = copy.actors[event.actor];
    return event.text.startsWith(actor) ? event.text : `${actor}${separator}${event.text}`;
  };
  return [orderedList(copy.events.map(event => [text(eventText(event))])),
    paragraph(copy.summary)];
}

// Mirrors FrameworkTabs.tsx: each framework's content under a bold label, in tab order.
const frameworkLabels = {
  'claude-agent-sdk': 'Claude Agent SDK',
  'openai-agents-sdk': 'OpenAI Agents SDK', 'pydantic-ai': 'Pydantic AI',
  'langchain-langgraph': 'LangChain and LangGraph', 'google-adk': 'Google ADK',
  'vercel-ai-sdk': 'Vercel AI SDK', mastra: 'Mastra',
};
async function expandFrameworkTabs(node, context) {
  const tabs = [];
  for (const child of node.children.filter(child => child.type === 'mdxJsxFlowElement')) {
    if (child.name !== 'FrameworkTab') fail(context, child, 'FrameworkTabs children must be FrameworkTab elements');
    const framework = attribute(child, 'framework', context);
    if (!Object.hasOwn(frameworkLabels, framework)) fail(context, child, `Unknown agent framework ${framework}`);
    tabs.push({framework, content: await transformChildren(child.children, context)});
  }
  // A block that opens every tab is written once, before the labels, so the export does not repeat it.
  const shared = [];
  while (tabs.length > 1 && tabs.every(tab => tab.content.length > 0 &&
      markdownNodeKey(tab.content[0]) === markdownNodeKey(tabs[0].content[0]))) {
    shared.push(tabs[0].content[0]);
    for (const tab of tabs) tab.content.shift();
  }
  return [...shared, ...tabs.flatMap(tab => [paragraph(frameworkLabels[tab.framework], true), ...tab.content])];
}

function markdownNodeKey(node) {
  return JSON.stringify(node, (key, value) => key === 'position' ? undefined : value);
}

// Mirrors DocsSection.tsx: a linked section name followed by its description.
async function expandDocsSection(node, context) {
  const to = attribute(node, 'to', context);
  const title = attribute(node, 'title', context);
  if (typeof to !== 'string' || typeof title !== 'string') fail(context, node, 'DocsSection requires string to and title');
  const description = (await transformChildren(node.children, context))
    .flatMap(child => child.type === 'paragraph' ? child.children : [child]);
  return [{type: 'paragraph', children: [
    {type: 'link', url: rewriteLink(to, context, node), children: [{type: 'text', value: title}]},
    {type: 'text', value: ': '},
    ...description,
  ]}];
}

async function expandAnchorElement(node, context) {
  const href = attribute(node, 'href', context);
  const id = attribute(node, 'id', context);
  if (typeof href !== 'string' && typeof id !== 'string') fail(context, node, '<a> requires a string href or id');
  const children = (await transformChildren(node.children, context))
    .flatMap(child => child.type === 'paragraph' ? child.children : [child]);
  const blockChild = children.find(child => !phrasingTypes.has(child.type));
  if (blockChild) fail(context, node, `<a> content must be inline, found ${blockChild.type}`);
  const content = typeof href === 'string' ? [{type: 'link', url: rewriteLink(href, context, node), children}] : children;
  const phrasing = [...(typeof id === 'string' ? [anchor(id)] : []), ...content];
  return node.type === 'mdxJsxFlowElement' ? [{type: 'paragraph', children: phrasing}] : phrasing;
}

async function expandSdkTabs(node, context) {
  const examples = attribute(node, 'examples', context) ?? {};
  if (typeof examples !== 'object' || examples === null) fail(context, node, 'SdkTabs examples must be an object literal');
  for (const language of Object.keys(examples)) {
    if (!sdkLabels[language]) fail(context, node, `Unknown SDK language in examples: ${language}`);
  }
  // Mirrors collectSnippets in SdkTabs.tsx: props first, then SdkSnippet children override.
  const snippets = new Map();
  for (const language of sdkOrder) {
    const body = attribute(node, language, context);
    if (body === undefined) continue;
    if (typeof body !== 'string') fail(context, node, `SdkTabs ${language} must be a string literal`);
    snippets.set(language, {children: [{type: 'code', lang: language, value: body.replace(/^\n+|\n+$/g, '')}],
      example: examples[language]});
  }
  for (const child of node.children) {
    if (child.name !== 'SdkSnippet') fail(context, child, 'SdkTabs children must be SdkSnippet elements');
    const snippet = await snippetContent(child, context);
    snippets.set(snippet.language, {...snippet, example: attribute(child, 'example', context) ?? examples[snippet.language]});
  }
  if (!snippets.size) fail(context, node, 'SdkTabs requires at least one SDK snippet');
  return sdkOrder.filter(language => snippets.has(language))
    .flatMap(language => snippetMarkdown(language, snippets.get(language), context));
}

async function snippetContent(node, context) {
  const language = attribute(node, 'lang', context);
  if (!sdkLabels[language]) fail(context, node, `Unknown SDK language: ${language}`);
  const children = await transformChildren(node.children, context);
  const codeBlocks = children.filter(child => child.type === 'code');
  if (!codeBlocks.length) fail(context, node, 'SdkSnippet must contain fenced code');
  for (const block of codeBlocks) block.lang ||= language;
  return {language, children};
}

function snippetMarkdown(language, {children, example}, context) {
  const result = [paragraph(sdkLabels[language], true), ...children];
  if (example) result.push(exampleParagraph(example, context));
  return result;
}

function exampleParagraph(path, context) {
  return {type: 'paragraph', children: [text(`${context.copy.example}: `),
    {type: 'link', url: `https://github.com/superdurable/dex/tree/main/${path}`, children: [text(path)]}]};
}

async function expandGraph(node, context) {
  const reference = node.attributes.find(item => item.name === 'graph');
  const expression = reference?.value?.data?.estree?.body?.[0]?.expression;
  if (expression?.type !== 'Identifier') fail(context, node, 'Graph must reference an imported JSON identifier');
  const importedPath = context.imports.get(expression.name);
  if (!importedPath?.endsWith('.json')) fail(context, node, 'Graph requires an imported JSON file');
  const graphPath = importedPath.startsWith('@site/') ? resolveSiteSource(context.siteDir, importedPath)
    : resolve(dirname(context.sourcePath), importedPath);
  const graph = JSON.parse(await readFile(graphPath, 'utf8'));
  const {copy} = context;
  const nodes = new Map(graph.nodes.map(item => [item.id, item]));
  const graphState = {context, node, copy, nodes};
  for (const edge of graph.edges) {
    for (const id of [edge.from, edge.to]) if (!nodes.has(id)) fail(context, node, `Graph edge references missing node ${id}`);
  }
  const start = nodes.get(graph.flow.startStepId);
  if (graph.flow.startStepId && !start) fail(context, node, `Graph start Step ${graph.flow.startStepId} is missing`);
  const handlers = graph.nodes.filter(item => item.kind === 'step' || item.kind === 'timeout_handler');
  // The start Step reads first, so the list follows the Flow from its beginning.
  const ordered = start ? [start, ...handlers.filter(item => item !== start)] : handlers;
  const steps = ordered.map(item => graphHandlerItem(item, graph, graphState));
  const rpcs = graph.nodes.filter(item => item.kind === 'rpc').map(item => graphHandlerItem(item, graph, graphState));
  return [paragraph(`${copy.flow}${copy.separator}${graph.flow.name}`, true),
    paragraph(copy.steps, true), orderedList(steps),
    ...(rpcs.length ? [paragraph(copy.graph.rpcs, true), list(rpcs)] : []),
    ...(graph.source?.path ? [exampleParagraph(graph.source.path, context)] : [])];
}

// One list item per Step, RPC, or timeout handler: its name, what it waits for, the state it
// touches, and where each of its decisions leads, in plain words.
function graphHandlerItem(handler, graph, graphState) {
  const {copy, nodes} = graphState;
  const words = copy.graph;
  const ownerOf = id => {
    let item = nodes.get(id);
    while (item?.parentId && !['step', 'rpc', 'timeout_handler'].includes(item.kind)) item = nodes.get(item.parentId);
    return item;
  };
  const details = [];
  for (const wait of graph.nodes.filter(item => item.kind === 'wait' && item.parentId === handler.id)) {
    details.push(waitPhrasing(wait, copy));
  }
  const resourceVerbs = new Map();
  for (const edge of graph.edges.filter(item => item.kind.startsWith('resource_'))) {
    const handlerEnd = ownerOf(edge.from) === handler ? edge.to : ownerOf(edge.to) === handler ? edge.from : undefined;
    if (!handlerEnd) continue;
    const verb = words.resource[edge.kind] ?? edge.label ?? edge.kind;
    const names = resourceVerbs.get(verb) ?? [];
    const name = nodes.get(handlerEnd).name;
    if (!names.includes(name)) names.push(name);
    resourceVerbs.set(verb, names);
  }
  for (const [verb, names] of resourceVerbs) details.push([text(`${verb} `), ...joinCode(names, copy)]);
  for (const decision of graph.nodes.filter(item => item.kind === 'decision' && item.parentId === handler.id)) {
    const type = decision.decision?.type ?? decision.name;
    if (type === 'rpcResult') continue;
    const targets = graph.edges.filter(edge => edge.from === decision.id && edge.kind === 'transition').map(edge => nodes.get(edge.to).name);
    const canceled = graph.edges.filter(edge => edge.from === decision.id && edge.kind === 'cancel').map(edge => nodes.get(edge.to).name);
    const phrase = targets.length ? [text(`${words.goesTo} `), ...joinCode(targets, copy)]
      : words.decisions[type] ? [text(words.decisions[type])] : [inlineCode(type)];
    if (canceled.length) phrase.push(text(`${words.cancelsJoiner}${words.cancels} `), ...joinCode(canceled, copy));
    details.push([...phrase, ...conditionPhrasing(decision, copy)]);
  }
  for (const edge of graph.edges.filter(item => item.from === handler.id && item.kind === 'failure_transition')) {
    details.push([text(`${words.onFailure(edge.label)} `), inlineCode(nodes.get(edge.to).name)]);
  }
  for (const edge of graph.edges.filter(item => item.kind === 'subflow' && ownerOf(item.from) === handler)) {
    details.push([text(`${words.startsSubFlow} `), inlineCode(nodes.get(edge.to).name)]);
  }
  const explanation = handler.metadata?.explanation;
  return [inlineCode(handler.name),
    ...(handler === nodes.get(graph.flow.startStepId) ? [text(words.startMarker)] : []),
    ...(handler.kind === 'timeout_handler' ? [text(words.timeoutMarker)] : []),
    ...(explanation ? [text(`${copy.separator}${explanation}`)] : []),
    ...(details.length ? [list(details)] : [])];
}

function joinCode(names, copy) {
  return names.flatMap((name, index) => [...(index ? [text(copy.graph.listJoiner)] : []), inlineCode(name)]);
}

function waitPhrasing(item, copy) {
  const words = copy.graph;
  const conditions = (item.wait?.conditions ?? []).flatMap((condition, index) => [
    text(`${index ? copy.graph.listJoiner : ' '}${words.conditionKinds[condition.kind] ?? condition.kind} `),
    inlineCode(condition.label || condition.expression || condition.resourceId || condition.subFlowId || '')]);
  const type = item.wait?.type ?? item.name;
  const lead = words.waits[type];
  return [...(lead ? [text(lead)] : [text('WaitFor '), inlineCode(type)]), ...conditions, ...conditionPhrasing(item, copy)];
}

function conditionPhrasing(item, copy) {
  return item.condition ? [text(` ${copy.when} `), inlineCode(item.condition)] : [];
}

function expandHostTabs(node, context) {
  const skill = attribute(node, 'skill', context);
  if (typeof skill !== 'string' || !Object.hasOwn(context.hostData.invocations, skill)) fail(context, node, `Unknown skill: ${skill}`);
  const {introduction, hosts} = context.hostData.locales[context.locale] ?? context.hostData.locales.en;
  const hostDepth = Math.min(context.headingDepth + 1, 6);
  const contentDepths = hosts.flatMap(host => hostHeadingDepths(host.children));
  const state = {context, node, invocations: context.hostData.invocations[skill],
    headingShift: contentDepths.length ? hostDepth + 1 - Math.min(...contentDepths) : 0};
  return [paragraph(introduction), ...hosts.flatMap(host => [heading(hostDepth, host.label),
    ...expandHostContent(host.children, state)])];
}

function hostHeadingDepths(content) {
  if (typeof content === 'string') return [];
  if (Array.isArray(content)) return content.flatMap(hostHeadingDepths);
  const depth = /^h([1-6])$/.exec(content.type)?.[1];
  return [...(depth ? [Number(depth)] : []), ...hostHeadingDepths(content.children ?? [])];
}

function expandHostContent(content, state) {
  if (typeof content === 'string') return [text(content)];
  if (Array.isArray(content)) return content.flatMap(child => expandHostContent(child, state));
  if (content.type === 'invocations') {
    const value = state.invocations[content.style];
    if (typeof value !== 'string') fail(state.context, state.node, `Unknown skill invocation style: ${content.style}`);
    return [{type: 'code', lang: 'text', value}];
  }
  if (content.type === 'code') {
    if (!(content.children ?? []).every(child => typeof child === 'string')) {
      fail(state.context, state.node, 'Skill host code must contain only text');
    }
    return [{type: 'code', lang: content.language ?? null, value: (content.children ?? []).join('')}];
  }
  if (!hostContainerTypes.has(content.type)) fail(state.context, state.node, `Unsupported skill host content type: ${content.type}`);
  const children = expandHostContent(content.children ?? [], state);
  if (content.type === 'p') return [{type: 'paragraph', children}];
  if (content.type === 'li') return [{type: 'listItem', spread: false, children: [{type: 'paragraph', children}]}];
  if (content.type === 'ol' || content.type === 'ul') {
    return [{type: 'list', ordered: content.type === 'ol', start: content.type === 'ol' ? content.start ?? 1 : null, spread: false, children}];
  }
  return [{type: 'heading', depth: Math.min(Number(content.type[1]) + state.headingShift, 6), children}];
}

function attribute(node, name, context) {
  const property = node.attributes.find(item => item.name === name);
  if (!property) return undefined;
  if (property.value === null) return true;
  if (typeof property.value === 'string') return property.value;
  const expression = property.value.data?.estree?.body?.[0]?.expression;
  return literalValue(expression, context, node);
}

function literalValue(expression, context, node) {
  if (expression?.type === 'Literal') return expression.value;
  if (expression?.type === 'TemplateLiteral' && !expression.expressions.length) return expression.quasis[0].value.cooked;
  if (expression?.type === 'ObjectExpression') {
    return Object.fromEntries(expression.properties.map(property => {
      if (property.type !== 'Property' || property.computed || property.kind !== 'init') {
        fail(context, node, 'Only literal MDX properties are supported');
      }
      return [property.key.name ?? property.key.value, literalValue(property.value, context, node)];
    }));
  }
  fail(context, node, 'Only literal MDX attributes are supported');
}

function imageProperties(node, context) {
  return {url: new URL(attribute(node, 'src', context), context.canonicalUrl).href,
    alt: attribute(node, 'alt', context) ?? ''};
}

function rewriteLink(href, context, node) {
  if (href.startsWith(pathnameProtocol)) return rewritePathnameLink(href, context, node);
  const target = new URL(href, context.canonicalUrl);
  if (target.origin !== new URL(context.siteUrl).origin) return href;
  if (/\.mdx?(?:[?#]|$)/.test(href) && !href.startsWith('/') && !/^https?:/i.test(href)) {
    const sourceTarget = resolve(dirname(context.sourcePath), decodeURIComponent(href.split(/[?#]/)[0]));
    const permalink = context.page.sourceRoutes.get(sourceTarget);
    if (!permalink) fail(context, node, `Cannot resolve source link ${href}`);
    target.pathname = markdownPath(permalink, context.baseUrl);
    return target.href;
  }
  // Docusaurus prefixes the locale base URL to root-relative links.
  if (href.startsWith('/') && !href.startsWith(context.baseUrl)) target.pathname = `${context.baseUrl}${target.pathname.slice(1)}`;
  const permalink = context.routes.get(routeKey(target.pathname));
  if (permalink) {
    target.pathname = markdownPath(permalink, context.baseUrl);
  } else if (!/^https?:/i.test(href) && !isKnownTarget(target.pathname, context)) {
    fail(context, node, `Link target ${href} is not a route or static file`);
  }
  return target.href;
}

// The MDX link wrapper serves pathname:// API links from the site root, without the locale base URL.
function rewritePathnameLink(href, context, node) {
  const pathname = href.slice(pathnameProtocol.length);
  if (!unverifiedLinkPrefixes.some(prefix => pathname.startsWith(prefix))) {
    fail(context, node, `Link target ${href} must start with pathname://${unverifiedLinkPrefixes.join(' or pathname://')}`);
  }
  return new URL(pathname, context.siteUrl).href;
}

function isKnownTarget(pathname, context) {
  if (context.knownRoutes.has(routeKey(pathname))) return true;
  if (context.otherLocaleBases.some(base => pathname.startsWith(base))) return true;
  if (!pathname.startsWith(context.baseUrl)) return false;
  const localePath = `/${pathname.slice(context.baseUrl.length)}`;
  if (unverifiedLinkPrefixes.some(prefix => localePath.startsWith(prefix))) return true;
  return existsSync(join(context.outDir, decodeURIComponent(pathname.slice(context.baseUrl.length))));
}

// Intraword underscores cannot delimit emphasis in CommonMark, so escaping them is noise.
function writeText(node, parent, state, info) {
  return state.safe(node.value, info).replace(/(?<=[\p{L}\p{N}])\\_(?=[\p{L}\p{N}])/gu, '_');
}

function isWithin(directory, file) { const path = relative(directory, file); return path !== '..' && !path.startsWith('../'); }
function routeKey(pathname) { return pathname.replace(/\/+$/, '') || '/'; }
const movedNoticePattern = /^(This page moved|本页已经?移)/;

function firstParagraphText(tree) {
  const lead = tree.children.find(node => node.type === 'paragraph');
  return lead ? singleLine(mdastToString(lead)) : '';
}

function singleLine(value) { return typeof value === 'string' ? value.replace(/\s+/g, ' ').trim() : ''; }
function text(value) { return {type: 'text', value}; }
function inlineCode(value) { return {type: 'inlineCode', value}; }
function anchor(id) { return {type: 'html', value: `<a id="${escapeAttribute(id)}"></a>`}; }
function paragraph(value, bold = false) { return {type: 'paragraph', children: [bold ? {type: 'strong', children: [text(value)]} : text(value)]}; }
function heading(depth, value) { return {type: 'heading', depth, children: [text(value)]}; }
function orderedList(items) {
  return {...list(items), ordered: true, start: 1};
}
function list(items) {
  return {type: 'list', ordered: false, spread: false, children: items.map(item => ({type: 'listItem', spread: false,
    children: [{type: 'paragraph', children: item.filter(child => child.type !== 'list')}, ...item.filter(child => child.type === 'list')]}))};
}
function escapeLabel(value) { return value.replace(/[\[\]\\]/g, '\\$&'); }
function escapeAttribute(value) { return value.replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;'); }
function sourceError(sourcePath, line, message) { return new Error(`${sourcePath}:${line}: ${message}`); }
function fail(context, node, message) { throw sourceError(context.sourcePath, node.position?.start.line ?? 1, message); }
