// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { setTimeout as delay } from "node:timers/promises";

import { DexServiceError, LongPollTimeoutError, type Client, type StreamMessage } from "@superdurable/dex";

import type { AgentSessionFlow } from "../agent-session-flow.js";
import type { AgentEvent } from "../agent-session-values.js";

/** Default long-poll duration of one `AgentEvents` read: 10 seconds. */
export const defaultAgentEventPollTimeoutMs = 10_000;

/** Long poll of the read that checks a resume token before a response commits to status 200. */
export const resumeTokenProbeTimeoutMs = 1_000;

const grpcInvalidArgument = 3;
const grpcFailedPrecondition = 9;
const turnStartSearchPageSize = 500;
const turnStartSearchPageLimit = 20;
const turnIdPattern = /^turn-(\d+)$/;

/** Validates a long-poll duration; Dex Stream reads take whole seconds. */
export function requireAgentEventPollTimeoutMs(pollTimeoutMs: number | undefined): number {
  const timeoutMs = pollTimeoutMs ?? defaultAgentEventPollTimeoutMs;
  if (!Number.isSafeInteger(timeoutMs) || timeoutMs < 1_000 || timeoutMs % 1_000 !== 0) {
    throw new RangeError("pollTimeoutMs must be a positive whole number of seconds, in milliseconds");
  }
  return timeoutMs;
}

/**
 * Reads the next `AgentEvents` message, or `undefined` when the long poll ends without one.
 * A disabled Stream Store holds no events, so the read waits as an empty poll would.
 */
export async function readNextAgentEvent(
  client: Client,
  flow: AgentSessionFlow,
  flowId: string,
  eventResumeToken: string,
  pollTimeoutMs: number,
): Promise<StreamMessage<AgentEvent> | undefined> {
  try {
    return await client.readStream(flowId, flow.agentEvents, eventResumeToken, pollTimeoutMs);
  } catch (error: unknown) {
    if (error instanceof LongPollTimeoutError) {
      return undefined;
    }
    if (isStreamStoreDisabledError(error)) {
      await delay(Math.min(pollTimeoutMs, resumeTokenProbeTimeoutMs));
      return undefined;
    }
    throw error;
  }
}

/** Reports whether Dex rejected a Stream resume token, such as one of another session. */
export function isStreamResumeTokenRejectedError(error: unknown): boolean {
  return error instanceof DexServiceError && Number(error.code) === grpcInvalidArgument;
}

/** Reports whether a Stream call failed because the Server's Stream Store is disabled. */
export function isStreamStoreDisabledError(error: unknown): boolean {
  return error instanceof DexServiceError && Number(error.code) === grpcFailedPrecondition;
}

/** The first read after a resume token, done before a response commits to its status. */
export type AgentEventResumeTokenProbe =
  | { readonly isAccepted: true; readonly firstMessage: StreamMessage<AgentEvent> | undefined }
  | { readonly isAccepted: false };

/** Reads once with a short long poll to learn whether Dex accepts a resume token. */
export async function probeAgentEventResumeToken(
  client: Client,
  flow: AgentSessionFlow,
  flowId: string,
  eventResumeToken: string,
): Promise<AgentEventResumeTokenProbe> {
  try {
    const firstMessage = await readNextAgentEvent(
      client,
      flow,
      flowId,
      eventResumeToken,
      resumeTokenProbeTimeoutMs,
    );
    return { isAccepted: true, firstMessage };
  } catch (error: unknown) {
    if (isStreamResumeTokenRejectedError(error)) {
      return { isAccepted: false };
    }
    throw error;
  }
}

/** Returns the resume token of the newest retained event, or `""` when the Stream holds none. */
export async function readAgentEventsTailResumeToken(
  client: Client,
  flow: AgentSessionFlow,
  flowId: string,
): Promise<string> {
  try {
    const page = await client.listStreamMessages(flowId, flow.agentEvents, 1);
    return page.messages[0]?.resumeToken ?? "";
  } catch (error: unknown) {
    if (isStreamStoreDisabledError(error)) {
      return "";
    }
    throw error;
  }
}

/**
 * Finds the position just before a turn's first retained event.
 *
 * @returns The resume token whose next event is the turn's `turn_started`, or the turn's oldest retained
 * event; `""` when that event is the oldest retained one; `undefined` when the Stream holds no event of
 * the turn or the search reached its page limit.
 */
export async function findAgentEventResumeTokenBeforeTurn(
  client: Client,
  flow: AgentSessionFlow,
  flowId: string,
  turnId: string,
): Promise<string | undefined> {
  try {
    return await searchAgentEventResumeTokenBeforeTurn(client, flow, flowId, turnId);
  } catch (error: unknown) {
    if (isStreamStoreDisabledError(error)) {
      return undefined;
    }
    throw error;
  }
}

async function searchAgentEventResumeTokenBeforeTurn(
  client: Client,
  flow: AgentSessionFlow,
  flowId: string,
  turnId: string,
): Promise<string | undefined> {
  const turnNumber = parseTurnNumber(turnId);
  let hasTurnEvent = false;
  let pageToken = "";
  for (let pageIndex = 0; pageIndex < turnStartSearchPageLimit; pageIndex += 1) {
    const page = await client.listStreamMessages(
      flowId,
      flow.agentEvents,
      turnStartSearchPageSize,
      pageToken,
    );
    for (const [index, message] of page.messages.entries()) {
      const event = message.value;
      if (event.turnId === turnId) {
        hasTurnEvent = true;
        if (event.type !== "turn_started") {
          continue;
        }
      } else if (!isEarlierTurn(event.turnId, turnNumber)) {
        continue;
      }
      // The events of the turn are all newer than this message, or this message is turn_started.
      const olderMessage = event.turnId === turnId ? page.messages[index + 1] : message;
      if (olderMessage !== undefined) {
        return hasTurnEvent ? olderMessage.resumeToken : undefined;
      }
      return readResumeTokenOlderThanPage(client, flow, flowId, page.nextPageToken);
    }
    if (page.nextPageToken === "") {
      return hasTurnEvent ? "" : undefined;
    }
    pageToken = page.nextPageToken;
  }
  return undefined;
}

/** Returns the number `n` of a `turn-<n>` turn ID, or `undefined` for another format. */
export function parseTurnNumber(turnId: string): number | undefined {
  const match = turnIdPattern.exec(turnId);
  return match?.[1] === undefined ? undefined : Number(match[1]);
}

/** Returns the Step type of a `StepType-Number` Step execution ID, or `""` when it has no number. */
export function readStepType(stepExecutionId: string): string {
  const separatorIndex = stepExecutionId.lastIndexOf("-");
  return separatorIndex <= 0 ? "" : stepExecutionId.slice(0, separatorIndex);
}

async function readResumeTokenOlderThanPage(
  client: Client,
  flow: AgentSessionFlow,
  flowId: string,
  nextPageToken: string,
): Promise<string> {
  if (nextPageToken === "") {
    return "";
  }
  const olderPage = await client.listStreamMessages(flowId, flow.agentEvents, 1, nextPageToken);
  return olderPage.messages[0]?.resumeToken ?? "";
}

function isEarlierTurn(turnId: string | null, turnNumber: number | undefined): boolean {
  if (turnId === null || turnNumber === undefined) {
    return false;
  }
  const otherTurnNumber = parseTurnNumber(turnId);
  return otherTurnNumber !== undefined && otherTurnNumber < turnNumber;
}
