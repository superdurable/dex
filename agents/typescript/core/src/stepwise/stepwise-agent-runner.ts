// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { builtInToolDefinitions, type BuiltInTool } from "../agent-built-in-tools.js";
import {
  ToolExecutionError,
  type AgentFrameworkRunner,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolCall,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type ResolvedToolPolicy,
} from "../agent-framework-runner.js";
import { describeToolCallOutcome, type FrameworkMessage } from "../agent-session-values.js";
import type { AgentDefinition } from "./agent-definition.js";
import type { FunctionTool, ToolCallContext } from "./function-tool.js";
import { findJsonSchemaViolation, serializeJsonWithSpacedSeparators } from "./json-schema-validation.js";
import type {
  ModelOutputItem,
  ModelRequest,
  ModelResponse,
  ModelToolDefinition,
  ModelToolResultItem,
} from "./model-client.js";
import {
  pairToolCallsWithResults,
  readStoredModelInputItem,
  toStepwiseFrameworkMessage,
} from "./stepwise-input-items.js";

const defaultMaxModelCallsPerTurn = 100;

/**
 * The {@link AgentFrameworkRunner} of the built-in Stepwise loop.
 *
 * {@link createStepwiseAgentSessionFlow} creates one; construct it directly only to pass it to
 * `createAgentSessionFlow` with options of your own. Each `RunModel` makes at most one model call
 * through {@link AgentDefinition.model}, from committed state only: the stored messages, the results
 * of the previous model response from the turn's final records, and the instructions. The runner
 * stores the turn's transcript as it goes through {@link ToolCallsRequested.newMessages}, so a long turn
 * never rewrites a growing run state.
 *
 * @example
 * ```ts
 * const runner = new StepwiseAgentRunner(definition);
 * const flow = createAgentSessionFlow(runner, "SupportAgent", { builtInTools: ["ask_user"] });
 * ```
 */
export class StepwiseAgentRunner implements AgentFrameworkRunner<DeferredToolOutcome> {
  /** The agent definition, frozen at construction. */
  public readonly definition: AgentDefinition;
  private readonly functionToolsByName: ReadonlyMap<string, FunctionTool>;
  private readonly maxModelCallsPerTurn: number;

  /**
   * Creates the runner and checks the definition.
   * @param definition - The agent definition.
   * @throws {TypeError} When the name, instructions, model, or tools are invalid, two tools share a
   * name, or a function tool uses the name of an enabled built-in tool.
   * @throws {RangeError} When `maxModelCallsPerTurn` is not a positive integer.
   */
  public constructor(definition: AgentDefinition) {
    if (typeof definition?.name !== "string" || definition.name.trim() === "") {
      throw new TypeError("AgentDefinition.name must be a non-blank string");
    }
    if (typeof definition.instructions !== "string" && typeof definition.instructions !== "function") {
      throw new TypeError("AgentDefinition.instructions must be a string or a function");
    }
    if (typeof definition.model?.callModel !== "function" || typeof definition.model.estimateInputTokens !== "function") {
      throw new TypeError("AgentDefinition.model must be a ModelClient");
    }
    const enabledBuiltInTools = new Set<string>(definition.builtInTools ?? []);
    const functionToolsByName = new Map<string, FunctionTool>();
    for (const tool of definition.tools ?? []) {
      if (tool?.kind !== "function_tool") {
        throw new TypeError("AgentDefinition.tools must hold tools created by functionTool");
      }
      if (functionToolsByName.has(tool.name)) {
        throw new TypeError(`two tools of agent ${definition.name} are named ${tool.name}`);
      }
      if (enabledBuiltInTools.has(tool.name)) {
        throw new TypeError(`tool ${tool.name} uses the name of an enabled built-in tool`);
      }
      functionToolsByName.set(tool.name, tool);
    }
    const maxModelCallsPerTurn = definition.maxModelCallsPerTurn ?? defaultMaxModelCallsPerTurn;
    if (!Number.isSafeInteger(maxModelCallsPerTurn) || maxModelCallsPerTurn <= 0) {
      throw new RangeError("AgentDefinition.maxModelCallsPerTurn must be a positive integer");
    }
    this.definition = Object.freeze({ ...definition });
    this.functionToolsByName = functionToolsByName;
    this.maxModelCallsPerTurn = maxModelCallsPerTurn;
  }

  /**
   * Converts user text into a stored `user_text` item.
   * @param text - User message text.
   * @returns The message with role `user`.
   */
  public buildUserMessage(text: string): FrameworkMessage {
    return toStepwiseFrameworkMessage({ type: "user_text", text });
  }

  /**
   * Makes the turn's next model call.
   * @param turn - The turn context.
   * @returns {@link TurnCompleted} with the results and final answer not stored yet, or
   * {@link ToolCallsRequested} with the previous results, this response, and its tool calls.
   * @throws Any error of the model call, which retries the `RunModel` attempt.
   */
  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    this.requireNoBuiltInToolNameClash(turn);
    const modelCallCount = readModelCallCount(turn.frameworkRunState);
    const unstoredToolResults = buildUnstoredToolResults(turn);
    const isToolBudgetSpent = modelCallCount >= this.maxModelCallsPerTurn;
    const tools = isToolBudgetSpent ? [] : this.listOfferedTools(turn);
    const request: ModelRequest = {
      instructions: await this.resolveInstructions(turn),
      inputItems: pairToolCallsWithResults([...turn.messages.map(readStoredModelInputItem), ...unstoredToolResults]),
      tools,
      toolChoice: isToolBudgetSpent ? "none" : "auto",
      settings: this.definition.modelSettings ?? {},
      metadata: {
        flowId: turn.flowId,
        turnId: turn.turnId,
        stepExecutionId: turn.stepExecutionId ?? "",
        attempt: turn.attempt,
      },
      cancellationSignal: turn.cancellationSignal,
    };
    const response = await this.definition.model.callModel(request, turn.events);
    requireModelResponse(response);
    const usage = { ...response.usage, modelCalls: 1 };
    const newMessages = [...unstoredToolResults, ...response.outputItems].map(toStepwiseFrameworkMessage);
    if (response.toolCalls.length === 0) {
      return { kind: "completed", newMessages, usage };
    }
    if (isToolBudgetSpent) {
      // The model asked for tools although none were offered; end the turn so it cannot loop.
      const notRunResults = response.toolCalls.map(
        (toolCall): ModelToolResultItem => ({
          type: "tool_result",
          toolCallId: toolCall.toolCallId,
          toolName: toolCall.toolName,
          output: `The tool call was not run: this turn reached its limit of ${this.maxModelCallsPerTurn} model calls.`,
        }),
      );
      return {
        kind: "completed",
        newMessages: [...newMessages, ...notRunResults.map(toStepwiseFrameworkMessage)],
        usage,
      };
    }
    return {
      kind: "tool_calls_requested",
      frameworkRunState: serializeJsonWithSpacedSeparators({ model_calls: modelCallCount + 1 }),
      newMessages,
      toolCalls: response.toolCalls.map(
        (toolCall): DeferredToolCall => ({
          toolCallId: toolCall.toolCallId,
          toolName: toolCall.toolName,
          arguments: toolCall.arguments,
          policy: this.resolveModelToolPolicy(turn, toolCall.toolName),
        }),
      ),
      usage,
    };
  }

  /**
   * Runs one function tool call after checking its arguments against the tool's schema.
   * @param call - The deferred call.
   * @returns The output: a `string` result unchanged, any other result as JSON; or the tool's
   * {@link ToolInputRequested}.
   * @throws {ToolExecutionError} For an unknown tool, arguments that are not a JSON object or break the
   * schema, or a `ToolExecutionError` from the tool.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    const toolCall = call.toolCall;
    const tool = this.functionToolsByName.get(toolCall.toolName);
    if (tool === undefined) {
      throw new ToolExecutionError(`unknown tool ${toolCall.toolName}`);
    }
    const toolArguments = parseToolArguments(tool.name, toolCall.arguments);
    const violation = findJsonSchemaViolation(toolArguments, tool.parameters, "arguments");
    if (violation !== undefined) {
      throw new ToolExecutionError(`invalid arguments for ${tool.name}: ${violation}`);
    }
    const context: ToolCallContext = {
      flowId: call.flowId,
      turnId: toolCall.turnId,
      toolCallId: toolCall.toolCallId,
      idempotencyKey: call.idempotencyKey,
      attempt: call.attempt,
      events: call.events,
      cancellationSignal: call.cancellationSignal,
      deadline: call.deadline,
      inputAnswers: call.inputAnswers ?? [],
      inputContinuation: toolCall.inputContinuation ?? null,
      heartbeat: () => call.heartbeat(),
    };
    const result = await tool.execute(toolArguments, context);
    if (typeof result === "string") {
      return { output: result };
    }
    if ((result as { readonly kind?: unknown } | null)?.kind === "tool_input_requested") {
      return result as DeferredToolOutcome;
    }
    return { output: serializeJsonWithSpacedSeparators(result === undefined ? null : result) };
  }

  /**
   * Returns the results of the turn's tool calls that are not stored yet.
   * @param turn - The ended turn, with every record final.
   * @returns One `tool_result` message per unstored result, in deferral order.
   */
  public buildUnfinishedTurnMessages(turn: AgentTurnContext): readonly FrameworkMessage[] {
    return buildUnstoredToolResults(turn).map(toStepwiseFrameworkMessage);
  }

  /** A built-in enabled only in the session options must not share a function tool's name. */
  private requireNoBuiltInToolNameClash(turn: AgentTurnContext): void {
    for (const builtInTool of turn.enabledBuiltInTools ?? []) {
      if (this.functionToolsByName.has(builtInTool)) {
        throw new TypeError(`tool ${builtInTool} uses the name of an enabled built-in tool`);
      }
    }
  }

  private listOfferedTools(turn: AgentTurnContext): ModelToolDefinition[] {
    const functionTools = [...this.functionToolsByName.values()].map((tool) => ({
      name: tool.name,
      description: tool.description,
      parameters: tool.parameters,
    }));
    const enabledBuiltInTools = turn.enabledBuiltInTools ?? new Set<BuiltInTool>();
    const builtInTools = builtInToolDefinitions
      .filter((definition) => enabledBuiltInTools.has(definition.name))
      .map((definition) => ({
        name: definition.name,
        description: definition.description,
        parameters: definition.parameters,
      }));
    return [...functionTools, ...builtInTools];
  }

  /** A session policy wins; otherwise a function tool's own effect and approval apply, as in the Python core. */
  private resolveModelToolPolicy(turn: AgentTurnContext, toolName: string): ResolvedToolPolicy {
    const sessionPolicies = turn.toolPolicies ?? this.definition.toolPolicies ?? {};
    if (Object.hasOwn(sessionPolicies, toolName)) {
      return turn.resolveToolPolicy(toolName);
    }
    const tool = this.functionToolsByName.get(toolName);
    if (tool === undefined) {
      // The session fixes a built-in call's policy; an unknown tool fails fast in CallTool, so the model sees why.
      return { effect: "read", requiresApproval: false };
    }
    const policy = turn.resolveToolPolicy(toolName, tool.effect);
    return tool.requiresApproval === undefined
      ? policy
      : { effect: policy.effect, requiresApproval: tool.requiresApproval };
  }

  private async resolveInstructions(turn: AgentTurnContext): Promise<string> {
    const instructions = this.definition.instructions;
    if (typeof instructions === "string") {
      return instructions;
    }
    const resolved = await instructions({ flowId: turn.flowId, turnId: turn.turnId, attempt: turn.attempt });
    if (typeof resolved !== "string") {
      throw new TypeError(`the instructions of agent ${this.definition.name} must resolve to a string`);
    }
    return resolved;
  }
}

/** Results of the turn's final records that no stored `tool_result` message holds yet, in deferral order. */
function buildUnstoredToolResults(turn: AgentTurnContext): ModelToolResultItem[] {
  const storedResultIds = new Set(
    turn.messages
      .filter((message) => message.turnId === turn.turnId && message.role === "tool")
      .map((message) => message.toolCallId),
  );
  return [...turn.toolCalls]
    .sort((left, right) => left.deferralOrder - right.deferralOrder)
    .filter((record) => !storedResultIds.has(record.toolCallId))
    .map((record) => ({
      type: "tool_result",
      toolCallId: record.toolCallId,
      toolName: record.toolName,
      output: describeToolCallOutcome(record),
    }));
}

function parseToolArguments(toolName: string, argumentsText: string): Readonly<Record<string, unknown>> {
  let parsed: unknown;
  try {
    parsed = JSON.parse(argumentsText === "" ? "{}" : argumentsText);
  } catch {
    // A fixed text, because the two cores' JSON parsers word their errors differently.
    throw new ToolExecutionError(`invalid arguments for ${toolName}: arguments are not valid JSON`);
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    throw new ToolExecutionError(`invalid arguments for ${toolName}: expected a JSON object`);
  }
  return parsed as Readonly<Record<string, unknown>>;
}

function readModelCallCount(frameworkRunState: string | null): number {
  if (frameworkRunState === null) {
    return 0;
  }
  const parsed = JSON.parse(frameworkRunState) as { readonly model_calls?: unknown };
  if (typeof parsed.model_calls !== "number" || !Number.isSafeInteger(parsed.model_calls)) {
    throw new TypeError("FrameworkRunState is not a Stepwise run state");
  }
  return parsed.model_calls;
}

function requireModelResponse(response: ModelResponse): void {
  if (
    !Array.isArray(response?.outputItems) ||
    !Array.isArray(response.toolCalls) ||
    !response.outputItems.every((item: ModelOutputItem) => item?.type === "model_output")
  ) {
    throw new TypeError("ModelClient.callModel must return output items and tool calls");
  }
}
