// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { TerminalUiAuth } from "./terminal-ui-environment.js";

/** The most code points a notice text keeps; a longer one keeps 499 and ends with `…`. */
export const maxNoticeCodePoints = 500;

/** What the session Flow the Worker host built supports, as the launch document reports it. */
export interface SessionFlowCapabilities {
  /** Present only when the runner takes images, with the limits of a message. */
  readonly images?: { readonly max_per_message: number; readonly max_bytes: number };
  /** Whether the Flow was built with plan mode. */
  readonly plan_mode: boolean;
  /** Whether the Flow supports steer and cancel. */
  readonly turn_control: boolean;
}

/** The parts of a chat launch document that come from the command, the server, and the Worker host. */
export interface ChatLaunchDetails {
  readonly commandName: string;
  readonly harnessName: string;
  readonly commandVersion: string;
  readonly serverAddress: string;
  readonly isTls: boolean;
  readonly webUrl: string | null;
  readonly flowType: string;
  readonly sessionId: string;
  readonly isResumed: boolean;
  readonly model: string;
  readonly workspace: string;
  readonly configPath: string | null;
  readonly methods: readonly string[];
  readonly capabilities: SessionFlowCapabilities;
  readonly logPath: string;
  /** The home directory, written as `~` in displayed paths. */
  readonly homeDirectory: string;
}

/** The `dex.auth` every launch document of this CLI core carries: its Client authenticates with nothing else. */
export const launchAuth: TerminalUiAuth = Object.freeze({ mode: "none" });

/**
 * Returns the chat launch document of contract 1. It holds no secret, and every path in `display` writes the home
 * directory as `~`. Startup notices go to the terminal before the handoff, so `notices` is empty.
 */
export function chatLaunchDocument(details: ChatLaunchDetails): Record<string, unknown> {
  const home = (path: string): string => displayPath(path, details.homeDirectory);
  return {
    type: "launch",
    contract: 1,
    mode: "chat",
    command: {
      name: details.commandName,
      harness: details.harnessName,
      language: "typescript",
      version: details.commandVersion,
    },
    dex: {
      server_address: details.serverAddress,
      tls: { enabled: details.isTls, ca_file: null, server_name: null },
      auth: launchAuth,
      web_url: details.webUrl,
    },
    session: { flow_type: details.flowType, session_id: details.sessionId, is_resumed: details.isResumed },
    display: {
      model: details.model,
      workspace: home(details.workspace),
      config_path: details.configPath === null ? null : home(details.configPath),
    },
    methods: [...details.methods],
    capabilities: details.capabilities,
    notices: [],
    log_path: home(details.logPath),
  };
}

/**
 * Returns a notice text as the launcher sends it: control characters become spaces, and a text over 500 code points
 * keeps its first 499 and ends with `…`.
 */
export function noticeText(text: string): string {
  const codePoints = [...text].map((character) => {
    const codePoint = character.codePointAt(0) ?? 0;
    return codePoint < 0x20 || (codePoint >= 0x7f && codePoint <= 0x9f) ? " " : character;
  });
  return codePoints.length > maxNoticeCodePoints
    ? `${codePoints.slice(0, maxNoticeCodePoints - 1).join("")}…`
    : codePoints.join("");
}

function displayPath(path: string, homeDirectory: string): string {
  if (homeDirectory === "" || homeDirectory === "/") {
    return path;
  }
  const home = homeDirectory.replace(/\/+$/, "");
  return path === home ? "~" : path.startsWith(`${home}/`) ? `~${path.slice(home.length)}` : path;
}
