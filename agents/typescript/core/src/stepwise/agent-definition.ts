// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { BuiltInTool } from "../agent-built-in-tools.js";
import type { ToolPolicy } from "../agent-framework-runner.js";
import type { ToolEffect } from "../agent-session-values.js";
import type { FunctionTool } from "./function-tool.js";
import type { ModelClient, ModelSettings } from "./model-client.js";

/** What a function for {@link AgentDefinition.instructions} receives in every `RunModel`. */
export interface InstructionContext {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** Turn ID. */
  readonly turnId: string;
  /** One-based `RunModel` attempt. */
  readonly attempt: number;
}

/**
 * An agent defined in Dex terms, run by the built-in Stepwise loop.
 *
 * Pass it to {@link createStepwiseAgentSessionFlow}. The session Flow, its Steps, approvals, events,
 * and RPCs are the ones every adapter uses: each model call is one `RunModel` Step, and each function
 * tool call is its own Step. Nothing replays a loop, so no code follows a determinism rule, and a
 * deployed change to the definition takes effect at the next model call.
 *
 * @example
 * ```ts
 * import { openai } from "@ai-sdk/openai";
 * import { createAiSdkModelClient } from "@superdurable/dex-agents/ai-sdk";
 * import { createStepwiseAgentSessionFlow } from "@superdurable/dex-agents/stepwise";
 *
 * export const supportAgentFlow = createStepwiseAgentSessionFlow(
 *   {
 *     name: "support",
 *     instructions: "You help customers with their orders.",
 *     model: createAiSdkModelClient(openai("gpt-5-mini")),
 *     tools: [lookupOrder, refundOrder],
 *     builtInTools: ["ask_user", "durable_wait"],
 *   },
 *   "SupportAgent",
 * );
 * ```
 */
export interface AgentDefinition {
  /** Agent name, non-blank. */
  readonly name: string;
  /**
   * System instructions, or a function that returns them. A function runs in every `RunModel`, so
   * it can depend on the turn; it must not have side effects.
   */
  readonly instructions: string | ((context: InstructionContext) => string | Promise<string>);
  /** The model client, such as `createAiSdkModelClient(model)`. */
  readonly model: ModelClient;
  /** Temperature, maximum output tokens, reasoning effort, and provider options. Defaults to none. */
  readonly modelSettings?: ModelSettings;
  /** Function tools from {@link functionTool}. Defaults to none. */
  readonly tools?: readonly FunctionTool[];
  /** Policies by tool name; a policy overrides a function tool's own effect and approval. Defaults to none. */
  readonly toolPolicies?: Readonly<Record<string, ToolPolicy>>;
  /** Effect of a tool that declares none. Defaults to `"unclassified"`. */
  readonly defaultToolEffect?: ToolEffect;
  /** Built-in tools the model sees, such as `["ask_user", "durable_wait"]`. Defaults to none. */
  readonly builtInTools?: readonly BuiltInTool[];
  /**
   * After this many model calls in one turn, the next call offers no tools, so the model must answer.
   * Defaults to 100.
   */
  readonly maxModelCallsPerTurn?: number;
  /** Milliseconds an approval or question may stay pending. Defaults to no deadline. */
  readonly interruptDeadlineMs?: number;
  /** Byte budget of the `AgentEvents` Stream. Defaults to 10 MiB. */
  readonly eventStreamCapacityBytes?: number;
}
