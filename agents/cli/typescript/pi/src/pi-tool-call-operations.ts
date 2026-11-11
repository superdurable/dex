// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { stat } from "node:fs/promises";
import { join } from "node:path";

import { createLocalBashOperations } from "@earendil-works/pi-coding-agent";
import { ToolExecutionError } from "@superdurable/dex-agents";
import type { WorkspaceDirectoryEntry, WorkspaceFileVersion, WorkspaceFiles } from "@superdurable/dex-agents-cli";
import { Minimatch } from "minimatch";

/** Prefix of the error that pi-coding-agent's shell execution throws when its timeout kills the command. */
const shellTimeoutErrorPrefix = "timeout:";
/** The largest timer pi-coding-agent's shell execution accepts, in milliseconds. */
const maximumShellTimeoutMs = 2_147_483_647;
const skippedFindDirectories = new Set([".git", "node_modules"]);

type BashExecution = ReturnType<typeof createLocalBashOperations>;

/** The stats pi-coding-agent's `ls` reads. */
interface DirectoryTest {
  isDirectory(): boolean;
}

/** Matches one `find` candidate by its path relative to the search directory, its absolute path, or its name. */
type FindMatcher = (relativePath: string, absolutePath: string, name: string) => boolean;

/**
 * One Pi tool call's file and shell operations, which pi-coding-agent's tools take as `operations`.
 *
 * Every file access goes through the runtime's {@link WorkspaceFiles}, with the absolute path Pi finally chose, so
 * the workspace boundary applies to the file Pi opens. The shell operation validates what it can before it spawns
 * and records when the command started.
 */
export class PiToolCallOperations {
  /** Whether the call started its command, after which a failure has an unknown outcome. */
  public hasEffectStarted = false;
  /** Whether the command's timeout killed it, so its outcome is known. */
  public hasTimedOut = false;
  private readonly readFiles = new Map<string, { bytes: Buffer; version: WorkspaceFileVersion }>();
  private readonly listedEntries = new Map<string, WorkspaceDirectoryEntry>();
  private readonly localBash: BashExecution = createLocalBashOperations();

  public constructor(private readonly files: WorkspaceFiles) {}

  public async requireRegularFile(absolutePath: string): Promise<void> {
    if (!(await this.files.statExisting(absolutePath)).isFile()) {
      throw new ToolExecutionError(`${absolutePath} is not a regular file`);
    }
  }

  public async readFileBytes(absolutePath: string): Promise<Buffer> {
    return (await this.readFileWithVersion(absolutePath)).bytes;
  }

  public async detectImageMimeType(absolutePath: string): Promise<string | undefined> {
    return detectImageMimeTypeFromBytes(await this.readFileBytes(absolutePath));
  }

  public async writeWholeFile(absolutePath: string, content: string): Promise<void> {
    await this.files.writeFile(absolutePath, content);
  }

  public async replaceReadFile(absolutePath: string, content: string): Promise<void> {
    const read = this.readFiles.get(absolutePath);
    if (read === undefined) {
      throw new ToolExecutionError(`${absolutePath} was not read before the edit; it was not changed`);
    }
    await this.files.replaceFileIfUnchanged(absolutePath, content, read.version);
  }

  public async requireExisting(absolutePath: string): Promise<boolean> {
    await this.files.statExisting(absolutePath);
    return true;
  }

  public async isDirectory(absolutePath: string): Promise<boolean> {
    return (await this.files.statExisting(absolutePath)).isDirectory();
  }

  /** Answers for an entry of a listed directory without following it; checks any other path. */
  public async describeDirectoryOrEntry(absolutePath: string): Promise<DirectoryTest> {
    const entry = this.listedEntries.get(absolutePath);
    if (entry !== undefined) {
      return { isDirectory: () => entry.isDirectory };
    }
    return this.files.statExisting(absolutePath);
  }

  public async readDirectoryNames(absolutePath: string): Promise<string[]> {
    const entries = await this.files.readDirectoryEntries(absolutePath);
    for (const entry of entries) {
      this.listedEntries.set(join(absolutePath, entry.name), entry);
    }
    return entries.map((entry) => entry.name);
  }

  /**
   * Finds the files and directories under a search directory that match a glob, as pi-coding-agent's `fd` search
   * does, without following links and without `.git` and `node_modules`.
   *
   * @returns Absolute paths, directories ending in `/`, at most `limit`.
   */
  public async findMatchingPaths(pattern: string, searchPath: string, limit: number): Promise<string[]> {
    const results: string[] = [];
    await this.collectFindMatches(searchPath, "", buildFindMatcher(pattern), limit, results);
    return results;
  }

  public async executeCommand(...execution: Parameters<BashExecution["exec"]>): ReturnType<BashExecution["exec"]> {
    const [, workingDirectory, options] = execution;
    const timeout = options.timeout;
    if (timeout !== undefined && (!Number.isFinite(timeout) || timeout <= 0 || timeout * 1000 > maximumShellTimeoutMs)) {
      throw new ToolExecutionError(
        `timeout must be a number of seconds above 0 and at most ${Math.floor(maximumShellTimeoutMs / 1000)}`,
      );
    }
    const workingDirectoryStats = await stat(workingDirectory).catch(() => undefined);
    if (workingDirectoryStats === undefined || !workingDirectoryStats.isDirectory()) {
      throw new ToolExecutionError(`the working directory ${workingDirectory} does not exist`);
    }
    this.hasEffectStarted = true;
    try {
      return await this.localBash.exec(...execution);
    } catch (error: unknown) {
      this.hasTimedOut = error instanceof Error && error.message.startsWith(shellTimeoutErrorPrefix);
      throw error;
    }
  }

  private async readFileWithVersion(absolutePath: string): Promise<{ bytes: Buffer; version: WorkspaceFileVersion }> {
    const cached = this.readFiles.get(absolutePath);
    if (cached !== undefined) {
      return cached;
    }
    const read = await this.files.readFileWithVersion(absolutePath);
    this.readFiles.set(absolutePath, read);
    return read;
  }

  private async collectFindMatches(
    directory: string,
    relativeDirectory: string,
    matcher: FindMatcher,
    limit: number,
    results: string[],
  ): Promise<void> {
    for (const entry of await this.files.readDirectoryEntries(directory)) {
      if (results.length >= limit) {
        return;
      }
      if (entry.isDirectory && skippedFindDirectories.has(entry.name)) {
        continue;
      }
      const relativePath = relativeDirectory === "" ? entry.name : `${relativeDirectory}/${entry.name}`;
      const absolutePath = join(directory, entry.name);
      if (matcher(relativePath, absolutePath, entry.name)) {
        results.push(entry.isDirectory ? `${absolutePath}/` : absolutePath);
      }
      if (entry.isDirectory) {
        await this.collectFindMatches(absolutePath, relativePath, matcher, limit, results);
      }
    }
  }
}

/**
 * Builds the matcher of a `find` glob as `fd --glob` reads it: a pattern without `/` matches the name, one with `/`
 * matches the path below the search directory, and an all-lowercase pattern ignores case.
 */
function buildFindMatcher(pattern: string): FindMatcher {
  const options = { dot: true, nocase: pattern === pattern.toLowerCase() };
  if (!pattern.includes("/")) {
    const nameMatcher = new Minimatch(pattern, options);
    return (_relativePath, _absolutePath, name) => nameMatcher.match(name);
  }
  if (pattern.startsWith("/")) {
    const absoluteMatcher = new Minimatch(pattern, options);
    return (_relativePath, absolutePath) => absoluteMatcher.match(absolutePath);
  }
  const relativePattern = pattern.startsWith("**/") || pattern === "**" ? pattern : `**/${pattern}`;
  const pathMatcher = new Minimatch(relativePattern, options);
  return (relativePath) => pathMatcher.match(relativePath);
}

/** Returns the MIME type of a PNG, JPEG, GIF, or WebP image from its first bytes, or `undefined`. */
function detectImageMimeTypeFromBytes(bytes: Buffer): string | undefined {
  if (bytes.subarray(0, 8).equals(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]))) {
    return "image/png";
  }
  if (bytes.subarray(0, 3).equals(Buffer.from([0xff, 0xd8, 0xff]))) {
    return "image/jpeg";
  }
  const header = bytes.subarray(0, 12).toString("latin1");
  if (header.startsWith("GIF87a") || header.startsWith("GIF89a")) {
    return "image/gif";
  }
  return header.startsWith("RIFF") && header.slice(8, 12) === "WEBP" ? "image/webp" : undefined;
}
