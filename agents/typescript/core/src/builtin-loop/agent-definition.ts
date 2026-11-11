// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { BuiltInTool } from "../agent-built-in-tools.js";
import type { CompactionPolicy } from "../agent-context-compaction.js";
import type { ModelSpendLimit } from "../agent-session-spend-limit.js";
import type { ToolPolicy } from "../agent-framework-runner.js";
import type { AgentPlan, InteractionMode, SessionSettings, ToolEffect } from "../agent-session-values.js";
import type { FunctionTool } from "./function-tool.js";
import type { ModelClient, ModelOutputItem, ModelSettings, ModelToolResultItem } from "./model-client.js";
import type { ToolSource } from "./tool-source.js";

/**
 * The {@link TurnContinuation} that makes the model answer again within the same turn.
 *
 * {@link message} is the instruction for the next model call, such as what the previous answer lacked. The runner
 * stores it in the turn as user text, so the next model request ends with a user message, which every provider
 * accepts.
 *
 * @example
 * ```ts
 * const continuation: ContinueTurn = { action: "continue", message: "Add the order number to your answer." };
 * ```
 * @since 0.5.0
 */
export interface ContinueTurn {
  /** Discriminator. */
  readonly action: "continue";
  /** Text the model receives as the next user message of the turn. It must not be blank. */
  readonly message: string;
}

/**
 * What {@link AgentDefinition.decideTurnContinuation} returns at the end of one loop iteration.
 *
 * - `"end"`: end the turn now. After tool calls, the runner makes no further model call.
 * - {@link ContinueTurn}: store its message in the turn as user text, then call the model. After an answer
 *   without tool calls, this makes another model call instead of ending the turn. After tool calls, the message
 *   follows the tool results in the same model call.
 * - `"default"`: do what the loop does without the hook: call the model again after tool calls, and end the turn
 *   after an answer.
 *
 * Later releases may add more forms, so handle only the values you return.
 *
 * @since 0.5.0
 */
export type TurnContinuation = "end" | "default" | ContinueTurn;

/**
 * What {@link AgentDefinition.decideTurnContinuation} receives at the end of one loop iteration.
 *
 * An iteration is one model call and the tool calls it requested. The runner never stores this context.
 *
 * @since 0.5.0
 */
export interface TurnContinuationContext {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** Turn ID. */
  readonly turnId: string;
  /** The {@link SessionSettings} the turn started with. Empty when the session has none. */
  readonly sessionSettings: SessionSettings;
  /**
   * The output items of this iteration's model response, in the order the model returned them. Read
   * {@link ModelOutputItem.text} for the answer and {@link ModelOutputItem.toolCalls} for the calls.
   */
  readonly modelOutput: readonly ModelOutputItem[];
  /**
   * The results of this iteration's tool calls, in the order the model requested them. Empty when the model
   * answered without tool calls.
   */
  readonly toolResults: readonly ModelToolResultItem[];
}

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
 * An agent defined in Dex terms, run by the built-in agent loop.
 *
 * Pass it to {@link createBuiltinAgentSessionFlow}. The session Flow, its Steps, approvals, events,
 * and RPCs are the ones every adapter uses: each model call is one `RunModel` Step, and each function
 * tool call is its own Step. Nothing replays a loop, so no code follows a determinism rule, and a
 * deployed change to the definition takes effect at the next model call.
 *
 * @example
 * ```ts
 * import { openai } from "@ai-sdk/openai";
 * import { createAiSdkModelClient } from "@superdurable/dex-agents/ai-sdk";
 * import { createBuiltinAgentSessionFlow } from "@superdurable/dex-agents/builtin-loop";
 *
 * export const supportAgentFlow = createBuiltinAgentSessionFlow(
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
  /**
   * The application's spend limit, called before and after each model call. Defaults to no checks. See
   * {@link ModelSpendLimit}.
   *
   * @since 0.3.0
   */
  readonly spendLimit?: ModelSpendLimit;
  /**
   * Decides, at the end of each loop iteration, whether the turn ends or continues.
   *
   * Use it to stop a turn once a tool call has done the work, or to make the model try again after an answer
   * that the application rejects. Absent, the default, the loop calls the model again after tool calls and ends
   * the turn after an answer.
   *
   * The runner calls it in two places:
   *
   * - After a batch of tool calls, in the next `RunModel` Step, before the model call. `"end"` completes the turn
   *   with no model call: the tool results become the turn's last messages, and the Step records zero model
   *   calls. A {@link ContinueTurn} adds its message after the tool results, and the model call receives both.
   *   `"default"` calls the model with the results.
   * - After a model call that requested no tools, in the same `RunModel` Step. A {@link ContinueTurn} stores the
   *   answer and its message as user text, then makes another model call in a new `RunModel` Step. `"end"` and
   *   `"default"` complete the turn.
   *
   * The continuation message is part of the turn's transcript: the model sees it as user text, and snapshots hold
   * it as a `user` message between the two answers. `convertAgentSnapshotToUIMessages` keeps such a message out of
   * the chat and shows the next model call as a new step of the same assistant message, as the UI stream does.
   *
   * At most 8 continuations in a row may follow answers without tool calls; a tool call resets the count. The 9th
   * ends the turn with that answer stored and the turn's `lastTurnError` set. Each continued model call counts
   * toward {@link maxModelCallsPerTurn}.
   *
   * The function may be synchronous or return a Promise. It runs inside the `RunModel` attempt, so a thrown error
   * or any other value, including a {@link ContinueTurn} with a blank message, fails that attempt, and the Step's
   * retry policy runs it again. After an answer, that retry also repeats the model call. The function can run more
   * than once for one iteration, so keep it free of side effects. `cancel_turn` and a steered message still end a
   * continued turn, because the session checks them before every `RunModel` reaches the runner.
   *
   * @example
   * ```ts
   * const definition: AgentDefinition = {
   *   name: "approvals",
   *   instructions: "Review the request, then call submit_decision.",
   *   model,
   *   tools: [submitDecision],
   *   decideTurnContinuation: ({ toolResults, modelOutput }) => {
   *     // Once submit_decision ran, the turn is done; skip the closing model call.
   *     if (toolResults.some((result) => result.toolName === "submit_decision")) {
   *       return "end";
   *     }
   *     const answer = modelOutput.map((item) => item.text ?? "").join("");
   *     if (toolResults.length === 0 && !answer.includes("DECISION:")) {
   *       return { action: "continue", message: "Finish with a line that starts with DECISION:." };
   *     }
   *     return "default";
   *   },
   * };
   * ```
   * @param context - This iteration's model output and tool results.
   * @returns `"end"`, `"default"`, or a {@link ContinueTurn}; see {@link TurnContinuation}.
   * @since 0.5.0
   */
  readonly decideTurnContinuation?: (
    context: TurnContinuationContext,
  ) => TurnContinuation | Promise<TurnContinuation>;
}
