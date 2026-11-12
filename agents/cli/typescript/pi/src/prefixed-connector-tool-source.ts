// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  ToolExecutionError,
  type AgentToolCall,
  type AgentTurnContext,
  type DeferredToolCall,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type SourceToolDefinition,
  type ToolCallStepOptions,
  type ToolSource,
  type ToolTaskCancellation,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";
import type { HarnessContext, TurnConfig } from "@superdurable/dex-agents-cli-core";

/**
 * The prefix of a connector tool whose name a Pi tool already has, such as `connector_read`.
 *
 * @since 0.6.0
 */
export const collidingConnectorToolPrefix = "connector_";

/** The key of the reference that a renamed connector call stores: the source's own name and reference. */
const renamedCallReferenceKey = "renamed_connector_tool";

interface RenamedCallReference {
  readonly toolName: string;
  readonly reference: string | null;
}

/**
 * A connectors tool source whose tools the agent sees beside Pi's own tools.
 *
 * - A connector tool keeps its name unless a Pi tool has it. Then the agent sees it as `connector_<name>`, and
 *   `approval.always` names it that way. When the source also lists that name, the prefix repeats until the name is
 *   free, so every offered name is unique. The reserved names are fixed, so every Worker renames the same tools.
 * - A renamed call stores the source's own tool name with the source's reference, so the source checks and runs the
 *   connector operation it listed on any Worker.
 * - Every call that is not a read waits for approval, and its tool Step refuses a call without one.
 *
 * @since 0.6.0
 */
export class PrefixedConnectorToolSource implements ToolSource {
  /** The wrapped source's name, so stored calls reach it on any Worker. */
  public readonly sourceName: string;
  private readonly listedByAgentName = new Map<string, SourceToolDefinition>();

  /**
   * Wraps one connectors tool source.
   *
   * @param source - The CLI core's connectors tool source.
   * @param reservedToolNames - The Pi tool names, which no connector tool may take.
   * @param context - The harness context, for `approval.always` of the renamed tools.
   */
  public constructor(
    private readonly source: ToolSource,
    private readonly reservedToolNames: ReadonlySet<string>,
    private readonly context: HarnessContext,
  ) {
    this.sourceName = source.sourceName;
  }

  /**
   * Lists the source's tools, each colliding name prefixed.
   *
   * @returns The tools, named as the agent sees them.
   */
  public async listTools(): Promise<readonly SourceToolDefinition[]> {
    return this.agentVisibleListing(await this.source.listTools());
  }

  /**
   * Returns the source's last listing without I/O, each colliding name prefixed.
   *
   * @returns The tools.
   */
  public cachedTools(): readonly SourceToolDefinition[] {
    return this.agentVisibleListing(this.source.cachedTools?.() ?? []);
  }

  /**
   * Builds the source's call under the agent's name, with approval for every call that is not a read.
   *
   * @param turn - The `RunModel` context.
   * @param call - The model's call.
   * @returns The deferred call.
   */
  public deferredToolCall(
    turn: AgentTurnContext,
    call: {
      readonly toolCallId: string;
      readonly toolName: string;
      readonly arguments: string;
      readonly listedTool: SourceToolDefinition;
    },
  ): DeferredToolCall {
    const sourceTool = this.listedByAgentName.get(call.toolName);
    if (sourceTool === undefined || sourceTool.name === call.toolName) {
      return withWriteApproval(this.source.deferredToolCall(turn, call), false);
    }
    const built = this.source.deferredToolCall(turn, { ...call, toolName: sourceTool.name, listedTool: sourceTool });
    const reference: RenamedCallReference = { toolName: sourceTool.name, reference: built.toolSourceReference ?? null };
    const turnConfig = this.context.resolveTurn(turn.sessionSettings ?? {});
    return withWriteApproval(
      {
        ...built,
        toolName: call.toolName,
        toolSourceReference: JSON.stringify({ [renamedCallReferenceKey]: reference }),
      },
      turnConfig.approval.always.includes(call.toolName),
    );
  }

  /**
   * Runs one call through the source under the source's own tool name.
   *
   * @param call - The approved call.
   * @returns The source's outcome.
   * @throws {ToolExecutionError} When a write had no approval, when `approval.always` of this Worker names the call's
   * tool and it had no approval, or when the source refuses or fails the call.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    const { toolName } = call.toolCall;
    // A call the source refused when it built it is a read, which the source refuses again here.
    if (call.toolCall.effect !== "read" && !call.toolCall.requiresApproval) {
      throw new ToolExecutionError(`tool ${toolName} writes and had no approval; it did not run`);
    }
    const renamed = readRenamedCallReference(call.toolCall);
    if (renamed === null) {
      return this.source.executeDeferredToolCall(call);
    }
    if (!call.toolCall.requiresApproval) {
      let permissions: TurnConfig;
      try {
        permissions = await this.context.resolveToolStepPermissions({ flowId: call.flowId, turnId: call.toolCall.turnId });
      } catch (error: unknown) {
        throw new ToolExecutionError(`tool ${toolName} did not run: the session's settings are rejected here: ${describe(error)}`, {
          cause: error,
        });
      }
      if (permissions.approval.always.includes(toolName)) {
        throw new ToolExecutionError(`tool ${toolName} now needs approval, and this call had none; it did not run`);
      }
    }
    return this.source.executeDeferredToolCall({ ...call, toolCall: asSourceCall(call.toolCall, renamed) });
  }

  /**
   * Polls one of the source's tool tasks.
   *
   * @param poll - The call's record.
   * @returns The poll outcome.
   */
  public pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    const renamed = readRenamedCallReference(poll.toolCall);
    return this.source.pollToolTask(renamed === null ? poll : { ...poll, toolCall: asSourceCall(poll.toolCall, renamed) });
  }

  /**
   * Cancels one of the source's abandoned tool tasks, when the source can.
   *
   * @param cancellation - The abandoned call's record.
   * @returns A Promise that resolves once the source took the cancellation.
   */
  public async cancelToolTask(cancellation: ToolTaskCancellation): Promise<void> {
    const renamed = readRenamedCallReference(cancellation.toolCall);
    await this.source.cancelToolTask?.(
      renamed === null ? cancellation : { ...cancellation, toolCall: asSourceCall(cancellation.toolCall, renamed) },
    );
  }

  /**
   * Returns the source's options for one call's tool Step.
   *
   * @param toolCall - The call's record.
   * @returns The options, or `undefined` for the defaults.
   */
  public toolCallStepOptions(toolCall: AgentToolCall): ToolCallStepOptions | undefined {
    const renamed = readRenamedCallReference(toolCall);
    return this.source.toolCallStepOptions?.(renamed === null ? toolCall : asSourceCall(toolCall, renamed));
  }

  private agentVisibleListing(tools: readonly SourceToolDefinition[]): readonly SourceToolDefinition[] {
    // The source's own names stay taken, so a renamed tool never shadows another connector tool.
    const taken = new Set(this.reservedToolNames);
    for (const tool of tools) {
      taken.add(tool.name);
    }
    const visible: SourceToolDefinition[] = [];
    for (const tool of tools) {
      let agentName = tool.name;
      if (this.reservedToolNames.has(tool.name)) {
        agentName = `${collidingConnectorToolPrefix}${tool.name}`;
        while (taken.has(agentName)) {
          agentName = `${collidingConnectorToolPrefix}${agentName}`;
        }
        taken.add(agentName);
      }
      this.listedByAgentName.set(agentName, tool);
      visible.push(agentName === tool.name ? tool : { ...tool, name: agentName });
    }
    return visible;
  }
}

function withWriteApproval(call: DeferredToolCall, isApprovalRequired: boolean): DeferredToolCall {
  const requiresApproval = call.policy.requiresApproval || isApprovalRequired || call.policy.effect !== "read";
  return requiresApproval === call.policy.requiresApproval ? call : { ...call, policy: { ...call.policy, requiresApproval } };
}

/** The fields of a call or a call record that a rename changes. */
interface NamedToolCall {
  readonly toolName: string;
  readonly toolSourceReference?: string;
}

function readRenamedCallReference(toolCall: NamedToolCall): RenamedCallReference | null {
  const text = toolCall.toolSourceReference;
  if (text === undefined) {
    return null;
  }
  let value: unknown;
  try {
    value = JSON.parse(text);
  } catch {
    return null;
  }
  const renamed = typeof value === "object" && value !== null ? (value as Record<string, unknown>)[renamedCallReferenceKey] : undefined;
  if (typeof renamed !== "object" || renamed === null) {
    return null;
  }
  const { toolName, reference } = renamed as Record<string, unknown>;
  if (typeof toolName !== "string" || (reference !== null && typeof reference !== "string")) {
    return null;
  }
  return { toolName, reference };
}

function asSourceCall<T extends NamedToolCall>(toolCall: T, renamed: RenamedCallReference): T {
  const { toolSourceReference: _renamedReference, ...rest } = toolCall;
  return {
    ...rest,
    toolName: renamed.toolName,
    ...(renamed.reference === null ? {} : { toolSourceReference: renamed.reference }),
  } as T;
}

function describe(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}
