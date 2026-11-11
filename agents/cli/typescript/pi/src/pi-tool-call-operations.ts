// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import type { BashOperations } from "@earendil-works/pi-coding-agent";
import { ToolExecutionError } from "@superdurable/dex-agents";
import {
  WorkspaceFileRefusedError,
  type WorkspaceDirectoryEntry,
  type WorkspaceFileVersion,
  type WorkspaceFiles,
} from "@superdurable/dex-agents-cli";
import { Minimatch } from "minimatch";

import { LocalShellExecution } from "./local-shell-execution.js";

const skippedFindDirectories = new Set([".git", "node_modules"]);

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
 * the workspace boundary applies to the file Pi opens. A write that fails with anything but a refusal may have
 * changed a file. The shell operation records when the shell process started.
 */
export class PiToolCallOperations {
  private hasMaybeChangedFile = false;
  private readonly shell = new LocalShellExecution();
  private readonly readFiles = new Map<string, { bytes: Buffer; version: WorkspaceFileVersion }>();
  private readonly listedEntries = new Map<string, WorkspaceDirectoryEntry>();

  public constructor(private readonly files: WorkspaceFiles) {}

  /** Whether the call may have changed a file or started its command, after which a failure has an unknown outcome. */
  public get hasEffectStarted(): boolean {
    return this.hasMaybeChangedFile || this.shell.hasStarted;
  }

  /** Whether the command's timeout killed it, so its outcome is known. */
  public get hasTimedOut(): boolean {
    return this.shell.hasTimedOut;
  }

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
    await this.recordingMaybeChangedFile(() => this.files.writeFile(absolutePath, content));
  }

  public async replaceReadFile(absolutePath: string, content: string): Promise<void> {
    const read = this.readFiles.get(absolutePath);
    if (read === undefined) {
      throw new ToolExecutionError(`${absolutePath} was not read before the edit; it was not changed`);
    }
    await this.recordingMaybeChangedFile(() => this.files.replaceFileIfUnchanged(absolutePath, content, read.version));
  }

  public async requireExisting(absolutePath: string): Promise<boolean> {
    await this.files.statExisting(absolutePath);
    return true;
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

  public executeCommand(...execution: Parameters<BashOperations["exec"]>): ReturnType<BashOperations["exec"]> {
    return this.shell.execute(...execution);
  }

  /** Runs a write; any failure but a refusal, which comes before any change, may have changed a file. */
  private async recordingMaybeChangedFile(write: () => Promise<string>): Promise<void> {
    try {
      await write();
    } catch (error: unknown) {
      if (!(error instanceof WorkspaceFileRefusedError)) {
        this.hasMaybeChangedFile = true;
      }
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
