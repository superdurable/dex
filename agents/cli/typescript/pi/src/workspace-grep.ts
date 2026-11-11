// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { basename, join } from "node:path";

import { DEFAULT_MAX_BYTES, formatSize, truncateHead, truncateLine } from "@earendil-works/pi-coding-agent";
import { ToolExecutionError } from "@superdurable/dex-agents";
import type { WorkspaceFiles } from "@superdurable/dex-agents-cli";
import type { PiAgentToolResultLike } from "@superdurable/dex-agents-pi";
import { Minimatch } from "minimatch";

const defaultMatchLimit = 100;
const maximumLineCharacters = 500;
const skippedDirectories = new Set([".git", "node_modules"]);

/**
 * The arguments of Pi's `grep` tool.
 *
 * @since 0.5.0
 */
export interface WorkspaceGrepArguments {
  /** A regular expression, or the exact text with `literal`. */
  readonly pattern: string;
  /** The directory or file to search, relative to the workspace or absolute inside it; defaults to the workspace. */
  readonly path?: string;
  /** A glob that filters files, relative to `path`; a leading `!` excludes the matches instead. */
  readonly glob?: string;
  /** Whether the search ignores case. */
  readonly ignoreCase?: boolean;
  /** Whether `pattern` is the exact text instead of a regular expression. */
  readonly literal?: boolean;
  /** Lines to show before and after each match. */
  readonly context?: number;
  /** The most matches to return; defaults to 100. */
  readonly limit?: number;
}

/** One output line of the search, a match or a context line. */
interface GrepOutputLine {
  readonly lineNumber: number;
  readonly isMatch: boolean;
}

/** What the search has found so far. */
interface GrepProgress {
  readonly lines: string[];
  matchCount: number;
  isLimitReached: boolean;
  isAnyLineCut: boolean;
}

/**
 * Pi's `grep` tool on the runtime's {@link WorkspaceFiles}, instead of `rg`.
 *
 * It walks the search directory without following links and without `.git` and `node_modules`, and reads each regular
 * file through the runtime layer, so a file outside the workspace or with more than one hard link is skipped. It
 * skips files with a NUL byte as binary. The output follows Pi's: `path:line: text` for a match, `path-line- text`
 * for a context line, and the same limit notices. It does not read `.gitignore`.
 *
 * @since 0.5.0
 */
export class WorkspaceGrep {
  /**
   * Creates the search.
   *
   * @param files - The workspace's file layer.
   */
  public constructor(private readonly files: WorkspaceFiles) {}

  /**
   * Runs one search.
   *
   * @param searchPath - The directory or file to search, relative to the workspace or absolute inside it.
   * @param grepArguments - The tool's arguments.
   * @param signal - Aborted when Dex cancels the call.
   * @returns The tool result.
   * @throws {ToolExecutionError} When the path is outside or missing, or the pattern or glob is invalid.
   */
  public async search(
    searchPath: string,
    grepArguments: WorkspaceGrepArguments,
    signal: AbortSignal | undefined,
  ): Promise<PiAgentToolResultLike> {
    const expression = buildExpression(grepArguments);
    const isIncluded = buildGlobFilter(grepArguments.glob);
    const contextLines = Math.max(0, Math.floor(grepArguments.context ?? 0));
    const limit = Math.max(1, Math.floor(grepArguments.limit ?? defaultMatchLimit));
    const progress: GrepProgress = { lines: [], matchCount: 0, isLimitReached: false, isAnyLineCut: false };
    const searched = await this.files.statExisting(searchPath);
    if (searched.isDirectory()) {
      await this.searchDirectory(searchPath, "", { expression, isIncluded, contextLines, limit, progress, signal });
    } else {
      await this.searchFile(searchPath, basename(searchPath), { expression, isIncluded: () => true, contextLines, limit, progress, signal });
    }
    return formatResult(progress, limit);
  }

  private async searchDirectory(directory: string, relativeDirectory: string, search: GrepSearch): Promise<void> {
    for (const entry of await this.files.readDirectoryEntries(directory)) {
      if (search.progress.isLimitReached) {
        return;
      }
      search.signal?.throwIfAborted();
      const relativePath = relativeDirectory === "" ? entry.name : `${relativeDirectory}/${entry.name}`;
      const absolutePath = join(directory, entry.name);
      if (entry.isDirectory && !skippedDirectories.has(entry.name)) {
        await this.searchDirectory(absolutePath, relativePath, search);
      } else if (!entry.isDirectory && !entry.isSymbolicLink && search.isIncluded(relativePath, entry.name)) {
        await this.searchFile(absolutePath, relativePath, search);
      }
    }
  }

  private async searchFile(absolutePath: string, displayPath: string, search: GrepSearch): Promise<void> {
    let bytes: Buffer;
    try {
      bytes = (await this.files.readFileWithVersion(absolutePath)).bytes;
    } catch (error: unknown) {
      if (error instanceof ToolExecutionError) {
        return;
      }
      throw error;
    }
    if (bytes.includes(0)) {
      return;
    }
    const fileLines = bytes.toString("utf8").split("\n");
    if (fileLines.at(-1) === "") {
      fileLines.pop();
    }
    for (const outputLine of selectOutputLines(fileLines, search)) {
      const { text, wasTruncated } = truncateLine(fileLines[outputLine.lineNumber - 1]?.replace(/\r$/, "") ?? "", maximumLineCharacters);
      search.progress.isAnyLineCut ||= wasTruncated;
      search.progress.lines.push(
        outputLine.isMatch ? `${displayPath}:${outputLine.lineNumber}: ${text}` : `${displayPath}-${outputLine.lineNumber}- ${text}`,
      );
    }
  }
}

/** One search's settings and progress, shared by the directory walk. */
interface GrepSearch {
  readonly expression: RegExp;
  readonly isIncluded: (relativePath: string, name: string) => boolean;
  readonly contextLines: number;
  readonly limit: number;
  readonly progress: GrepProgress;
  readonly signal: AbortSignal | undefined;
}

/**
 * Picks a file's output lines: each match with its context, up to the match limit. A match past the limit only
 * marks the limit reached; one inside the last match's trailing context shows as context.
 */
function selectOutputLines(fileLines: readonly string[], search: GrepSearch): GrepOutputLine[] {
  const selected = new Map<number, boolean>();
  let lastIncludedLine = 0;
  for (let index = 0; index < fileLines.length; index += 1) {
    if (!search.expression.test(fileLines[index] ?? "")) {
      continue;
    }
    const lineNumber = index + 1;
    if (search.progress.matchCount >= search.limit) {
      search.progress.isLimitReached = true;
      if (lineNumber <= lastIncludedLine) {
        continue;
      }
      break;
    }
    search.progress.matchCount += 1;
    const first = Math.max(1, lineNumber - search.contextLines);
    lastIncludedLine = Math.min(fileLines.length, lineNumber + search.contextLines);
    for (let contextLine = first; contextLine <= lastIncludedLine; contextLine += 1) {
      selected.set(contextLine, selected.get(contextLine) === true || contextLine === lineNumber);
    }
  }
  return [...selected.entries()]
    .sort(([left], [right]) => left - right)
    .map(([lineNumber, isMatch]) => ({ lineNumber, isMatch }));
}

function buildExpression(grepArguments: WorkspaceGrepArguments): RegExp {
  const source = grepArguments.literal === true ? grepArguments.pattern.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") : grepArguments.pattern;
  try {
    return new RegExp(source, grepArguments.ignoreCase === true ? "i" : "");
  } catch (error: unknown) {
    throw new ToolExecutionError(`invalid regular expression: ${error instanceof Error ? error.message : String(error)}`);
  }
}

/** Builds the file filter of a glob as `rg --glob` reads it: without `/` it matches names at any depth. */
function buildGlobFilter(glob: string | undefined): (relativePath: string, name: string) => boolean {
  if (glob === undefined || glob === "") {
    return () => true;
  }
  const isExcluding = glob.startsWith("!");
  const pattern = glob.replace(/^!?/, "").replace(/^(?:\.\/)+/, "");
  const matcher = new Minimatch(pattern, { dot: true });
  const matchesPath = pattern.includes("/")
    ? (relativePath: string) => matcher.match(relativePath)
    : (_relativePath: string, name: string) => matcher.match(name);
  return (relativePath, name) => matchesPath(relativePath, name) !== isExcluding;
}

function formatResult(progress: GrepProgress, limit: number): PiAgentToolResultLike {
  if (progress.matchCount === 0) {
    return { content: [{ type: "text", text: "No matches found" }] };
  }
  const truncation = truncateHead(progress.lines.join("\n"), { maxLines: Number.MAX_SAFE_INTEGER });
  const notices: string[] = [];
  if (progress.isLimitReached) {
    notices.push(`${limit} matches limit reached. Use limit=${limit * 2} for more, or refine pattern`);
  }
  if (truncation.truncated) {
    notices.push(`${formatSize(DEFAULT_MAX_BYTES)} limit reached`);
  }
  if (progress.isAnyLineCut) {
    notices.push(`Some lines truncated to ${maximumLineCharacters} chars. Use read tool to see full lines`);
  }
  const text = notices.length === 0 ? truncation.content : `${truncation.content}\n\n[${notices.join(". ")}]`;
  return { content: [{ type: "text", text }] };
}
