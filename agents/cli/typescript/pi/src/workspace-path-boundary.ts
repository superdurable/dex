// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { lstatSync, readlinkSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";
import { fileURLToPath } from "node:url";

const unicodeSpaces = /[  -   　]/g;
const maximumSymbolicLinkHops = 40;

/**
 * Keeps the Pi file tools inside the workspace.
 *
 * It resolves a tool's `path` argument as pi-coding-agent does: Unicode spaces become spaces, a leading `@` is
 * dropped, `~` is the home directory, a `file://` URL is its path, and a relative path is relative to the
 * workspace. It then resolves every symbolic link, as the kernel would, and checks that the result is inside the
 * workspace's real path. A dangling link counts as its target. This is not a sandbox: another process can change
 * the workspace between the check and the tool's access.
 *
 * @since 0.5.0
 */
export class WorkspacePathBoundary {
  /** The workspace's real path. */
  public readonly workspace: string;

  /**
   * Binds the boundary to a workspace.
   *
   * @param workspace - The workspace directory, which exists.
   */
  public constructor(workspace: string) {
    this.workspace = realpathSync.native(workspace);
  }

  /**
   * Returns why a tool must not use a path argument, or `null` when the path is inside the workspace.
   *
   * @param toolName - The tool's name, for the message.
   * @param pathValue - The `path` argument; `undefined` and `null` mean the workspace itself.
   * @returns The reason, or `null`.
   */
  public refusalReason(toolName: string, pathValue: unknown): string | null {
    if (pathValue === undefined || pathValue === null) {
      return null;
    }
    if (typeof pathValue !== "string") {
      return `${toolName} needs a string path`;
    }
    let absolutePath: string;
    try {
      absolutePath = this.resolveToolPath(pathValue);
    } catch {
      return `${toolName} cannot read the path ${JSON.stringify(pathValue)}`;
    }
    return this.isInside(absolutePath) ? null : this.outsideMessage(toolName);
  }

  /**
   * Returns whether an absolute path, after resolving its symbolic links, is inside the workspace.
   *
   * @param absolutePath - The path.
   * @returns `true` when it is the workspace or inside it.
   */
  public isInside(absolutePath: string): boolean {
    const resolved = resolveSymbolicLinks(resolve(absolutePath));
    if (resolved === null) {
      return false;
    }
    const fromWorkspace = relative(this.workspace, resolved);
    return !(fromWorkspace === ".." || fromWorkspace.startsWith(`..${sep}`) || isAbsolute(fromWorkspace));
  }

  /**
   * Returns the refusal message for a path outside the workspace.
   *
   * @param toolName - The tool's name.
   * @returns The message.
   */
  public outsideMessage(toolName: string): string {
    return `${toolName} may only use paths inside the workspace ${this.workspace}`;
  }

  /** Resolves a tool's path argument to an absolute path, as pi-coding-agent's `resolveToCwd` does. */
  private resolveToolPath(pathText: string): string {
    let text = pathText.replace(unicodeSpaces, " ");
    if (text.startsWith("@")) {
      text = text.slice(1);
    }
    if (text === "~") {
      text = homedir();
    } else if (text.startsWith("~/")) {
      text = join(homedir(), text.slice(2));
    } else if (/^file:\/\//.test(text)) {
      text = fileURLToPath(text);
    }
    return isAbsolute(text) ? resolve(text) : resolve(this.workspace, text);
  }
}

/**
 * Resolves every symbolic link of an absolute path, one component at a time, as the kernel would.
 *
 * A dangling link resolves to the target it would create, so a write through it is checked where it lands. Names
 * after the first missing component cannot be links and stay as they are.
 *
 * @param path - The absolute path.
 * @returns The resolved path, or `null` after more than {@link maximumSymbolicLinkHops} links.
 */
function resolveSymbolicLinks(path: string): string | null {
  let pending = path.split(sep).filter((name) => name !== "");
  let current: string = sep;
  let hops = 0;
  while (pending.length > 0) {
    const [name, ...rest] = pending as [string, ...string[]];
    pending = rest;
    if (name === ".") {
      continue;
    }
    if (name === "..") {
      current = dirname(current);
      continue;
    }
    const candidate = join(current, name);
    let isSymbolicLink: boolean;
    try {
      isSymbolicLink = lstatSync(candidate).isSymbolicLink();
    } catch {
      return resolve(candidate, ...pending);
    }
    if (!isSymbolicLink) {
      current = candidate;
      continue;
    }
    hops += 1;
    if (hops > maximumSymbolicLinkHops) {
      return null;
    }
    const target = readlinkSync(candidate);
    if (isAbsolute(target)) {
      current = sep;
    }
    pending = [...target.split(sep).filter((targetName) => targetName !== ""), ...pending];
  }
  return current;
}
