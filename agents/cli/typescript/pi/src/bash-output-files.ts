// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readdir, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, dirname, join } from "node:path";

/** The file name prefix of the temporary files in which pi-coding-agent's `bash` keeps a cut output. */
const outputFilePrefix = "pi-bash";
const fullOutputHint = "Full output: ";

/**
 * Tracks the temporary file in which pi-coding-agent's `bash` keeps one call's full output, so the call can delete it
 * and drop its path from the result: the file lies outside the workspace, where the model cannot read it.
 *
 * Only a file that Pi named for this call is deleted: the `fullOutputPath` of the result or of a progress update, or,
 * when Pi threw, the last `Full output:` hint of its message, if that file did not exist before the call. A result
 * replaces only the hint of its own `fullOutputPath`, and command output that imitates the hint never names a file
 * that existed before.
 */
export class BashOutputFiles {
  private readonly reportedPaths = new Set<string>();

  private constructor(private readonly namesBeforeCall: ReadonlySet<string>) {}

  /**
   * Notes the temporary files that exist before the call.
   *
   * @returns The tracker.
   */
  public static async beforeCall(): Promise<BashOutputFiles> {
    const names = await readdir(tmpdir()).catch(() => [] as string[]);
    return new BashOutputFiles(new Set(names.filter((name) => name.startsWith(outputFilePrefix))));
  }

  /**
   * Notes the `fullOutputPath` of Pi's result or progress details.
   *
   * @param details - The details, of unknown shape.
   */
  public noteDetails(details: unknown): void {
    const path = (details as { fullOutputPath?: unknown } | undefined)?.fullOutputPath;
    if (typeof path === "string" && this.isOutputFilePath(path)) {
      this.reportedPaths.add(path);
    }
  }

  /**
   * Deletes the call's output file and replaces Pi's last hint that names it.
   *
   * @param text - The result text, or the message Pi threw.
   * @param isThrownMessage - Whether Pi threw the text, so its details are lost.
   * @returns The text with the hint replaced.
   */
  public async removeFromText(text: string, isThrownMessage: boolean): Promise<string> {
    const hintStart = text.lastIndexOf(fullOutputHint);
    const hintEnd = hintStart < 0 ? -1 : text.indexOf("]", hintStart);
    const hintedPath = hintEnd < 0 ? undefined : text.slice(hintStart + fullOutputHint.length, hintEnd);
    const isCallFile =
      hintedPath !== undefined &&
      (this.reportedPaths.has(hintedPath) ||
        (isThrownMessage && this.isOutputFilePath(hintedPath) && !this.namesBeforeCall.has(basename(hintedPath))));
    await this.removeReportedFiles();
    if (!isCallFile) {
      return text;
    }
    await removeFile(hintedPath);
    return `${text.slice(0, hintStart)}The full output is not kept; run the command again with less output${text.slice(hintEnd)}`;
  }

  /**
   * Deletes the output files Pi reported for the call, such as after a cancellation.
   *
   * @returns A Promise that resolves after the deletes.
   */
  public async removeReportedFiles(): Promise<void> {
    for (const path of this.reportedPaths) {
      await removeFile(path);
    }
  }

  private isOutputFilePath(path: string): boolean {
    return dirname(path) === tmpdir() && basename(path).startsWith(outputFilePrefix) && join(tmpdir(), basename(path)) === path;
  }
}

async function removeFile(path: string): Promise<void> {
  // A leftover temporary file is harmless, so a failed delete does not fail the call.
  await rm(path, { force: true }).catch(() => undefined);
}
