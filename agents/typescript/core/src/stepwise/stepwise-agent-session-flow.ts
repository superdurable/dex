// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createAgentSessionFlow, type AgentSessionFlow, type AgentSessionOptions } from "../agent-session-flow.js";
import type { AgentDefinition } from "./agent-definition.js";
import { StepwiseAgentRunner } from "./stepwise-agent-runner.js";

/**
 * Creates the agent session Flow for an agent defined in Dex terms, run by the built-in Stepwise loop.
 *
 * It builds the session Flow with a {@link StepwiseAgentRunner} and derives the session options from
 * the definition, so tool policies, built-in tools, and the deadlines are declared once. The Flow is the
 * same session Flow every adapter uses, with the same Steps, RPCs, and events. Construct it once at
 * application startup and register the same object in the Worker and Client Registry.
 *
 * @example
 * ```ts
 * const supportAgentFlow = createStepwiseAgentSessionFlow(
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
 * @returns A new Flow; its `runner` is the {@link StepwiseAgentRunner}.
 * @throws {TypeError} When the definition is invalid; see {@link StepwiseAgentRunner} and
 * `createAgentSessionFlow`.
 * @throws {RangeError} When a limit or deadline in the definition is out of range.
 */
export function createStepwiseAgentSessionFlow(
  definition: AgentDefinition,
  flowType: string,
): AgentSessionFlow<StepwiseAgentRunner> {
  const runner = new StepwiseAgentRunner(definition);
  const options: AgentSessionOptions = {
    ...(definition.toolPolicies === undefined ? {} : { toolPolicies: definition.toolPolicies }),
    ...(definition.defaultToolEffect === undefined ? {} : { defaultToolEffect: definition.defaultToolEffect }),
    ...(definition.builtInTools === undefined ? {} : { builtInTools: definition.builtInTools }),
    ...(definition.interruptDeadlineMs === undefined ? {} : { interruptDeadlineMs: definition.interruptDeadlineMs }),
    ...(definition.eventStreamCapacityBytes === undefined
      ? {}
      : { eventStreamCapacityBytes: definition.eventStreamCapacityBytes }),
  };
  return createAgentSessionFlow(runner, flowType, options);
}
