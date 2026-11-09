/*
 * Copyright (c) 2026 Super Durable, Inc.
 *
 * Licensed under the Super Durable Source License 1.0.
 * You may not use this file except in compliance with the License.
 * See the LICENSE file in the repository root.
 *
 * SPDX-License-Identifier: LicenseRef-Super-Durable-1.0
 */

import {createContext, useContext} from 'react';

/** How a code block renders: an editor with line numbers, a terminal with prompts, or plain text. */
export type IdeCodeMode = 'editor' | 'terminal' | 'plain';

/** One collapsed run of lines, 1-based and inclusive. */
export type IdeFoldRange = {start: number; end: number; label: string};

export type IdeCodeBlockInfo = {
  mode: IdeCodeMode;
  metastring?: string;
};

export const IdeCodeBlockContext = createContext<IdeCodeBlockInfo>({mode: 'editor'});

/** The runnable example a snippet comes from, set by SdkTabs so the editor tab can name the file. */
export const IdeExampleFileContext = createContext<string | undefined>(undefined);

export function useIdeCodeBlockInfo(): IdeCodeBlockInfo {
  return useContext(IdeCodeBlockContext);
}

const terminalLanguages = new Set(['bash', 'sh', 'shell', 'console', 'zsh', 'powershell']);
const plainLanguages = new Set(['text', 'txt', 'plaintext', 'plain', 'ascii']);

export function ideCodeMode(language: string | undefined): IdeCodeMode {
  if (!language || plainLanguages.has(language)) {
    return 'plain';
  }
  return terminalLanguages.has(language) ? 'terminal' : 'editor';
}

const languageLabels: Record<string, string> = {
  go: 'Go',
  python: 'Python',
  py: 'Python',
  java: 'Java',
  typescript: 'TypeScript',
  ts: 'TypeScript',
  tsx: 'TypeScript',
  javascript: 'JavaScript',
  js: 'JavaScript',
  rust: 'Rust',
  rs: 'Rust',
  json: 'JSON',
  yaml: 'YAML',
  yml: 'YAML',
  toml: 'TOML',
  sql: 'SQL',
  protobuf: 'Protobuf',
  proto: 'Protobuf',
  diff: 'Diff',
  dockerfile: 'Dockerfile',
  bash: 'Shell',
  sh: 'Shell',
  shell: 'Shell',
};

export function ideLanguageLabel(language: string): string {
  return languageLabels[language] ?? language.toUpperCase();
}

/** Parses a fence metastring attribute such as fold="12-40,55-60". */
export function explicitFoldRanges(metastring: string | undefined, lineCount: number): IdeFoldRange[] {
  const match = metastring?.match(/(?:^|\s)fold=["']([\d,\s-]+)["']/);
  if (!match) {
    return [];
  }
  return match[1].split(',').flatMap((part) => {
    const [startText, endText] = part.trim().split('-');
    const start = Number(startText);
    const end = Math.min(Number(endText ?? startText), lineCount);
    if (!Number.isInteger(start) || !Number.isInteger(end) || start < 1 || end < start) {
      return [];
    }
    return [{start, end, label: `${end - start + 1} lines`}];
  });
}

const importLine: Record<string, RegExp> = {
  python: /^(import |from \S+ import )/,
  go: /^(import |package )/,
  java: /^(import |package )/,
  typescript: /^import[\s{]/,
  javascript: /^import[\s{]/,
  rust: /^(use |extern crate )/,
};

/** Finds the leading import block, so it starts collapsed like in an editor. */
export function importFoldRange(lines: string[], language: string): IdeFoldRange | undefined {
  const startsImport = importLine[language === 'ts' ? 'typescript' : language === 'py' ? 'python' : language];
  if (!startsImport) {
    return undefined;
  }
  let index = 0;
  while (index < lines.length && lines[index].trim() === '') {
    index += 1;
  }
  const start = index;
  let last = -1;
  let openStatement = false;
  for (; index < lines.length; index += 1) {
    const line = lines[index];
    const trimmed = line.trim();
    if (openStatement) {
      last = index;
      if ((language === 'go' && trimmed === ')') || /[;)]\s*$|from ['"].+['"];?\s*$/.test(trimmed)) {
        openStatement = false;
      }
      continue;
    }
    if (trimmed === '') {
      continue;
    }
    if (!startsImport.test(line)) {
      break;
    }
    last = index;
    openStatement = (language === 'go' && /\($/.test(trimmed)) ||
      (/^(import|use)\b/.test(trimmed) && /[{(]\s*$/.test(trimmed));
  }
  const count = last - start + 1;
  if (last < 0 || count < 3) {
    return undefined;
  }
  return {start: start + 1, end: last + 1, label: `${count} import lines`};
}

/** Collapses the tail of a long block that has no explicit folds. */
export function longBlockFoldRange(lineCount: number): IdeFoldRange | undefined {
  const visibleLines = 30;
  if (lineCount <= 50) {
    return undefined;
  }
  return {start: visibleLines + 1, end: lineCount, label: `${lineCount - visibleLines} more lines`};
}
