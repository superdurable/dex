// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type {
  AgentFrameworkRunner,
  AgentSessionFlow,
  DeferredToolOutcome,
  SessionSettings,
  ToolSource,
} from "@superdurable/dex-agents";

import type { EffectiveConfig, TurnConfig } from "./cli-config.js";

/**
 * A group of local tools that the runtime offers, as the configuration's `local_tools` names them.
 *
 * - `files`: `read_file`, `list_directory`, `search_files`, `write_file`, and `edit_file`, inside the workspace.
 * - `shell`: `run_shell_command`, in the workspace.
 * - `web_fetch`: `fetch_web_page`.
 * - `browser`: `browser_navigate`, `browser_snapshot`, `browser_click`, `browser_fill`, and `browser_submit_form`.
 *
 * @since 0.5.0
 */
export type LocalToolGroup = "files" | "shell" | "web_fetch" | "browser";

/**
 * Every {@link LocalToolGroup}, in configuration order.
 *
 * @since 0.5.0
 */
export const localToolGroups: readonly LocalToolGroup[] = Object.freeze(["files", "shell", "web_fetch", "browser"]);

/**
 * One agent harness that a Dex Agents CLI command runs, such as the Claude Agent SDK or a Stepwise agent.
 *
 * A harness package implements this interface and calls {@link runAgentCli} from its bin. The runtime owns
 * everything else: the configuration file, the Dex server, the connectors, the local tools, the Worker, and the
 * terminal chat. The harness only builds the session Flow from a {@link HarnessContext}. It works for a runner-based
 * adapter, which passes `context.toolSources` to its framework runner and calls `createAgentSessionFlow`, and for
 * a Stepwise harness, which puts them in `AgentDefinition.toolSources` and calls `createStepwiseAgentSessionFlow`.
 *
 * A harness that brings its own tools for some local tool groups, such as Claude Code's Read, Write, and Bash,
 * lists those groups in {@link replacedLocalTools}. The runtime then leaves them out of `context.toolSources`, and
 * the harness enables its own tools from `context.config.local_tools` with the same policy.
 *
 * The Python runtime has the same concepts: `AgentHarness` with `name`, `config_section`, `flow_type`,
 * `default_model`, `replaced_local_tools`, and `create_session_flow(context)`.
 *
 * @example
 * ```ts
 * import { createStepwiseAgentSessionFlow } from "@superdurable/dex-agents/stepwise";
 * import { runAgentCli, type AgentHarness } from "@superdurable/dex-agents-cli";
 *
 * const harness: AgentHarness = {
 *   name: "builtin",
 *   configSection: "builtin",
 *   flowType: "DexAgentsBuiltinCliSession",
 *   defaultModel: "openai/gpt-5-mini",
 *   createSessionFlow: (context) =>
 *     createStepwiseAgentSessionFlow(
 *       {
 *         name: "assistant",
 *         instructions: (turn) => context.resolveTurn(turn.sessionSettings ?? {}).instructions ?? "Help the user.",
 *         model: modelFor(context.config.model),
 *         resolveModel: (settings) => modelFor(context.resolveTurn(settings).model),
 *         toolSources: context.toolSources,
 *         builtInTools: ["ask_user"],
 *       },
 *       context.flowType,
 *     ),
 * };
 * process.exitCode = await runAgentCli(harness);
 * ```
 * @since 0.5.0
 */
export interface AgentHarness {
  /**
   * The harness name, such as `claude-sdk`. The command is `dex-agents-<name>`, and its configuration file is
   * `~/.dex/agents/<name>.yaml`.
   */
  readonly name: string;
  /** The configuration file section with this harness's own options, such as `claude` or `builtin`. */
  readonly configSection: string;
  /**
   * The base Flow type of the command's sessions, such as `DexAgentsClaudeSdkCliSession`. Keep it stable.
   *
   * The runtime registers `<flowType>-<hash>`, from `namespaceSessionFlowType`, where the hash covers the
   * host name and the configuration file's real path. A Worker then takes only the Steps of sessions that a
   * command with the same host and configuration file started, so an approved write or shell command runs with
   * the workspace and policies it was approved under. Processes that share one configuration file share their
   * sessions' Steps. `--resume` and the `sessions` subcommand see only the sessions of the same configuration.
   */
  readonly flowType: string;
  /** The model when the file sets none, or `null` when the command must refuse to run without one. */
  readonly defaultModel: string | null;
  /**
   * The local tool groups that the harness replaces with its own tools. The runtime offers the other groups.
   * Defaults to none.
   */
  readonly replacedLocalTools?: readonly LocalToolGroup[];
  /**
   * Builds the session Flow. The runtime calls it once at startup and registers the Flow in its Worker and Client.
   *
   * @param context - The configuration, the instructions, the tool sources, and the per-turn resolver.
   * @returns The session Flow, of type {@link HarnessContext.flowType}.
   * @throws Any error, which stops the command with its message.
   */
  createSessionFlow(context: HarnessContext): AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>>;
}

/**
 * What a harness receives to build its session Flow.
 *
 * Settings that the session may change per turn, the model and the instructions, come from
 * {@link resolveTurn}; call it with the turn's `sessionSettings` in each model call. The tool sources already
 * apply the turn's narrowing and approval rules themselves.
 *
 * @since 0.5.0
 */
export interface HarnessContext {
  /** The Flow type to build: {@link AgentHarness.flowType} with the host and configuration hash. */
  readonly flowType: string;
  /** The file's effective configuration, after command-line overrides. */
  readonly config: EffectiveConfig;
  /** The absolute workspace directory, which exists. */
  readonly workspace: string;
  /**
   * The file's instructions: `system_prompt`, or the text of `system_prompt_file`, or `null` for the harness's
   * default instructions.
   */
  readonly instructions: string | null;
  /** The file's harness section, `config.harness_options`. */
  readonly harnessOptions: Readonly<Record<string, unknown>>;
  /**
   * The tool sources to offer, in order: the connectors, then the local tools the harness does not replace.
   * Each call runs in its own Dex tool Step.
   */
  readonly toolSources: readonly ToolSource[];
  /**
   * Returns one turn's configuration and instructions from the session settings the turn started with.
   *
   * It is pure and cached. Call it from model calls only: settings that fail resolution throw, which fails the
   * model call and then the turn, with the error code and JSON Pointer in `lastTurnError`.
   *
   * @param sessionSettings - The turn's `sessionSettings`, or `{}`.
   * @returns The turn's configuration and its instructions, `null` for the harness default.
   * @throws {ConfigError} When the settings fail the schema or widen the file.
   */
  resolveTurn(sessionSettings: SessionSettings): ResolvedTurn;
  /**
   * Returns the configuration of the turn whose model call is running, or the file's configuration outside a
   * model call. A harness that replaces local tool groups reads it in its own tool callbacks, such as an approval
   * hook that runs during the model call, together with {@link isLocalToolGroupEnabled} and
   * `resolveTurnToolPolicy`.
   *
   * @returns The configuration and instructions.
   */
  currentTurn(): ResolvedTurn;
}

/**
 * One turn's configuration, with its instructions read.
 *
 * @since 0.5.0
 */
export interface ResolvedTurn extends TurnConfig {
  /** The turn's instructions: its `system_prompt`, the file's prompt file text, or `null`. */
  readonly instructions: string | null;
}

/**
 * Returns whether a turn switches a local tool group on: `files`, `web_fetch`, and `browser` by their switches,
 * and `shell` by `shell.enabled`. A harness that replaces a group checks it before offering or running its own
 * tool of that group.
 *
 * @example
 * ```ts
 * const turn = context.currentTurn();
 * if (!isLocalToolGroupEnabled(turn, "shell")) {
 *   return { behavior: "deny", message: "shell commands are switched off for this session" };
 * }
 * ```
 * @param turn - The turn's configuration.
 * @param group - The group.
 * @returns `true` when the group is on.
 * @since 0.5.0
 */
export function isLocalToolGroupEnabled(turn: TurnConfig, group: LocalToolGroup): boolean {
  return group === "shell" ? turn.local_tools.shell.enabled : turn.local_tools[group];
}

/**
 * Returns whether a shell command waits for approval in a turn: unless the turn's allow list has its exact text.
 *
 * @param turn - The turn's configuration.
 * @param command - The command text.
 * @returns `true` when the command needs approval.
 * @since 0.5.0
 */
export function isShellCommandApprovalRequired(turn: TurnConfig, command: string): boolean {
  return !turn.local_tools.shell.allow.includes(command);
}
