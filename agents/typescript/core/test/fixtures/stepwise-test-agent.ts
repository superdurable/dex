// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import { createAiSdkModelClient } from "../../src/ai-sdk/index.js";
import { createAgentSessionFlow, type AgentSessionFlow, type ToolPolicy } from "../../src/index.js";
import {
  BuiltinLoopAgentRunner,
  createBuiltinAgentSessionFlow,
  functionTool,
  type AgentDefinition,
  type ToolCallContext,
  type TurnContinuation,
  type TurnContinuationContext,
} from "../../src/builtin-loop/index.js";
import { FileGate, InvocationLog } from "../../src/testing/index.js";
import { createStepwiseScriptedLanguageModel } from "./stepwise-scripted-language-model.js";

/** Flow type of the Stepwise test agent. */
export const stepwiseTestAgentFlowType = "StepwiseTestAgent";
/** Environment variable naming the marker directory of a Stepwise test Worker child. */
export const stepwiseMarkerDirectoryVariable = "STEPWISE_AGENT_MARKER_DIRECTORY";

/** Options of the Stepwise test agent variants. */
export interface StepwiseTestAgentOptions {
  readonly flowType?: string;
  readonly interruptDeadlineMs?: number;
  readonly maxModelCallsPerTurn?: number;
  /**
   * Tool policies passed only to `createAgentSessionFlow`, not in the agent definition, so the Flow is
   * built from a `BuiltinLoopAgentRunner` and these options.
   */
  readonly sessionToolPolicies?: Readonly<Record<string, ToolPolicy>>;
  /** Whether the agent sets {@link decideBuiltinLoopTestTurnContinuation} as its `decideTurnContinuation`. */
  readonly hasTurnContinuationHook?: boolean;
  /** How this deploy changed `issue_refund`; omitted keeps the original tool. */
  readonly refundToolDeploy?: RefundToolDeploy;
}

/**
 * A deploy of `issue_refund`: `renamed` registers it as `refund_payment`, and `schema_changed` requires
 * `refund_order_id` instead of `order_id`.
 */
export type RefundToolDeploy = "renamed" | "schema_changed";

/** Set to a {@link RefundToolDeploy}, a Stepwise test Worker child serves that deploy of `issue_refund`. */
export const refundToolDeployVariable = "STEPWISE_AGENT_REFUND_TOOL_DEPLOY";

/** Set to `1`, a built-in loop test Worker child serves the agent with its `decideTurnContinuation` hook. */
export const builtinLoopTurnContinuationHookVariable = "BUILTIN_LOOP_AGENT_TURN_CONTINUATION_HOOK";

/**
 * The test agent's `decideTurnContinuation`: `end` after a tool result that mentions `end-after-tools`, and after an
 * answer `draft for <words>`, a continuation whose message `finalize <words>` is the scripted model's next command.
 */
export function decideBuiltinLoopTestTurnContinuation(context: TurnContinuationContext): TurnContinuation {
  if (context.toolResults.some((result) => result.output.includes("end-after-tools"))) {
    return "end";
  }
  const answer = context.modelOutput.map((item) => item.text ?? "").join("");
  if (context.toolResults.length === 0 && answer.startsWith("draft for ")) {
    return { action: "continue", message: `finalize ${answer.slice("draft for ".length)}` };
  }
  return "default";
}

/**
 * Creates the Stepwise test agent on the scripted AI SDK mock model; tests and Worker children build it
 * identically. Tools record every run in `<tool>.log` as `<flowId> <toolCallId> attempt <n> key <key>`.
 * `lookup_order` waits at the `lookup` FileGate for an order ID that starts with `gated`, and
 * `charge_card`, a write that needs no approval, always waits at the `charge_card` FileGate.
 * `issue_refund`, an `idempotent_write`, always waits at the `issue_refund` FileGate.
 */
export function createStepwiseTestAgentFlow(
  markerDirectory: string,
  options: StepwiseTestAgentOptions = {},
): AgentSessionFlow<BuiltinLoopAgentRunner> {
  const recordRun = (toolName: string, context: ToolCallContext) =>
    new InvocationLog(join(markerDirectory, `${toolName}.log`)).record(
      `${context.flowId} ${context.toolCallId} attempt ${context.attempt} key ${context.idempotencyKey}`,
    );
  const orderParameters = {
    type: "object",
    properties: { order_id: { type: "string", description: "The order ID." } },
    required: ["order_id"],
    additionalProperties: false,
  };
  const definition: AgentDefinition = {
      name: "stepwise-test",
      instructions: ({ turnId, attempt }) => `You are the test agent. Turn ${turnId}, attempt ${attempt}.`,
      model: createAiSdkModelClient(createStepwiseScriptedLanguageModel(markerDirectory)),
      modelSettings: { temperature: 0 },
      tools: [
        functionTool<{ order_id: string }>({
          name: "lookup_order",
          description: "Look up an order by its ID.",
          parameters: orderParameters,
          effect: "read",
          execute: async ({ order_id }, context) => {
            recordRun("lookup_order", context);
            if (order_id.startsWith("gated")) {
              const gate = new FileGate(markerDirectory, "lookup");
              gate.markReached();
              await gate.waitForRelease(context.cancellationSignal);
            }
            return { order_id, status: "shipped" };
          },
        }),
        functionTool<{ order_id: string }>({
          name: "refund_order",
          description: "Refund an order.",
          parameters: orderParameters,
          effect: "non_idempotent_write",
          execute: ({ order_id }, context) => {
            recordRun("refund_order", context);
            return `refund issued for ${order_id}`;
          },
        }),
        functionTool<{ order_id: string }>({
          name: "charge_card",
          description: "Charge the card of an order.",
          parameters: orderParameters,
          effect: "non_idempotent_write",
          requiresApproval: false,
          execute: async (_args, context) => {
            recordRun("charge_card", context);
            const gate = new FileGate(markerDirectory, "charge_card");
            gate.markReached();
            await gate.waitForRelease(context.cancellationSignal);
            return "card charged";
          },
        }),
        functionTool<Record<string, string>>({
          name: options.refundToolDeploy === "renamed" ? "refund_payment" : "issue_refund",
          description: "Refund an order's payment, once per idempotency key.",
          parameters:
            options.refundToolDeploy === "schema_changed"
              ? {
                  type: "object",
                  properties: { refund_order_id: { type: "string", description: "The order ID." } },
                  required: ["refund_order_id"],
                  additionalProperties: false,
                }
              : orderParameters,
          effect: "idempotent_write",
          requiresApproval: false,
          execute: async (_args, context) => {
            recordRun("issue_refund", context);
            const gate = new FileGate(markerDirectory, "issue_refund");
            gate.markReached();
            await gate.waitForRelease(context.cancellationSignal);
            return "refund issued";
          },
        }),
      ],
      builtInTools: ["ask_user", "durable_wait"],
      ...(options.interruptDeadlineMs === undefined ? {} : { interruptDeadlineMs: options.interruptDeadlineMs }),
      ...(options.maxModelCallsPerTurn === undefined ? {} : { maxModelCallsPerTurn: options.maxModelCallsPerTurn }),
      ...(options.hasTurnContinuationHook === true
        ? { decideTurnContinuation: decideBuiltinLoopTestTurnContinuation }
        : {}),
  };
  const flowType = options.flowType ?? stepwiseTestAgentFlowType;
  if (options.sessionToolPolicies === undefined) {
    return createBuiltinAgentSessionFlow(definition, flowType);
  }
  return createAgentSessionFlow(new BuiltinLoopAgentRunner(definition), flowType, {
    builtInTools: definition.builtInTools ?? [],
    toolPolicies: options.sessionToolPolicies,
  });
}
