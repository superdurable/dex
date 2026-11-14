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
  MediaStore,
  SessionSettings,
  ToolSource,
} from "@superdurable/dex-agents";

import type { EffectiveConfig, TurnConfig } from "./cli-config.js";
import type { ToolStepTurn } from "./turn-config-resolver.js";

/**
 * A group of local tools that the CLI core offers, as the configuration's `local_tools` names them.
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
 * Where a command's workspace is when its configuration file sets no `workspace`.
 *
 * - `dex_agent_workspace`: `~/dex-agent-workspace`, the CLI core's `defaultWorkspace`.
 * - `launch_directory`: the directory the command starts in, as Pi uses it. Each directory then has its own
 *   sessions, because the session Flow type hashes the workspace.
 *
 * @since 0.6.0
 */
export type HarnessDefaultWorkspaceLocation = "dex_agent_workspace" | "launch_directory";

/**
 * One agent harness that a Dex Agents CLI command runs, such as the Claude Agent SDK or a built-in loop agent.
 *
 * A harness package implements this interface and calls {@link runAgentCli} from its bin. The CLI core owns
 * everything else: the configuration file, the Dex server, the connectors, the local tools, the Worker, and the
 * terminal chat. The harness only builds the session Flow from a {@link HarnessContext}. It works for a runner-based
 * adapter, which passes `context.toolSources` to its framework runner and calls `createAgentSessionFlow`, and for
 * a built-in loop harness, which puts them in `AgentDefinition.toolSources` and calls `createBuiltinAgentSessionFlow`.
 * An adapter that takes a media store gets `context.mediaStore`, so the model sees the images the user attaches.
 *
 * A harness that brings its own tools for some local tool groups, such as Claude Code's Read, Write, and Bash,
 * lists those groups in {@link replacedLocalTools}. The CLI core then leaves them out of `context.toolSources`, and
 * the harness enables its own tools from `context.config.local_tools` with the same policy.
 *
 * The Python CLI core has the same concepts: `AgentHarness` with `name`, `config_section`, `flow_type`,
 * `default_model`, `replaced_local_tools`, and `create_session_flow(context)`.
 *
 * @example
 * ```ts
 * import { createBuiltinAgentSessionFlow } from "@superdurable/dex-agents/builtin-loop";
 * import { runAgentCli, type AgentHarness } from "@superdurable/dex-agents-cli-core";
 *
 * const harness: AgentHarness = {
 *   name: "builtin",
 *   configSection: "builtin",
 *   flowType: "DexAgentsBuiltinCliSession",
 *   defaultModel: "openai/gpt-5-mini",
 *   createSessionFlow: (context) =>
 *     createBuiltinAgentSessionFlow(
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
   * The CLI core registers `<flowType>-<suffix>`, from `namespaceSessionFlowType`, where the suffix hashes the
   * host name, the user, the configuration file's real path, and the workspace. A Worker then takes only the
   * Steps of sessions started in the same execution environment, so an approved write or shell command runs
   * where it was approved. Processes in the same environment share their sessions' Steps. `--resume` and the
   * `sessions` subcommand see only the sessions of the same environment.
   *
   * Changed in 0.6.8: every new session has this Flow type itself and runs on its own task queue, which only the
   * command that has the session open pulls. `--resume` opens a session of this Flow type from any execution
   * environment, and the `sessions` subcommand lists them all. The CLI core still registers `<flowType>-<suffix>` for
   * the sessions that releases before 0.6.8 started; they still resume only in their own execution environment.
   */
  readonly flowType: string;
  /** The model when the file sets none, or `null` when the command must refuse to run without one. */
  readonly defaultModel: string | null;
  /**
   * The local tool groups that the harness replaces with its own tools. The CLI core offers the other groups.
   * Defaults to none.
   */
  readonly replacedLocalTools?: readonly LocalToolGroup[];
  /**
   * Where the workspace is when the configuration file sets none. Defaults to `dex_agent_workspace`.
   *
   * With `launch_directory`, `init` writes no `workspace`, so the command keeps working in the directory it
   * starts in. A `workspace` in the file still overrides it. The Python CLI core has no such option.
   *
   * @since 0.6.0
   */
  readonly defaultWorkspaceLocation?: HarnessDefaultWorkspaceLocation;
  /**
   * Whether the harness's runner fixes its tools when the Flow is built, instead of listing the tool sources in
   * every model call. Defaults to `false`; every TypeScript adapter lists them per model call.
   *
   * With `true`, the CLI core lists the connector tools the file enables before it calls
   * {@link createSessionFlow}, so the connector source's `cachedTools()` holds them. When the connectors MCP server
   * does not answer at startup, or that listing fails, the command warns that this run has no connector tools and
   * that a restart brings them back. The Python CLI core calls it `is_tool_set_fixed_at_build`.
   *
   * @since 0.5.0
   */
  readonly isToolSetFixedAtBuild?: boolean;
  /**
   * Which chat the command shows at a terminal: `"dexcli"`, the `dexcli` terminal UI when it is available, or
   * `"plain"`, the text chat only. Defaults to `"dexcli"`. A harness with its own terminal interface, such as Pi,
   * declares `"plain"`. Without a terminal, or with `--plain`, every command uses the text chat. The Python CLI
   * core calls it `terminal_interface`.
   *
   * @since 0.6.0
   */
  readonly terminalInterface?: "dexcli" | "plain";
  /**
   * The values of the `reasoning_effort` session setting that the harness maps to its SDK, from least to most
   * effort, such as `["low", "medium", "high"]`. Defaults to none.
   *
   * Declare a value only when the harness passes it to every model call of a turn whose settings carry it: read
   * the turn's {@link ResolvedTurn.reasoningEffort} from {@link HarnessContext.resolveTurn} or
   * {@link HarnessContext.currentTurn} in each model call, and pass nothing when it is absent, so the model's own
   * default applies. The CLI core rejects any other value, and then the turn fails with
   * `unsupported_reasoning_effort` at `/reasoning_effort`. A harness that declares none rejects every
   * `reasoning_effort`. The terminal UI's `/effort` offers these values, through the launch document's
   * `capabilities.reasoning_effort`, only when the list is not empty.
   *
   * Each value matches `^[a-z][a-z0-9_-]{0,31}$`, and no value repeats; otherwise {@link runAgentCli} throws at
   * startup. The Python CLI core calls it `supported_reasoning_efforts`.
   *
   * @example
   * ```ts
   * const harness: AgentHarness = {
   *   name: "builtin",
   *   configSection: "builtin",
   *   flowType: "DexAgentsBuiltinCliSession",
   *   defaultModel: "openai/gpt-5.2",
   *   supportedReasoningEfforts: ["low", "medium", "high"],
   *   suggestedModels: ["openai/gpt-5-mini", "anthropic/claude-opus-5-5"],
   *   createSessionFlow: (context) =>
   *     createBuiltinAgentSessionFlow(
   *       {
   *         name: "assistant",
   *         instructions: "Help the user.",
   *         model: modelFor(context.config.model, undefined),
   *         // modelFor returns a client that sets ModelRequest.settings.reasoningEffort when the effort is set.
   *         resolveModel: (settings) => {
   *           const turn = context.resolveTurn(settings);
   *           return modelFor(turn.model, turn.reasoningEffort);
   *         },
   *         toolSources: context.toolSources,
   *       },
   *       context.flowType,
   *     ),
   * };
   * ```
   * @since 0.6.2
   */
  readonly supportedReasoningEfforts?: readonly string[];
  /**
   * Model IDs that the terminal UI's `/model` offers after the configured model and {@link defaultModel}, in
   * order, in the same form as the configuration's `model`. Defaults to none.
   *
   * They are suggestions only: `/model` takes any other ID too, and the turn that uses an unknown ID fails in its
   * model call. Each ID is a non-empty string, and no ID repeats; otherwise {@link runAgentCli} throws at startup.
   * The Python CLI core calls it `suggested_models`.
   *
   * @since 0.6.2
   */
  readonly suggestedModels?: readonly string[];
  /**
   * Builds the session Flow. The CLI core calls it once at startup and registers the Flow in its Worker and Client.
   *
   * Build the Flow with `summaryAttributesIndexed` equal to {@link HarnessContext.isSessionSummaryIndexed}, such as
   * through an adapter factory's `summaryAttributesIndexed` option. The CLI core checks the Flow's persistence schema,
   * which decides the index keys the Worker registers, and its `options`. It refuses to start when the Flow declares
   * the indexed session summary while the file's `sessions.summary` leaves it off, or the other way around. The check
   * verifies what the Flow declares; it does not sandbox harness code.
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
  /**
   * The Flow type to build: {@link AgentHarness.flowType} with the execution environment hash.
   *
   * Changed in 0.6.8: the CLI core calls {@link AgentHarness.createSessionFlow} twice, once with
   * {@link AgentHarness.flowType} itself for new sessions and once with the hashed Flow type for sessions from before
   * 0.6.8. Each call must build a new Flow.
   */
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
   * The tool sources to offer, in order: the connectors, the remote MCP servers of `mcp_servers`, then the local
   * tools the harness does not replace. Each call runs in its own Dex tool Step.
   *
   * Changed in 0.6.4: adds the `mcp_servers` source, whose `sourceName` is `mcp_servers`.
   */
  readonly toolSources: readonly ToolSource[];
  /**
   * The store of the session's images, built from the file's `media` section: the images the user attaches with
   * `/image` and the images connector tools return.
   *
   * Pass it to the adapter, such as `mediaStore: context.mediaStore`, so its runner reads the images from the same
   * store. A runner shows a user message's images only when it implements `buildUserMessageWithImages`; otherwise
   * the terminal chat says the message was not sent.
   *
   * The CLI core always provides it. It is optional only so that harness tests can build a context without one; with
   * `exactOptionalPropertyTypes`, spread it as `...(context.mediaStore === undefined ? {} : { mediaStore: context.mediaStore })`.
   *
   * @since 0.5.0
   */
  readonly mediaStore?: MediaStore;
  /**
   * Whether the session Flow must keep the indexed session summary, from the file's `sessions.summary` and
   * `dex.server`. Pass it as the Flow's `summaryAttributesIndexed`, such as
   * `summaryAttributesIndexed: context.isSessionSummaryIndexed === true`.
   *
   * The CLI core always sets it. It is optional only so that harness tests can build a context without it; absent
   * means `false`.
   *
   * @since 0.6.0
   */
  readonly isSessionSummaryIndexed?: boolean;
  /**
   * Returns one turn's configuration and instructions from the session settings the turn started with.
   *
   * It is pure and cached. Call it from model calls only: settings that fail resolution throw, which fails the
   * model call and then the turn, with the error code and JSON Pointer in `lastTurnError`.
   *
   * @param sessionSettings - The turn's `sessionSettings`, or `{}`.
   * @returns The turn's configuration and its instructions, `null` for the harness default. Since 0.6.2, also the
   * turn's `reasoningEffort`, when its settings set one.
   * @throws {ConfigError} When the settings fail the schema or widen the file, or, since 0.6.2, set a
   * `reasoning_effort` that {@link AgentHarness.supportedReasoningEfforts} does not list.
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
  /**
   * Returns the permissions of a tool Step: this Worker's file configuration narrowed by the settings the
   * Step's turn started with. A harness that replaces local tool groups calls it inside its own tool Step, right
   * before acting, and refuses the call when the group is off, a shell command that ran without approval is no
   * longer allow-listed, or `approval.always` now lists the tool. {@link currentTurn} does not work there, because
   * a tool Step runs outside the model call and maybe on another Worker.
   *
   * @example
   * ```ts
   * execute: async ({ command }, call) => {
   *   const permissions = await context.resolveToolStepPermissions(call); // a built-in loop ToolCallContext
   *   if (!isLocalToolGroupEnabled(permissions, "shell")) {
   *     throw new ToolExecutionError("shell commands are switched off; the command did not run");
   *   }
   *   return runCommand(command);
   * }
   * ```
   * @param step - The Step's `flowId` and `turnId`; a built-in loop `ToolCallContext` has both.
   * @returns The Step's turn configuration on this Worker.
   * @throws {ConfigError} When the turn's settings widen this Worker's file; refuse the call then.
   */
  resolveToolStepPermissions(step: ToolStepTurn): Promise<ResolvedTurn>;
}

/**
 * One turn's configuration, with its instructions read.
 *
 * @since 0.5.0
 */
export interface ResolvedTurn extends TurnConfig {
  /** The turn's instructions: its `system_prompt`, the file's prompt file text, or `null`. */
  readonly instructions: string | null;
  /**
   * The turn's reasoning effort: its session settings' `reasoning_effort`, one of
   * {@link AgentHarness.supportedReasoningEfforts}. Absent when the settings set none, and always absent outside a
   * model call's turn, because the configuration file has no reasoning effort. When it is absent, pass no reasoning
   * effort to the SDK, so the model's own default applies.
   *
   * @example
   * ```ts
   * const turn = context.currentTurn();
   * const modelSettings = turn.reasoningEffort === undefined ? {} : { reasoning: { effort: turn.reasoningEffort } };
   * ```
   * @since 0.6.2
   */
  readonly reasoningEffort?: string;
}

const reasoningEffortPattern = /^[a-z][a-z0-9_-]{0,31}$/;

// The terminal UI's /effort uses it to remove the setting.
const reservedReasoningEffort = "default";

// /model refuses an ID with a space or a control character.
const modelIdForbiddenCharacters = /[\s\p{Cc}]/u;

/**
 * Throws when a harness declares a malformed or repeated reasoning effort or suggested model.
 * @param harness - The command's harness.
 * @throws {Error} Naming the harness and the bad declaration.
 */
export function requireValidHarnessDeclarations(harness: AgentHarness): void {
  const efforts = declaredStrings(harness, "supportedReasoningEfforts");
  for (const effort of efforts) {
    if (!reasoningEffortPattern.test(effort)) {
      throw new Error(
        `the ${harness.name} harness declares the reasoning effort ${JSON.stringify(effort)}, which is not a name matching ${reasoningEffortPattern.source}`,
      );
    }
    if (effort === reservedReasoningEffort) {
      throw new Error(
        `the ${harness.name} harness declares the reasoning effort "default", which the terminal UI reserves for the model's default`,
      );
    }
  }
  if (new Set(efforts).size !== efforts.length) {
    throw new Error(`the ${harness.name} harness declares a reasoning effort twice`);
  }
  const models = declaredStrings(harness, "suggestedModels");
  for (const model of models) {
    if (model === "" || modelIdForbiddenCharacters.test(model)) {
      throw new Error(
        `the ${harness.name} harness suggests the model ${JSON.stringify(model)}, which is not a non-empty ID without spaces or control characters`,
      );
    }
  }
  if (new Set(models).size !== models.length) {
    throw new Error(`the ${harness.name} harness suggests a model twice`);
  }
}

function declaredStrings(
  harness: AgentHarness,
  declaration: "supportedReasoningEfforts" | "suggestedModels",
): readonly string[] {
  const declared: unknown = harness[declaration] ?? [];
  // A bare string would otherwise read as a list of its characters.
  if (!Array.isArray(declared) || !declared.every((value) => typeof value === "string")) {
    throw new Error(`the ${harness.name} harness's ${declaration} is not an array of strings`);
  }
  return declared as readonly string[];
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
