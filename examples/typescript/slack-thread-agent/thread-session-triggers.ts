/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

// Slack Trigger targets that drive one agent session per thread. A new thread starts the
// session; each reply in the thread becomes the session's next user message.

import {
  newDexFlowTriggerTarget,
  newDexRPCTriggerTarget,
  type TriggerEvent,
  type TriggerOptions,
  type TriggerTarget,
} from "@superdurable/connectors-sdk";
import type { LoadedProject } from "@superdurable/connectors-sdk/projectconfig";
import {
  newProjectMessageTriggerRunner,
  type MessageEvent,
  type MessageTriggerRunner,
} from "@superdurable/connector-slack";
import type { Client } from "@superdurable/dex";
import type { AgentSessionStartInput, AgentUserMessage } from "@superdurable/dex-agents";

import type { ThreadAgentFlow } from "./thread-agent-flow.ts";

// The wire name of the session's user-message RPC. It keys each reply's request ID.
const sendUserMessageRPCName = "send_user_message";

/** The session ID of a thread. A root message and its replies resolve to the same ID. */
export function resolveThreadSessionId(event: TriggerEvent<MessageEvent>): string {
  const { teamId, channelId, threadTimestamp } = event.payload;
  return `slack-thread-${teamId}-${channelId}-${threadTimestamp}`;
}

/** A session refuses blank text, so the targets skip messages without any. */
function hasText(event: TriggerEvent<MessageEvent>): boolean {
  return event.payload.text.trim() !== "";
}

/** Starts one session per new thread. The event ID is the start's request ID. */
export function newThreadSessionStartTarget(
  client: Client,
  flow: ThreadAgentFlow,
  options?: TriggerOptions,
): TriggerTarget<MessageEvent> {
  return newDexFlowTriggerTarget(
    client,
    flow,
    hasText,
    resolveThreadSessionId,
    (event): AgentSessionStartInput => ({ initialMessage: event.payload.text }),
    options,
  );
}

/** Sends each thread reply to the session once. Every redelivery keeps one request ID. */
export function newThreadReplyTarget(
  client: Client,
  flow: ThreadAgentFlow,
  options?: TriggerOptions,
): TriggerTarget<MessageEvent> {
  return newDexRPCTriggerTarget(
    client,
    flow.sendUserMessage,
    hasText,
    resolveThreadSessionId,
    (event): AgentUserMessage => ({ text: event.payload.text }),
    { ...options, rpcName: sendUserMessageRPCName },
  );
}

/** Binding of the channelThreadCreated Trigger that starts sessions. */
export const threadSessionStartBinding = "thread-session-start";
/** Binding of the threadReplyCreated Trigger that sends replies. */
export const threadSessionReplyBinding = "thread-session-reply";

/** Runs both bindings on one runner. Each binding's durable inbox records an event before Slack is acknowledged. */
export function newThreadSessionTriggerRunner(
  project: LoadedProject,
  connectionName: string,
  client: Client,
  flow: ThreadAgentFlow,
): MessageTriggerRunner {
  return newProjectMessageTriggerRunner(project, connectionName, {
    channelThreadCreatedRoutes: [
      { bindingName: threadSessionStartBinding, target: newThreadSessionStartTarget(client, flow) },
    ],
    threadReplyCreatedRoutes: [{ bindingName: threadSessionReplyBinding, target: newThreadReplyTarget(client, flow) }],
  });
}
