// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

/** How the terminal UI authenticates to the Dex server: the launch document's `dex.auth`. */
export type TerminalUiAuth =
  | { readonly mode: "none" }
  | { readonly mode: "bearer"; readonly source: "env" | "file" }
  | { readonly mode: "api_key"; readonly token_url: string; readonly server_id: string };

/** The terminal and locale variables the terminal UI may see. */
export const terminalUiTerminalVariables: readonly string[] = Object.freeze([
  "HOME",
  "TERM",
  "COLORTERM",
  "TERM_PROGRAM",
  "NO_COLOR",
  "LANG",
  "LC_ALL",
  "LC_CTYPE",
  "TZ",
  "DEX_AGENTS_TUI_DEBUG",
]);

/** Returns the one variable that holds the secret `auth` needs, if any. */
export function terminalUiAuthVariable(auth: TerminalUiAuth): string | undefined {
  switch (auth.mode) {
    case "bearer":
      return auth.source === "file" ? "DEX_CREDENTIAL_FILE" : "DEX_CREDENTIAL";
    case "api_key":
      return "DEX_API_KEY";
    default:
      return undefined;
  }
}

/**
 * Returns the terminal UI's environment: the allow-listed terminal and locale variables the launcher has, plus the
 * one auth variable `auth` names. Nothing else passes, so provider keys and `PATH` stay in the launcher.
 */
export function buildTerminalUiEnvironment(
  launcherEnvironment: NodeJS.ProcessEnv,
  auth: TerminalUiAuth,
): Record<string, string> {
  const authVariable = terminalUiAuthVariable(auth);
  const names = authVariable === undefined ? terminalUiTerminalVariables : [...terminalUiTerminalVariables, authVariable];
  const environment: Record<string, string> = {};
  for (const name of names) {
    const value = launcherEnvironment[name];
    if (value !== undefined) {
      environment[name] = value;
    }
  }
  return environment;
}
