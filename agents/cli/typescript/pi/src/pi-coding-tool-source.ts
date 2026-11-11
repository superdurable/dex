// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { access, mkdir, readFile, writeFile } from "node:fs/promises";
import { constants } from "node:fs";

import {
  createBashTool,
  createEditTool,
  createFindTool,
  createGrepTool,
  createLocalBashOperations,
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
  isLocalToolGroupEnabled,
  isShellCommandApprovalRequired,
  resolveTurnToolPolicy,
  type HarnessContext,
  type LocalToolGroup,
  type TurnConfig,
} from "@superdurable/dex-agents-cli";
import { piAgentFunctionTool, type PiAgentToolLike } from "@superdurable/dex-agents-pi";

import { WorkspacePathBoundary } from "./workspace-path-boundary.js";

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
  /** Whether the tool takes a `path` argument that must stay inside the workspace. */
  readonly hasPathArgument: boolean;
}

/**
 * pi-coding-agent's tools that replace the runtime's `files` and `shell` groups, by their Pi names, in the order
 * the command offers them.
 *
 * @since 0.5.0
 */
export const piCodingLocalTools: Readonly<Record<string, PiCodingLocalTool>> = Object.freeze({
  read: { group: "files", effect: "read", isApprovalRequired: false, hasPathArgument: true },
  grep: { group: "files", effect: "read", isApprovalRequired: false, hasPathArgument: true },
  find: { group: "files", effect: "read", isApprovalRequired: false, hasPathArgument: true },
  ls: { group: "files", effect: "read", isApprovalRequired: false, hasPathArgument: true },
  write: { group: "files", effect: "non_idempotent_write", isApprovalRequired: true, hasPathArgument: true },
  edit: { group: "files", effect: "non_idempotent_write", isApprovalRequired: true, hasPathArgument: true },
  bash: { group: "shell", effect: "non_idempotent_write", isApprovalRequired: true, hasPathArgument: false },
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
/** Prefix of the error that pi-coding-agent's shell execution throws when its timeout kills the command. */
const shellTimeoutErrorPrefix = "timeout:";

/**
 * pi-coding-agent's `read`, `grep`, `find`, `ls`, `write`, `edit`, and `bash` tools as a {@link ToolSource}, so each
 * call runs in its own Dex tool Step with the CLI runtime's local tool rules.
 *
 * - Each model call lists the tools whose group the running turn switches on.
 * - Reads run at once. `write` and `edit` wait for approval on every call. A `bash` command waits unless the turn's
 *   shell allow list has its exact text. `approval.always` adds approval by tool name.
 * - File tools refuse a `path` outside the workspace before asking for approval, and again in the tool Step.
 *   `write` and `edit` also check the exact file Pi opens.
 * - In its tool Step, a call is checked again with the executing Worker's configuration through
 *   `HarnessContext.resolveToolStepPermissions`. A call refused there records `failed` and does not run.
 * - A `write`, `edit`, or `bash` call that fails before it writes or starts its command records `failed`, and the
 *   model sees why. A failure after that records `outcome_unknown`, and the call never runs again.
 * - A `bash` call without `timeout` gets the source's default timeout. A command killed by its timeout records
 *   `failed`, and the model sees its output and the timeout.
 *
 * The working directory is the workspace. Register the source even when the file switches both groups off, so it
 * refuses calls that another process requested.
 *
 * @since 0.5.0
 */
export class PiCodingToolSource implements ToolSource {
  /** The stable source name stored with each call. */
  public readonly sourceName = "pi_coding_tools";
  private readonly boundary: WorkspacePathBoundary;
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
    this.boundary = new WorkspacePathBoundary(context.workspace);
    this.definitions = Object.keys(piCodingLocalTools).map((toolName) => {
      const tool = this.createPiTool(toolName, new PiToolCallOperations(this.boundary, toolName));
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
   * switched-off tool, or with a path outside the workspace, is refused without approval.
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
    const refusal =
      tool === undefined || !isLocalToolGroupEnabled(turnConfig, tool.group)
        ? `${call.toolName} is switched off for this session`
        : this.pathRefusal(call.toolName, tool, readArguments(call.arguments));
    if (tool === undefined || refusal !== null) {
      return {
        toolCallId: call.toolCallId,
        toolName: call.toolName,
        arguments: call.arguments,
        policy: { effect: "read", requiresApproval: false },
        toolSourceReference: JSON.stringify({ [refusalReferenceKey]: refusal }),
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
   * @throws {ToolExecutionError} When the call is refused, its arguments are invalid, or the tool fails before its
   * side effect.
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
    const pathRefusal = this.pathRefusal(toolName, tool, toolArguments);
    if (pathRefusal !== null) {
      throw new ToolExecutionError(`${pathRefusal}; the call did not run`);
    }
    const operations = new PiToolCallOperations(this.boundary, toolName);
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

  private pathRefusal(
    toolName: string,
    tool: PiCodingLocalTool,
    toolArguments: Readonly<Record<string, unknown>> | undefined,
  ): string | null {
    if (!tool.hasPathArgument || toolArguments === undefined) {
      return null;
    }
    return this.boundary.refusalReason(toolName, toolArguments["path"]);
  }

  /** Creates a pi-coding-agent tool in the workspace, with the call's guarded operations. */
  private createPiTool(toolName: string, operations: PiToolCallOperations): PiAgentToolLike {
    const workspace = this.boundary.workspace;
    switch (toolName) {
      case "read":
        return createReadTool(workspace);
      case "grep":
        return createGrepTool(workspace);
      case "find":
        return createFindTool(workspace);
      case "ls":
        return createLsTool(workspace);
      case "write":
        return createWriteTool(workspace, {
          operations: {
            mkdir: (directory) => operations.makeDirectory(directory),
            writeFile: (absolutePath, content) => operations.writeTextFile(absolutePath, content),
          },
        });
      case "edit":
        return createEditTool(workspace, {
          operations: {
            access: (absolutePath) => operations.checkReadWriteAccess(absolutePath),
            readFile: (absolutePath) => operations.readFileBytes(absolutePath),
            writeFile: (absolutePath, content) => operations.writeTextFile(absolutePath, content),
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

type BashExecution = ReturnType<typeof createLocalBashOperations>;

/** One call's file and shell operations: paths stay inside the workspace, and the first effect is noted. */
class PiToolCallOperations {
  /** Whether the call wrote a file or started its command. */
  public hasEffectStarted = false;
  /** Whether the command's timeout killed it, so its outcome is known. */
  public hasTimedOut = false;
  private readonly localBash: BashExecution = createLocalBashOperations();

  public constructor(
    private readonly boundary: WorkspacePathBoundary,
    private readonly toolName: string,
  ) {}

  public async makeDirectory(directory: string): Promise<void> {
    this.requireInside(directory);
    await mkdir(directory, { recursive: true });
  }

  public async writeTextFile(absolutePath: string, content: string): Promise<void> {
    this.requireInside(absolutePath);
    this.hasEffectStarted = true;
    await writeFile(absolutePath, content, "utf-8");
  }

  public async checkReadWriteAccess(absolutePath: string): Promise<void> {
    this.requireInside(absolutePath);
    await access(absolutePath, constants.R_OK | constants.W_OK);
  }

  public async readFileBytes(absolutePath: string): Promise<Buffer> {
    this.requireInside(absolutePath);
    return readFile(absolutePath);
  }

  public async executeCommand(...execution: Parameters<BashExecution["exec"]>): ReturnType<BashExecution["exec"]> {
    this.hasEffectStarted = true;
    try {
      return await this.localBash.exec(...execution);
    } catch (error: unknown) {
      this.hasTimedOut = error instanceof Error && error.message.startsWith(shellTimeoutErrorPrefix);
      throw error;
    }
  }

  private requireInside(absolutePath: string): void {
    if (!this.boundary.isInside(absolutePath)) {
      throw new ToolExecutionError(this.boundary.outsideMessage(this.toolName));
    }
  }
}

/** Makes an error before the call's first effect, or a timeout kill, a failure the model sees. */
function definiteFailuresBeforeEffect(tool: PiAgentToolLike, operations: PiToolCallOperations): PiAgentToolLike {
  return {
    ...tool,
    execute: async (toolCallId, params, signal, onUpdate) => {
      try {
        return await tool.execute(toolCallId, params, signal, onUpdate);
      } catch (error: unknown) {
        // After the first effect the outcome is unknown, unless the timeout killed the command.
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
