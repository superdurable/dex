// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { encodeAgentSnapshot } from "../agent-session-codecs.js";
import type { AgentEvent, AgentSnapshot } from "../agent-session-values.js";
import type { AgentEventJson } from "./agent-ui-message-types.js";

/**
 * Converts an `AgentEvent` to its snake_case wire JSON, the format of the plain Server-Sent Events stream.
 *
 * @example
 * ```ts
 * const message = await client.readStream(sessionId, flow.agentEvents, resumeToken);
 * response.write(`data: ${JSON.stringify(convertAgentEventToJson(message.value))}\n\n`);
 * ```
 * @param event - Event read from the `AgentEvents` Stream.
 * @returns A new object with `type`, `turn_id`, `attempt`, `step_execution_id`, and `payload` text.
 */
export function convertAgentEventToJson(event: AgentEvent): AgentEventJson {
  return {
    type: event.type,
    turn_id: event.turnId,
    attempt: event.attempt,
    step_execution_id: event.stepExecutionId,
    payload: event.payload,
  };
}

/**
 * Converts an `AgentSnapshot` to its snake_case wire JSON, the same JSON the Python core produces.
 *
 * Use it when a browser shared with Python backends reads the snapshot.
 *
 * @param snapshot - Output of the `get_agent_snapshot` RPC.
 * @returns A new JSON object with `state`, `messages`, `pending_interrupts`, `tool_calls`, `usage`, and
 * `queued_message_count`.
 */
export function convertAgentSnapshotToJson(snapshot: AgentSnapshot): Readonly<Record<string, unknown>> {
  return encodeAgentSnapshot(snapshot) as Readonly<Record<string, unknown>>;
}
