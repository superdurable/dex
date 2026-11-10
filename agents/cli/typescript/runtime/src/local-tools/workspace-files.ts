// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { lstat, mkdir, open, readdir, realpath, rename, rm, writeFile } from "node:fs/promises";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";

import { ToolExecutionError } from "@superdurable/dex-agents";

const maximumReadBytes = 256 * 1024;
const maximumSearchMatches = 200;
const maximumSearchFileBytes = 1024 * 1024;
const maximumListedEntries = 1000;
const skippedDirectories = new Set([".git", "node_modules"]);

/**
 * File operations confined to one workspace directory.
 *
 * Every path is relative to the workspace, or absolute inside it. A path that leaves the workspace, through `..`,
 * an absolute path, or a symbolic link whose target is outside, fails with a {@link ToolExecutionError}.
 */
export class WorkspaceFiles {
  /**
   * Creates the operations.
   * @param workspace - The workspace's real path, without symbolic links.
   */
  public constructor(private readonly workspace: string) {}

  /**
   * Reads a UTF-8 text file, at most 256 KiB.
   * @param path - The file.
   * @returns The text, with a note when it was cut.
   */
  public async readFile(path: string): Promise<string> {
    const file = await this.resolveExisting(path);
    const handle = await open(file, "r");
    try {
      const buffer = Buffer.alloc(maximumReadBytes + 1);
      const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
      const text = buffer.subarray(0, Math.min(bytesRead, maximumReadBytes)).toString("utf8");
      return bytesRead > maximumReadBytes ? `${text}\n[cut at ${maximumReadBytes} bytes]` : text;
    } finally {
      await handle.close();
    }
  }

  /**
   * Writes a UTF-8 text file, creating missing directories. The write goes to a temporary file first, so a
   * repeated call leaves the same content.
   * @param path - The file.
   * @param content - The new content.
   * @returns What was written.
   */
  public async writeFile(path: string, content: string): Promise<string> {
    const file = await this.resolveForWrite(path);
    await mkdir(dirname(file), { recursive: true });
    await this.resolveForWrite(path);
    const temporary = `${file}.dex-write-${process.pid}`;
    try {
      await writeFile(temporary, content, "utf8");
      await rename(temporary, file);
    } catch (error: unknown) {
      await rm(temporary, { force: true });
      throw error;
    }
    return `wrote ${Buffer.byteLength(content, "utf8")} bytes to ${this.displayPath(file)}`;
  }

  /**
   * Lists a directory, directories ending in `/`.
   * @param path - The directory.
   * @returns One entry per line.
   */
  public async listDirectory(path: string): Promise<string> {
    const directory = await this.resolveExisting(path);
    const entries = await readdir(directory, { withFileTypes: true });
    const names = entries
      .map((entry) => (entry.isDirectory() ? `${entry.name}/` : entry.name))
      .sort()
      .slice(0, maximumListedEntries);
    return names.length === 0 ? "(empty directory)" : names.join("\n");
  }

  /**
   * Finds lines containing a text in the files under a directory, skipping `.git` and `node_modules`.
   * @param query - The text to find, compared exactly.
   * @param path - The directory to search.
   * @returns `path:line: text` per match, at most 200.
   */
  public async searchFiles(query: string, path: string): Promise<string> {
    if (query === "") {
      throw new ToolExecutionError("query must not be empty");
    }
    const root = await this.resolveExisting(path);
    const matches: string[] = [];
    await this.searchDirectory(root, query, matches);
    return matches.length === 0 ? "no matches" : matches.join("\n");
  }

  private async searchDirectory(directory: string, query: string, matches: string[]): Promise<void> {
    const entries = await readdir(directory, { withFileTypes: true });
    entries.sort((left, right) => (left.name < right.name ? -1 : left.name > right.name ? 1 : 0));
    for (const entry of entries) {
      if (matches.length >= maximumSearchMatches) {
        return;
      }
      const child = join(directory, entry.name);
      if (entry.isDirectory() && !skippedDirectories.has(entry.name)) {
        await this.searchDirectory(child, query, matches);
      } else if (entry.isFile()) {
        await this.searchFile(child, query, matches);
      }
    }
  }

  private async searchFile(file: string, query: string, matches: string[]): Promise<void> {
    const handle = await open(file, "r");
    let text: string;
    try {
      const buffer = Buffer.alloc(maximumSearchFileBytes);
      const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
      if (buffer.subarray(0, bytesRead).includes(0)) {
        return;
      }
      text = buffer.subarray(0, bytesRead).toString("utf8");
    } finally {
      await handle.close();
    }
    text.split("\n").forEach((line, index) => {
      if (matches.length < maximumSearchMatches && line.includes(query)) {
        matches.push(`${this.displayPath(file)}:${index + 1}: ${line.slice(0, 500)}`);
      }
    });
  }

  /** Resolves a path that must exist, following symbolic links, and checks it stays inside. */
  private async resolveExisting(path: string): Promise<string> {
    const candidate = this.resolveLexically(path);
    let real: string;
    try {
      real = await realpath(candidate);
    } catch (error: unknown) {
      throw new ToolExecutionError(`${path} does not exist in the workspace`, { cause: error });
    }
    this.requireInside(real, path);
    return real;
  }

  /** Resolves a path to write: its deepest existing ancestor must resolve inside, and so must the file. */
  private async resolveForWrite(path: string): Promise<string> {
    const candidate = this.resolveLexically(path);
    if (candidate === this.workspace) {
      throw new ToolExecutionError("the workspace itself is a directory, not a file");
    }
    const existing = await lstat(candidate).catch(() => undefined);
    if (existing !== undefined) {
      if (existing.isSymbolicLink()) {
        this.requireInside(await realpath(candidate).catch(() => join(this.workspace, "..")), path);
      } else if (existing.isDirectory()) {
        throw new ToolExecutionError(`${path} is a directory`);
      }
    }
    let ancestor = dirname(candidate);
    for (;;) {
      const real = await realpath(ancestor).catch(() => undefined);
      if (real !== undefined) {
        this.requireInside(real, path);
        return candidate;
      }
      ancestor = dirname(ancestor);
    }
  }

  private resolveLexically(path: string): string {
    if (typeof path !== "string" || path === "" || path.includes("\0")) {
      throw new ToolExecutionError("path must be a non-empty string");
    }
    const candidate = resolve(this.workspace, path);
    this.requireInside(candidate, path);
    return candidate;
  }

  private requireInside(candidate: string, path: string): void {
    const fromWorkspace = relative(this.workspace, candidate);
    if (fromWorkspace === "") {
      return;
    }
    if (fromWorkspace === ".." || fromWorkspace.startsWith(`..${sep}`) || isAbsolute(fromWorkspace)) {
      throw new ToolExecutionError(`${path} is outside the workspace ${this.workspace}`);
    }
  }

  private displayPath(file: string): string {
    return relative(this.workspace, file) || ".";
  }
}
