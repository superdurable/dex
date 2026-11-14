// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Session task queues and the lines about where a session runs. The values match the Python CLI core; see
// agents/conformance/cli_session_task_queues.json.

import { createHash } from "node:crypto";

/** The prefix of every session task queue. */
const sessionTaskQueuePrefix = "s-";
/** The number of hexadecimal SHA-256 characters after the prefix. */
const sessionTaskQueueHashLength = 20;
/** The suffix of an environment-hashed session Flow type: `-` and 6 lowercase base36 characters. */
const legacySessionFlowTypeSuffix = /^-[0-9a-z]{6}$/;

/**
 * Returns the task queue of one session: `s-` and the first 20 lowercase hexadecimal characters of the SHA-256 of
 * the session ID in UTF-8.
 *
 * Every session that the CLI core starts runs on its own task queue, so only the command that has the session open
 * runs its Steps and RPCs. The name always meets the task queue rules: 22 characters of `[a-z0-9-]`.
 *
 * @example
 * ```ts
 * sessionTaskQueue("claude-sdk-20261113-203123-3efeb4"); // "s-" and 20 hexadecimal characters
 * ```
 * @param sessionId - The session's Flow ID.
 * @returns The session's task queue.
 * @since 0.6.8
 */
export function sessionTaskQueue(sessionId: string): string {
  const digest = createHash("sha256").update(sessionId, "utf8").digest("hex");
  return `${sessionTaskQueuePrefix}${digest.slice(0, sessionTaskQueueHashLength)}`;
}

/**
 * Returns whether a Flow type is a harness's environment-hashed session Flow type, from a release before 0.6.8:
 * the harness Flow type, `-`, and 6 lowercase base36 characters.
 * @param harnessFlowType - The harness's Flow type.
 * @param flowType - A session's Flow type.
 */
export function isLegacySessionFlowType(harnessFlowType: string, flowType: string): boolean {
  return (
    flowType.startsWith(harnessFlowType) && legacySessionFlowTypeSuffix.test(flowType.slice(harnessFlowType.length))
  );
}

/** The keys of a session environment that decide whether a takeover moved the session to another environment. */
const environmentIdentityKeys = ["host_name", "user_id", "config_identity", "workspace"] as const;

/**
 * Returns `<host name>:<workspace>` of a session environment, or `—` when it names neither.
 * @param environment - A session's `startEnvironment` or `SessionLastTerminalEnvironment`.
 */
export function describeSessionEnvironment(environment: Readonly<Record<string, string>> | undefined): string {
  const hostName = environment?.host_name ?? "";
  const workspace = environment?.workspace ?? "";
  return hostName === "" && workspace === "" ? "—" : `${hostName}:${workspace}`;
}

/**
 * Returns `<user>@<host name>:<workspace> (<configuration identity>)` of a session environment, where the user is
 * `user_name`, or `user_id` without it. A missing part is empty.
 * @param environment - A session's `startEnvironment` or `SessionLastTerminalEnvironment`.
 */
export function describeSessionEnvironmentIdentity(environment: Readonly<Record<string, string>>): string {
  const user = environment.user_name ?? environment.user_id ?? "";
  return `${user}@${environment.host_name ?? ""}:${environment.workspace ?? ""} (${environment.config_identity ?? ""})`;
}

/**
 * Returns whether two session environments differ: another host name, user, configuration file, or workspace.
 * @param previous - Where the session was last open.
 * @param current - This command's environment.
 */
export function isOtherSessionEnvironment(
  previous: Readonly<Record<string, string>>,
  current: Readonly<Record<string, string>>,
): boolean {
  return environmentIdentityKeys.some((key) => (previous[key] ?? "") !== (current[key] ?? ""));
}

/** Returns the line printed once a command took a session over from another environment. */
export function takeoverLine(sessionId: string, previousEnvironment: string, environment: string): string {
  return `Session ${sessionId} was last open on ${previousEnvironment}; its model and tool calls now run here, on ${environment}.`;
}

/** The line printed when a takeover confirmed that the turn in progress in the other environment ended. */
export const cancelledTurnLine =
  "Cancelled the turn that was in progress there; none of its model or tool calls run here.";

/** The line printed when a takeover could not confirm that the turn in progress there ended. */
export const turnNotCancelledLine =
  "Could not confirm that the turn in progress there was cancelled; this command still refuses its model and tool calls.";

/** Returns why this command refuses a call of the turn that was in progress when it took the session over. */
export function takenOverTurnReason(sessionId: string): string {
  return `its turn started in another place before this command took session ${sessionId} over`;
}

/** Returns the error of a refused tool call. */
export function refusedToolCallError(toolName: string, reason: string): string {
  return `tool ${toolName} did not run: ${reason}`;
}

/** Returns the error of a refused model call. */
export function refusedModelCallError(reason: string): string {
  return `the model call did not run: ${reason}`;
}

/** Returns the error for `--resume` of an environment-hashed session that another execution environment runs. */
export function otherEnvironmentLegacySessionError(sessionId: string, flowType: string): string {
  return (
    `session ${sessionId} is a ${flowType} session of another execution environment, from a release before ` +
    "sessions could move; resume it as the user, with the configuration file and workspace, that started it"
  );
}

/** Returns the error for `--resume` of a session that another harness's command runs. */
export function otherHarnessSessionError(sessionId: string, flowType: string, commandName: string): string {
  return `session ${sessionId} is a ${flowType} session, which ${commandName} does not run; resume it with the command that started it`;
}

/** Returns the error for a session of the harness's Flow type that runs on another task queue than its own. */
export function foreignTaskQueueSessionError(sessionId: string, commandName: string): string {
  return `session ${sessionId} was not started by ${commandName}, so it runs on another task queue and cannot be opened here`;
}

/** Returns the error for a Dex server without named task queues. */
export function namedTaskQueueServerError(commandName: string): string {
  return (
    `${commandName} needs a Dex server with named task queues, such as the one dexcli 1.47.5 or later starts; ` +
    "this server is older. Upgrade it, or stop the dexcli dev this command reuses"
  );
}

/** Reports whether an SDK error says the server's protocol has no named task queues. */
export function isNamedTaskQueueProtocolError(error: unknown): boolean {
  return error instanceof Error && /task queue .* requires (Dex )?Server protocol version/.test(error.message);
}
