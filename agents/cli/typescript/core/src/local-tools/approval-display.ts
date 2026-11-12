// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { basename, extname } from "node:path";

import { isCredentialLikeName, maxApprovalPreviewBytes, type AgentInterruptDisplay } from "@superdurable/dex-agents";

import type { WorkspaceFiles } from "./workspace-files.js";

/** Largest file, and largest new content, a file write preview diffs: 64 KiB. */
const maximumPreviewInputBytes = 64 * 1024;
/** Most lines a file write preview has, its two header lines included. */
const maximumPreviewLines = 200;
const contextLines = 3;
const credentialFileNames = new Set([".netrc", ".npmrc", ".pypirc", ".pgpass", "credentials", ".git-credentials"]);
const credentialFileExtensions = new Set([".pem", ".key", ".p12", ".pfx", ".jks", ".keystore"]);

/**
 * The display of a `write_file` call: its workspace path, its size, and a diff against the file now.
 * @param files - The workspace files.
 * @param path - The `path` argument.
 * @param content - The `content` argument.
 * @returns The display.
 */
export function buildWriteFileDisplay(files: WorkspaceFiles, path: string, content: string): AgentInterruptDisplay {
  const source = files.readTextForPreview(path, maximumPreviewInputBytes);
  const target = source?.displayPath ?? path;
  return {
    target,
    summary: `Writes ${describeBytes(content)} to ${target}.`,
    preview: source === undefined ? null : buildFilePreview(source.displayPath, source.text, content),
  };
}

/**
 * The display of an `edit_file` call: its workspace path, the replaced and new sizes, and a diff against the file
 * now when `oldText` occurs in it once.
 * @param files - The workspace files.
 * @param path - The `path` argument.
 * @param oldText - The `old_text` argument.
 * @param newText - The `new_text` argument.
 * @returns The display.
 */
export function buildEditFileDisplay(
  files: WorkspaceFiles,
  path: string,
  oldText: string,
  newText: string,
): AgentInterruptDisplay {
  const source = files.readTextForPreview(path, maximumPreviewInputBytes);
  const target = source?.displayPath ?? path;
  const current = source?.text ?? null;
  const isEditable = current !== null && oldText !== "" && current.split(oldText).length === 2;
  return {
    target,
    summary: `Replaces ${describeBytes(oldText)} with ${describeBytes(newText)} in ${target}.`,
    preview:
      isEditable && source !== undefined
        ? buildFilePreview(source.displayPath, current, current.replace(oldText, () => newText))
        : null,
  };
}

/**
 * The display of a `run_shell_command` call: the working directory and the full command.
 * @param workspace - The workspace, the command's working directory.
 * @param command - The `command` argument.
 * @returns The display.
 */
export function buildShellCommandDisplay(workspace: string, command: string): AgentInterruptDisplay {
  return { target: workspace, summary: `Runs a shell command in ${workspace}: ${command}`, preview: null };
}

/**
 * Whether a file's name says it holds credentials, such as `.env`, `.env.local`, `id_ed25519`, `server.pem`, or
 * `api_key.txt`, so a write preview shows only its size.
 * @param path - The file's path.
 * @returns `true` for a credential file.
 */
export function isCredentialFileName(path: string): boolean {
  const name = basename(path).toLowerCase();
  const extension = extname(name);
  const stem = extension === "" ? name : name.slice(0, -extension.length);
  return (
    name === ".env" ||
    name.startsWith(".env.") ||
    name.startsWith("id_") ||
    credentialFileNames.has(name) ||
    credentialFileExtensions.has(extension) ||
    isCredentialLikeName(stem.replace(/^\.+/, ""))
  );
}

function describeBytes(text: string): string {
  const bytes = Buffer.byteLength(text, "utf8");
  return bytes === 1 ? "1 byte" : `${bytes} bytes`;
}

/** The labelled diff, or `null` for a credential file or a diff over the bounds. */
function buildFilePreview(displayPath: string, currentText: string | null, newText: string): string | null {
  if (isCredentialFileName(displayPath) || Buffer.byteLength(newText, "utf8") > maximumPreviewInputBytes) {
    return null;
  }
  const diff = buildUnifiedDiff(displayPath, currentText, newText);
  return diff !== undefined && Buffer.byteLength(diff, "utf8") <= maxApprovalPreviewBytes ? diff : null;
}

type DiffLine = { readonly kind: " " | "-" | "+"; readonly text: string };

/**
 * A unified diff with 3 lines of context, whose header lines say which side is the file now, or `undefined` when
 * it would have more than {@link maximumPreviewLines} lines.
 */
function buildUnifiedDiff(displayPath: string, currentText: string | null, newText: string): string | undefined {
  const oldLines = currentText === null ? [] : splitLines(currentText);
  const newLines = splitLines(newText);
  const edits = diffLines(oldLines, newLines, maximumPreviewLines);
  if (edits === undefined) {
    return undefined;
  }
  const output = [
    currentText === null ? "--- /dev/null (no file now)" : `--- ${displayPath} (the file now)`,
    `+++ ${displayPath} (after this write)`,
  ];
  for (const hunk of groupHunks(edits)) {
    output.push(hunk.header, ...hunk.lines.map((line) => `${line.kind}${line.text}`));
    if (output.length > maximumPreviewLines) {
      return undefined;
    }
  }
  return `${output.join("\n")}\n`;
}

function splitLines(text: string): string[] {
  if (text === "") {
    return [];
  }
  const lines = text.split("\n");
  if (lines.at(-1) === "") {
    lines.pop();
  }
  return lines;
}

/** Myers' shortest edit script, or `undefined` when it needs more than `maximumEdits` inserts and deletes. */
function diffLines(oldLines: readonly string[], newLines: readonly string[], maximumEdits: number): DiffLine[] | undefined {
  const oldCount = oldLines.length;
  const newCount = newLines.length;
  const limit = Math.min(oldCount + newCount, maximumEdits);
  const offset = limit + 1;
  const furthest = new Int32Array(2 * limit + 3);
  const trace: Int32Array[] = [];
  for (let edits = 0; edits <= limit; edits += 1) {
    trace.push(furthest.slice());
    for (let diagonal = -edits; diagonal <= edits; diagonal += 2) {
      const isDown =
        diagonal === -edits || (diagonal !== edits && furthest[diagonal - 1 + offset]! < furthest[diagonal + 1 + offset]!);
      let oldIndex = isDown ? furthest[diagonal + 1 + offset]! : furthest[diagonal - 1 + offset]! + 1;
      let newIndex = oldIndex - diagonal;
      while (oldIndex < oldCount && newIndex < newCount && oldLines[oldIndex] === newLines[newIndex]) {
        oldIndex += 1;
        newIndex += 1;
      }
      furthest[diagonal + offset] = oldIndex;
      if (oldIndex >= oldCount && newIndex >= newCount) {
        return backtrack(trace, oldLines, newLines, offset);
      }
    }
  }
  return undefined;
}

function backtrack(
  trace: readonly Int32Array[],
  oldLines: readonly string[],
  newLines: readonly string[],
  offset: number,
): DiffLine[] {
  const lines: DiffLine[] = [];
  let oldIndex = oldLines.length;
  let newIndex = newLines.length;
  for (let edits = trace.length - 1; edits >= 0; edits -= 1) {
    const furthest = trace[edits]!;
    const diagonal = oldIndex - newIndex;
    const isDown =
      diagonal === -edits || (diagonal !== edits && furthest[diagonal - 1 + offset]! < furthest[diagonal + 1 + offset]!);
    const previousDiagonal = isDown ? diagonal + 1 : diagonal - 1;
    const previousOld = furthest[previousDiagonal + offset]!;
    const previousNew = previousOld - previousDiagonal;
    while (oldIndex > previousOld && newIndex > previousNew) {
      oldIndex -= 1;
      newIndex -= 1;
      lines.push({ kind: " ", text: oldLines[oldIndex]! });
    }
    if (edits > 0) {
      if (isDown) {
        newIndex -= 1;
        lines.push({ kind: "+", text: newLines[newIndex]! });
      } else {
        oldIndex -= 1;
        lines.push({ kind: "-", text: oldLines[oldIndex]! });
      }
    }
  }
  return lines.reverse();
}

/** Groups the edit script into hunks with {@link contextLines} lines of context, as `diff -u` does. */
function groupHunks(lines: readonly DiffLine[]): { header: string; lines: DiffLine[] }[] {
  const changed = lines.flatMap((line, index) => (line.kind === " " ? [] : [index]));
  const hunks: { header: string; lines: DiffLine[] }[] = [];
  let next = 0;
  while (next < changed.length) {
    let last = changed[next]!;
    let end = next + 1;
    while (end < changed.length && changed[end]! - last <= 2 * contextLines + 1) {
      last = changed[end]!;
      end += 1;
    }
    const start = Math.max(0, changed[next]! - contextLines);
    const stop = Math.min(lines.length, last + contextLines + 1);
    const before = lines.slice(0, start);
    const hunkLines = lines.slice(start, stop);
    const oldStart = before.filter((line) => line.kind !== "+").length;
    const newStart = before.filter((line) => line.kind !== "-").length;
    const oldLength = hunkLines.filter((line) => line.kind !== "+").length;
    const newLength = hunkLines.filter((line) => line.kind !== "-").length;
    hunks.push({
      header: `@@ -${formatRange(oldStart, oldLength)} +${formatRange(newStart, newLength)} @@`,
      lines: hunkLines,
    });
    next = end;
  }
  return hunks;
}

function formatRange(start: number, length: number): string {
  if (length === 1) {
    return `${start + 1}`;
  }
  return `${length === 0 ? start : start + 1},${length}`;
}
