// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { realpathSync } from "node:fs";
import { basename, dirname } from "node:path";

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
 * outermost ancestor of the workspace to the workspace. Each picked file is then read again through the runtime's
 * `WorkspaceFiles`, scoped to the file's own directory. A file that is a symbolic link out of that directory, that
 * has more than one hard link, or that is not a regular file is skipped with a warning, once per process, so no file
 * from outside its directory reaches the instructions.
 *
 * @since 0.5.0
 */
export class ProjectContextFiles {
  private readonly warnedPaths = new Set<string>();

  /**
   * Creates the loader.
   *
   * @param workspace - The workspace directory, the innermost directory Pi searches.
   * @param warn - Writes a warning for the person at the terminal.
   */
  public constructor(
    private readonly workspace: string,
    private readonly warn: (line: string) => void,
  ) {}

  /**
   * Loads the files on this Worker. It reads the files again in every call and keeps no content.
   *
   * @returns The files that stay inside their directories, in Pi's order.
   */
  public async load(): Promise<ProjectContextFile[]> {
    const loaded: ProjectContextFile[] = [];
    for (const candidate of loadProjectContextFiles({ cwd: this.workspace, agentDir: getAgentDir() })) {
      try {
        const directory = new WorkspaceFiles(realpathSync.native(dirname(candidate.path)));
        const { bytes } = await directory.readFileWithVersion(basename(candidate.path));
        loaded.push({ path: candidate.path, content: bytes.toString("utf8").replace(/^﻿/, "") });
      } catch (error: unknown) {
        this.warnOnce(candidate.path, error instanceof Error ? error.message : String(error));
      }
    }
    return loaded;
  }

  private warnOnce(path: string, reason: string): void {
    if (this.warnedPaths.has(path)) {
      return;
    }
    this.warnedPaths.add(path);
    this.warn(`dex-agents-pi: skipped the project context file ${path}: ${reason}`);
  }
}
