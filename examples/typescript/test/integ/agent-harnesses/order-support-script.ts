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

/** What a scripted order-support model does next: call one tool, or answer. */
export type OrderSupportScriptedReply =
  | { readonly kind: "call_tool"; readonly toolName: string; readonly orderId: string }
  | { readonly kind: "answer"; readonly text: string };

const toolNamesByCommand = new Map([
  ["lookup", "lookup_order"],
  ["refund", "refund_order"],
]);

/**
 * The script every harness test model follows. `lookup <id>` and `refund <id>` call a tool.
 * With its result, the model answers `<tool>: <result>`; other text answers `echo: <text>`.
 */
export function decideOrderSupportReply(
  userText: string,
  toolResultsByName: ReadonlyMap<string, string>,
): OrderSupportScriptedReply {
  const [command = "", orderId = ""] = userText.trim().split(/\s+/);
  const toolName = toolNamesByCommand.get(command);
  if (toolName === undefined) {
    return { kind: "answer", text: `echo: ${userText}` };
  }
  const result = toolResultsByName.get(toolName);
  return result === undefined ? { kind: "call_tool", toolName, orderId } : { kind: "answer", text: `${toolName}: ${result}` };
}
