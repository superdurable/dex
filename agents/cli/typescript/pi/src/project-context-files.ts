// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { realpathSync } from "node:fs";
import { dirname, resolve } from "node:path";

import { getAgentDir, loadProjectContextFiles } from "@earendil-works/pi-coding-agent";
import { WorkspaceFiles } from "@superdurable/dex-agents-cli";

/**
 * One project context file that a model call adds to the instructions.
 *
 * @since 0.5.0
 */
export interface ProjectContextFile {
  /** The file's path, as Pi names it. */
  readonly path: string;
  /** The file's UTF-8 text, without a byte order mark. */
  readonly content: string;
}

/**
 * Loads Pi's project context files, such as `AGENTS.md`, confined to the directory each one is in.
 *
 * Pi picks the files and their order: the global one in Pi's agent directory, then one per directory from the
 * outermost ancestor of the workspace to the workspace. Pi itself skips a candidate that is not a regular file. Each
 * picked file is then read again through the runtime's `WorkspaceFiles`, scoped to the real path its directory had
 * when the loader first saw it. That real path is pinned for the life of the process and never re-derived, so a
 * directory swapped for a link elsewhere, even during a load, cannot authorize the new place. A file that leaves its
 * pinned directory, has more than one hard link, or whose directory was not pinned is skipped with a warning, once
 * per file.
 *
 * @since 0.5.0
 */
export class ProjectContextFiles {
  private readonly pinnedScopes = new Map<string, WorkspaceFiles>();
  private readonly warnedPaths = new Set<string>();

  /**
   * Creates the loader and pins the real paths of the workspace and each of its ancestors.
   *
   * @param workspace - The workspace directory, the innermost directory Pi searches.
   * @param warn - Writes a warning for the person at the terminal.
   */
  public constructor(
    private readonly workspace: string,
    private readonly warn: (line: string) => void,
  ) {
    let directory = resolve(workspace);
    for (;;) {
      this.pinScope(directory);
      const parent = dirname(directory);
      if (parent === directory) {
        break;
      }
      directory = parent;
    }
  }

  /**
   * Loads the files on this Worker. It reads the files again in every call and keeps no content.
   *
   * @returns The files that stay inside their pinned directories, in Pi's order.
   */
  public async load(): Promise<ProjectContextFile[]> {
    const agentDirectory = getAgentDir();
    // Pin before the first await, so nothing can swap the directory between Pi's pick and the pin.
    this.pinScope(resolve(agentDirectory));
    const loaded: ProjectContextFile[] = [];
    for (const candidate of loadProjectContextFiles({ cwd: this.workspace, agentDir: agentDirectory })) {
      const scope = this.pinnedScopes.get(dirname(resolve(candidate.path)));
      if (scope === undefined) {
        this.warnOnce(candidate.path, "its directory is not one the loader pinned");
        continue;
      }
      try {
        const { bytes } = await scope.readFileWithVersion(resolve(candidate.path));
        loaded.push({ path: candidate.path, content: bytes.toString("utf8").replace(/^﻿/, "") });
      } catch (error: unknown) {
        this.warnOnce(candidate.path, error instanceof Error ? error.message : String(error));
      }
    }
    return loaded;
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
    this.pinnedScopes.set(directory, new WorkspaceFiles(realPath));
  }

  private warnOnce(path: string, reason: string): void {
    if (this.warnedPaths.has(path)) {
      return;
    }
    this.warnedPaths.add(path);
    this.warn(`dex-agents-pi: skipped the project context file ${path}: ${reason}`);
  }
}
