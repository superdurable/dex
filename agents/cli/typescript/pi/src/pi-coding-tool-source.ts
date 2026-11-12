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
  type AgentImageReference,
  type ToolEffect,
  type ToolExecutionResult,
  type ToolSource,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";
import type { ToolCallContext } from "@superdurable/dex-agents/builtin-loop";
import {
  isLocalToolGroupEnabled,
  resolveTurnToolPolicy,
  type HarnessContext,
  type LocalToolGroup,
  type TurnConfig,
} from "@superdurable/dex-agents-cli-core";
import { piAgentFunctionTool, type PiAgentToolLike } from "@superdurable/dex-agents-pi";

import { PiToolCallEffects } from "./pi-tool-call-effects.js";

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
  /**
   * Whether every call waits for approval without `approval.always`. Changed in 0.6.0: `false` for every tool, as in
   * Pi.
   */
  readonly isApprovalRequired: boolean;
}

/**
 * pi-coding-agent's tools that replace the CLI core's `files` and `shell` groups, by their Pi names, in the order
 * the command offers them.
 *
 * Changed in 0.6.0: no tool asks for approval by itself; `approval.always` adds it.
 *
 * @since 0.5.0
 */
export const piCodingLocalTools: Readonly<Record<string, PiCodingLocalTool>> = Object.freeze({
  read: { group: "files", effect: "read", isApprovalRequired: false },
  grep: { group: "files", effect: "read", isApprovalRequired: false },
  find: { group: "files", effect: "read", isApprovalRequired: false },
  ls: { group: "files", effect: "read", isApprovalRequired: false },
  write: { group: "files", effect: "non_idempotent_write", isApprovalRequired: false },
  edit: { group: "files", effect: "non_idempotent_write", isApprovalRequired: false },
  bash: { group: "shell", effect: "non_idempotent_write", isApprovalRequired: false },
});

/**
 * Seconds a `bash` command runs before it is killed when the model passes no `timeout`.
 *
 * Pi itself has no default timeout, because its user can abort a running command. Cancelling a Dex turn waits for a
 * running tool Step instead, so the command keeps one.
 *
 * @since 0.5.0
 */
export const defaultBashTimeoutSeconds = 120;

/**
 * The largest `bash` default timeout in seconds that `pi.bash_timeout_seconds` may set.
 *
 * @since 0.5.0
 */
export const maximumBashTimeoutSeconds = 600;

const refusalReferenceKey = "refused";

/**
 * pi-coding-agent's `read`, `grep`, `find`, `ls`, `write`, `edit`, and `bash` tools as a {@link ToolSource}, so each
 * call runs in its own Dex tool Step with Pi's own behavior.
 *
 * - The tools are Pi's, built with Pi's default local operations and the workspace as their working directory. A
 *   relative path resolves against the workspace, and an absolute path or `~` reaches any file you can reach: Pi
 *   has no workspace boundary. `grep` and `find` run `rg` and `fd`, which Pi downloads to `~/.pi/agent/bin` when
 *   they are missing, unless `PI_OFFLINE` is set.
 * - Each model call lists the tools whose group the running turn switches on.
 * - As in Pi, no call waits for approval unless the turn's `approval.always` names the tool.
 * - Reads (`read`, `grep`, `find`, `ls`) run as `read` and retry. `write`, `edit`, and `bash` run as
 *   `non_idempotent_write`, so a call never runs twice.
 * - In its tool Step, a call is checked again with the executing Worker's configuration through
 *   `HarnessContext.resolveToolStepPermissions`. A call refused there records `failed` and does not run.
 * - A failure before a `write` created a directory or wrote its file, before an `edit` wrote its file, or before `bash`
 *   started its shell, records `failed`, and the
 *   model sees why. A `bash` command that exits with a non-zero code or that its timeout killed records `failed`
 *   too, with its output. Any other failure after the effect started records `outcome_unknown`.
 * - A call without `timeout` gets the source's default timeout, because cancelling a turn does not stop a running
 *   tool Step. A `timeout` from the model follows Pi's rules.
 * - `bash` output that Pi writes as it runs reaches the session as `pi.tool_progress` events.
 * - With `HarnessContext.mediaStore`, the images a tool returns, such as `read` of a PNG, are uploaded to it in the
 *   tool Step and reach the model after the output text: at most `maxImagesPerMessage` accepted images, and a
 *   refused or excess image becomes `[image omitted: <mime type>]`. Without a store, every image is that note.
 *
 * Register the source even when the file switches both groups off, so it refuses calls that another process
 * requested.
 *
 * Changed in 0.6.0: the tools use Pi's own operations instead of the CLI core's workspace file layer, and they wait
 * for approval only when `approval.always` names them.
 *
 * @since 0.5.0
 */
export class PiCodingToolSource implements ToolSource {
  /** The stable source name stored with each call. */
  public readonly sourceName = "pi_coding_tools";
  private readonly workspace: string;
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
    this.definitions = Object.keys(piCodingLocalTools).map((toolName) => {
      const tool = this.createPiTool(toolName, new PiToolCallEffects());
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
   * Builds the deferred call with the tool's effect and `approval.always`. A call of a switched-off tool is refused
   * without approval.
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
    return {
      toolCallId: call.toolCallId,
      toolName: call.toolName,
      arguments: call.arguments,
      policy: resolveTurnToolPolicy(turn, call.toolName, tool.effect, tool.isApprovalRequired, turnConfig),
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
    await this.authorizeOnThisWorker(call, tool);
    const effects = new PiToolCallEffects();
    // The recorded effect picked the tool Step, so it also decides how a failure is recorded.
    const functionTool = piAgentFunctionTool(definiteFailuresBeforeEffect(this.createPiTool(toolName, effects), effects), {
      effect: call.toolCall.effect,
    });
    const context: ToolCallContext = {
      flowId: call.flowId,
      turnId: call.toolCall.turnId,
      toolCallId: call.toolCall.toolCallId,
      idempotencyKey: call.idempotencyKey,
      effect: call.toolCall.effect,
      attempt: call.attempt,
      events: call.events,
      cancellationSignal: call.cancellationSignal,
      deadline: call.deadline,
      inputAnswers: call.inputAnswers ?? [],
      inputContinuation: call.toolCall.inputContinuation ?? null,
      heartbeat: () => call.heartbeat(),
      ...(this.context.mediaStore === undefined ? {} : { mediaStore: this.context.mediaStore }),
    };
    const executedArguments =
      toolName === "bash" && toolArguments["timeout"] === undefined
        ? { ...toolArguments, timeout: this.bashTimeoutSeconds }
        : toolArguments;
    return toToolExecutionResult(await functionTool.execute(executedArguments, context));
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
        timeout: { ...properties["timeout"], description: `Timeout in seconds; defaults to ${this.bashTimeoutSeconds}` },
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
  private async authorizeOnThisWorker(call: DeferredToolExecution, tool: PiCodingLocalTool): Promise<void> {
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
    if (!call.toolCall.requiresApproval && permissions.approval.always.includes(toolName)) {
      throw new ToolExecutionError(`${toolName} now needs approval, and this call had none; it did not run`);
    }
  }

  /** Creates a pi-coding-agent tool with Pi's local operations, which note in `effects` when they may change state. */
  private createPiTool(toolName: string, effects: PiToolCallEffects): PiAgentToolLike {
    const workspace = this.workspace;
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
        return createWriteTool(workspace, { operations: effects.writeOperations() });
      case "edit":
        return createEditTool(workspace, { operations: effects.editOperations() });
      case "bash":
        return createBashTool(workspace, { operations: effects.bashOperations() });
      default:
        throw new ToolExecutionError(`unknown Pi tool ${toolName}`);
    }
  }
}

/**
 * Records a failure with a known outcome as failed, and one after a possible effect as outcome_unknown. A command
 * that its timeout killed has a known outcome.
 */
function definiteFailuresBeforeEffect(tool: PiAgentToolLike, effects: PiToolCallEffects): PiAgentToolLike {
  return {
    ...tool,
    execute: async (toolCallId, params, signal, onUpdate) => {
      try {
        return await tool.execute(toolCallId, params, signal, onUpdate);
      } catch (error: unknown) {
        if (signal?.aborted === true) {
          throw error;
        }
        if (effects.hasEffectStarted && !effects.hasTimedOut) {
          // A plain error makes a single-attempt call record outcome_unknown.
          throw error instanceof ToolExecutionError ? new Error(error.message, { cause: error }) : error;
        }
        throw error instanceof ToolExecutionError ? error : new ToolExecutionError(describe(error), { cause: error });
      }
    },
  };
}

/** Converts the Pi wrapper's result, its text or a `functionToolResult` with images, into the call's result. */
function toToolExecutionResult(result: unknown): ToolExecutionResult {
  if (typeof result === "string") {
    return { output: result };
  }
  const { output, images } = result as { readonly output: string; readonly images?: readonly AgentImageReference[] };
  return images === undefined || images.length === 0 ? { output } : { output, images };
}

function readRecordedRefusal(toolSourceReference: string | null | undefined): string | null {
  if (toolSourceReference === undefined || toolSourceReference === null) {
    return null;
  }
  const reference = JSON.parse(toolSourceReference) as Record<string, unknown>;
  const refusal = reference[refusalReferenceKey];
  return typeof refusal === "string" ? refusal : null;
}

function parseArguments(argumentsText: string): Readonly<Record<string, unknown>> {
  let value: unknown;
  try {
    value = JSON.parse(argumentsText);
  } catch {
    value = undefined;
  }
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new ToolExecutionError("the arguments must be a JSON object; the call did not run");
  }
  return value as Readonly<Record<string, unknown>>;
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
