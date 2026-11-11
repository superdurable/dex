// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { realpathSync } from "node:fs";

import {
  createBashTool,
  createEditTool,
  createFindTool,
  createGrepTool,
  createLsTool,
  createReadTool,
  createWriteTool,
} from "@earendil-works/pi-coding-agent";
import {
  ToolExecutionError,
  type AgentTurnContext,
  type DeferredToolCall,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type SourceToolDefinition,
  type ToolEffect,
  type ToolSource,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";
import type { ToolCallContext } from "@superdurable/dex-agents/builtin-loop";
import {
  WorkspaceFiles,
  isLocalToolGroupEnabled,
  isShellCommandApprovalRequired,
  resolveTurnToolPolicy,
  type HarnessContext,
  type LocalToolGroup,
  type TurnConfig,
} from "@superdurable/dex-agents-cli";
import { piAgentFunctionTool, type PiAgentToolLike } from "@superdurable/dex-agents-pi";

import { PiToolCallOperations } from "./pi-tool-call-operations.js";

/**
 * How the command runs one of pi-coding-agent's tools: the local tool group that switches it, its effect, and
 * whether every call waits for approval.
 *
 * @since 0.5.0
 */
export interface PiCodingLocalTool {
  /** The `local_tools` group that switches the tool on and off. */
  readonly group: LocalToolGroup;
  /** The tool's effect, a floor that a session policy may raise. */
  readonly effect: ToolEffect;
  /** Whether every call waits for approval; `bash` waits unless the allow list has the command. */
  readonly isApprovalRequired: boolean;
}

/**
 * pi-coding-agent's tools that replace the runtime's `files` and `shell` groups, by their Pi names, in the order
 * the command offers them.
 *
 * @since 0.5.0
 */
export const piCodingLocalTools: Readonly<Record<string, PiCodingLocalTool>> = Object.freeze({
  read: { group: "files", effect: "read", isApprovalRequired: false },
  grep: { group: "files", effect: "read", isApprovalRequired: false },
  find: { group: "files", effect: "read", isApprovalRequired: false },
  ls: { group: "files", effect: "read", isApprovalRequired: false },
  write: { group: "files", effect: "non_idempotent_write", isApprovalRequired: true },
  edit: { group: "files", effect: "non_idempotent_write", isApprovalRequired: true },
  bash: { group: "shell", effect: "non_idempotent_write", isApprovalRequired: true },
});

/**
 * Seconds a `bash` command runs before it is killed when the model passes no `timeout`: the runtime shell tool's
 * default.
 *
 * @since 0.5.0
 */
export const defaultBashTimeoutSeconds = 120;

/**
 * The largest `bash` timeout in seconds that pi-coding-agent accepts.
 *
 * @since 0.5.0
 */
export const maximumBashTimeoutSeconds = 2_147_483;

const refusalReferenceKey = "refused";

/**
 * pi-coding-agent's `read`, `grep`, `find`, `ls`, `write`, `edit`, and `bash` tools as a {@link ToolSource}, so each
 * call runs in its own Dex tool Step with the CLI runtime's local tool rules.
 *
 * - Each model call lists the tools whose group the running turn switches on.
 * - Reads run at once. `write` and `edit` wait for approval on every call. A `bash` command waits unless the turn's
 *   shell allow list has its exact text. `approval.always` adds approval by tool name.
 * - In its tool Step, a call is checked again with the executing Worker's configuration through
 *   `HarnessContext.resolveToolStepPermissions`. A call refused there records `failed` and does not run.
 * - Every file access of the file tools goes through the runtime's `WorkspaceFiles`, with the path Pi finally
 *   opens, so the runtime's workspace boundary applies: no path outside the workspace, no final symbolic link
 *   followed, no file with more than one hard link, and writes by temporary file and rename. `edit` fails with a
 *   conflict when the file changed after it read it. `find` walks the workspace through it instead of running
 *   `fd`. `grep` checks its search directory through it, then runs `rg`.
 * - A file tool's failure records `failed`, and the model sees why: writes replace the file by rename, so a failed
 *   write leaves it unchanged.
 * - A `bash` call that fails before its command starts records `failed`. A failure after that records
 *   `outcome_unknown`, and the call never runs again, except that a command killed by its timeout records `failed`
 *   with its output. A call without `timeout` gets the source's default timeout.
 *
 * The working directory is the workspace. Register the source even when the file switches both groups off, so it
 * refuses calls that another process requested.
 *
 * @since 0.5.0
 */
export class PiCodingToolSource implements ToolSource {
  /** The stable source name stored with each call. */
  public readonly sourceName = "pi_coding_tools";
  private readonly workspace: string;
  private readonly files: WorkspaceFiles;
  private readonly definitions: readonly SourceToolDefinition[];

  /**
   * Creates the source.
   *
   * @param context - The harness context: the workspace, the per-turn configuration, and the tool Step permissions.
   * @param bashTimeoutSeconds - Seconds a `bash` command runs when the model passes no `timeout`; defaults to
   * {@link defaultBashTimeoutSeconds}.
   * @throws {RangeError} When `bashTimeoutSeconds` is not a whole number from 1 to {@link maximumBashTimeoutSeconds}.
   */
  public constructor(
    private readonly context: HarnessContext,
    private readonly bashTimeoutSeconds: number = defaultBashTimeoutSeconds,
  ) {
    if (!Number.isSafeInteger(bashTimeoutSeconds) || bashTimeoutSeconds < 1 || bashTimeoutSeconds > maximumBashTimeoutSeconds) {
      throw new RangeError(`bashTimeoutSeconds must be a whole number from 1 to ${maximumBashTimeoutSeconds}`);
    }
    this.workspace = realpathSync.native(context.workspace);
    this.files = new WorkspaceFiles(this.workspace);
    this.definitions = Object.keys(piCodingLocalTools).map((toolName) => {
      const tool = this.createPiTool(toolName, new PiToolCallOperations(this.files));
      const parameters = tool.parameters as Record<string, unknown>;
      return {
        name: tool.name,
        description: tool.description,
        inputSchema: toolName === "bash" ? this.describeBashTimeout(parameters) : parameters,
      };
    });
  }

  /**
   * Lists the tools of the groups that the running turn switches on.
   *
   * @returns The tools.
   */
  public listTools(): Promise<readonly SourceToolDefinition[]> {
    return Promise.resolve(this.offeredTools(this.context.currentTurn()));
  }

  /**
   * Returns the tools the running turn, or the file, switches on, without I/O.
   *
   * @returns The tools.
   */
  public cachedTools(): readonly SourceToolDefinition[] {
    return this.offeredTools(this.context.currentTurn());
  }

  /**
   * Builds the deferred call with the tool's policy, the shell allow list, and `approval.always`. A call of a
   * switched-off tool is refused without approval.
   *
   * @param turn - The `RunModel` context.
   * @param call - The model's call.
   * @returns The deferred call.
   */
  public deferredToolCall(
    turn: AgentTurnContext,
    call: { readonly toolCallId: string; readonly toolName: string; readonly arguments: string },
  ): DeferredToolCall {
    const turnConfig = this.context.resolveTurn(turn.sessionSettings ?? {});
    const tool = Object.hasOwn(piCodingLocalTools, call.toolName) ? piCodingLocalTools[call.toolName] : undefined;
    if (tool === undefined || !isLocalToolGroupEnabled(turnConfig, tool.group)) {
      return {
        toolCallId: call.toolCallId,
        toolName: call.toolName,
        arguments: call.arguments,
        policy: { effect: "read", requiresApproval: false },
        toolSourceReference: JSON.stringify({ [refusalReferenceKey]: `${call.toolName} is switched off for this session` }),
      };
    }
    let isApprovalRequired = tool.isApprovalRequired;
    if (tool.group === "shell") {
      const command = readArguments(call.arguments)?.["command"];
      isApprovalRequired = typeof command !== "string" || isShellCommandApprovalRequired(turnConfig, command);
    }
    return {
      toolCallId: call.toolCallId,
      toolName: call.toolName,
      arguments: call.arguments,
      policy: resolveTurnToolPolicy(turn, call.toolName, tool.effect, isApprovalRequired, turnConfig),
    };
  }

  /**
   * Runs one call in its tool Step, after checking it again with this Worker's configuration.
   *
   * @param call - The approved call.
   * @returns The tool's output.
   * @throws {ToolExecutionError} When the call is refused, its arguments are invalid, or the tool fails with a known
   * outcome.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    const { toolName, toolSourceReference } = call.toolCall;
    const recordedRefusal = readRecordedRefusal(toolSourceReference);
    if (recordedRefusal !== null) {
      throw new ToolExecutionError(`${recordedRefusal}; the call did not run`);
    }
    const tool = Object.hasOwn(piCodingLocalTools, toolName) ? piCodingLocalTools[toolName] : undefined;
    if (tool === undefined) {
      throw new ToolExecutionError(`unknown Pi tool ${toolName}; the call did not run`);
    }
    const toolArguments = parseArguments(call.toolCall.arguments);
    await this.authorizeOnThisWorker(call, tool, toolArguments);
    const operations = new PiToolCallOperations(this.files);
    // The recorded effect picked the tool Step, so it also decides how a failure is recorded.
    const functionTool = piAgentFunctionTool(
      definiteFailuresBeforeEffect(this.createPiTool(toolName, operations), operations),
      { effect: call.toolCall.effect },
    );
    const context: ToolCallContext = {
      flowId: call.flowId,
      turnId: call.toolCall.turnId,
      toolCallId: call.toolCall.toolCallId,
      idempotencyKey: call.idempotencyKey,
      attempt: call.attempt,
      events: call.events,
      cancellationSignal: call.cancellationSignal,
      deadline: call.deadline,
      inputAnswers: call.inputAnswers ?? [],
      inputContinuation: call.toolCall.inputContinuation ?? null,
      heartbeat: () => call.heartbeat(),
    };
    const executedArguments =
      toolName === "bash" && toolArguments["timeout"] === undefined
        ? { ...toolArguments, timeout: this.bashTimeoutSeconds }
        : toolArguments;
    const output = await functionTool.execute(executedArguments, context);
    return { output: typeof output === "string" ? output : JSON.stringify(output) };
  }

  /**
   * Pi tools start no tasks.
   *
   * @returns Never.
   * @throws {ToolExecutionError} Always.
   */
  public pollToolTask(_poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    return Promise.reject(new ToolExecutionError("Pi tools start no tasks"));
  }

  /** Replaces Pi's "no default timeout" text, since this source applies one. */
  private describeBashTimeout(parameters: Record<string, unknown>): Record<string, unknown> {
    const properties = parameters["properties"] as Record<string, Record<string, unknown>>;
    return {
      ...parameters,
      properties: {
        ...properties,
        timeout: {
          ...properties["timeout"],
          description: `Timeout in seconds; defaults to ${this.bashTimeoutSeconds}`,
        },
      },
    };
  }

  private offeredTools(turn: TurnConfig): readonly SourceToolDefinition[] {
    return this.definitions.filter((definition) => {
      const tool = piCodingLocalTools[definition.name];
      return tool !== undefined && isLocalToolGroupEnabled(turn, tool.group);
    });
  }

  /** Checks the call with this Worker's file, because another process may have requested it. */
  private async authorizeOnThisWorker(
    call: DeferredToolExecution,
    tool: PiCodingLocalTool,
    toolArguments: Readonly<Record<string, unknown>>,
  ): Promise<void> {
    const { toolName } = call.toolCall;
    let permissions: TurnConfig;
    try {
      permissions = await this.context.resolveToolStepPermissions({ flowId: call.flowId, turnId: call.toolCall.turnId });
    } catch (error: unknown) {
      throw new ToolExecutionError(`${toolName} did not run: the session's settings are rejected here: ${describe(error)}`, {
        cause: error,
      });
    }
    if (!isLocalToolGroupEnabled(permissions, tool.group)) {
      throw new ToolExecutionError(`${toolName} is switched off in the configuration of the Worker that ran it; it did not run`);
    }
    if (call.toolCall.requiresApproval) {
      return;
    }
    if (permissions.approval.always.includes(toolName)) {
      throw new ToolExecutionError(`${toolName} now needs approval, and this call had none; it did not run`);
    }
    const command = toolArguments["command"];
    if (tool.group === "shell" && (typeof command !== "string" || isShellCommandApprovalRequired(permissions, command))) {
      throw new ToolExecutionError(
        "the command is not on the shell allow list of the Worker that ran it, and the call had no approval; it did not run",
      );
    }
  }

  /** Creates a pi-coding-agent tool in the workspace whose file and shell access goes through `operations`. */
  private createPiTool(toolName: string, operations: PiToolCallOperations): PiAgentToolLike {
    const workspace = this.workspace;
    switch (toolName) {
      case "read":
        return createReadTool(workspace, {
          operations: {
            access: (absolutePath) => operations.requireRegularFile(absolutePath),
            readFile: (absolutePath) => operations.readFileBytes(absolutePath),
            detectImageMimeType: (absolutePath) => operations.detectImageMimeType(absolutePath),
          },
        });
      case "grep":
        return createGrepTool(workspace, { operations: { isDirectory: (absolutePath) => operations.isDirectory(absolutePath) } });
      case "find":
        return createFindTool(workspace, {
          operations: {
            exists: (absolutePath) => operations.requireExisting(absolutePath),
            glob: (pattern, searchPath, options) => operations.findMatchingPaths(pattern, searchPath, options.limit),
          },
        });
      case "ls":
        return createLsTool(workspace, {
          operations: {
            exists: (absolutePath) => operations.requireExisting(absolutePath),
            stat: (absolutePath) => operations.describeDirectoryOrEntry(absolutePath),
            readdir: (absolutePath) => operations.readDirectoryNames(absolutePath),
          },
        });
      case "write":
        return createWriteTool(workspace, {
          operations: {
            // WorkspaceFiles.writeFile creates the missing directories inside the workspace itself.
            mkdir: () => Promise.resolve(),
            writeFile: (absolutePath, content) => operations.writeWholeFile(absolutePath, content),
          },
        });
      case "edit":
        return createEditTool(workspace, {
          operations: {
            access: (absolutePath) => operations.requireRegularFile(absolutePath),
            readFile: (absolutePath) => operations.readFileBytes(absolutePath),
            writeFile: (absolutePath, content) => operations.replaceReadFile(absolutePath, content),
          },
        });
      case "bash":
        return createBashTool(workspace, {
          operations: { exec: (command, cwd, options) => operations.executeCommand(command, cwd, options) },
        });
      default:
        throw new ToolExecutionError(`unknown Pi tool ${toolName}`);
    }
  }
}

/** Makes an error with a known outcome a failure the model sees: any before the command started, or a timeout kill. */
function definiteFailuresBeforeEffect(tool: PiAgentToolLike, operations: PiToolCallOperations): PiAgentToolLike {
  return {
    ...tool,
    execute: async (toolCallId, params, signal, onUpdate) => {
      try {
        return await tool.execute(toolCallId, params, signal, onUpdate);
      } catch (error: unknown) {
        const isOutcomeUnknown = operations.hasEffectStarted && !operations.hasTimedOut;
        if (isOutcomeUnknown || signal?.aborted === true || error instanceof ToolExecutionError) {
          throw error;
        }
        throw new ToolExecutionError(describe(error), { cause: error });
      }
    },
  };
}

function readRecordedRefusal(toolSourceReference: string | null | undefined): string | null {
  if (toolSourceReference === undefined || toolSourceReference === null) {
    return null;
  }
  const reference = JSON.parse(toolSourceReference) as Record<string, unknown>;
  const refusal = reference[refusalReferenceKey];
  return typeof refusal === "string" ? refusal : null;
}

function readArguments(argumentsText: string): Readonly<Record<string, unknown>> | undefined {
  try {
    const value: unknown = JSON.parse(argumentsText);
    return typeof value === "object" && value !== null && !Array.isArray(value) ? (value as Record<string, unknown>) : undefined;
  } catch {
    return undefined;
  }
}

function parseArguments(argumentsText: string): Readonly<Record<string, unknown>> {
  const toolArguments = readArguments(argumentsText);
  if (toolArguments === undefined) {
    throw new ToolExecutionError("the arguments must be a JSON object; the call did not run");
  }
  return toolArguments;
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
