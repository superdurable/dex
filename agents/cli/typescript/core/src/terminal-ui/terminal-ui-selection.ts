// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** What decides, before `dexcli` is probed, whether a run may hand off to the terminal UI. */
export interface TerminalUiSelectionInput {
  /** `DEX_AGENTS_TERMINAL_UI=1`; the terminal UI stays off without it until it becomes the default. */
  readonly isTerminalUiEnabled: boolean;
  /** `process.platform`. */
  readonly platform: NodeJS.Platform;
  /** `--plain`. */
  readonly isPlainRequested: boolean;
  /** Whether stdin and stdout are terminals. */
  readonly isInteractive: boolean;
  /** `TERM`. */
  readonly term: string | undefined;
  /** The harness's declaration; `"plain"` keeps the text chat. */
  readonly harnessTerminalInterface: "dexcli" | "plain";
}

/**
 * Returns whether a chat may try the terminal UI. Windows is refused before anything POSIX is tried, and so are
 * `--plain`, a run without a terminal, `TERM=dumb`, and a harness that declares the text chat only. These choose
 * the plain chat without a message; only a failed probe of `dexcli` prints why.
 */
export function isTerminalUiSelected(input: TerminalUiSelectionInput): boolean {
  return (
    input.isTerminalUiEnabled &&
    input.platform !== "win32" &&
    !input.isPlainRequested &&
    input.isInteractive &&
    input.term !== "dumb" &&
    input.harnessTerminalInterface !== "plain"
  );
}
