// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { basename, join } from "node:path";
import { Worker } from "node:worker_threads";

import { DEFAULT_MAX_BYTES, formatSize, truncateHead, truncateLine } from "@earendil-works/pi-coding-agent";
import { ToolExecutionError } from "@superdurable/dex-agents";
import type { WorkspaceFiles } from "@superdurable/dex-agents-cli";
import type { PiAgentToolResultLike } from "@superdurable/dex-agents-pi";
import { Minimatch } from "minimatch";

const defaultMatchLimit = 100;
const maximumLineCharacters = 500;
const skippedDirectories = new Set([".git", "node_modules"]);

/**
 * The longest a `grep` call spends matching, in milliseconds, before it fails.
 *
 * @since 0.5.0
 */
export const defaultGrepMatchTimeoutMs = 10_000;

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

/** One search's settings, matcher, and progress, shared by the directory walk. */
interface GrepSearch {
  readonly matcher: GrepLineMatcher;
  readonly glob: GrepGlobFilter;
  readonly contextLines: number;
  readonly limit: number;
  readonly progress: GrepProgress;
  readonly signal: AbortSignal | undefined;
}

/**
 * Pi's `grep` tool on the runtime's {@link WorkspaceFiles}, instead of `rg`.
 *
 * It walks the search directory without following links and without `.git` and `node_modules`, and reads each
 * regular file through the runtime layer, so a file outside the workspace or with more than one hard link is
 * skipped. FIFOs, sockets, devices, and files with a NUL byte are skipped too. Matching runs in a worker thread with
 * one time budget per call, so a pattern that backtracks without end fails the call instead of blocking the Worker.
 * The output follows Pi's: `path:line: text` for a match, `path-line- text` for a context line, and the same limit
 * notices. It does not read `.gitignore`.
 *
 * @since 0.5.0
 */
export class WorkspaceGrep {
  /**
   * Creates the search.
   *
   * @param files - The workspace's file layer.
   * @param matchTimeoutMs - The longest one call spends matching; defaults to {@link defaultGrepMatchTimeoutMs}.
   */
  public constructor(
    private readonly files: WorkspaceFiles,
    private readonly matchTimeoutMs: number = defaultGrepMatchTimeoutMs,
  ) {}

  /**
   * Runs one search.
   *
   * @param searchPath - The directory or file to search, relative to the workspace or absolute inside it.
   * @param grepArguments - The tool's arguments.
   * @param signal - Aborted when Dex cancels the call.
   * @returns The tool result.
   * @throws {ToolExecutionError} When the path is outside or missing, the pattern or glob is invalid, or matching
   * takes longer than the call's time budget.
   */
  public async search(
    searchPath: string,
    grepArguments: WorkspaceGrepArguments,
    signal: AbortSignal | undefined,
  ): Promise<PiAgentToolResultLike> {
    const glob = new GrepGlobFilter(grepArguments.glob);
    const matcher = new GrepLineMatcher(grepArguments, this.matchTimeoutMs);
    try {
      const search: GrepSearch = {
        matcher,
        glob,
        contextLines: Math.max(0, Math.floor(grepArguments.context ?? 0)),
        limit: Math.max(1, Math.floor(grepArguments.limit ?? defaultMatchLimit)),
        progress: { lines: [], matchCount: 0, isLimitReached: false, isAnyLineCut: false },
        signal,
      };
      const searched = await this.files.statExisting(searchPath);
      if (searched.isDirectory()) {
        await this.searchDirectory(searchPath, "", false, search);
      } else if (searched.isFile()) {
        await this.searchFile(searchPath, basename(searchPath), search);
      }
      return formatResult(search.progress, search.limit);
    } finally {
      await matcher.close();
    }
  }

  private async searchDirectory(
    directory: string,
    relativeDirectory: string,
    isInsideIncludedDirectory: boolean,
    search: GrepSearch,
  ): Promise<void> {
    for (const entry of await this.files.readDirectoryEntries(directory)) {
      if (search.progress.isLimitReached) {
        return;
      }
      search.signal?.throwIfAborted();
      const relativePath = relativeDirectory === "" ? entry.name : `${relativeDirectory}/${entry.name}`;
      if (entry.isDirectory) {
        if (!skippedDirectories.has(entry.name) && !search.glob.isDirectoryExcluded(relativePath, entry.name)) {
          const isIncluded = isInsideIncludedDirectory || search.glob.isDirectoryIncluded(relativePath, entry.name);
          await this.searchDirectory(join(directory, entry.name), relativePath, isIncluded, search);
        }
      } else if (entry.isFile && search.glob.isFileIncluded(relativePath, entry.name, isInsideIncludedDirectory)) {
        await this.searchFile(join(directory, entry.name), relativePath, search);
      }
    }
  }

  private async searchFile(path: string, displayPath: string, search: GrepSearch): Promise<void> {
    let bytes: Buffer;
    try {
      bytes = (await this.files.readFileWithVersion(path)).bytes;
    } catch (error: unknown) {
      if (error instanceof ToolExecutionError) {
        return;
      }
      throw error;
    }
    if (bytes.includes(0)) {
      return;
    }
    const fileLines = bytes.toString("utf8").split("\n").map((line) => line.replace(/\r$/, ""));
    if (fileLines.at(-1) === "") {
      fileLines.pop();
    }
    const matchingIndexes = await search.matcher.matchLines(fileLines, search.signal);
    for (const outputLine of selectOutputLines(fileLines.length, matchingIndexes, search)) {
      const { text, wasTruncated } = truncateLine(fileLines[outputLine.lineNumber - 1] ?? "", maximumLineCharacters);
      search.progress.isAnyLineCut ||= wasTruncated;
      search.progress.lines.push(
        outputLine.isMatch ? `${displayPath}:${outputLine.lineNumber}: ${text}` : `${displayPath}-${outputLine.lineNumber}- ${text}`,
      );
    }
  }
}

/** Matches lines in a worker thread, which it terminates when the call's time budget runs out or the call aborts. */
class GrepLineMatcher {
  private readonly source: string;
  private readonly flags: string;
  private worker: Worker | undefined;
  private remainingMs: number;

  public constructor(grepArguments: WorkspaceGrepArguments, timeoutMs: number) {
    this.source =
      grepArguments.literal === true ? grepArguments.pattern.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") : grepArguments.pattern;
    this.flags = grepArguments.ignoreCase === true ? "i" : "";
    try {
      // Compiling never backtracks; only matching runs in the worker.
      new RegExp(this.source, this.flags);
    } catch (error: unknown) {
      throw new ToolExecutionError(`invalid regular expression: ${error instanceof Error ? error.message : String(error)}`);
    }
    this.remainingMs = timeoutMs;
  }

  /**
   * Returns the indexes of the lines that match.
   *
   * @throws {ToolExecutionError} When matching takes longer than the remaining budget.
   */
  public async matchLines(lines: readonly string[], signal: AbortSignal | undefined): Promise<number[]> {
    signal?.throwIfAborted();
    const worker = this.startedWorker();
    const startedAt = Date.now();
    try {
      return await new Promise<number[]>((resolvePromise, rejectPromise) => {
        const timer = setTimeout(() => {
          finish();
          rejectPromise(
            new ToolExecutionError(
              "the pattern took too long to match; use a simpler pattern, literal: true, or a narrower path or glob",
            ),
          );
        }, Math.max(0, this.remainingMs));
        const onAbort = (): void => {
          finish();
          rejectPromise(signal?.reason ?? new Error("aborted"));
        };
        const onMessage = (matchingIndexes: number[]): void => {
          finish();
          resolvePromise(matchingIndexes);
        };
        const onError = (error: Error): void => {
          finish();
          rejectPromise(error);
        };
        const finish = (): void => {
          clearTimeout(timer);
          signal?.removeEventListener("abort", onAbort);
          worker.off("message", onMessage);
          worker.off("error", onError);
        };
        signal?.addEventListener("abort", onAbort, { once: true });
        worker.on("message", onMessage);
        worker.on("error", onError);
        worker.postMessage({ source: this.source, flags: this.flags, lines });
      });
    } catch (error: unknown) {
      await this.close();
      throw error;
    } finally {
      this.remainingMs -= Date.now() - startedAt;
    }
  }

  /** Terminates the worker thread, if one started. */
  public async close(): Promise<void> {
    const worker = this.worker;
    this.worker = undefined;
    await worker?.terminate();
  }

  private startedWorker(): Worker {
    this.worker ??= new Worker(new URL("./grep-match-worker.js", import.meta.url));
    return this.worker;
  }
}

/**
 * The file filter of a `grep` glob, after `rg --glob`: a pattern without `/` matches names at any depth, one with
 * `/` matches the path below the search root (a leading `/` anchors it there too), a trailing `/` matches only
 * directories, and a leading `!` excludes. A matching directory includes or excludes its whole subtree.
 */
class GrepGlobFilter {
  private readonly matcher: Minimatch | undefined;
  private readonly isExcluding: boolean;
  private readonly isPathAnchored: boolean;
  private readonly isDirectoryOnly: boolean;

  public constructor(glob: string | undefined) {
    this.isExcluding = glob?.startsWith("!") === true;
    let pattern = (glob ?? "").replace(/^!/, "").replace(/^(?:\.\/)+/, "");
    this.isDirectoryOnly = pattern.endsWith("/");
    pattern = pattern.replace(/\/+$/, "");
    this.isPathAnchored = pattern.includes("/");
    pattern = pattern.replace(/^\//, "");
    this.matcher = pattern === "" ? undefined : new Minimatch(pattern, { dot: true });
  }

  public isDirectoryExcluded(relativePath: string, name: string): boolean {
    return this.isExcluding && this.matches(relativePath, name);
  }

  public isDirectoryIncluded(relativePath: string, name: string): boolean {
    return !this.isExcluding && this.matches(relativePath, name);
  }

  public isFileIncluded(relativePath: string, name: string, isInsideIncludedDirectory: boolean): boolean {
    if (this.matcher === undefined) {
      return true;
    }
    const isFileMatch = !this.isDirectoryOnly && this.matches(relativePath, name);
    return this.isExcluding ? !isFileMatch : isFileMatch || isInsideIncludedDirectory;
  }

  private matches(relativePath: string, name: string): boolean {
    if (this.matcher === undefined) {
      return false;
    }
    return this.isPathAnchored ? this.matcher.match(relativePath) : this.matcher.match(name);
  }
}

/**
 * Picks a file's output lines: each match with its context, up to the match limit. A match past the limit only
 * marks the limit reached; one inside the last match's trailing context shows as context.
 */
function selectOutputLines(lineCount: number, matchingIndexes: readonly number[], search: GrepSearch): GrepOutputLine[] {
  const selected = new Map<number, boolean>();
  let lastIncludedLine = 0;
  for (const index of matchingIndexes) {
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
    lastIncludedLine = Math.min(lineCount, lineNumber + search.contextLines);
    for (let contextLine = first; contextLine <= lastIncludedLine; contextLine += 1) {
      selected.set(contextLine, selected.get(contextLine) === true || contextLine === lineNumber);
    }
  }
  return [...selected.entries()]
    .sort(([left], [right]) => left - right)
    .map(([lineNumber, isMatch]) => ({ lineNumber, isMatch }));
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
