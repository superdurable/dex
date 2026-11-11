// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createHash } from "node:crypto";
import { constants, type Stats } from "node:fs";
import { lstat, mkdir, open, readdir, realpath, rename, rm, type FileHandle } from "node:fs/promises";
import { basename, dirname, isAbsolute, join, relative, resolve, sep } from "node:path";

import { ToolExecutionError } from "@superdurable/dex-agents";

const maximumReadBytes = 256 * 1024;
const maximumEditBytes = 4 * 1024 * 1024;
const maximumSearchMatches = 200;
const maximumSearchFileBytes = 1024 * 1024;
const maximumListedEntries = 1000;
const maximumWholeFileBytes = 64 * 1024 * 1024;
const skippedDirectories = new Set([".git", "node_modules"]);
const noFollowFlag = constants.O_NOFOLLOW ?? 0;

/**
 * The version of a file that {@link WorkspaceFiles.readFileWithVersion} read: its identity, size, modification time,
 * and the SHA-256 of its content. Pass it back to {@link WorkspaceFiles.replaceFileIfUnchanged} unchanged.
 *
 * @since 0.5.0
 */
export interface WorkspaceFileVersion {
  /** The file's stats when it was read. */
  readonly stats: Stats;
  /** The SHA-256 of the content that was read, in hexadecimal. */
  readonly sha256: string;
}

/**
 * One entry of a workspace directory, as {@link WorkspaceFiles.readDirectoryEntries} lists it, without following
 * links.
 *
 * @since 0.5.0
 */
export interface WorkspaceDirectoryEntry {
  /** The entry's name. */
  readonly name: string;
  /** Whether the entry is a directory. A symbolic link to a directory is not. */
  readonly isDirectory: boolean;
  /** Whether the entry is a symbolic link. */
  readonly isSymbolicLink: boolean;
}

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
 * call runs. Reads, listings, and searches check each opened file after the open and never return a file outside.
 * Node has no `openat` or `renameat`, so a write whose directory is swapped at the last moment is detected after
 * the rename and fails loudly instead of being prevented.
 *
 * A harness that replaces the `files` group with its own tools runs their file access through it, so its tools
 * keep the same boundary. Every method throws a {@link ToolExecutionError} for a refused or failed operation.
 *
 * @example
 * ```ts
 * import { realpathSync } from "node:fs";
 * import { functionTool } from "@superdurable/dex-agents/builtin-loop";
 * import { WorkspaceFiles } from "@superdurable/dex-agents-cli";
 *
 * const files = new WorkspaceFiles(realpathSync.native(context.workspace));
 * const readNotes = functionTool<{ path: string }>({
 *   name: "read_notes",
 *   description: "Read a notes file in the workspace.",
 *   parameters: { type: "object", properties: { path: { type: "string" } }, required: ["path"] },
 *   effect: "read",
 *   execute: ({ path }) => files.readFile(path),
 * });
 * ```
 * @since 0.5.0
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
    let readVersion: WorkspaceFileVersion;
    try {
      const read = await readText(file.handle, maximumEditBytes);
      if (read.isCut) {
        throw new ToolExecutionError(`${path} is larger than ${maximumEditBytes} bytes; edit_file does not change it`);
      }
      text = read.text;
      readVersion = { stats: file.stats, sha256: sha256Of(read.bytes) };
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
    const target = await this.replaceFile(path, text.replace(oldText, () => newText), false, readVersion);
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
   * Reads a whole regular file as bytes, with the version that {@link replaceFileIfUnchanged} checks.
   *
   * The file opens without following a final link, must be the file its path names after the open, and must have
   * one hard link, as for {@link readFile}.
   *
   * @param path - The file, relative to the workspace or absolute inside it.
   * @returns The content and its version.
   * @throws {ToolExecutionError} When the path is outside, missing, not a regular file, hard-linked, or larger than
   * 64 MiB.
   * @since 0.5.0
   */
  public async readFileWithVersion(path: string): Promise<{ bytes: Buffer; version: WorkspaceFileVersion }> {
    const file = await this.openRegularFile(path);
    try {
      if (file.stats.size > maximumWholeFileBytes) {
        throw new ToolExecutionError(`${path} is larger than ${maximumWholeFileBytes} bytes`);
      }
      const bytes = await file.handle.readFile();
      return { bytes, version: { stats: file.stats, sha256: sha256Of(bytes) } };
    } finally {
      await file.handle.close();
    }
  }

  /**
   * Replaces an existing file with new content when it still has the version that {@link readFileWithVersion}
   * returned.
   *
   * It writes a temporary file in the checked directory and renames it over the target, as {@link editFile} does.
   * A file that changed after the read fails with a conflict and keeps its content. The check reads at most 4 MiB,
   * so a file larger than that always reports a conflict.
   *
   * @param path - The file, relative to the workspace or absolute inside it.
   * @param content - The whole new content, as UTF-8.
   * @param version - The version the caller read.
   * @returns What was written.
   * @throws {ToolExecutionError} When the path is outside, missing, a link, hard-linked, or changed after the read.
   * @since 0.5.0
   */
  public async replaceFileIfUnchanged(path: string, content: string, version: WorkspaceFileVersion): Promise<string> {
    const target = await this.replaceFile(path, content, false, version);
    return `wrote ${Buffer.byteLength(content, "utf8")} bytes to ${this.displayPath(target)}`;
  }

  /**
   * Returns the stats of an existing path inside the workspace, following links that stay inside.
   *
   * @param path - The path, relative to the workspace or absolute inside it.
   * @returns The stats of the file or directory the path names.
   * @throws {ToolExecutionError} When the path is outside, missing, or changed while it was checked.
   * @since 0.5.0
   */
  public async statExisting(path: string): Promise<Stats> {
    const realPath = await this.resolveExisting(path);
    const stats = await lstat(realPath);
    await this.requireUnchanged(realPath, path);
    return stats;
  }

  /**
   * Lists a directory's entries in name order, without following links.
   *
   * @param path - The directory, relative to the workspace or absolute inside it.
   * @returns Every entry.
   * @throws {ToolExecutionError} When the path is outside, missing, or changed while it was listed.
   * @since 0.5.0
   */
  public async readDirectoryEntries(path: string): Promise<WorkspaceDirectoryEntry[]> {
    const directory = await this.resolveExisting(path);
    const entries = await readdir(directory, { withFileTypes: true });
    await this.requireUnchanged(directory, path);
    return entries
      .map((entry) => ({ name: entry.name, isDirectory: entry.isDirectory(), isSymbolicLink: entry.isSymbolicLink() }))
      .sort((left, right) => (left.name < right.name ? -1 : left.name > right.name ? 1 : 0));
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
      if (!stats.isFile() || stats.nlink > 1 || !(await this.isOpenedFileAt(file, stats))) {
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

  /**
   * Writes content to a temporary file in the checked directory and renames it over the target, then checks that
   * the target is that file, inside the workspace. Node has no `renameat`, so a directory swapped between the last
   * check and the rename is detected after the write rather than prevented; writes are approved calls.
   */
  private async replaceFile(
    path: string,
    content: string,
    isCreatingMissing: boolean,
    expectedVersion?: WorkspaceFileVersion,
  ): Promise<string> {
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
    let written: Stats;
    try {
      try {
        await handle.writeFile(content, "utf8");
        written = await handle.stat();
      } finally {
        await handle.close();
      }
      await this.requireUnchanged(directory, path);
      if (expectedVersion !== undefined) {
        await this.requireVersionUnchanged(target, expectedVersion, path);
      }
      await rename(temporary, target);
    } catch (error: unknown) {
      await rm(temporary, { force: true });
      throw error;
    }
    if (!(await this.isOpenedFileAt(target, written))) {
      // The written file may have landed outside; remove it where its known names still find it.
      await removeIfSameFile(target, written);
      await removeIfSameFile(temporary, written);
      throw new ToolExecutionError(
        `${path} changed while it was written: the new content may not be at ${path} inside the workspace; check the workspace`,
      );
    }
    return target;
  }

  /** Checks that a file still has the identity, size, modification time, and content that edit_file read. */
  private async requireVersionUnchanged(target: string, expected: WorkspaceFileVersion, path: string): Promise<void> {
    const conflict = (): never => {
      throw new ToolExecutionError(`${path} changed after edit_file read it; read the file again and retry the edit`);
    };
    const current = await lstat(target).catch(() => undefined);
    if (
      current === undefined ||
      current.dev !== expected.stats.dev ||
      current.ino !== expected.stats.ino ||
      current.size !== expected.stats.size ||
      current.mtimeMs !== expected.stats.mtimeMs
    ) {
      conflict();
    }
    const handle = await open(target, constants.O_RDONLY | noFollowFlag).catch(() => undefined);
    if (handle === undefined) {
      return conflict();
    }
    try {
      const read = await readText(handle, maximumEditBytes);
      if (read.isCut || sha256Of(read.bytes) !== expected.sha256) {
        conflict();
      }
    } finally {
      await handle.close();
    }
  }

  /** Returns whether a path resolves, inside the workspace, to the file whose stats an open handle gave. */
  private async isOpenedFileAt(path: string, opened: Stats): Promise<boolean> {
    const real = await realpath(path).catch(() => undefined);
    if (real === undefined || !this.isInside(real)) {
      return false;
    }
    const current = await lstat(real).catch(() => undefined);
    return current !== undefined && current.dev === opened.dev && current.ino === opened.ino;
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

  private isInside(candidate: string): boolean {
    const fromWorkspace = relative(this.workspace, candidate);
    return fromWorkspace !== ".." && !fromWorkspace.startsWith(`..${sep}`) && !isAbsolute(fromWorkspace);
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

async function readText(
  handle: FileHandle,
  maximumBytes: number,
): Promise<{ text: string; bytes: Buffer; isCut: boolean }> {
  const buffer = Buffer.alloc(maximumBytes + 1);
  const { bytesRead } = await handle.read(buffer, 0, buffer.length, 0);
  const bytes = buffer.subarray(0, Math.min(bytesRead, maximumBytes));
  return { text: bytes.toString("utf8"), bytes, isCut: bytesRead > maximumBytes };
}

function sha256Of(bytes: Buffer): string {
  return createHash("sha256").update(bytes).digest("hex");
}

async function removeIfSameFile(path: string, written: Stats): Promise<void> {
  const current = await lstat(path).catch(() => undefined);
  if (current !== undefined && current.dev === written.dev && current.ino === written.ino) {
    await rm(path, { force: true });
  }
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
