// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// A session at the state limit may have no room left to store `AgentState`. The commit that would store it
// fails on the same limit, and the Flow closes without it. Dex always records that close, so a reader takes a
// failed Flow's status and reason from it.

import { FlowNotActiveOrNotFoundError, FlowNotFoundError, type Client } from "@superdurable/dex";

import type { AgentFrameworkRunner, DeferredToolOutcome } from "./agent-framework-runner.js";
import { validateImageReferences } from "./agent-media.js";
import type { AgentSessionFlow } from "./agent-session-flow.js";
import type { AgentSnapshot, AgentUserMessage } from "./agent-session-values.js";

const unknownFailure = "the session's Flow failed";

/**
 * Reports a message sent to a session that failed. {@link sendAgentUserMessage} throws it in place of the Dex
 * error for a closed Flow, and it stays a `FlowNotActiveOrNotFoundError`. The message did not reach the session.
 * Messages queued earlier stay readable in {@link AgentSnapshot.queuedMessages} and never run.
 */
export class AgentSessionFailedError extends FlowNotActiveOrNotFoundError {
  /**
   * Creates the error from the Dex error it replaces.
   * @param sessionId - The failed session.
   * @param reason - Why the session failed, such as `run state of 67492 bytes exceeds the maximum of 65536 bytes`.
   * @param cause - The Dex error for the closed Flow.
   */
  public constructor(
    sessionId: string,
    public readonly reason: string,
    cause: FlowNotActiveOrNotFoundError,
  ) {
    super(cause.code, cause.subStatus, `session ${sessionId} failed: ${reason}`, cause.operation, sessionId, { cause });
  }
}

/**
 * Returns the snapshot of a session whose Flow failed, with what the session stores when it has room: status
 * `failed`, the reason, no active turn, and no pending interrupts. `toolCalls` lists the active turn's calls, so it
 * is empty too; a stale turn's calls will never finish. A snapshot with no state, or with status `failed`, is
 * returned unchanged.
 * @param snapshot - The stored snapshot.
 * @param reason - The Flow's failure reason.
 * @returns The snapshot to show.
 */
export function withSessionFailure(snapshot: AgentSnapshot, reason: string): AgentSnapshot {
  const state = snapshot.state;
  if (state === null || !mayHideSessionFailure(snapshot)) {
    return snapshot;
  }
  return {
    ...snapshot,
    state: {
      ...state,
      status: "failed",
      turnCount: state.turnCount + (state.activeTurnId === null ? 0 : 1),
      activeTurnId: null,
      acceptsTurnControl: false,
      lastTurnError: reason,
    },
    pendingInterrupts: [],
    toolCalls: [],
  };
}

/**
 * Returns why a session's Flow failed.
 * @param client - An open Client.
 * @param sessionId - Running or retained closed session.
 * @returns The Flow's failure reason, or `undefined` when the Flow did not fail or does not exist.
 */
export async function readSessionFailure(client: Client, sessionId: string): Promise<string | undefined> {
  try {
    if ((await client.describeFlow(sessionId)).status !== "failed") {
      return undefined;
    }
  } catch (error: unknown) {
    if (error instanceof FlowNotFoundError) {
      return undefined;
    }
    throw error;
  }
  return (await client.waitForFlow(sessionId)).errorMessage || unknownFailure;
}

/**
 * Returns the session snapshot, with a failed Flow's status and reason. Prefer it to invoking `getAgentSnapshot`
 * directly: that RPC reads only what the session stored. It costs one more call to Dex.
 * @param client - An open Client whose Registry contains `flow`.
 * @param flow - The registered agent session Flow.
 * @param sessionId - Running or retained closed session.
 * @returns The snapshot. See {@link withSessionFailure}.
 */
export async function readAgentSnapshot(
  client: Client,
  flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>,
  sessionId: string,
): Promise<AgentSnapshot> {
  const snapshot = await client.invokeRPC(flow.getAgentSnapshot, sessionId);
  if (!mayHideSessionFailure(snapshot)) {
    return snapshot;
  }
  const reason = await readSessionFailure(client, sessionId);
  return reason === undefined ? snapshot : withSessionFailure(snapshot, reason);
}

/** Reports whether the Flow's close could add to this snapshot. */
function mayHideSessionFailure(snapshot: AgentSnapshot): boolean {
  return snapshot.state !== null && snapshot.state.status !== "failed";
}

/**
 * Queues one user message, and reports a failed session clearly.
 * @param client - An open Client whose Registry contains `flow`.
 * @param flow - The registered agent session Flow.
 * @param sessionId - Running session.
 * @param message - The user message. Upload its {@link AgentUserMessage.images} with `MediaStore.putImage` first.
 * @returns `true` when queued; `false` for blank text, for a `planMode` message when plan mode is off, or for images
 * when the session's runner does not implement `buildUserMessageWithImages`.
 * @throws {ImageValidationError} When `validateImageReferences` refuses the images; nothing is sent. Since 0.5.0.
 * @throws {@link AgentSessionFailedError} when the session failed. Its `reason` says why.
 * @throws {FlowNotActiveOrNotFoundError} When the session is not running.
 * @throws {DexServiceError} With `RESOURCE_EXHAUSTED` when the message does not fit under
 * `limits.maxFlowStateBytes`. Nothing was queued, and the session stays as it was.
 */
export async function sendAgentUserMessage(
  client: Client,
  flow: AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>,
  sessionId: string,
  message: AgentUserMessage,
): Promise<boolean> {
  validateImageReferences(message.images ?? []);
  try {
    return await client.invokeRPC(flow.sendUserMessage, sessionId, message);
  } catch (error) {
    if (!(error instanceof FlowNotActiveOrNotFoundError)) {
      throw error;
    }
    const reason = await readSessionFailure(client, sessionId);
    if (reason === undefined) {
      throw error;
    }
    throw new AgentSessionFailedError(sessionId, reason, error);
  }
}
