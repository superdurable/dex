// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { constants, type Stats } from "node:fs";
import { lstat, mkdir, open, readdir, realpath, rename, rm, type FileHandle } from "node:fs/promises";
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path";

import { ToolExecutionError } from "@superdurable/dex-agents";

const maximumReadBytes = 256 * 1024;
const maximumEditBytes = 4 * 1024 * 1024;
const maximumSearchMatches = 200;
const maximumSearchFileBytes = 1024 * 1024;
const maximumListedEntries = 1000;
const skippedDirectories = new Set([".git", "node_modules"]);
const noFollowFlag = constants.O_NOFOLLOW ?? 0;

/** A regular file opened inside the workspace, with the path it was checked at. */
interface OpenedFile {
  readonly handle: FileHandle;
  readonly realPath: string;
  readonly stats: Stats;
}

/**
 * File operations confined to one workspace directory.
 *
 * Every path is relative to the workspace, or absolute inside it, compared after resolving symbolic links with
 * the operating system's canonical case. A path that leaves the workspace through `..`, an absolute path, or a
 * symbolic link fails with a {@link ToolExecutionError}. Files open without following a final symbolic link, and
 * the opened file must be the one the path names after the open, so a link swapped in meanwhile is caught. A
 * regular file with more than one hard link is refused, because its other names may be outside the workspace.
 * Writes go to a new temporary file in the checked directory and replace the target by rename.
 *
 * This is not a sandbox: an approved shell command, or another program, can still change the workspace while a
 * call runs.
 */
export class WorkspaceFiles {
  /**
   * Creates the operations.
   * @param workspace - The workspace's canonical real path.
   */
  public constructor(private readonly workspace: string) {}

  /**
   * Reads a UTF-8 text file, at most 256 KiB.
   * @param path - The file.
   * @returns The text, with a note when it was cut.
   */
  public async readFile(path: string): Promise<string> {
    const file = await this.openRegularFile(path);
    try {
      const { text, isCut } = await readText(file.handle, maximumReadBytes);
      return isCut ? `${text}\n[cut at ${maximumReadBytes} bytes]` : text;
    } finally {
      await file.handle.close();
    }
  }

  /**
   * Writes a UTF-8 text file, creating missing directories. A repeated call leaves the same content.
   * @param path - The file.
   * @param content - The new content.
   * @returns What was written.
   */
  public async writeFile(path: string, content: string): Promise<string> {
    const target = await this.replaceFile(path, content, true);
    return `wrote ${Buffer.byteLength(content, "utf8")} bytes to ${this.displayPath(target)}`;
  }

  /**
   * Replaces one exact occurrence of a text in an existing UTF-8 file.
   * @param path - The file.
   * @param oldText - The text to replace; it must occur exactly once.
   * @param newText - The replacement.
   * @returns What was edited.
   */
  public async editFile(path: string, oldText: string, newText: string): Promise<string> {
    if (oldText === "") {
      throw new ToolExecutionError("old_text must not be empty");
    }
    const file = await this.openRegularFile(path);
    let text: string;
    try {
      const read = await readText(file.handle, maximumEditBytes);
      if (read.isCut) {
        throw new ToolExecutionError(`${path} is larger than ${maximumEditBytes} bytes; edit_file does not change it`);
      }
      text = read.text;
    } finally {
      await file.handle.close();
    }
    const occurrences = text.split(oldText).length - 1;
    if (occurrences !== 1) {
      throw new ToolExecutionError(
        occurrences === 0
          ? `old_text does not occur in ${path}`
          : `old_text occurs ${occurrences} times in ${path}; include more surrounding text so that it occurs once`,
      );
    }
    const target = await this.replaceFile(path, text.replace(oldText, () => newText), false);
    return `edited ${this.displayPath(target)}: replaced 1 occurrence`;
  }

  /**
   * Lists a directory, directories ending in `/`.
   * @param path - The directory.
   * @returns One entry per line.
   */
  public async listDirectory(path: string): Promise<string> {
    const directory = await this.resolveExisting(path);
    const entries = await readdir(directory, { withFileTypes: true });
    await this.requireUnchanged(directory, path);
    const names = entries
      .map((entry) => (entry.isDirectory() ? `${entry.name}/` : entry.isSymbolicLink() ? `${entry.name}@` : entry.name))
      .sort()
      .slice(0, maximumListedEntries);
    return names.length === 0 ? "(empty directory)" : names.join("\n");
  }

  /**
   * Finds lines containing a text in the files under a directory. It skips `.git`, `node_modules`, symbolic links,
   * and files with more than one hard link.
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
    if ((await realpath(directory).catch(() => undefined)) !== directory) {
      return;
    }
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
    const handle = await open(file, constants.O_RDONLY | noFollowFlag).catch(() => undefined);
    if (handle === undefined) {
      return;
    }
    let text: string;
    try {
      const stats = await handle.stat();
      if (!stats.isFile() || stats.nlink > 1) {
        return;
      }
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

  /** Opens an existing regular file inside, without following a final link, and checks it after the open. */
  private async openRegularFile(path: string): Promise<OpenedFile> {
    const realPath = await this.resolveExisting(path);
    let handle: FileHandle;
    try {
      handle = await open(realPath, constants.O_RDONLY | noFollowFlag);
    } catch (error: unknown) {
      throw new ToolExecutionError(`${path} cannot be opened: ${describe(error)}`, { cause: error });
    }
    try {
      const stats = await handle.stat();
      await this.requireSameFile(realPath, stats, path);
      if (!stats.isFile()) {
        throw new ToolExecutionError(`${path} is not a regular file`);
      }
      requireSingleLink(stats, path);
      return { handle, realPath, stats };
    } catch (error: unknown) {
      await handle.close();
      throw error;
    }
  }

  /** Writes content to a temporary file in the checked directory and renames it over the target. */
  private async replaceFile(path: string, content: string, isCreatingMissing: boolean): Promise<string> {
    const candidate = resolve(this.workspace, path);
    if (candidate === this.workspace) {
      throw new ToolExecutionError("the workspace itself is a directory, not a file");
    }
    const directory = isCreatingMissing
      ? await this.makeDirectoryInside(dirname(candidate), path)
      : await this.resolveExisting(dirname(candidate));
    const target = join(directory, basename(candidate));
    const existing = await lstat(target).catch(() => undefined);
    if (existing === undefined && !isCreatingMissing) {
      throw new ToolExecutionError(`${path} does not exist in the workspace`);
    }
    if (existing !== undefined) {
      if (existing.isSymbolicLink()) {
        throw new ToolExecutionError(`${path} is a symbolic link; write to the file it names instead`);
      }
      if (!existing.isFile()) {
        throw new ToolExecutionError(`${path} is not a regular file`);
      }
      requireSingleLink(existing, path);
    }
    const temporary = join(directory, `.${basename(candidate)}.dex-write-${process.pid}-${Date.now()}`);
    const handle = await open(
      temporary,
      constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | noFollowFlag,
      existing === undefined ? 0o644 : existing.mode & 0o777,
    );
    try {
      try {
        await handle.writeFile(content, "utf8");
      } finally {
        await handle.close();
      }
      await this.requireUnchanged(directory, path);
      await rename(temporary, target);
    } catch (error: unknown) {
      await rm(temporary, { force: true });
      throw error;
    }
    return target;
  }

  /** Creates missing directories one component at a time, refusing links, and returns the real directory. */
  private async makeDirectoryInside(directory: string, path: string): Promise<string> {
    const missing: string[] = [];
    let existing = directory;
    while ((await lstat(existing).catch(() => undefined)) === undefined) {
      missing.unshift(basename(existing));
      existing = dirname(existing);
    }
    let current = await this.resolveExisting(existing);
    for (const name of missing) {
      const child = join(current, name);
      await mkdir(child).catch((error: NodeJS.ErrnoException) => {
        if (error.code !== "EEXIST") {
          throw error;
        }
      });
      const stats = await lstat(child);
      if (!stats.isDirectory()) {
        throw new ToolExecutionError(`${path} goes through ${this.displayPath(child)}, which is not a directory`);
      }
      current = await this.resolveExisting(child);
    }
    return current;
  }

  /** Resolves a path that must exist, following links, and checks it stays inside. */
  private async resolveExisting(path: string): Promise<string> {
    if (typeof path !== "string" || path === "" || path.includes("\0")) {
      throw new ToolExecutionError("path must be a non-empty string");
    }
    const candidate = resolve(this.workspace, path);
    let real: string;
    try {
      real = await realpath(candidate);
    } catch (error: unknown) {
      // Say "outside" rather than "missing" when the existing part of the path already leaves the workspace.
      let ancestor = dirname(candidate);
      let realAncestor = await realpath(ancestor).catch(() => undefined);
      while (realAncestor === undefined && ancestor !== dirname(ancestor)) {
        ancestor = dirname(ancestor);
        realAncestor = await realpath(ancestor).catch(() => undefined);
      }
      this.requireInside(realAncestor ?? candidate, path);
      throw new ToolExecutionError(`${path} does not exist in the workspace`, { cause: error });
    }
    this.requireInside(real, path);
    return real;
  }

  /** Checks that a checked path still resolves to itself, inside the workspace. */
  private async requireUnchanged(realPath: string, path: string): Promise<void> {
    const again = await realpath(realPath).catch(() => undefined);
    if (again !== realPath) {
      throw new ToolExecutionError(`${path} changed while the tool used it`);
    }
  }

  /** Checks that the opened file is the one its checked path names now. */
  private async requireSameFile(realPath: string, opened: Stats, path: string): Promise<void> {
    await this.requireUnchanged(realPath, path);
    const current = await lstat(realPath).catch(() => undefined);
    if (current === undefined || current.dev !== opened.dev || current.ino !== opened.ino) {
      throw new ToolExecutionError(`${path} changed while the tool opened it`);
    }
  }

  private requireInside(candidate: string, path: string): void {
    const fromWorkspace = relative(this.workspace, candidate);
    if (fromWorkspace === "..") {
      throw new ToolExecutionError(`${path} is outside the workspace ${this.workspace}`);
    }
    if (fromWorkspace.startsWith(`..${sep}`) || isAbsolute(fromWorkspace)) {
      throw new ToolExecutionError(`${path} is outside the workspace ${this.workspace}`);
    }
  }

  private displayPath(file: string): string {
    return relative(this.workspace, file) || ".";
  }
}

async function readText(handle: FileHandle, maximumBytes: number): Promise<{ text: string; isCut: boolean }> {
  const buffer = Buffer.alloc(maximumBytes + 1);
  const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
  return {
    text: buffer.subarray(0, Math.min(bytesRead, maximumBytes)).toString("utf8"),
    isCut: bytesRead > maximumBytes,
  };
}

function requireSingleLink(stats: Stats, path: string): void {
  if (stats.isFile() && stats.nlink > 1) {
    throw new ToolExecutionError(
      `${path} has ${stats.nlink} hard links; the file tools refuse files with more than one link, because another link may be outside the workspace`,
    );
  }
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
