// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { BuiltInTool } from "../agent-built-in-tools.js";
import type { CompactionPolicy } from "../agent-context-compaction.js";
import type { ToolPolicy } from "../agent-framework-runner.js";
import type { AgentPlan, InteractionMode, SessionSettings, ToolEffect } from "../agent-session-values.js";
import type { FunctionTool } from "./function-tool.js";
import type { ModelClient, ModelSettings } from "./model-client.js";
import type { ToolSource } from "./tool-source.js";

/**
 * How the first model call of a planning turn without a draft uses `write_todos`, as
 * {@link AgentDefinition.planningToolChoice}: `auto` lets the model choose, and `required` forces it.
 */
export type PlanningToolChoice = "auto" | "required";

/** What a function for {@link AgentDefinition.instructions} receives in every `RunModel`. */
export interface InstructionContext {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** Turn ID. */
  readonly turnId: string;
  /** One-based `RunModel` attempt. */
  readonly attempt: number;
  /** The turn's mode. The runner always sets it; it is optional so that hand-built contexts keep compiling. */
  readonly interactionMode?: InteractionMode;
  /**
   * The session's plan before this model call, or `null`. The runner already shows it to the model; use it to
   * adapt the instructions. The runner always sets it.
   */
  readonly plan?: AgentPlan | null;
  /**
   * The {@link SessionSettings} the turn started with, such as a persona or prompt name. Empty when the session
   * has none. The runner always sets it; treat `undefined` as `{}`.
   */
  readonly sessionSettings?: SessionSettings;
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
  /**
   * The model client, such as `createAiSdkModelClient(model)`. It serves every model call unless
   * {@link resolveModel} is set.
   */
  readonly model: ModelClient;
  /**
   * Maps the turn's `AgentTurnContext.sessionSettings` to the model client of each model call, including the
   * calls that write and size a summary.
   *
   * It runs on every `RunModel` and `CompactContext` attempt, so keep it fast and free of I/O: look the model up
   * in clients the Worker built at startup. The settings come from the application's start input or
   * `update_session_settings`, so choose only from models the Worker allows. A session without settings passes
   * `{}`. An exception fails that attempt like a model error, and its message becomes the turn's
   * `lastTurnError`, which snapshots show, so never put the settings in an error message. Absent, {@link model}
   * serves every call. Only model calls see the settings: tool calls do not receive them, and the compaction
   * policy's `contextWindowTokens` is one value for every resolved model, so set it for the smallest window. This
   * core does not check whether a resolved model accepts a forced tool choice; see {@link planningToolChoice}.
   *
   * @example
   * ```ts
   * const models: Record<string, ModelClient> = {
   *   "gpt-5-mini": createAiSdkModelClient(openai("gpt-5-mini")),
   *   "gpt-5": createAiSdkModelClient(openai("gpt-5")),
   * };
   * const definition: AgentDefinition = {
   *   name: "support",
   *   instructions: "You help customers with their orders.",
   *   model: models["gpt-5-mini"],
   *   resolveModel: (settings) => models[String(settings.model)] ?? models["gpt-5-mini"],
   * };
   * ```
   * @param sessionSettings - The settings the turn started with.
   * @returns The model client for this call.
   */
  readonly resolveModel?: (sessionSettings: SessionSettings) => ModelClient;
  /** Temperature, maximum output tokens, reasoning effort, and provider options. Defaults to none. */
  readonly modelSettings?: ModelSettings;
  /** Function tools from {@link functionTool}. Defaults to none. */
  readonly tools?: readonly FunctionTool[];
  /**
   * Tool sources, such as `McpToolSource`, whose tools the runner lists in every `RunModel` of a chat or
   * executing turn and offers after the function tools. Each call of a source tool runs in its own tool Step
   * through the source, with the source's policy, effect, idempotency key, and tool tasks. A listed name that
   * repeats a function tool, an enabled built-in tool, or another source's tool fails that `RunModel` attempt
   * with a `TypeError`; a source's `declaredToolNames` are checked when the runner is constructed. A source whose
   * listing fails or takes longer than {@link toolSourceListingTimeoutMs} offers no tools in that model call. See
   * {@link ToolSource}. Defaults to none.
   */
  readonly toolSources?: readonly ToolSource[];
  /**
   * Milliseconds one model call waits for each tool source's `listTools`. A source that takes longer offers no tools
   * in that call, and its listing may finish in the background. Defaults to 10 seconds.
   */
  readonly toolSourceListingTimeoutMs?: number;
  /** Policies by tool name; a policy overrides a function tool's own effect and approval. Defaults to none. */
  readonly toolPolicies?: Readonly<Record<string, ToolPolicy>>;
  /** Effect of a tool that declares none. Defaults to `"unclassified"`. */
  readonly defaultToolEffect?: ToolEffect;
  /** Built-in tools the model sees, such as `["ask_user", "durable_wait"]`. Defaults to none. */
  readonly builtInTools?: readonly BuiltInTool[];
  /**
   * Whether the session accepts `planMode` user messages. A planning turn offers only `write_todos`, and the
   * `execute_plan` RPC then runs the draft with every tool. Requires `write_todos` in {@link builtInTools}.
   * Defaults to `false`.
   */
  readonly planModeEnabled?: boolean;
  /**
   * `auto`, the default, lets the model of a planning turn call `write_todos` or answer in text, such as
   * with a clarifying question; the plan note tells it to write the plan with `write_todos`, and a text
   * answer ends the turn without a draft. `required` forces `write_todos` on the first model call of a
   * planning turn while no draft exists. It needs a provider and model that accept a forced tool choice:
   * Claude Opus 5.5 and Claude Sonnet 5.5, for example, refuse one, and so does Anthropic extended thinking,
   * which fails every attempt of that model call. Unlike the Python core, this core cannot check the model
   * when the definition is built.
   */
  readonly planningToolChoice?: PlanningToolChoice;
  /**
   * After this many model calls in one turn, the next call offers no tools, so the model must answer.
   * Defaults to 100.
   */
  readonly maxModelCallsPerTurn?: number;
  /** Milliseconds an approval or question may stay pending. Defaults to no deadline. */
  readonly interruptDeadlineMs?: number;
  /** Byte budget of the `AgentEvents` Stream. Defaults to 10 MiB. */
  readonly eventStreamCapacityBytes?: number;
  /**
   * When to summarize earlier messages so the model input stays under the window, counted by the model
   * client's `estimateInputTokens`. Defaults to no compaction. See {@link CompactionPolicy}.
   */
  readonly compaction?: CompactionPolicy;
}
