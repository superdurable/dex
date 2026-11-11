// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { realpathSync } from "node:fs";
import { lstat, realpath, stat } from "node:fs/promises";
import { dirname, join, resolve } from "node:path";

import { WorkspaceFiles } from "@superdurable/dex-agents-cli-core";

/** The names Pi looks for in each directory, in its order; the first regular file wins. */
const contextFileNames = ["AGENTS.override.md", "AGENTS.md", "AGENTS.MD", "CLAUDE.md", "CLAUDE.MD"];
/** The largest project context file the command reads. */
const maximumContextFileBytes = 1024 * 1024;

/**
 * One project context file that a model call adds to the instructions.
 *
 * @since 0.5.0
 */
export interface ProjectContextFile {
  /** The file's path in its directory. */
  readonly path: string;
  /** The file's UTF-8 text, without a byte order mark. */
  readonly content: string;
}

/** One directory that may hold a project context file. */
interface ContextDirectory {
  readonly path: string;
  /** Whether its files must belong to the current user or root: true for the workspace's ancestors. */
  readonly isOwnershipChecked: boolean;
}

/**
 * Loads the project context files, such as `AGENTS.md`, as Pi finds them, confined to the directory each one is in.
 *
 * It searches Pi's directories in Pi's order: Pi's global agent directory, then each directory from the outermost
 * ancestor of the workspace to the workspace. In each, the first of `AGENTS.override.md`, `AGENTS.md`, `AGENTS.MD`,
 * `CLAUDE.md`, and `CLAUDE.MD` that is a regular file, or a link to one, is the candidate. Every candidate is read
 * through the CLI core's `WorkspaceFiles`, at most 1 MiB, never with a synchronous or blocking read.
 *
 * The real path of each directory is pinned the first time the loader sees it and never derived again. A candidate
 * loads only when its real path is directly in its pinned directory: a link to a file elsewhere, even below that
 * directory, is skipped, as is a file with more than one hard link. In an ancestor of the workspace, both the file
 * and, for a link such as `AGENTS.md -> CLAUDE.md`, the link itself must belong to the current user or root, so
 * another user's file or link in a shared directory such as `/tmp` never reaches the instructions. Each skipped file
 * is reported once through `warn`.
 *
 * @since 0.5.0
 */
export class ProjectContextFiles {
  private readonly pinnedScopes = new Map<string, { readonly realPath: string; readonly files: WorkspaceFiles }>();
  private readonly warnedPaths = new Set<string>();
  private readonly directories: readonly ContextDirectory[];

  /**
   * Creates the loader and pins the real paths of the workspace and each of its ancestors.
   *
   * @param workspace - The workspace directory, the innermost directory searched.
   * @param agentDirectory - Pi's global agent directory, such as `~/.pi/agent`.
   * @param warn - Writes a warning for the person at the terminal.
   * @param ownerUserId - The user whose files the ancestors may hold, besides root; defaults to the current user.
   * `undefined` on a platform without user IDs, which skips the ownership check.
   */
  public constructor(
    workspace: string,
    private readonly agentDirectory: string,
    private readonly warn: (line: string) => void,
    private readonly ownerUserId: number | undefined = typeof process.getuid === "function" ? process.getuid() : undefined,
  ) {
    const workspaceDirectory = resolve(workspace);
    const ancestors: string[] = [];
    for (let directory = dirname(workspaceDirectory); ; directory = dirname(directory)) {
      ancestors.unshift(directory);
      if (dirname(directory) === directory) {
        break;
      }
    }
    this.directories = [
      { path: resolve(agentDirectory), isOwnershipChecked: false },
      ...ancestors.map((path) => ({ path, isOwnershipChecked: true })),
      { path: workspaceDirectory, isOwnershipChecked: false },
    ].filter((directory, index, all) => all.findIndex((other) => other.path === directory.path) === index);
    for (const directory of this.directories.slice(1)) {
      this.pinScope(directory.path);
    }
  }

  /**
   * Loads the files on this Worker. It reads the files again in every call and keeps no content.
   *
   * @returns The files that pass the checks, in Pi's order.
   */
  public async load(): Promise<ProjectContextFile[]> {
    // Pi's agent directory may appear later; pin it before the first await of this load.
    this.pinScope(resolve(this.agentDirectory));
    const loaded: ProjectContextFile[] = [];
    for (const directory of this.directories) {
      const candidate = await this.findCandidate(directory.path);
      if (candidate === undefined) {
        continue;
      }
      const file = await this.readCandidate(candidate, directory);
      if (file !== undefined) {
        loaded.push(file);
      }
    }
    return loaded;
  }

  /** Returns the first context file in a directory that is a regular file or a link to one. */
  private async findCandidate(directory: string): Promise<string | undefined> {
    for (const name of contextFileNames) {
      const path = join(directory, name);
      const entry = await lstat(path).catch(() => undefined);
      if (entry === undefined) {
        continue;
      }
      // stat never blocks, even on a FIFO, and follows a link to see what it names.
      const target = entry.isSymbolicLink() ? await stat(path).catch(() => undefined) : entry;
      if (target?.isFile() === true) {
        return path;
      }
    }
    return undefined;
  }

  private async readCandidate(candidate: string, directory: ContextDirectory): Promise<ProjectContextFile | undefined> {
    const scope = this.pinnedScopes.get(directory.path);
    if (scope === undefined) {
      this.warnOnce(candidate, "its directory is not one the loader pinned");
      return undefined;
    }
    try {
      if (directory.isOwnershipChecked) {
        // Another user may own a link whose target is ours, so the link's owner counts too.
        const link = await lstat(candidate);
        if (link.isSymbolicLink() && !this.isOwnerAllowed(link.uid)) {
          this.warnOnce(candidate, `it is a link that belongs to user ${link.uid}, not to you or root`);
          return undefined;
        }
      }
      const realPath = await realpath(candidate);
      if (dirname(realPath) !== scope.realPath) {
        this.warnOnce(candidate, `it resolves to ${realPath}, outside its directory ${scope.realPath}`);
        return undefined;
      }
      const { bytes, version } = await scope.files.readFileWithVersion(realPath, maximumContextFileBytes);
      const current = await lstat(realPath);
      if (current.dev !== version.stats.dev || current.ino !== version.stats.ino) {
        this.warnOnce(candidate, "it changed while it was read");
        return undefined;
      }
      if (directory.isOwnershipChecked && !this.isOwnerAllowed(version.stats.uid)) {
        this.warnOnce(candidate, `it belongs to user ${version.stats.uid}, not to you or root`);
        return undefined;
      }
      return { path: candidate, content: bytes.toString("utf8").replace(/^﻿/, "") };
    } catch (error: unknown) {
      this.warnOnce(candidate, error instanceof Error ? error.message : String(error));
      return undefined;
    }
  }

  private isOwnerAllowed(ownerId: number): boolean {
    return this.ownerUserId === undefined || ownerId === this.ownerUserId || ownerId === 0;
  }

  /** Pins a directory's current real path once; a missing directory stays unpinned until it exists. */
  private pinScope(directory: string): void {
    if (this.pinnedScopes.has(directory)) {
      return;
    }
    let realPath: string;
    try {
      realPath = realpathSync.native(directory);
    } catch {
      return;
    }
    this.pinnedScopes.set(directory, { realPath, files: new WorkspaceFiles(realPath) });
  }

  private warnOnce(path: string, reason: string): void {
    if (this.warnedPaths.has(path)) {
      return;
    }
    this.warnedPaths.add(path);
    this.warn(`dex-agents-pi: skipped the project context file ${path}: ${reason}`);
  }
}
