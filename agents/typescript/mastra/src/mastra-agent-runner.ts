// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { Agent } from "@mastra/core/agent";
import type { AIV5Type, MessageListInput } from "@mastra/core/agent/message-list";
import { MASTRA_RESOURCE_ID_KEY, MASTRA_THREAD_ID_KEY, RequestContext } from "@mastra/core/request-context";
import type { MastraModelOutput } from "@mastra/core/stream";
import {
  describeToolCallOutcome,
  isFinalToolCallStatus,
  type AgentEventWriter,
  type AgentFrameworkRunner,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type AgentUsage,
  type DeferredToolCall,
  type DeferredToolExecution,
  type FrameworkMessage,
  type ToolExecutionResult,
  type ToolPolicy,
} from "@superdurable/dex-agents";

import { executeMastraDeferredToolCall } from "./mastra-deferred-tool-execution.js";
import {
  assignIncreasingCreatedAt,
  buildMastraToolResultMessage,
  buildMastraUserFrameworkMessage,
  decodeMastraTurnRunState,
  decodeStoredMastraMessages,
  encodeMastraTurnRunState,
  listPendingMastraToolInvocations,
  projectMastraMessage,
  type MastraTurnRunState,
} from "./mastra-message-codec.js";
import { planMastraModelStepTools, type MastraModelStepToolPlan } from "./mastra-tool-plan.js";

/**
 * The session and turn for which the runner asks the application for a Mastra `RequestContext`.
 *
 * {@link MastraAgentRunnerOptions.createRequestContext} receives it once per model call (`RunModel`) and
 * once per tool call (`CallTool` or `CallSingleAttemptTool`).
 */
export interface MastraTurnIdentity {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** Turn ID, `turn-<n>`. */
  readonly turnId: string;
}

/**
 * Options of {@link MastraAgentRunner}.
 *
 * Applications usually pass these through {@link createMastraAgentSessionFlow}, which also forwards the
 * session options to the shared Flow.
 */
export interface MastraAgentRunnerOptions {
  /**
   * Tool policies by tool name, the same object passed to the session Flow. Besides classifying the
   * agent's tools, the names allow tools that Mastra adds outside the agent's `tools`, such as sub-agents
   * (`agent-<name>`) and workflows (`workflow-<name>`). Mastra runs such a tool itself inside `RunModel`,
   * so the runner offers it to the model only when its policy here is `read` without approval, and it
   * is at-least-once. Defaults to none.
   */
  readonly toolPolicies?: Readonly<Record<string, ToolPolicy>>;
  /**
   * Maximum model calls in one `RunModel`, passed to Mastra as `maxSteps`. A `RunModel` makes one model
   * call whenever the model calls an agent tool or answers. Mastra calls the model again inside the same
   * `RunModel` only after the model called nothing but tools Mastra runs itself, or a tool it was not
   * offered. Defaults to Mastra's own default, which is 5 in `@mastra/core` 1.81.
   */
  readonly maxSteps?: number;
  /**
   * Whether MCP annotations on tools that came from an MCP server (tools with `mcpMetadata`) classify
   * their effect. Annotations on tools the application created with `createTool` are always read.
   * Defaults to `false`, because a server's hints are not a guarantee.
   */
  readonly shouldTrustMcpServerToolAnnotations?: boolean;
  /**
   * Creates the Mastra `RequestContext` for one model call or one tool call. It must build the same
   * values in any Worker process, because a turn's Steps may run in different processes. It must not set
   * `mastra__threadId` or `mastra__resourceId`, which select a Mastra Memory thread; the runner throws
   * `TypeError` then. Defaults to an empty `RequestContext`.
   */
  readonly createRequestContext?: (turn: MastraTurnIdentity) => RequestContext;
}

/**
 * Runs a Mastra `Agent` inside the shared Dex agent session Flow.
 *
 * Most applications call {@link createMastraAgentSessionFlow} instead. Construct the runner yourself only
 * to pass it to `createAgentSessionFlow` from `@superdurable/dex-agents` with other session wiring.
 *
 * The session runs in Step mode: every model call is a `RunModel` Step and every tool call its own tool
 * Step, and the tool calls of one model response run in parallel. Each `RunModel` calls `agent.stream()`
 * with the stored history and no Memory thread, so Dex holds the conversation and Mastra keeps nothing
 * between Steps. History is stored as Mastra messages (`MastraDBMessage` JSON). Every agent tool, `read`
 * tools included, is passed to Mastra as a client tool without its `execute` function, so Mastra stops
 * after the model call and returns the calls; the runner returns `ToolCallsRequested`. The next
 * `RunModel` appends each recorded result as a tool result message and calls `agent.stream()` again.
 *
 * The runner keeps no state between calls, so one instance may serve many sessions concurrently.
 *
 * @example
 * ```ts
 * import { Agent } from "@mastra/core/agent";
 * import { createAgentSessionFlow } from "@superdurable/dex-agents";
 * import { MastraAgentRunner } from "@superdurable/dex-agents-mastra";
 *
 * const toolPolicies = { search_orders: { effect: "read" } } as const;
 * const runner = new MastraAgentRunner(supportAgent, { toolPolicies, maxSteps: 10 });
 * const flow = createAgentSessionFlow(runner, "SupportAgent", { toolPolicies });
 * ```
 */
export class MastraAgentRunner implements AgentFrameworkRunner {
  private readonly toolPolicies: Readonly<Record<string, ToolPolicy>>;
  private readonly maxSteps: number | undefined;
  private readonly shouldTrustMcpServerToolAnnotations: boolean;
  private readonly createRequestContext: (turn: MastraTurnIdentity) => RequestContext;

  /**
   * Creates a runner for one Mastra agent.
   *
   * @param agent - The Mastra agent. Its tools, instructions, model, and processors are read in every
   * Step, so changes to a dynamic configuration apply from the next Step.
   * @param options - Tool policy names, step limit, MCP annotation trust, and request context factory.
   * @throws {RangeError} When `maxSteps` is not a positive integer.
   */
  public constructor(
    private readonly agent: Agent,
    options: MastraAgentRunnerOptions = {},
  ) {
    if (options.maxSteps !== undefined && (!Number.isInteger(options.maxSteps) || options.maxSteps < 1)) {
      throw new RangeError(`maxSteps must be a positive integer, got ${options.maxSteps}`);
    }
    this.toolPolicies = options.toolPolicies ?? {};
    this.maxSteps = options.maxSteps;
    this.shouldTrustMcpServerToolAnnotations = options.shouldTrustMcpServerToolAnnotations ?? false;
    this.createRequestContext = options.createRequestContext ?? (() => new RequestContext());
  }

  /**
   * Builds the stored Mastra user message for one user text.
   *
   * The `frameworkItem` is a `MastraDBMessage` without `id` and `createdAt`, because this method must be
   * deterministic. At replay the runner assigns the ID `dex-message-<sequence>` and the stored message time.
   *
   * @param text - User message text.
   * @returns The user message.
   */
  public buildUserMessage(text: string): FrameworkMessage {
    return buildMastraUserFrameworkMessage(text);
  }

  /**
   * Makes one model call of a turn with `agent.stream()`.
   *
   * Every tool of the agent is passed as a Mastra client tool, so Mastra stops after the model call
   * and returns the requested calls without running them. When the turn continues, the recorded result
   * of every call of the previous response is appended first as an AI SDK tool result message.
   *
   * @param turn - Turn context from the session.
   * @returns `TurnCompleted` with every Mastra message of the turn, or `ToolCallsRequested` with the calls
   * of this model response and the turn's messages so far as `FrameworkRunState`.
   * @throws {TypeError} When a tool name is invalid, a tool that Mastra adds replaces an agent tool, or the
   * `RequestContext` selects a Mastra Memory thread or resource.
   * @throws {Error} When the model call fails, the attempt is cancelled, Mastra suspends a tool itself, or
   * the model response leaves the turn without any message, such as an empty response; the session reruns
   * the model call, and after the last attempt the turn fails.
   */
  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    const requestContext = this.createTurnRequestContext({ flowId: turn.flowId, turnId: turn.turnId });
    const history = decodeStoredMastraMessages(turn.messages);
    const runState = turn.frameworkRunState === null ? null : decodeMastraTurnRunState(turn.frameworkRunState);
    const toolPlan = planMastraModelStepTools(
      await this.agent.listTools({ requestContext }),
      this.toolPolicies,
      (toolName, frameworkEffect) => turn.resolveToolPolicy(toolName, frameworkEffect),
      this.shouldTrustMcpServerToolAnnotations,
    );
    await this.assertClientToolsReachModel(toolPlan, requestContext);
    const messages: MessageListInput = [
      ...assignIncreasingCreatedAt([...history, ...(runState?.turnMessages ?? [])]),
      ...buildRecordedToolResultMessages(turn, runState),
    ];
    const output = await this.agent.stream(messages, {
      requestContext,
      clientTools: toolPlan.clientTools,
      activeTools: [...toolPlan.activeToolNames],
      requireToolApproval: false,
      abortSignal: turn.cancellationSignal,
      ...(this.maxSteps === undefined ? {} : { maxSteps: this.maxSteps }),
    });
    const suspendedToolNames = await forwardMastraStreamEvents(output, turn.events, toolPlan);
    const usage = await readMastraModelStepUsage(output, turn);
    if (suspendedToolNames.length > 0) {
      throw new Error(
        `Mastra suspended tool ${suspendedToolNames.join(", ")} inside RunModel; ` +
          "only the session's approval can pause a tool call",
      );
    }
    const historyMessageIds = new Set(history.map((message) => message.id));
    const turnMessages = output.messageList.get.all
      .db()
      .filter((message) => !historyMessageIds.has(message.id));
    const pendingInvocations = listPendingMastraToolInvocations(turnMessages);
    if (pendingInvocations.length === 0) {
      if (turnMessages.length === 0) {
        throw new Error(`Mastra produced no message for turn ${turn.turnId}`);
      }
      return { kind: "completed", newMessages: turnMessages.map(projectMastraMessage), usage };
    }
    const toolCalls = pendingInvocations.map((invocation): DeferredToolCall => {
      const policy = toolPlan.clientToolPolicies.get(invocation.toolName);
      if (policy === undefined) {
        throw new Error(`Mastra left tool call ${invocation.toolCallId} of ${invocation.toolName} without a result`);
      }
      return {
        toolCallId: invocation.toolCallId,
        toolName: invocation.toolName,
        arguments: JSON.stringify(invocation.args ?? {}),
        policy,
      };
    });
    return {
      kind: "tool_calls_requested",
      frameworkRunState: encodeMastraTurnRunState({
        turnMessages,
        pendingToolCalls: toolCalls.map(({ toolCallId, toolName }) => ({ toolCallId, toolName })),
      }),
      toolCalls,
      usage,
    };
  }

  /**
   * Runs the agent's original tool for one tool call, in `CallTool` or `CallSingleAttemptTool`.
   *
   * The tool receives the parsed arguments and a context with `toolCallId`, `abortSignal`, and a copy of
   * the `RequestContext` from {@link MastraAgentRunnerOptions.createRequestContext} that also holds the
   * call's idempotency key under {@link idempotencyKeyRequestContextKey}. A Mastra input validation
   * failure and a `ToolExecutionError` thrown by the tool are recorded as `failed`. A tool that calls
   * `suspend()` fails with `ToolExecutionError`. Any other thrown error is an unexpected failure:
   * `CallTool` retries it, and the session records `failed` for a `read` and `outcome_unknown` for a write
   * once the attempts run out. A Mastra tool whose `requestContextSchema` rejects the `RequestContext` is
   * recorded as `failed`, because Mastra checks it before the tool runs. A validation error that Mastra
   * returns otherwise, such as from the tool's `outputSchema`, is unexpected, because the tool may have run.
   *
   * @param call - The approved call.
   * @returns The tool result: a string unchanged, `undefined` as empty text, other values as JSON text.
   * @throws {ToolExecutionError} When the tool is missing, has no `execute`, rejects its input, or throws it.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const requestContext = this.createTurnRequestContext({ flowId: call.flowId, turnId: call.toolCall.turnId });
    return executeMastraDeferredToolCall(
      await this.agent.listTools({ requestContext }),
      this.agent.id,
      call,
      requestContext,
    );
  }

  private createTurnRequestContext(turn: MastraTurnIdentity): RequestContext {
    const requestContext = this.createRequestContext(turn);
    // Mastra would load and save that Memory thread, so the model would see the history twice.
    for (const memoryKey of [MASTRA_THREAD_ID_KEY, MASTRA_RESOURCE_ID_KEY]) {
      if (requestContext.has(memoryKey)) {
        throw new TypeError(`createRequestContext must not set ${memoryKey}: Dex stores the conversation`);
      }
    }
    return requestContext;
  }

  // Tools Mastra adds itself, such as sub-agents, are merged after client tools and would replace them.
  private async assertClientToolsReachModel(
    toolPlan: MastraModelStepToolPlan,
    requestContext: RequestContext,
  ): Promise<void> {
    const executionTools = await this.agent.getToolsForExecution({ requestContext, clientTools: toolPlan.clientTools });
    for (const toolName of Object.keys(toolPlan.clientTools)) {
      const executionTool: unknown = executionTools[toolName];
      if (typeof (executionTool as { readonly execute?: unknown } | undefined)?.execute === "function") {
        throw new TypeError(
          `tool ${toolName} of Mastra agent ${this.agent.id} is replaced by a tool Mastra adds itself, ` +
            "such as a sub-agent or workflow tool; rename one of them",
        );
      }
    }
  }
}

function buildRecordedToolResultMessages(
  turn: AgentTurnContext,
  runState: MastraTurnRunState | null,
): AIV5Type.ToolModelMessage[] {
  return (runState?.pendingToolCalls ?? []).map((pendingToolCall) => {
    const record = turn.recordedToolCall(pendingToolCall.toolCallId);
    if (record === undefined || !isFinalToolCallStatus(record.status)) {
      throw new Error(`turn ${turn.turnId} resumed without a final record for ${pendingToolCall.toolCallId}`);
    }
    return buildMastraToolResultMessage(pendingToolCall, describeToolCallOutcome(record));
  });
}

/** Writes live events and returns the tools Mastra suspended natively, which the runner cannot resume. */
async function forwardMastraStreamEvents(
  output: MastraModelOutput<unknown>,
  events: AgentEventWriter,
  toolPlan: MastraModelStepToolPlan,
): Promise<string[]> {
  const suspendedToolNames: string[] = [];
  const isRunInsideTurn = (toolName: string): boolean => toolPlan.mastraRunToolNames.has(toolName);
  for await (const chunk of output.fullStream) {
    switch (chunk.type) {
      case "text-delta":
        events.writeTextDelta(chunk.payload.text);
        break;
      case "reasoning-delta":
        events.writeReasoningDelta(chunk.payload.text);
        break;
      case "tool-call":
        if (isRunInsideTurn(chunk.payload.toolName)) {
          events.writeToolCallStarted(chunk.payload.toolCallId, chunk.payload.toolName);
        }
        break;
      case "tool-result":
        if (isRunInsideTurn(chunk.payload.toolName)) {
          events.writeToolCallFinished(chunk.payload.toolCallId, chunk.payload.toolName, "succeeded");
        }
        break;
      case "tool-error":
        if (isRunInsideTurn(chunk.payload.toolName)) {
          events.writeToolCallFinished(chunk.payload.toolCallId, chunk.payload.toolName, "failed");
        }
        break;
      case "tool-call-approval":
      case "tool-call-suspended":
        suspendedToolNames.push(chunk.payload.toolName);
        break;
      case "tripwire":
        events.writeEvent("mastra.tripwire", { reason: chunk.payload.reason });
        break;
      default:
        break;
    }
  }
  return suspendedToolNames;
}

async function readMastraModelStepUsage(
  output: MastraModelOutput<unknown>,
  turn: AgentTurnContext,
): Promise<AgentUsage> {
  const [finishReason, totalUsage, steps] = await Promise.all([output.finishReason, output.totalUsage, output.steps]);
  if (turn.cancellationSignal.aborted) {
    throw new Error(`turn ${turn.turnId} was cancelled`, { cause: turn.cancellationSignal.reason });
  }
  if (output.error !== undefined) {
    throw output.error;
  }
  if (finishReason === "error") {
    throw new Error(`Mastra agent stream of turn ${turn.turnId} finished with an error`);
  }
  return {
    modelCalls: steps.length,
    inputTokens: totalUsage.inputTokens ?? 0,
    outputTokens: totalUsage.outputTokens ?? 0,
    cachedInputTokens: totalUsage.cachedInputTokens ?? 0,
    reasoningTokens: totalUsage.reasoningTokens ?? 0,
  };
}
