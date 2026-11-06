// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

import {readFile, mkdir, writeFile} from 'node:fs/promises';
import {dirname, join, relative, resolve} from 'node:path';
import {unified} from 'unified';
import remarkParse from 'remark-parse';
import remarkMdx from 'remark-mdx';
import remarkFrontmatter from 'remark-frontmatter';
import remarkGfm from 'remark-gfm';
import remarkStringify from 'remark-stringify';

const labels = {python: 'Python', go: 'Go', java: 'Java', typescript: 'TypeScript', rust: 'Rust'};
const diagrams = new Set(['DexSystemDiagram', 'ExecutionApproachDiagram', 'StepWaitExecuteDiagram',
  'StepDecisionMovementsDiagram', 'RpcRequestDiagram', 'ChannelScopeDiagram']);
const parser = unified().use(remarkParse).use(remarkMdx).use(remarkFrontmatter).use(remarkGfm);
const writer = unified().use(remarkStringify, {fences: true}).use(remarkGfm);

export function markdownPath(permalink, baseUrl) {
  const route = permalink.replace(/\/+$/, '');
  return route === baseUrl.replace(/\/+$/, '') ? `${baseUrl}index.md` : `${route}.md`;
}

export async function exportMarkdown({allContent, siteDir, outDir, baseUrl, siteConfig, i18n}) {
  const instances = Object.values(allContent['docusaurus-plugin-content-docs'] ?? {});
  const pages = [];
  for (const instance of instances) {
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
      pages.push(...version.docs.map(metadata => ({...metadata, sourceRoutes})));
    }
  }
  const routes = new Map(pages.map(page => [routeKey(page.permalink), page.permalink]));
  const hostData = JSON.parse(await readFile(join(siteDir, 'src/data/dex-skill-hosts.json'), 'utf8'));
  const entries = [];
  const writtenPaths = new Set();
  for (const page of pages) {
    const sourcePath = resolveSiteSource(siteDir, page.source);
    const source = await readFile(sourcePath, 'utf8');
    const context = {page, siteDir, sourcePath, routes, baseUrl, siteUrl: siteConfig.url,
      canonicalUrl: new URL(siteConfig.trailingSlash ? `${page.permalink.replace(/\/+$/, '')}/` : page.permalink, siteConfig.url).href,
      locale: i18n.currentLocale, hostData, imports: new Map(), headingIds: new Map()};
    let tree;
    try {
      tree = parser.parse(prepareSource(source, context.headingIds));
    } catch (error) {
      throw new Error(`${sourcePath}:${error.line ?? 1}: ${error.message}`, {cause: error});
    }
    collectImports(tree, context);
    tree.children = await transformChildren(tree.children, context);
    const frontMatter = {title: page.title,
      ...(page.frontMatter.description ? {description: page.frontMatter.description} : {}),
      canonical_url: context.canonicalUrl,
      source: relative(resolve(siteDir, '..'), sourcePath).split('\\').join('/'),
      locale: i18n.currentLocale};
    const yaml = Object.entries(frontMatter).map(([key, value]) => `${key}: ${JSON.stringify(value)}`).join('\n');
    const markdown = `---\n${yaml}\n---\n\n${tree.children.some(node => node.type === 'heading' && node.depth === 1) ? '' : `# ${page.title}\n\n`}${writer.stringify(tree)}`;
    const pathname = markdownPath(page.permalink, baseUrl);
    if (writtenPaths.has(pathname)) throw new Error(`Duplicate Markdown output: ${pathname}`);
    writtenPaths.add(pathname);
    await writeOutput(outDir, baseUrl, pathname, markdown);
    entries.push(`- [${escapeLabel(page.title)}](${new URL(pathname, siteConfig.url).href})${
      page.frontMatter.description ? `: ${String(page.frontMatter.description).replace(/\s+/g, ' ').trim()}` : ''}`);
  }
  await writeOutput(outDir, baseUrl, `${baseUrl}llms.txt`, `# ${siteConfig.title}\n\n${entries.join('\n')}\n`);
  console.log(`[markdown-export] Exported ${pages.length} pages (${i18n.currentLocale}) from ${instances.length} docs instance(s).`);
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

export function prepareSource(source, headingIds) {
  let fence;
  const admonitions = [];
  const lines = [];
  for (const [index, sourceLine] of source.split('\n').entries()) {
    let line = sourceLine;
    const fenceMatch = line.match(/^\s*(`{3,}|~{3,})/);
    if (fenceMatch) {
      if (!fence) fence = fenceMatch[1];
      else if (fenceMatch[1][0] === fence[0] && fenceMatch[1].length >= fence.length) fence = undefined;
    }
    if (!fence && !fenceMatch) {
      const headingMatch = line.match(/^#{1,6}\s.*\{#([^}]+)\}\s*$/);
      if (headingMatch) {
        headingIds.set(index + 1, headingMatch[1]);
        line = line.replace(/\s*\{#[^}]+\}\s*$/, '');
      }
      const open = line.match(/^\s*(:{3,})(note|tip|info|warning|danger|caution)(?:\[([^\]]+)\]|\s+(.*))?\s*$/);
      if (open) {
        const label = open[2][0].toUpperCase() + open[2].slice(1);
        admonitions.push(open[1].length);
        lines.push(`${'> '.repeat(admonitions.length)}**${label}${open[3] || open[4] ? `: ${open[3] || open[4]}` : ''}**`);
        continue;
      }
      if (/^\s*:{3,}\s*$/.test(line) && admonitions.length) {
        admonitions.pop();
        lines.push('> '.repeat(admonitions.length));
        continue;
      }
    }
    lines.push(`${'> '.repeat(admonitions.length)}${line}`);
  }
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
  if (node.type === 'image') node.url = new URL(node.url, context.canonicalUrl).href;
  if (node.children) node.children = await transformChildren(node.children, context);
  const headingId = context.headingIds.get(node.position?.start.line);
  if (node.type === 'heading' && headingId) {
    return [{type: 'html', value: `<a id="${escapeAttribute(headingId)}"></a>`}, node];
  }
  return [node];
}

async function expandJsx(node, context) {
  const name = node.name;
  if (name === 'SdkTabs') {
    const examples = attribute(node, 'examples', context) ?? {};
    const expanded = [];
    for (const child of node.children) {
      if (child.name === 'SdkSnippet') {
        expanded.push(...await expandSnippet(child, context, examples));
      } else {
        expanded.push(...await transformNode(child, context));
      }
    }
    return expanded;
  }
  if (name === 'SdkSnippet') return expandSnippet(node, context, {});
  if (diagrams.has(name)) {
    const description = attribute(node, 'markdownDescription', context);
    if (typeof description !== 'string' || !description.trim()) fail(context, node, `${name} requires markdownDescription`);
    return [paragraph(description)];
  }
  if (name === 'DocsFlowDefinitionGraph') return expandGraph(node, context);
  if (name === 'DexSkillHostTabs') {
    const skill = attribute(node, 'skill', context);
    if (!context.hostData.invocations[skill]) fail(context, node, `Unknown skill: ${skill}`);
    const {introduction, hosts} = context.hostData.locales[context.locale] ?? context.hostData.locales.en;
    return [paragraph(introduction), ...hosts.flatMap(host => [heading(2, host.label),
      ...expandHostContent(host.children, context.hostData.invocations[skill])])];
  }
  if (name === 'img') return [{type: node.type === 'mdxJsxTextElement' ? 'image' : 'paragraph',
    ...(node.type === 'mdxJsxTextElement' ? imageProperties(node, context) : {children: [{type: 'image', ...imageProperties(node, context)}]})}];
  if (name === 'br') return [{type: 'break'}];
  if (['div', 'span', 'p', 'figure', 'figcaption'].includes(name)) return transformChildren(node.children, context);
  if (['strong', 'b', 'em', 'i'].includes(name)) return [{type: ['strong', 'b'].includes(name) ? 'strong' : 'emphasis',
    children: await transformChildren(node.children, context)}];
  if (name === 'a') {
    const href = attribute(node, 'href', context);
    if (href) {
      const children = await transformChildren(node.children, context);
      const link = {type: 'link', url: rewriteLink(href, context, node),
        children: children.flatMap(child => child.type === 'paragraph' ? child.children : [child])};
      return node.type === 'mdxJsxFlowElement' ? [{type: 'paragraph', children: [link]}] : [link];
    }
    const id = attribute(node, 'id', context);
    if (id) return [{type: 'html', value: `<a id="${escapeAttribute(id)}"></a>`}];
  }
  fail(context, node, `Unknown JSX component <${name}>`);
}

async function expandSnippet(node, context, examples) {
  const language = attribute(node, 'lang', context);
  if (!labels[language]) fail(context, node, `Unknown SDK language: ${language}`);
  const children = await transformChildren(node.children, context);
  const codeBlocks = children.filter(child => child.type === 'code');
  if (!codeBlocks.length) fail(context, node, 'SdkSnippet must contain fenced code');
  for (const block of codeBlocks) block.lang = language;
  const result = [paragraph(labels[language], true), ...children];
  const example = attribute(node, 'example', context) ?? examples[language];
  if (example) result.push({type: 'paragraph', children: [text(context.locale === 'zh-Hans' ? '例子: ' : 'Example: '),
    {type: 'link', url: `https://github.com/superdurable/dex/tree/main/${example}`, children: [text(example)]}]});
  return result;
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
  const nodes = new Map(graph.nodes.map(item => [item.id, item]));
  return [paragraph(`Flow: ${graph.flow.name}`, true), paragraph('Steps', true),
    list(graph.nodes.filter(item => item.kind === 'step').map(item => `${item.name}${
      item.metadata?.explanation ? `: ${item.metadata.explanation}` : ''}`)),
    paragraph('Edges', true), list(graph.edges.map(edge => `${graphNodeLabel(edge.from, nodes, context, node)} → ${graphNodeLabel(edge.to, nodes, context, node)}${
      edge.label ? `: ${edge.label}` : ''}${edge.condition ? ` (${edge.condition})` : ''}`)),
    ...(graph.source?.path ? [{type: 'paragraph', children: [text('Example: '), {type: 'link',
      url: `https://github.com/superdurable/dex/tree/main/${graph.source.path}`, children: [text(graph.source.path)]}]}] : [])];
}

function graphNodeLabel(id, nodes, context, node) {
  const item = nodes.get(id);
  if (!item) fail(context, node, `Graph edge references missing node ${id}`);
  return `${item.name} (${item.kind}${item.parentId ? `; ${nodes.get(item.parentId)?.name ?? item.parentId}` : ''})`;
}

function expandHostContent(content, invocations) {
  if (typeof content === 'string') return [text(content)];
  if (Array.isArray(content)) return content.flatMap(child => expandHostContent(child, invocations));
  if (content.type === 'invocations') return [{type: 'code', lang: 'text', value: invocations[content.style]}];
  if (content.type === 'code') return [{type: 'code', lang: content.language, value: content.children.join('')}];
  const children = expandHostContent(content.children ?? [], invocations);
  if (content.type === 'p') return [{type: 'paragraph', children}];
  if (/^h[1-6]$/.test(content.type)) return [{type: 'heading', depth: Number(content.type[1]), children}];
  if (content.type === 'li') return [{type: 'listItem', children: [{type: 'paragraph', children}]}];
  if (content.type === 'ol') return [{type: 'list', ordered: true, start: content.start ?? 1, children}];
  throw new Error(`Unsupported skill host content: ${content.type}`);
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
  const target = new URL(href, context.canonicalUrl);
  if (target.origin !== new URL(context.siteUrl).origin) return href;
  let permalink;
  if (/\.mdx?(?:[?#]|$)/.test(href) && !href.startsWith('/') && !/^https?:/.test(href)) {
    const sourceTarget = resolve(dirname(context.sourcePath), decodeURIComponent(href.split(/[?#]/)[0]));
    permalink = context.page.sourceRoutes.get(sourceTarget);
    if (!permalink) fail(context, node, `Cannot resolve source link ${href}`);
  } else {
    permalink = context.routes.get(routeKey(target.pathname));
    if (!permalink && href.startsWith('/') && !href.startsWith(context.baseUrl)) {
      permalink = context.routes.get(routeKey(`${context.baseUrl}${target.pathname.slice(1)}`));
    } else if (context.locale !== 'en' && href.startsWith('/') && context.baseUrl !== '/') {
      permalink = context.routes.get(routeKey(`${context.baseUrl}${target.pathname.slice(1)}`)) ?? permalink;
    }
  }
  if (permalink) target.pathname = markdownPath(permalink, context.baseUrl);
  return target.href;
}

function isWithin(directory, file) { const path = relative(directory, file); return path !== '..' && !path.startsWith('../'); }
function routeKey(pathname) { return pathname.replace(/\/+$/, '') || '/'; }
function text(value) { return {type: 'text', value}; }
function paragraph(value, bold = false) { return {type: 'paragraph', children: [bold ? {type: 'strong', children: [text(value)]} : text(value)]}; }
function heading(depth, value) { return {type: 'heading', depth, children: [text(value)]}; }
function list(values) { return {type: 'list', ordered: false, children: values.map(value => ({type: 'listItem', children: [paragraph(value)]}))}; }
function escapeLabel(value) { return value.replace(/[\[\]\\]/g, '\\$&'); }
function escapeAttribute(value) { return value.replaceAll('&', '&amp;').replaceAll('"', '&quot;').replaceAll('<', '&lt;'); }
function fail(context, node, message) { throw new Error(`${context.sourcePath}:${node.position?.start.line ?? 1}: ${message}`); }
