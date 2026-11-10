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

/** Prefix of every answer the scripted model gives, as the Python demo model does. */
export const scriptedReplyPrefix = "Local demo response: ";

/** Reason the scripted model passes to `cancel_order`. */
export const scriptedCancelReason = "requested by the customer";

/** The Slack connector tool's default name, `<connectorId>_<operation in snake case>`. */
export const slackPostThreadReplyToolName = "slack_post_thread_reply";

/** What a scripted order support model does next: call one tool, or answer. */
export type OrderSupportScriptedReply =
  | { readonly kind: "call_tool"; readonly toolName: string; readonly arguments: Readonly<Record<string, string>> }
  | { readonly kind: "answer"; readonly text: string };

/**
 * The script every harness test model follows, the same as the Python demo model. "cancel order <id>" calls
 * cancel_order, other text with "order <id>" calls look_up_order, and tool results become the answer.
 * "reply in slack thread <channel> <timestamp>: <text>" calls slack_post_thread_reply.
 */
export function decideOrderSupportReply(userText: string, toolOutputs: readonly string[]): OrderSupportScriptedReply {
  if (toolOutputs.length > 0) {
    return { kind: "answer", text: scriptedReplyPrefix + toolOutputs.join(" ") };
  }
  const slackReplyMatch = /\breply in slack thread (\S+) (\S+): (.+)$/i.exec(userText);
  if (slackReplyMatch !== null) {
    return {
      kind: "call_tool",
      toolName: slackPostThreadReplyToolName,
      arguments: {
        channelId: slackReplyMatch[1] ?? "",
        threadTimestamp: slackReplyMatch[2] ?? "",
        text: slackReplyMatch[3] ?? "",
      },
    };
  }
  const cancelMatch = /\bcancel order (\w+)/i.exec(userText);
  if (cancelMatch !== null) {
    return {
      kind: "call_tool",
      toolName: "cancel_order",
      arguments: { order_id: cancelMatch[1] ?? "", reason: scriptedCancelReason },
    };
  }
  const orderMatch = /\border (\w+)/i.exec(userText);
  if (orderMatch !== null) {
    return { kind: "call_tool", toolName: "look_up_order", arguments: { order_id: orderMatch[1] ?? "" } };
  }
  return { kind: "answer", text: `${scriptedReplyPrefix}ask me to look up or cancel an order.` };
}
