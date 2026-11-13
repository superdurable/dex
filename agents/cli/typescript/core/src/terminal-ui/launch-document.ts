// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { DexAuthSettings, DexTlsSettings } from "../dex-connection.js";
import type { TerminalUiAuth } from "./terminal-ui-environment.js";

/** The most code points a notice text keeps; a longer one keeps 499 and ends with `…`. */
export const maxNoticeCodePoints = 500;
/** The most UTF-8 bytes the launch document's notices take, so it stays far below the 1 MiB line limit. */
export const maxLaunchNoticeBytes = 64 * 1024;

/** What the session Flow the Worker host built supports, as the launch document reports it. */
export interface SessionFlowCapabilities {
  /** Present only when the runner takes images, with the limits of a message. */
  readonly images?: { readonly max_per_message: number; readonly max_bytes: number };
  /** Whether the Flow was built with plan mode. */
  readonly plan_mode: boolean;
  /** Whether the Flow supports steer and cancel. */
  readonly turn_control: boolean;
  /** Present only when the harness declares reasoning efforts, with its values from least to most effort. */
  readonly reasoning_effort?: { readonly values: readonly string[] };
  /**
   * Whether the launcher takes the lease of a session the terminal UI switches to: the terminal UI then hands a
   * switch back with `switch_session` and exits.
   */
  readonly launcher_session_switch?: boolean;
}

/** The parts of a chat launch document that come from the command, the server, and the Worker host. */
export interface ChatLaunchDetails {
  readonly commandName: string;
  readonly harnessName: string;
  readonly commandVersion: string;
  readonly serverAddress: string;
  /** The launch document's `dex.tls`. */
  readonly tls: DexTlsSettings;
  /** The launch document's `dex.auth`. */
  readonly auth: TerminalUiAuth;
  readonly webUrl: string | null;
  readonly flowType: string;
  readonly sessionId: string;
  readonly isResumed: boolean;
  readonly model: string;
  /** The harness's `defaultModel`, which `/model` offers after {@link model}. */
  readonly harnessDefaultModel: string | null;
  /** The harness's `suggestedModels`, which `/model` offers after the default model. */
  readonly suggestedModels: readonly string[];
  readonly workspace: string;
  readonly configPath: string | null;
  readonly methods: readonly string[];
  readonly capabilities: SessionFlowCapabilities;
  readonly logPath: string;
  /** The home directory, written as `~` in displayed paths. */
  readonly homeDirectory: string;
  /** The lines printed while the command started a `dexcli dev`, shown again as `info` notices. */
  readonly startedDexcliDevLines: readonly string[];
  /** The Worker host's log lines before its session opened, shown as `warning` notices. */
  readonly workerHostStartupLines: readonly string[];
}

/** Returns the launch document's `dex.auth` of a command's auth settings: the token file's path stays out. */
export function terminalUiAuthOf(auth: DexAuthSettings): TerminalUiAuth {
  switch (auth.mode) {
    case "api_key":
      return { mode: "api_key", token_url: auth.token_url, server_id: auth.server_id };
    case "bearer":
      return { mode: "bearer", source: auth.source };
    default:
      return { mode: "none" };
  }
}

/**
 * Returns the chat launch document of contract 1. It holds no secret, and every path in `display` writes the home
 * directory as `~`. Its `notices` are the `dexcli dev` lines, then the Worker host's startup lines, each cut by
 * {@link noticeText}, within {@link maxLaunchNoticeBytes}.
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
      tls: { enabled: details.tls.enabled, ca_file: details.tls.ca_file, server_name: details.tls.server_name },
      auth: details.auth,
      web_url: details.webUrl,
    },
    session: { flow_type: details.flowType, session_id: details.sessionId, is_resumed: details.isResumed },
    display: {
      model: details.model,
      workspace: home(details.workspace),
      config_path: details.configPath === null ? null : home(details.configPath),
      models: displayModels(details),
    },
    methods: [...details.methods],
    capabilities: details.capabilities,
    notices: launchNotices(details, home(details.logPath)),
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

/**
 * Returns the launch notices in order, within {@link maxLaunchNoticeBytes} of JSON. The lines that do not fit become
 * one last notice that says how many more the log has.
 */
function launchNotices(details: ChatLaunchDetails, logPathShown: string): { level: string; text: string }[] {
  const lines = [
    ...details.startedDexcliDevLines.map((text) => ({ level: "info", text: noticeText(text) })),
    ...details.workerHostStartupLines.map((text) => ({ level: "warning", text: noticeText(text) })),
  ];
  const notices: { level: string; text: string }[] = [];
  let byteCount = 0;
  for (const notice of lines) {
    // One byte more for the comma between array elements.
    byteCount += Buffer.byteLength(JSON.stringify(notice), "utf8") + 1;
    if (byteCount > maxLaunchNoticeBytes) {
      const remaining = lines.length - notices.length;
      notices.push({ level: "warning", text: noticeText(`… ${remaining} more lines in ${logPathShown}`) });
      break;
    }
    notices.push(notice);
  }
  return notices;
}

/** Returns the models `/model` offers: the configured model, the harness default, then the suggestions, once each. */
function displayModels(details: ChatLaunchDetails): string[] {
  const models = [details.model, ...(details.harnessDefaultModel === null ? [] : [details.harnessDefaultModel])];
  return [...new Set([...models, ...details.suggestedModels])];
}

function displayPath(path: string, homeDirectory: string): string {
  if (homeDirectory === "" || homeDirectory === "/") {
    return path;
  }
  const home = homeDirectory.replace(/\/+$/, "");
  return path === home ? "~" : path.startsWith(`${home}/`) ? `~${path.slice(home.length)}` : path;
}
