// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { AsyncLocalStorage } from "node:async_hooks";
import { lstatSync, readlinkSync, realpathSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, isAbsolute, join, relative, resolve, sep } from "node:path";

import {
  createAgentSessionFlow,
  ToolExecutionError,
  type AgentSessionFlow,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolExecution,
  type ToolEffect,
  type ToolExecutionResult,
  type ToolPolicy,
} from "@superdurable/dex-agents";
import {
  ConfigError,
  isLocalToolGroupEnabled,
  isShellCommandApprovalRequired,
  resolveTurnToolPolicy,
  type AgentHarness,
  type HarnessContext,
  type LocalToolGroup,
  type TurnConfig,
} from "@superdurable/dex-agents-cli";
import {
  ClaudeAgentRunner,
  ClaudeCodeToolDenial,
  type ClaudeAgentQueryOptions,
  type ClaudeAgentRunnerOptions,
} from "@superdurable/dex-agents-claude-agent-sdk";

/**
 * The base Flow type of the command's sessions, before the runtime namespaces it.
 *
 * @since 0.5.0
 */
export const claudeSdkCliFlowType = "DexAgentsClaudeSdkCliSession";

/**
 * The model the command uses when the configuration file sets none.
 *
 * @since 0.5.0
 */
export const defaultClaudeModel = "claude-opus-5-5";

/**
 * The instructions the agent gets when the configuration sets none.
 *
 * @since 0.5.0
 */
export const defaultInstructions =
  "You are a helpful assistant. Use your tools to work with the files and commands in your working directory and " +
  "with the connected services.";

/**
 * How the command treats one of Claude Code's own tools: the local tool group that switches it, its effect, whether
 * every call waits for approval, and the input field that names a path.
 *
 * @since 0.5.0
 */
export interface ClaudeCodeLocalTool {
  /** The `local_tools` group that switches the tool on and off. */
  readonly group: LocalToolGroup;
  /** The tool's effect. */
  readonly effect: ToolEffect;
  /** Whether every call waits for approval; `Bash` waits unless the allow list has the command. */
  readonly isApprovalRequired: boolean;
  /** The input field that names a path the tool reads or writes, or `null`. */
  readonly pathArgument: string | null;
}

/**
 * Claude Code's tools that replace the runtime's `files`, `shell`, and `web_fetch` groups, in the order the
 * command offers them.
 *
 * @since 0.5.0
 */
export const claudeCodeLocalTools: Readonly<Record<string, ClaudeCodeLocalTool>> = Object.freeze({
  Read: { group: "files", effect: "read", isApprovalRequired: false, pathArgument: "file_path" },
  Grep: { group: "files", effect: "read", isApprovalRequired: false, pathArgument: "path" },
  Glob: { group: "files", effect: "read", isApprovalRequired: false, pathArgument: "path" },
  Write: { group: "files", effect: "idempotent_write", isApprovalRequired: true, pathArgument: "file_path" },
  Edit: { group: "files", effect: "non_idempotent_write", isApprovalRequired: true, pathArgument: "file_path" },
  Bash: { group: "shell", effect: "non_idempotent_write", isApprovalRequired: true, pathArgument: null },
  WebFetch: { group: "web_fetch", effect: "read", isApprovalRequired: false, pathArgument: null },
});

const runningTurn = new AsyncLocalStorage<AgentTurnContext>();
const globCharacters = /[*?[{]/;
const maximumSymbolicLinkHops = 40;

/**
 * The `dex-agents-claude-sdk` command: a Claude Agent SDK agent in the terminal.
 *
 * Claude Code's own Read, Grep, Glob, Write, Edit, Bash, and WebFetch tools replace the runtime's `files`, `shell`,
 * and `web_fetch` groups; the runtime's browser tools and connector tools stay. Each turn offers only the Claude
 * Code tools whose group the turn switches on, with the workspace as the working directory. Reads run at once,
 * Write, Edit, and Bash wait for approval, and a Bash command whose whole text is in the shell allow list runs at
 * once. File tools refuse paths outside the workspace. The `claude` section has no options yet; any key in it is a
 * configuration error.
 *
 * @example
 * ```ts
 * import { processAgentCliIo, runAgentCli } from "@superdurable/dex-agents-cli";
 * import { claudeSdkHarness } from "@superdurable/dex-agents-claude-sdk-cli";
 *
 * process.exit(await runAgentCli(claudeSdkHarness, processAgentCliIo()));
 * ```
 * @since 0.5.0
 */
export const claudeSdkHarness: AgentHarness = {
  name: "claude-sdk",
  configSection: "claude",
  flowType: claudeSdkCliFlowType,
  defaultModel: defaultClaudeModel,
  replacedLocalTools: ["files", "shell", "web_fetch"],
  createSessionFlow: (context) => createClaudeSdkSessionFlow(context),
};

/**
 * Builds the Claude agent session Flow from the effective configuration.
 *
 * @param context - The configuration, workspace, and tool sources.
 * @returns The Flow, with `context.flowType` as its type.
 * @throws {ConfigError} When the `claude` section has a key.
 * @since 0.5.0
 */
export function createClaudeSdkSessionFlow(context: HarnessContext): AgentSessionFlow<ClaudeAgentRunner> {
  validateClaudeSection(context.harnessOptions);
  const localTools = new ClaudeCodeLocalTools(context);
  const runner = new ReauthorizingClaudeAgentRunner(context, localTools, (_options) => localTools.buildOptions(), {
    toolSources: context.toolSources,
    claudeCodeToolPermission: (turn, toolName, toolInput) => localTools.decideToolCall(turn, toolName, toolInput),
  });
  return createAgentSessionFlow(runner, context.flowType);
}

/**
 * Rejects every key of the `claude` section, which has no options yet.
 *
 * @param options - The section, `{}` when the file omits it.
 * @throws {ConfigError} A `schema` error with the first key's JSON Pointer.
 * @since 0.5.0
 */
export function validateClaudeSection(options: Readonly<Record<string, unknown>>): void {
  const [firstKey] = Object.keys(options).sort();
  if (firstKey !== undefined) {
    const escaped = firstKey.replaceAll("~", "~0").replaceAll("/", "~1");
    throw new ConfigError("schema", `/claude/${escaped}`, "additionalProperties");
  }
}

/**
 * Applies the runtime's local tool rules to Claude Code's own tools.
 *
 * @since 0.5.0
 */
export class ClaudeCodeLocalTools {
  private readonly workspace: string;

  /**
   * Binds the rules to one command's configuration and workspace.
   *
   * @param context - The harness context.
   */
  public constructor(private readonly context: HarnessContext) {
    this.workspace = realpathSync.native(context.workspace);
  }

  /**
   * Returns one turn segment's options: the turn's model, instructions, and tools.
   *
   * The environment passes through unchanged, so `ANTHROPIC_API_KEY` or the machine's Claude Code login reaches
   * the CLI.
   *
   * @returns Options with only the Claude Code tools the turn switches on, the workspace as `cwd`, and no settings
   * files.
   */
  public buildOptions(): ClaudeAgentQueryOptions {
    const turn = this.context.currentTurn();
    // A resumed CLI must find a recorded call's tool to replay it; new calls stay denied.
    const recordedToolNames = new Set((runningTurn.getStore()?.toolCalls ?? []).map((record) => record.toolName));
    return {
      model: turn.model,
      systemPrompt: turn.instructions ?? defaultInstructions,
      tools: Object.entries(claudeCodeLocalTools)
        .filter(([toolName, tool]) => isLocalToolGroupEnabled(turn, tool.group) || recordedToolNames.has(toolName))
        .map(([toolName]) => toolName),
      cwd: this.context.workspace,
      // Settings files could allow tools before the session's policy sees them.
      settingSources: [],
    };
  }

  /**
   * Decides one call of a Claude Code tool in the model call.
   *
   * @param turn - The `RunModel` context.
   * @param toolName - The tool's Claude Code name.
   * @param toolInput - The call's input.
   * @returns The call's policy, or a denial for a switched-off tool or a path outside the workspace.
   */
  public decideToolCall(
    turn: AgentTurnContext,
    toolName: string,
    toolInput: Readonly<Record<string, unknown>>,
  ): ToolPolicy | ClaudeCodeToolDenial {
    const turnConfig = this.context.resolveTurn(turn.sessionSettings ?? {});
    const tool = Object.hasOwn(claudeCodeLocalTools, toolName) ? claudeCodeLocalTools[toolName] : undefined;
    if (tool === undefined) {
      return new ClaudeCodeToolDenial(`${toolName} is not available in this session`);
    }
    const refusal = this.refusalReason(turnConfig, toolName, toolInput);
    if (refusal !== null) {
      return new ClaudeCodeToolDenial(refusal);
    }
    let isApprovalRequired = tool.isApprovalRequired;
    if (toolName === "Bash") {
      const command = typeof toolInput["command"] === "string" ? toolInput["command"] : "";
      // A background command outlives the turn, so it always waits for approval.
      isApprovalRequired =
        isShellCommandApprovalRequired(turnConfig, command) || toolInput["run_in_background"] === true;
    }
    return resolveTurnToolPolicy(turn, toolName, tool.effect, isApprovalRequired, turnConfig);
  }

  /**
   * Returns why a call of a Claude Code tool must not run, or `null`.
   *
   * @param turnConfig - The configuration of the call's turn.
   * @param toolName - The tool's Claude Code name.
   * @param toolInput - The call's input.
   * @returns The reason when the turn switches the tool's group off or a file tool names a path outside the
   * workspace; `null` otherwise.
   */
  public refusalReason(
    turnConfig: TurnConfig,
    toolName: string,
    toolInput: Readonly<Record<string, unknown>>,
  ): string | null {
    const tool = Object.hasOwn(claudeCodeLocalTools, toolName) ? claudeCodeLocalTools[toolName] : undefined;
    if (tool === undefined) {
      return null;
    }
    if (!isLocalToolGroupEnabled(turnConfig, tool.group)) {
      return `${toolName} is switched off for this session`;
    }
    const paths: string[] = [];
    const pathValue = tool.pathArgument === null ? undefined : toolInput[tool.pathArgument];
    if (typeof pathValue === "string") {
      paths.push(pathValue);
    }
    const patternArgument = toolName === "Glob" ? "pattern" : toolName === "Grep" ? "glob" : undefined;
    const pattern = patternArgument === undefined ? undefined : toolInput[patternArgument];
    if (typeof pattern === "string") {
      // Brace expansion can produce .. from any part of a pattern, so none may contain it.
      if (pattern.includes("..")) {
        return `${toolName} may only use paths inside the workspace ${this.workspace}`;
      }
      paths.push(globBase(pattern));
    }
    for (const path of paths) {
      if (!this.isInsideWorkspace(path)) {
        return `${toolName} may only use paths inside the workspace ${this.workspace}`;
      }
    }
    return null;
  }

  private isInsideWorkspace(pathText: string): boolean {
    if (pathText.split(/[\\/]/).includes("..")) {
      return false;
    }
    const expanded = pathText === "~" || pathText.startsWith("~/") ? join(homedir(), pathText.slice(1)) : pathText;
    const candidate = isAbsolute(expanded) ? expanded : join(this.workspace, expanded);
    const resolved = resolveSymbolicLinks(candidate);
    if (resolved === null) {
      return false;
    }
    const fromWorkspace = relative(this.workspace, resolved);
    const isOutside = fromWorkspace === ".." || fromWorkspace.startsWith(`..${sep}`) || isAbsolute(fromWorkspace);
    return !isOutside;
  }
}

/** Gives the options their turn, and checks an approved Claude Code tool call in its tool Step. */
class ReauthorizingClaudeAgentRunner extends ClaudeAgentRunner {
  public constructor(
    private readonly context: HarnessContext,
    private readonly localTools: ClaudeCodeLocalTools,
    options: ConstructorParameters<typeof ClaudeAgentRunner>[0],
    runnerOptions: ClaudeAgentRunnerOptions,
  ) {
    super(options, runnerOptions);
  }

  public override async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    return runningTurn.run(turn, () => super.runModelStep(turn));
  }

  public override async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const toolName = call.toolCall.toolName;
    if (Object.hasOwn(claudeCodeLocalTools, toolName)) {
      const permissions = await this.context.resolveToolStepPermissions({
        flowId: call.flowId,
        turnId: call.toolCall.turnId,
      });
      const refusal = this.localTools.refusalReason(permissions, toolName, parseToolInput(call.toolCall.arguments));
      if (refusal !== null) {
        throw new ToolExecutionError(`${refusal}; the call did not run`);
      }
    }
    return super.executeDeferredToolCall(call);
  }
}

/** Returns the leading directories of a glob pattern, before its first glob character. */
function globBase(pattern: string): string {
  const parts: string[] = [];
  for (const part of pattern.split("/")) {
    if (globCharacters.test(part)) {
      break;
    }
    parts.push(part);
  }
  const base = parts.join("/");
  return base === "" ? (pattern.startsWith("/") ? "/" : ".") : base;
}

/**
 * Resolves every symbolic link of an absolute path, one component at a time, as the kernel would.
 *
 * A dangling link resolves to the target it would create, so a write through it is checked where it lands. Names
 * after the first missing component cannot be links and stay as they are.
 *
 * @param path - The absolute path.
 * @returns The resolved path, or `null` after more than {@link maximumSymbolicLinkHops} links.
 */
function resolveSymbolicLinks(path: string): string | null {
  let pending = path.split(sep).filter((name) => name !== "");
  let current: string = sep;
  let hops = 0;
  while (pending.length > 0) {
    const [name, ...rest] = pending as [string, ...string[]];
    pending = rest;
    if (name === ".") {
      continue;
    }
    if (name === "..") {
      current = dirname(current);
      continue;
    }
    const candidate = join(current, name);
    let isSymbolicLink: boolean;
    try {
      isSymbolicLink = lstatSync(candidate).isSymbolicLink();
    } catch {
      return resolve(candidate, ...pending);
    }
    if (!isSymbolicLink) {
      current = candidate;
      continue;
    }
    hops += 1;
    if (hops > maximumSymbolicLinkHops) {
      return null;
    }
    const target = readlinkSync(candidate);
    if (isAbsolute(target)) {
      current = sep;
    }
    pending = [...target.split(sep).filter((targetName) => targetName !== ""), ...pending];
  }
  return current;
}

function parseToolInput(text: string): Readonly<Record<string, unknown>> {
  const parsed: unknown = JSON.parse(text);
  return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed)
    ? (parsed as Record<string, unknown>)
    : {};
}
