// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { maxApprovalPreviewBytes, redactInlineCredentials, type AgentInterruptDisplay } from "@superdurable/dex-agents";

import type { WorkspaceFiles } from "./workspace-files.js";

// The Python CLI core builds the same text; the shared cases are in agents/conformance/approval_display.json.

/** Largest file, and largest new content, a file write preview diffs: 64 KiB. */
const maximumPreviewInputBytes = 64 * 1024;
/** Most lines a file write preview has, its header and marker lines included. */
const maximumPreviewLines = 200;
const contextLines = 3;
const noNewlineMarker = "\\ No newline at end of file";
const credentialDirectories = new Set([".ssh", ".aws", ".kube", ".gnupg", ".docker", "gcloud"]);
const credentialFileNames = new Set([
  ".netrc",
  ".npmrc",
  ".pypirc",
  ".pgpass",
  ".git-credentials",
  ".htpasswd",
  ".dockercfg",
  "credentials",
  "kubeconfig",
]);
const credentialFileExtensions = new Set([".pem", ".key", ".p12", ".pfx", ".jks", ".keystore", ".kubeconfig", ".ppk", ".gpg", ".asc"]);
const credentialStemWords = new Set([
  "apikey",
  "apikeys",
  "cred",
  "creds",
  "credential",
  "credentials",
  "passwd",
  "password",
  "passwords",
  "privatekey",
  "secret",
  "secrets",
  "token",
  "tokens",
]);

const credentialStemEndings = ["apikey", "credentials", "password", "passwd", "secret", "secrets", "token"] as const;

/**
 * The display of a `write_file` call: its workspace path, its size, and a diff against the file now.
 * @param files - The workspace files.
 * @param path - The `path` argument.
 * @param content - The `content` argument.
 * @returns The display.
 */
export function buildWriteFileDisplay(files: WorkspaceFiles, path: string, content: string): AgentInterruptDisplay {
  const source = files.readTextForPreview(path, maximumPreviewInputBytes);
  const target = escapeControlCharacters(source?.displayPath ?? path);
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
  const target = escapeControlCharacters(source?.displayPath ?? path);
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
 * The display of a `run_shell_command` call: the working directory and the command with its inline credentials
 * redacted. The full command stays in the call's arguments, and the session cuts a summary over
 * `maxApprovalSummaryBytes`.
 * @param workspace - The workspace, the command's working directory.
 * @param command - The `command` argument.
 * @returns The display.
 */
export function buildShellCommandDisplay(workspace: string, command: string): AgentInterruptDisplay {
  const directory = escapeControlCharacters(workspace);
  return {
    target: directory,
    summary: `Runs a shell command in ${directory}: ${redactInlineCredentials(command)}`,
    preview: null,
  };
}

/**
 * The text with each control character written as `\u00XX`, so a path stays on one line.
 * @param text - The text.
 * @returns The escaped text.
 */
export function escapeControlCharacters(text: string): string {
  return text.replace(/[\u0000-\u001f\u007f]/g, (character) => `\\u${character.charCodeAt(0).toString(16).padStart(4, "0")}`);
}

/**
 * Whether a file's path says it holds credentials, so a write preview shows only its size: any file in `.ssh`,
 * `.aws`, `.kube`, `.gnupg`, `.docker`, or `gcloud`; `.env`, `.env.local`, and `prod.env`; `.netrc`, `.npmrc`,
 * `.pypirc`, `.pgpass`, `.git-credentials`, `.htpasswd`, `.dockercfg`, `credentials`, and `kubeconfig`; names that
 * start with `id_`; the extensions `.pem`, `.key`, `.p12`, `.pfx`, `.jks`, `.keystore`, `.kubeconfig`, `.ppk`, `.gpg`,
 * and `.asc`; and names whose part before the first dot has a word such as `secret`, `credentials`, `token`,
 * `password`, or `api key`, or a word ending in one, such as `secrets.yaml`, `client_secret.json`, or `githubtoken.txt`.
 * @param path - The file's path.
 * @returns `true` for a credential file.
 */
export function isCredentialFileName(path: string): boolean {
  const parts = path.split(/[/\\]/).filter((part) => part !== "");
  if (parts.length === 0) {
    return false;
  }
  if (parts.slice(0, -1).some((part) => credentialDirectories.has(part.toLowerCase()))) {
    return true;
  }
  const original = parts.at(-1)!;
  const name = original.toLowerCase();
  const dot = name.lastIndexOf(".");
  const extension = dot > 0 ? name.slice(dot) : "";
  const stem = original.replace(/^\.+/, "").split(".")[0] ?? "";
  const stemWords = stem
    .replace(/(?<=[a-z0-9])(?=[A-Z])/g, "_")
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter((word) => word !== "");
  return (
    name === ".env" ||
    name.startsWith(".env.") ||
    name.endsWith(".env") ||
    name.startsWith("id_") ||
    credentialFileNames.has(name) ||
    credentialFileExtensions.has(extension) ||
    stemWords.some(
      (word) => credentialStemWords.has(word) || credentialStemEndings.some((ending) => word.endsWith(ending)),
    ) ||
    hasWords(stemWords, ["private", "key"]) ||
    hasWords(stemWords, ["api", "key"])
  );
}

/**
 * The labelled diff of a file write, or `null` for a credential file, new content over 64 KiB, either side with an
 * inline credential (see `redactInlineCredentials`), and a diff over 200 lines or `maxApprovalPreviewBytes`.
 * @param displayPath - The file's workspace path.
 * @param currentText - The file's text now, or `null` for a new file.
 * @param newText - The content after the write.
 * @returns The diff or `null`.
 */
export function buildFilePreview(displayPath: string, currentText: string | null, newText: string): string | null {
  if (
    isCredentialFileName(displayPath) ||
    Buffer.byteLength(newText, "utf8") > maximumPreviewInputBytes ||
    hasInlineCredential(newText) ||
    (currentText !== null && hasInlineCredential(currentText))
  ) {
    return null;
  }
  const diff = buildUnifiedDiff(escapeControlCharacters(displayPath), currentText, newText);
  return diff !== undefined && Buffer.byteLength(diff, "utf8") <= maxApprovalPreviewBytes ? diff : null;
}

function hasInlineCredential(text: string): boolean {
  return redactInlineCredentials(text) !== text;
}

function hasWords(words: readonly string[], phrase: readonly string[]): boolean {
  return words.some((_word, index) => phrase.every((part, offset) => words[index + offset] === part));
}

function describeBytes(text: string): string {
  const bytes = Buffer.byteLength(text, "utf8");
  return bytes === 1 ? "1 byte" : `${bytes} bytes`;
}

/** One line of a file side; only a last line can lack its newline. */
interface FileLine {
  readonly text: string;
  readonly isMissingNewline: boolean;
}

type DiffLine = { readonly kind: " " | "-" | "+"; readonly line: FileLine };

/**
 * A unified diff with 3 lines of context, whose header lines say which side is the file now, or `undefined` when
 * it would have more than {@link maximumPreviewLines} lines.
 */
function buildUnifiedDiff(headerPath: string, currentText: string | null, newText: string): string | undefined {
  const oldLines = currentText === null ? [] : splitLines(currentText);
  const newLines = splitLines(newText);
  const edits = diffLines(oldLines, newLines, maximumPreviewLines);
  if (edits === undefined) {
    return undefined;
  }
  const output = [
    currentText === null ? "--- /dev/null (no file now)" : `--- ${headerPath} (the file now)`,
    `+++ ${headerPath} (after this write)`,
  ];
  for (const hunk of groupHunks(edits)) {
    output.push(hunk.header);
    for (const line of hunk.lines) {
      output.push(`${line.kind}${line.line.text}`);
      if (line.line.isMissingNewline) {
        output.push(noNewlineMarker);
      }
    }
    if (output.length > maximumPreviewLines) {
      return undefined;
    }
  }
  const diff = `${output.join("\n")}\n`;
  return diff.split("\n").length - 1 <= maximumPreviewLines ? diff : undefined;
}

function splitLines(text: string): FileLine[] {
  if (text === "") {
    return [];
  }
  const parts = text.split("\n");
  const isMissingNewline = parts.at(-1) !== "";
  if (!isMissingNewline) {
    parts.pop();
  }
  return parts.map((part, index) => ({ text: part, isMissingNewline: isMissingNewline && index === parts.length - 1 }));
}

function isSameLine(left: FileLine, right: FileLine): boolean {
  return left.text === right.text && left.isMissingNewline === right.isMissingNewline;
}

/** Myers' shortest edit script, or `undefined` when it needs more than `maximumEdits` inserts and deletes. */
function diffLines(oldLines: readonly FileLine[], newLines: readonly FileLine[], maximumEdits: number): DiffLine[] | undefined {
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
      while (oldIndex < oldCount && newIndex < newCount && isSameLine(oldLines[oldIndex]!, newLines[newIndex]!)) {
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
  oldLines: readonly FileLine[],
  newLines: readonly FileLine[],
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
      lines.push({ kind: " ", line: oldLines[oldIndex]! });
    }
    if (edits > 0) {
      if (isDown) {
        newIndex -= 1;
        lines.push({ kind: "+", line: newLines[newIndex]! });
      } else {
        oldIndex -= 1;
        lines.push({ kind: "-", line: oldLines[oldIndex]! });
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
