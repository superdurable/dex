// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// A Chinese page that exists must keep up with its English page: both have the same number of
// H2 sections. A page that differs on purpose goes in the allowlist below, with the reason.
// Run with --list to print every mismatch, including allowed ones.

import {existsSync, readdirSync, readFileSync, statSync} from 'node:fs';
import {join, relative} from 'node:path';
import {fileURLToPath} from 'node:url';

const docsRoot = fileURLToPath(new URL('..', import.meta.url));
const englishRoot = join(docsRoot, 'content');
const chineseRoot = join(docsRoot, 'i18n/zh-Hans/docusaurus-plugin-content-docs/current');

// Each entry: [path relative to content/, reason].
const allowlist = [];

function mdxFiles(dir) {
  const files = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) files.push(...mdxFiles(path));
    else if (/\.mdx?$/.test(name)) files.push(path);
  }
  return files;
}

function sectionCount(file) {
  let fence;
  let count = 0;
  for (const raw of readFileSync(file, 'utf8').split('\n')) {
    const fenceMatch = raw.match(/^\s*(`{3,}|~{3,})/);
    if (fenceMatch) {
      if (!fence) fence = fenceMatch[1][0];
      else if (fenceMatch[1][0] === fence) fence = undefined;
      continue;
    }
    if (!fence && /^## /.test(raw)) count += 1;
  }
  return count;
}

const mismatches = [];
for (const chinese of mdxFiles(chineseRoot)) {
  const rel = relative(chineseRoot, chinese);
  const english = join(englishRoot, rel);
  if (!existsSync(english)) continue;
  const en = sectionCount(english);
  const zh = sectionCount(chinese);
  if (en !== zh) mismatches.push({rel, en, zh, allowed: allowlist.find(([path]) => path === rel)?.[1]});
}

if (process.argv.includes('--list')) {
  for (const {rel, en, zh, allowed} of mismatches) console.log(`${rel}: en ${en}, zh ${zh}${allowed ? ` (allowed: ${allowed})` : ''}`);
  console.log(`${mismatches.length} pages differ`);
  process.exit(0);
}
// Code is the same in both locales. Text fences are diagrams and prompts, which may be translated.
function codeBlocks(file) {
  const source = readFileSync(file, 'utf8');
  const blocks = [...source.matchAll(/^( *)(`{3,}|~{3,})([^\n]*)\n[\s\S]*?\n\1\2[ \t]*$/gm)]
    .filter((match) => !/^text\b/.test(match[3].trim()))
    .map((match) => match[0].trim());
  const props = [...source.matchAll(/examples=\{\{[\s\S]*?\}\}/g)].map((match) => match[0]);
  return [...blocks, ...props];
}
const codeDrift = [];
for (const chinese of mdxFiles(chineseRoot)) {
  const rel = relative(chineseRoot, chinese);
  const english = join(englishRoot, rel);
  if (!existsSync(english)) continue;
  const chineseBlocks = new Set(codeBlocks(chinese));
  const missing = codeBlocks(english).filter((block) => !chineseBlocks.has(block));
  if (missing.length > 0) codeDrift.push(`${rel}: ${missing.length} code block(s) differ from English. First: ${missing[0].split('\n').slice(0, 2).join(' ').slice(0, 120)}`);
}
if (codeDrift.length > 0) {
  console.error(codeDrift.join('\n'));
  console.error('Copy code blocks and SDK snippet props from the English page unchanged.');
  process.exit(1);
}

const failures = mismatches.filter(({allowed}) => !allowed);
if (failures.length > 0) {
  console.error(failures.map(({rel, en, zh}) => `${rel}: English has ${en} H2 sections, Chinese has ${zh}.\n  → Translate the missing sections, or allowlist the page with a reason in tests/docs-locale-sections.mjs.`).join('\n'));
  console.error(`${failures.length} Chinese pages have fallen behind their English page.`);
  process.exit(1);
}
console.log('Locale section check passed: every Chinese page has as many H2 sections as its English page, and the same code.');
