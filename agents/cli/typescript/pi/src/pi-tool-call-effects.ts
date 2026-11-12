// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { constants } from "node:fs";
import { access, mkdir, readFile, writeFile } from "node:fs/promises";

import {
  createLocalBashOperations,
  type BashOperations,
  type EditOperations,
  type WriteOperations,
} from "@earendil-works/pi-coding-agent";

/** Messages of Pi's local shell that it throws before it starts the shell process. */
const beforeSpawnFailurePrefixes = ["Invalid timeout:", "Working directory does not exist:"];

/**
 * One Pi tool call's file and shell operations: Pi's own local operations, which also note when the call may have
 * changed something, so a later failure records `outcome_unknown` instead of `failed`.
 *
 * The file operations do what Pi's defaults do, which pi-coding-agent does not export. The shell operation is
 * Pi's `createLocalBashOperations`.
 */
export class PiToolCallEffects {
  private hasMaybeChangedState = false;
  private hasShellTimedOut = false;

  /** Whether the call may have written a file or started its command, after which a failure has an unknown outcome. */
  public get hasEffectStarted(): boolean {
    return this.hasMaybeChangedState;
  }

  /** Whether the command's timeout killed it, so its outcome is known. */
  public get hasTimedOut(): boolean {
    return this.hasShellTimedOut;
  }

  /** Pi's `write` operations. */
  public writeOperations(): WriteOperations {
    return {
      mkdir: async (directory) => {
        await mkdir(directory, { recursive: true });
      },
      writeFile: (absolutePath, content) => {
        this.hasMaybeChangedState = true;
        return writeFile(absolutePath, content, "utf-8");
      },
    };
  }

  /** Pi's `edit` operations. */
  public editOperations(): EditOperations {
    return {
      readFile: (absolutePath) => readFile(absolutePath),
      access: (absolutePath) => access(absolutePath, constants.R_OK | constants.W_OK),
      writeFile: (absolutePath, content) => {
        this.hasMaybeChangedState = true;
        return writeFile(absolutePath, content, "utf-8");
      },
    };
  }

  /** Pi's local `bash` operations. */
  public bashOperations(): BashOperations {
    const local = createLocalBashOperations();
    return {
      exec: async (command, cwd, options) => {
        this.hasMaybeChangedState = true;
        try {
          return await local.exec(command, cwd, options);
        } catch (error: unknown) {
          const message = error instanceof Error ? error.message : "";
          if (beforeSpawnFailurePrefixes.some((prefix) => message.startsWith(prefix))) {
            this.hasMaybeChangedState = false;
          }
          // Pi throws `timeout:<seconds>` after it killed the command's process tree.
          if (message.startsWith("timeout:")) {
            this.hasShellTimedOut = true;
          }
          throw error;
        }
      },
    };
  }
}
