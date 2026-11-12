// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Super Durable Source License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Super-Durable-1.0

// Positive-first writing rule: each paragraph, list item, and callout opens with what is true.
// A negation comes second, as a clarification, or goes when the positive already implies it.
// A real warning whose point is the negation keeps it: list the sentence's opening words in the
// page's front matter, with a YAML comment that says why (MDX comments would reach the Markdown export):
//   # <reason>
//   positive_first_allow:
//     - "<opening words>"
// Run with --list to print every opening this check flags, one per line.

import {readdirSync, readFileSync, statSync} from 'node:fs';
import {join, relative} from 'node:path';
import {fileURLToPath} from 'node:url';

const docsRoot = fileURLToPath(new URL('..', import.meta.url));
const roots = ['content', 'i18n/zh-Hans/docusaurus-plugin-content-docs/current'];

function mdxFiles(dir) {
  const files = [];
  for (const name of readdirSync(dir)) {
    const path = join(dir, name);
    if (statSync(path).isDirectory()) files.push(...mdxFiles(path));
    else if (/\.mdx?$/.test(name)) files.push(path);
  }
  return files;
}

// Plain text of a markdown line: no list marker, emphasis, inline code ticks, links, or JSX tags.
function plain(text) {
  return text
    .replace(/^\s*(?:[-*+]|\d+\.)\s+(\[[ xX]\]\s+)?/, '')
    .replace(/<[^>]+>/g, '')
    .replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
    .replace(/[*_`]/g, '')
    .trim();
}

const englishOpen = /^(?:do not|don't|never|avoid|no|nothing|none|not|neither|nor)\b/i;
// A subject of up to four words, then a negated verb: "Dex does not replay", "A wait does not hold".
const englishVerb = /^(?!(?:if|when|whenever|unless|until|because|although|though|while|after|before|once|since|so|even)\b)(?:[\w'.:/-]+\s+){1,4}?(?:does not|do not|doesn't|don't|is not|isn't|are not|aren't|was not|wasn't|were not|weren't|cannot|can't|can not|may not|might not|will not|won't|should not|shouldn't|must not|need not|never|has no|have no)\b/i;
const chineseOpen = /^(?:不|没有|无需|不要|别|不能|无法|并非|并不|从不|绝不)/;
// A short subject, then a negated verb: "Dex 不会重放", "等待不会占用".
const chineseVerb = /^[^，,。；：:]{1,10}?(?:不会|不能|不是|不需要|不必|不要|没有|无法|并不|并非|从不|绝不|不支持|不保证|不占用)/;
const chineseSubordinate = /^(?:如果|若|当|除非|因为|虽然|只要|一旦|在)/;

// A condition clause ("没有空闲的 Worker 时，…") opens a case, not a negative claim.
const chineseCondition = /^[^，,。；]{1,24}?时[，,]/;
// A negation inside a relative clause ("A call that never ran shows …") belongs to the subject.
const relativeNegation = /\b(?:that|which|who)\s+(?:never|does not|do not|did not|is not|are not|cannot|has no|have no)\b/i;

function flagged(sentence, chinese) {
  if (chinese) {
    if (chineseCondition.test(sentence)) return false;
    if (chineseOpen.test(sentence)) return true;
    return !chineseSubordinate.test(sentence) && chineseVerb.test(sentence);
  }
  if (englishOpen.test(sentence)) return true;
  const verb = sentence.match(englishVerb);
  return Boolean(verb) && !relativeNegation.test(verb[0]);
}

// Headings are names, so any negation in one counts: "If something does not work" reads better as "Troubleshooting".
const englishHeadingNegation = /\b(?:not|no|never|nothing|none|cannot|can't|don't|doesn't|isn't|aren't|won't|without)\b/i;
const chineseHeadingNegation = /(?:不|没有|无需|无法|并非|别)/;
// 不同 ("difference") and 区别 ("distinction") carry the characters without negating anything.
function headingNegative(text, chinese) {
  return chinese ? chineseHeadingNegation.test(text.replace(/不同|区别/g, '')) : englishHeadingNegation.test(text);
}

// Headings that name a failure case keep their negation: the failure is the subject.
// Each entry: [file, heading text, reason].
const headingAllowlist = [
  ['content/concepts/client.mdx', 'Deliver an event when no Worker runs', 'names the case: no Worker is running when the event arrives'],
  ['i18n/zh-Hans/docusaurus-plugin-content-docs/current/concepts/client.mdx', '没有 Worker 时投递事件', 'the Chinese copy of the same case'],
  ['content/correctness-and-expressiveness/workflow-patterns.mdx', 'WCP-12 Multiple Instances without Synchronization', 'the Workflow Patterns catalogue name'],
  ['content/correctness-and-expressiveness/workflow-patterns.mdx', 'WCP-15 Multiple Instances without a priori Run-Time Knowledge', 'the Workflow Patterns catalogue name'],
  ['i18n/zh-Hans/docusaurus-plugin-content-docs/current/production/server-zd-operations.mdx', '不支持的功能', 'names the list of unsupported features, like the English "Unsupported features"'],
];
function headingAllowed(rel, text) {
  return headingAllowlist.some(([file, heading]) => rel === file && text === heading);
}

function firstSentence(text) {
  const match = text.match(/^(.+?[.!?。！？])(\s|$)/);
  return match ? match[1] : text;
}

const hits = [];
for (const root of roots) {
  for (const file of mdxFiles(join(docsRoot, root))) {
    const rel = relative(docsRoot, file);
    const chinese = rel.startsWith('i18n/');
    const source = readFileSync(file, 'utf8');
    const lines = source.split('\n');
    const frontMatterText = source.match(/^---\n([\s\S]*?)\n---/)?.[1] ?? '';
    const allowBlock = frontMatterText.match(/^positive_first_allow:\n((?:\s+-\s+.+\n?)+)/m)?.[1] ?? '';
    const allowed = [...allowBlock.matchAll(/-\s+["']?(.+?)["']?\s*$/gm)].map((match) => match[1]);
    let fence;
    let frontMatter = lines[0] === '---';
    let previous = '';
    lines.forEach((raw, index) => {
      const line = raw.trimEnd();
      if (frontMatter) {
        if (index > 0 && line === '---') frontMatter = false;
        previous = line;
        return;
      }
      const fenceMatch = line.match(/^\s*(`{3,}|~{3,})/);
      if (fenceMatch) {
        if (!fence) fence = fenceMatch[1][0];
        else if (fenceMatch[1][0] === fence) fence = undefined;
        previous = line;
        return;
      }
      if (fence) return;
      const trimmed = line.trim();
      const isListItem = /^\s*(?:[-*+]|\d+\.)\s+/.test(line);
      const startsBlock = isListItem || previous.trim() === '' || /^\s*:::/.test(previous) || /^\s*<[A-Za-z][^>]*>\s*$/.test(previous);
      const skip =
        trimmed === '' ||
        /^(#|\||:::|import |export |<|\{|\}|\/\/|>)/.test(trimmed) ||
        /^\w+[=:]/.test(trimmed);
      const heading = trimmed.match(/^#{2,6}\s+(.+?)(?:\s*\{#[^}]+\})?$/);
      if (heading) {
        const text = plain(heading[1]);
        if (headingNegative(text, chinese) && !headingAllowed(rel, text)) hits.push(`${rel}:${index + 1}: heading "${text}"`);
      }
      if (!skip && startsBlock) {
        const sentence = firstSentence(plain(line));
        if (sentence && flagged(sentence, chinese)) {
          if (!allowed.some((opening) => sentence.startsWith(opening))) hits.push(`${rel}:${index + 1}: ${sentence}`);
        }
      }
      previous = line;
    });
  }
}

if (process.argv.includes('--list')) {
  console.log(hits.join('\n'));
  console.log(`${hits.length} openings`);
  process.exit(0);
}
if (hits.length > 0) {
  console.error(hits.map((hit) => `${hit}\n  → Open with what is true; put the negation second, or cut it when the positive already implies it.`).join('\n'));
  console.error(`${hits.length} paragraph, list item, or callout openings start with a negation.`);
  process.exit(1);
}
console.log('Positive-first check passed: every heading, paragraph, list item, and callout opens with what is true.');
