// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Fork a session from a CLI command: the new session ID, its task queue, and the lines. The values match the
// Python CLI core; see agents/conformance/session_fork.json.

import { randomBytes } from "node:crypto";

import { FlowNotFoundError, type Client } from "@superdurable/dex";
import {
  AgentSessionForkError,
  forkAgentSession,
  type AgentSessionFork,
  type AgentSessionForkSource,
} from "@superdurable/dex-agents";

import { sessionTaskQueue } from "./session-task-queues.js";

/** The chat command that forks the open session: `/fork [message-number]`. */
export const forkCommand = "/fork";
/** The line `/fork` writes for an argument that is not a message number. */
export const chatForkUsageLine = "[not forked] usage: /fork [message-number]";
// The SDK's message when the server negotiated a protocol without clone fork.
const cloneForkUnsupportedMessage = /^cloneForkFlow requires Server protocol version/;

/** A fork the command could not make; the message is one line for the user. */
export class SessionForkFailure extends Error {
  public constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "SessionForkFailure";
  }
}

/**
 * Returns `<harness>-<YYYYMMDD>-<HHMMSS>-<hex6>`, the format of every CLI session ID.
 * @param harnessName - The harness name.
 * @param now - The time to use.
 */
export function newCliSessionId(harnessName: string, now: Date = new Date()): string {
  const timestamp = now.toISOString().replace(/[-:]/g, "").replace(/\..*$/, "").replace("T", "-");
  return `${harnessName}-${timestamp}-${randomBytes(3).toString("hex")}`;
}

/**
 * Returns the task queue of a fork, or `""` to keep the source's.
 *
 * A session of the harness's Flow type opens only on its own task queue, so its fork always gets the fork's own one,
 * also when the source ran on a shared queue. An environment-hashed session from before 0.6.8 keeps its task queue,
 * the default one that the command's other Worker pulls.
 * @param sourceFlowType - The source session's Flow type.
 * @param harnessFlowType - The harness's Flow type.
 * @param sessionId - The fork's session ID.
 */
export function forkTaskQueue(sourceFlowType: string, harnessFlowType: string, sessionId: string): string {
  return sourceFlowType === harnessFlowType ? sessionTaskQueue(sessionId) : "";
}

/**
 * Forks a session of one of `flows`' Flow types into `sessionId`.
 * @param client - A Client whose Registry has every Flow of `flows`.
 * @param flows - The session Flows this command runs, by Flow type.
 * @param harnessFlowType - The harness's Flow type; see {@link forkTaskQueue}.
 * @param sourceSessionId - The session to fork.
 * @param sessionId - The new session's ID.
 * @param afterMessage - The last message the fork keeps, or `null` for the current state.
 * @param commandName - `dex-agents-<harness>`, for the lines.
 * @throws {SessionForkFailure} When the session cannot be forked; its message says why.
 */
export async function forkCliSession(
  client: Client,
  flows: ReadonlyMap<string, AgentSessionForkSource>,
  harnessFlowType: string,
  sourceSessionId: string,
  sessionId: string,
  afterMessage: number | null,
  commandName: string,
): Promise<AgentSessionFork> {
  let flowType: string;
  try {
    flowType = (await client.describeFlow(sourceSessionId)).flowType;
  } catch (error: unknown) {
    if (error instanceof FlowNotFoundError) {
      throw new SessionForkFailure(missingSessionError(sourceSessionId, commandName), { cause: error });
    }
    throw error;
  }
  const flow = flows.get(flowType);
  if (flow === undefined) {
    throw new SessionForkFailure(otherHarnessForkError(sourceSessionId, flowType, commandName));
  }
  try {
    return await forkAgentSession(client, flow, sourceSessionId, sessionId, {
      afterMessage,
      taskQueue: forkTaskQueue(flowType, harnessFlowType, sessionId),
    });
  } catch (error: unknown) {
    if (error instanceof AgentSessionForkError) {
      throw new SessionForkFailure(error.message, { cause: error });
    }
    if (error instanceof Error && cloneForkUnsupportedMessage.test(error.message)) {
      throw new SessionForkFailure(unsupportedServerError(commandName), { cause: error });
    }
    throw error;
  }
}

/** Returns the error when a fork exists but could not be opened. */
export function openFailureError(sessionId: string, error: string): string {
  return `session ${sessionId} was forked, but it could not be opened: ${error}`;
}

/** Returns the line written once a fork exists. */
export function forkedLine(sourceSessionId: string, afterMessage: number, sessionId: string): string {
  return `Forked session ${sourceSessionId} after message ${afterMessage} into session ${sessionId}.`;
}

/** Returns the question whether to open the fork now; Enter means yes. */
export function openPrompt(sessionId: string): string {
  return `Open session ${sessionId} now? [Y/n] `;
}

/** Returns the line that says how to open a session later. */
export function resumeHint(commandName: string, sessionId: string): string {
  return `Resume it with: ${commandName} --resume ${sessionId}`;
}

/** Returns the error of `fork` without a session ID. */
export function forkUsageError(commandName: string): string {
  return `fork needs a session ID: ${commandName} fork <session-id> [--at <message-number>]`;
}

/** Returns the line `/fork` writes when the fork failed. */
export function chatForkFailureLine(error: string): string {
  return `[not forked] ${error}`;
}

/** Returns the error for a Dex server without clone fork. */
export function unsupportedServerError(commandName: string): string {
  return (
    `${commandName} needs a Dex server with clone fork to fork sessions, such as the one dexcli 1.47.8 or later ` +
    "starts; this server is older. Upgrade it, or stop the dexcli dev this command reuses"
  );
}

/** Returns the error for a fork of a session that another harness's command runs. */
export function otherHarnessForkError(sessionId: string, flowType: string, commandName: string): string {
  return (
    `session ${sessionId} is a ${flowType} session, which ${commandName} does not run; ` +
    "fork it with the command that started it"
  );
}

/** Returns the error for a fork of a session that does not exist. */
export function missingSessionError(sessionId: string, commandName: string): string {
  return `session ${sessionId} does not exist; list sessions with ${commandName} sessions`;
}

/** Returns whether an answer to the open prompt means yes: empty, `y`, or `yes`. */
export function isYesAnswer(answer: string): boolean {
  return ["", "y", "yes"].includes(answer.trim().toLowerCase());
}

/**
 * Parses the argument of `/fork` or `--at`: a non-negative decimal message number, or `null` when it is empty.
 * @returns The number, `null`, or `undefined` when it is not a message number.
 */
export function parseForkMessageNumber(text: string): number | null | undefined {
  const trimmed = text.trim();
  if (trimmed === "") {
    return null;
  }
  return /^\d+$/.test(trimmed) ? Number(trimmed) : undefined;
}
