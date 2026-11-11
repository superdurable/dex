// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { MediaStore } from "../agent-media.js";
import { createAgentSessionFlow, type AgentSessionFlow, type AgentSessionOptions } from "../agent-session-flow.js";
import type { AgentDefinition } from "./agent-definition.js";
import { BuiltinLoopAgentRunner } from "./builtin-loop-agent-runner.js";

/**
 * Options of {@link createBuiltinAgentSessionFlow} that are not part of the agent definition.
 *
 * @since 0.5.0
 */
export interface BuiltinAgentSessionFlowOptions {
  /**
   * The media store that holds the session's images, such as `S3MediaStore` from `@superdurable/dex-agents/s3`.
   * Defaults to none.
   *
   * With a store, `send_user_message` accepts images, and the model sees each image of a user message or a tool
   * result. Stored messages keep only references: the runner reads the bytes with `loadImages` right before each
   * model call, so a store read failure fails that call and its `RunModel` Step retries. Use the same store, or one
   * that reads the same objects, in every Worker process of the Flow type. Without a store, `send_user_message`
   * refuses images and tool result images do not reach the model.
   *
   * @example
   * ```ts
   * import { S3MediaStore } from "@superdurable/dex-agents/s3";
   *
   * const mediaStore = S3MediaStore.fromStoreUri("s3://agent-media/sessions");
   * const flow = createBuiltinAgentSessionFlow(supportAgent, "SupportAgent", { mediaStore });
   * ```
   * @since 0.5.0
   */
  readonly mediaStore?: MediaStore;
}

/**
 * Creates the agent session Flow for an agent defined in Dex terms, run by the built-in agent loop.
 *
 * It builds the session Flow with a {@link BuiltinLoopAgentRunner} and derives the session options from
 * the definition, so tool policies, built-in tools, plan mode, the deadlines, and compaction are declared once. The Flow is the
 * same session Flow every adapter uses, with the same Steps, RPCs, and events. Construct it once at
 * application startup and register the same object in the Worker and Client Registry.
 *
 * @example
 * ```ts
 * const supportAgentFlow = createBuiltinAgentSessionFlow(
 *   {
 *     name: "support",
 *     instructions: "You help customers with their orders.",
 *     model: createAiSdkModelClient(openai("gpt-5-mini")),
 *     tools: [lookupOrder, refundOrder],
 *     builtInTools: ["ask_user", "durable_wait"],
 *     interruptDeadlineMs: 24 * 60 * 60 * 1000,
 *   },
 *   "SupportAgent",
 * );
 * const registry = new Registry([supportAgentFlow]);
 * await client.startFlow(supportAgentFlow, sessionId, { initialMessage: "Where is order 42?" });
 * ```
 * @param definition - The agent definition.
 * @param flowType - Flow type, unique within the Registry. Keep it stable: running sessions resume by it.
 * @param flowOptions - The media store; see {@link BuiltinAgentSessionFlowOptions}. Since 0.5.0.
 * @returns A new Flow; its `runner` is the {@link BuiltinLoopAgentRunner}.
 * @throws {TypeError} When the definition is invalid; see {@link BuiltinLoopAgentRunner} and
 * `createAgentSessionFlow`.
 * @throws {RangeError} When a limit or deadline in the definition is out of range.
 *
 * It replaces `createStepwiseAgentSessionFlow`, which `@superdurable/dex-agents/stepwise` keeps as a deprecated
 * alias.
 *
 * @since 0.5.0
 */
export function createBuiltinAgentSessionFlow(
  definition: AgentDefinition,
  flowType: string,
  flowOptions: BuiltinAgentSessionFlowOptions = {},
): AgentSessionFlow<BuiltinLoopAgentRunner> {
  const runner = new BuiltinLoopAgentRunner(definition, flowOptions.mediaStore);
  const options: AgentSessionOptions = {
    ...(definition.toolPolicies === undefined ? {} : { toolPolicies: definition.toolPolicies }),
    ...(definition.defaultToolEffect === undefined ? {} : { defaultToolEffect: definition.defaultToolEffect }),
    ...(definition.builtInTools === undefined ? {} : { builtInTools: definition.builtInTools }),
    ...(definition.planModeEnabled === undefined ? {} : { planModeEnabled: definition.planModeEnabled }),
    ...(definition.interruptDeadlineMs === undefined ? {} : { interruptDeadlineMs: definition.interruptDeadlineMs }),
    ...(definition.eventStreamCapacityBytes === undefined
      ? {}
      : { eventStreamCapacityBytes: definition.eventStreamCapacityBytes }),
    ...(definition.compaction === undefined ? {} : { compaction: definition.compaction }),
    ...(definition.spendLimit === undefined ? {} : { spendLimit: definition.spendLimit }),
  };
  return createAgentSessionFlow(runner, flowType, options);
}
