// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Stepwise code written against the published @superdurable/dex-agents 0.4.0 API; `npm run typecheck` must
// accept it.

import type { AgentEventWriter } from "../../src/index.js";
import { functionTool, type FunctionTool, type ToolCallContext } from "../../src/stepwise/index.js";

declare const events: AgentEventWriter;

/** A unit test builds a context with the 0.4.0 fields only. */
export const handBuiltToolCallContext: ToolCallContext = {
  flowId: "session-1",
  turnId: "turn-1",
  toolCallId: "call-1",
  idempotencyKey: "session-1/turn-1/call-1",
  attempt: 1,
  events,
  cancellationSignal: new AbortController().signal,
  deadline: undefined,
  inputAnswers: [],
  inputContinuation: null,
  heartbeat: async () => undefined,
};

/** A tool built without argument conversion, and one implemented by hand. */
export const lookupOrder: FunctionTool = functionTool<{ order_id: string }>({
  name: "lookup_order",
  description: "Look up an order.",
  parameters: { type: "object", properties: { order_id: { type: "string" } }, required: ["order_id"] },
  effect: "read",
  execute: ({ order_id }, context) => `${order_id} ${context.idempotencyKey}`,
});

export const handBuiltFunctionTool: FunctionTool = {
  kind: "function_tool",
  name: "echo",
  description: "Echo the arguments.",
  parameters: { type: "object" },
  effect: "read",
  requiresApproval: undefined,
  execute: async (args: unknown) => args,
};

export async function runWithHandBuiltContext(): Promise<unknown> {
  return lookupOrder.execute({ order_id: "7" }, handBuiltToolCallContext);
}
