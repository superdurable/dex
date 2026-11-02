// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { builtInToolDefinitions, type BuiltInTool } from "../agent-built-in-tools.js";
import type { ContextSummaryRequest, ContextSummaryResult } from "../agent-context-compaction.js";
import {
  ToolExecutionError,
  type AgentFrameworkRunner,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolCall,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type ResolvedToolPolicy,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "../agent-framework-runner.js";
import { buildPlanNote } from "../agent-plans.js";
import {
  describeToolCallOutcome,
  type ContextSummary,
  type FrameworkMessage,
} from "../agent-session-values.js";
import type { AgentDefinition } from "./agent-definition.js";
import type { FunctionTool, ToolCallContext } from "./function-tool.js";
import { findJsonSchemaViolation, serializeJsonWithSpacedSeparators } from "./json-schema-validation.js";
import type {
  ModelInputItem,
  ModelOutputItem,
  ModelOutputWriter,
  ModelRequest,
  ModelRequestMetadata,
  ModelResponse,
  ModelToolChoice,
  ModelToolDefinition,
  ModelToolResultItem,
} from "./model-client.js";
import {
  pairToolCallsWithResults,
  readStoredModelInputItem,
  toStepwiseFrameworkMessage,
} from "./stepwise-input-items.js";
import type { SourceToolDefinition, ToolSource } from "./tool-source.js";

interface ListedSourceTool {
  readonly index: number;
  readonly source: ToolSource;
  readonly tool: SourceToolDefinition;
}
type ListedSourceTools = ReadonlyMap<string, ListedSourceTool>;
const toolSourceMethods = ["listTools", "deferredToolCall", "executeDeferredToolCall", "pollToolTask"] as const;
// The runner's envelope around a source's toolSourceReference: the building source's name.
const toolSourceNameKey = "stepwise_tool_source";
// An unlisted name never reaches a source, so it fails without asking anyone to approve it.
const unlistedSourceToolPolicy: ResolvedToolPolicy = { effect: "unclassified", requiresApproval: false };
const toolSourceReferenceKey = "reference";

/** Default {@link AgentDefinition.toolSourceListingTimeoutMs}: 10 seconds. */
export const defaultToolSourceListingTimeoutMs = 10_000;

export const defaultMaxModelCallsPerTurn = 100;

/** First line of the user text that carries the {@link ContextSummary} into the model input. */
export const contextSummaryNoteHeader = "[Dex] Summary of the earlier conversation:";

/** Instructions of the model call that writes a {@link ContextSummary}. */
export const contextSummaryInstructions =
  "You summarize the earlier part of an agent conversation. The agent continues from your " +
  "summary and the messages after it, so keep every fact, decision, user request and " +
  "preference, open task, and tool result that it still needs, including what an earlier " +
  "summary says. Write plain text.";

/** Last user text of the model call that writes a {@link ContextSummary}. */
export const contextSummaryRequest = "[Dex] Summarize the conversation above. Reply with the summary only.";

/** Result of a tool call the model made after the turn's model call limit; the same text as the Python core. */
export function buildToolCallLimitReachedResult(maxModelCallsPerTurn: number): string {
  return `The tool call was not run: this turn reached its limit of ${maxModelCallsPerTurn} model calls.`;
}

/**
 * Returns the user text that says how many of the oldest messages a summary request left out.
 * @param omittedCount - Messages left out, at least 1.
 * @returns `[Dex] <omittedCount> earlier messages did not fit in this request and are left out.`, or
 * `[Dex] 1 earlier message did not fit in this request and is left out.` for one message.
 */
export function buildContextSummaryOmissionNote(omittedCount: number): string {
  if (omittedCount === 1) {
    return "[Dex] 1 earlier message did not fit in this request and is left out.";
  }
  return `[Dex] ${omittedCount} earlier messages did not fit in this request and are left out.`;
}

/**
 * Returns the user text that puts a summary before the newer messages in a model input.
 * @param summary - The session's summary.
 * @returns {@link contextSummaryNoteHeader}, a newline, and the summary text.
 */
export function buildContextSummaryNote(summary: ContextSummary): string {
  return `${contextSummaryNoteHeader}\n${summary.text}`;
}

// The summary is not the assistant's answer, so its live text goes nowhere.
const discardedModelOutput: ModelOutputWriter = {
  writeTextDelta: () => undefined,
  writeReasoningDelta: () => undefined,
};

/**
 * The {@link AgentFrameworkRunner} of the built-in Stepwise loop.
 *
 * {@link createStepwiseAgentSessionFlow} creates one; construct it directly only to pass it to
 * `createAgentSessionFlow` with options of your own. Each `RunModel` makes at most one model call
 * through {@link AgentDefinition.model}, from committed state only: the stored messages, the results
 * of the previous model response from the turn's final records, and the instructions. The runner
 * stores the turn's transcript as it goes through {@link ToolCallsRequested.newMessages}, so a long turn
 * never rewrites a growing run state. It supports plan mode: a `planning` turn offers only `write_todos`, and
 * every model call shows the plan with {@link buildPlanNote} before the turn's messages. It implements the
 * compaction methods, so a session with a compaction policy starts the model input with the summary. Its
 * {@link pollToolTask} passes a tool task to the {@link ToolSource} that started it; function tools start no
 * tasks.
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
  /** The runner honors {@link AgentTurnContext.interactionMode}, so plan mode can be enabled. */
  public readonly supportsPlanMode = true;
  private readonly functionToolsByName: ReadonlyMap<string, FunctionTool>;
  private readonly toolSources: readonly ToolSource[];
  private readonly toolSourcesByName: ReadonlyMap<string, ToolSource>;
  private readonly maxModelCallsPerTurn: number;
  private readonly toolSourceListingTimeoutMs: number;
  /** The last listing of each source, so estimates count its tools without listing again. */
  private readonly lastListings = new Map<number, readonly SourceToolDefinition[]>();

  /**
   * Creates the runner and checks the definition.
   * @param definition - The agent definition.
   * @throws {TypeError} When the name, instructions, model, or tools are invalid, two tools share a
   * name, a function tool uses the name of an enabled built-in tool, `planModeEnabled` is set without
   * `write_todos`, `planningToolChoice` is neither `auto` nor `required`, or a tool source lacks a
   * {@link ToolSource} method.
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
    const toolSources = [...(definition.toolSources ?? [])];
    for (const source of toolSources) {
      const missing = toolSourceMethods.find(
        (method) => typeof (source as unknown as Record<string, unknown> | null)?.[method] !== "function",
      );
      if (missing !== undefined) {
        throw new TypeError(`tool source ${String(source)} has no ${missing} method`);
      }
    }
    const toolSourcesByName = new Map<string, ToolSource>();
    for (const source of toolSources) {
      if (typeof source.sourceName !== "string" || source.sourceName.trim() === "") {
        throw new TypeError(`tool source ${String(source)} needs a non-blank sourceName`);
      }
      if (toolSourcesByName.has(source.sourceName)) {
        throw new TypeError(`two tool sources are named ${source.sourceName}`);
      }
      toolSourcesByName.set(source.sourceName, source);
    }
    refuseDeclaredToolNameCollisions(toolSources, new Set(functionToolsByName.keys()), enabledBuiltInTools);
    const toolSourceListingTimeoutMs = definition.toolSourceListingTimeoutMs ?? defaultToolSourceListingTimeoutMs;
    if (!(toolSourceListingTimeoutMs > 0)) {
      throw new RangeError("AgentDefinition.toolSourceListingTimeoutMs must be positive");
    }
    if (definition.planModeEnabled === true && !enabledBuiltInTools.has("write_todos")) {
      throw new TypeError("planModeEnabled requires the write_todos built-in tool");
    }
    const planningToolChoice: unknown = definition.planningToolChoice ?? "auto";
    if (planningToolChoice !== "auto" && planningToolChoice !== "required") {
      throw new TypeError(`planningToolChoice must be "auto" or "required", not ${String(planningToolChoice)}`);
    }
    const maxModelCallsPerTurn = definition.maxModelCallsPerTurn ?? defaultMaxModelCallsPerTurn;
    if (!Number.isSafeInteger(maxModelCallsPerTurn) || maxModelCallsPerTurn <= 0) {
      throw new RangeError("AgentDefinition.maxModelCallsPerTurn must be a positive integer");
    }
    this.definition = Object.freeze({ ...definition });
    this.functionToolsByName = functionToolsByName;
    this.toolSources = toolSources;
    this.toolSourcesByName = toolSourcesByName;
    this.toolSourceListingTimeoutMs = toolSourceListingTimeoutMs;
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
   * @throws {TypeError} When a function tool uses the name of an enabled built-in tool, or a tool source lists a
   * name that repeats a function tool, an enabled built-in tool, or another source's tool. No model request is
   * made.
   * @throws Any error of the model call or of a tool source's `listTools`, which retries the `RunModel` attempt.
   */
  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    this.requireNoBuiltInToolNameClash(turn);
    const modelCallCount = readModelCallCount(turn.frameworkRunState);
    const unstoredToolResults = buildUnstoredToolResults(turn);
    const isToolBudgetSpent = modelCallCount >= this.maxModelCallsPerTurn;
    const sourceTools: ListedSourceTools =
      isToolBudgetSpent || turn.interactionMode === "planning" ? new Map() : await this.listSourceTools(turn);
    const request = await this.buildModelRequest(
      turn,
      unstoredToolResults,
      modelCallCount,
      isToolBudgetSpent,
      sourceTools,
    );
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
          output: buildToolCallLimitReachedResult(this.maxModelCallsPerTurn),
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
      toolCalls: response.toolCalls.map((toolCall): DeferredToolCall => {
        const listed = sourceTools.get(toolCall.toolName);
        if (listed !== undefined) {
          const built = listed.source.deferredToolCall(turn, { ...toolCall, listedTool: listed.tool });
          return {
            ...built,
            toolSourceReference: writeToolSourceEnvelope(listed.source.sourceName, built.toolSourceReference),
          };
        }
        return {
          toolCallId: toolCall.toolCallId,
          toolName: toolCall.toolName,
          arguments: toolCall.arguments,
          policy: this.resolveModelToolPolicy(turn, toolCall.toolName),
        };
      }),
      usage,
    };
  }

  /**
   * Runs one function tool call after checking its arguments against the tool's schema, or passes a call that a
   * tool source built in `runModelStep` to that source, with the source's own `toolSourceReference` and without a
   * listing. Any other name is an unknown tool and never reaches a source.
   * @param call - The deferred call.
   * @returns The output: a `string` result unchanged, any other result as JSON; the tool's
   * {@link ToolInputRequested}; or a source's outcome, such as {@link ToolTaskStarted}.
   * @throws {ToolExecutionError} For an unknown tool, arguments that are not a JSON object or break the
   * schema, or a `ToolExecutionError` from the tool or its source.
   * @throws {RangeError} When a source built the call but the definition no longer has it, so the effect decides
   * the outcome.
   * @throws Any other error of the tool source.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    const toolCall = call.toolCall;
    const tool = this.functionToolsByName.get(toolCall.toolName);
    if (tool === undefined) {
      const routed = this.routeToToolSource(toolCall);
      if (routed === undefined) {
        throw new ToolExecutionError(buildUnknownToolError(toolCall.toolName));
      }
      return routed.source.executeDeferredToolCall({ ...call, toolCall: withToolSourceReference(toolCall, routed.reference) });
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
   * Polls a tool source's task once, through the source that built the call.
   * @param poll - The call's record from `PollToolTask`.
   * @returns The source's `pollToolTask` outcome.
   * @throws {RangeError} When no tool source built the call. The task may still run, so `PollToolTask` retries
   * and then records the call by its effect.
   * @throws Any error of the source.
   */
  public async pollToolTask(poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    const routed = this.routeToToolSource(poll.toolCall);
    if (routed === undefined) {
      throw new RangeError(`no tool source built the call of tool ${poll.toolCall.toolName}`);
    }
    return routed.source.pollToolTask({ ...poll, toolCall: withToolSourceReference(poll.toolCall, routed.reference) });
  }

  /**
   * Returns the results of the turn's tool calls that are not stored yet.
   * @param turn - The ended turn, with every record final.
   * @returns One `tool_result` message per unstored result, in deferral order.
   */
  public buildUnfinishedTurnMessages(turn: AgentTurnContext): readonly FrameworkMessage[] {
    return buildUnstoredToolResults(turn).map(toStepwiseFrameworkMessage);
  }

  /**
   * Estimates the model input of the next `runModelStep` with the model client.
   * @param turn - The context of the next model call.
   * @returns `ModelClient.estimateInputTokens` of the request `runModelStep` would send: instructions,
   * summary, messages, unstored tool results, and tools. Tool source tools come from each source's `cachedTools`
   * when it has one, else from the runner's last listing, as in the Python core.
   */
  public async estimateInputTokens(turn: AgentTurnContext): Promise<number> {
    const modelCallCount = readModelCallCount(turn.frameworkRunState);
    const isToolBudgetSpent = modelCallCount >= this.maxModelCallsPerTurn;
    const request = await this.buildModelRequest(
      turn,
      buildUnstoredToolResults(turn),
      modelCallCount,
      isToolBudgetSpent,
      this.readCachedSourceTools(),
    );
    return this.definition.model.estimateInputTokens(request);
  }

  /**
   * Chooses the last message to summarize, at a boundary between model responses.
   *
   * The runner cuts only before a user message or before the first output item of a model response whose
   * earlier tool calls all have stored results, so no tool call is separated from its result and no
   * response is split. It keeps the newest messages that fit the budget, and never summarizes the newest
   * stored message.
   *
   * @param turn - The context of the model call that crossed the threshold.
   * @param retainedTokenBudget - Tokens that the kept messages may use.
   * @returns The sequence of the last message to summarize: the earliest cut whose kept messages fit the
   * budget, else the latest cut; `null` when every message already fits or no cut is possible.
   */
  public selectCompactionCutoff(turn: AgentTurnContext, retainedTokenBudget: number): number | null {
    const items = turn.messages.map(readStoredModelInputItem);
    const cuts = findCompactionCuts(items);
    if (cuts.length === 0) {
      return null;
    }
    const keptTokens = new Array<number>(items.length + 1).fill(0);
    keptTokens[items.length] = this.estimateItems(turn, buildUnstoredToolResults(turn));
    for (let index = items.length - 1; index >= 0; index -= 1) {
      keptTokens[index] = (keptTokens[index + 1] ?? 0) + this.estimateItems(turn, items.slice(index, index + 1));
    }
    if ((keptTokens[0] ?? 0) <= retainedTokenBudget) {
      return null;
    }
    const cut = cuts.find((candidate) => (keptTokens[candidate] ?? 0) <= retainedTokenBudget) ?? cuts.at(-1);
    return cut === undefined ? null : (turn.messages[cut - 1]?.sequence ?? null);
  }

  /**
   * Folds the previous summary and the messages of the range into new text, with one model call.
   *
   * The call uses {@link contextSummaryInstructions}, no tools, the previous summary as a note, the
   * range's items, and {@link contextSummaryRequest} as the last user text. Its text streams nowhere,
   * because it is not the assistant's answer. When the request would exceed `request.inputTokenBudget`,
   * such as after a smaller context window or when compaction starts on a long session, the runner leaves
   * out the oldest items of the range and says so with {@link buildContextSummaryOmissionNote}, so the
   * call and its `maxOutputTokens`, when set, still fit. It leaves them out up to a boundary between model responses, as
   * {@link selectCompactionCutoff} cuts, so it never splits a response or separates a tool call from its
   * result.
   *
   * @param request - The previous summary and the messages to summarize.
   * @returns The response text and the usage of the call.
   * @throws {Error} When the model returned no text, or any error of the model call.
   */
  public async summarizeContext(request: ContextSummaryRequest): Promise<ContextSummaryResult> {
    const rangeItems = request.messages.map(readStoredModelInputItem);
    let omittedCount = 0;
    const budget =
      request.inputTokenBudget === undefined
        ? undefined
        : // The window also holds the summary the call writes.
          request.inputTokenBudget - (this.definition.modelSettings?.maxOutputTokens ?? 0);
    if (budget !== undefined) {
      // Estimate by parts, then leave out the oldest items up to the first cut that fits.
      const model = this.definition.model;
      const itemTokens = rangeItems.map((item) =>
        model.estimateInputTokens({ ...this.buildSummaryRequest(request, [], 0), instructions: "", inputItems: [item] }),
      );
      let tokens =
        model.estimateInputTokens(this.buildSummaryRequest(request, [], rangeItems.length)) +
        itemTokens.reduce((total, itemToken) => total + itemToken, 0);
      // A cut never splits a model response or separates a tool call from its result.
      for (const cut of [...findCompactionCuts(rangeItems), rangeItems.length]) {
        if (tokens <= budget) {
          break;
        }
        tokens -= itemTokens.slice(omittedCount, cut).reduce((total, itemToken) => total + itemToken, 0);
        omittedCount = cut;
      }
    }
    const response = await this.definition.model.callModel(
      this.buildSummaryRequest(request, rangeItems.slice(omittedCount), omittedCount),
      discardedModelOutput,
    );
    requireModelResponse(response);
    if (response.text.trim() === "") {
      throw new Error("the summary model call returned no text");
    }
    return { text: response.text.trim(), usage: { ...response.usage, modelCalls: 1 } };
  }

  private buildSummaryRequest(
    request: ContextSummaryRequest,
    rangeItems: readonly ModelInputItem[],
    omittedCount: number,
  ): ModelRequest {
    const items: ModelInputItem[] = [];
    if (request.previousSummary !== null) {
      items.push({ type: "user_text", text: buildContextSummaryNote(request.previousSummary) });
    }
    if (omittedCount > 0) {
      items.push({ type: "user_text", text: buildContextSummaryOmissionNote(omittedCount) });
    }
    items.push(...rangeItems, { type: "user_text", text: contextSummaryRequest });
    return {
      instructions: contextSummaryInstructions,
      inputItems: pairToolCallsWithResults(items),
      tools: [],
      toolChoice: "none",
      settings: this.definition.modelSettings ?? {},
      metadata: {
        flowId: request.flowId,
        turnId: request.turnId,
        stepExecutionId: request.stepExecutionId,
        attempt: request.attempt,
      },
      cancellationSignal: request.cancellationSignal,
    };
  }

  private async buildModelRequest(
    turn: AgentTurnContext,
    unstoredToolResults: readonly ModelToolResultItem[],
    modelCallCount: number,
    isToolBudgetSpent: boolean,
    sourceTools: ListedSourceTools,
  ): Promise<ModelRequest> {
    const contextSummary = turn.contextSummary ?? null;
    const summaryItems: ModelInputItem[] =
      contextSummary === null ? [] : [{ type: "user_text", text: buildContextSummaryNote(contextSummary) }];
    let toolChoice: ModelToolChoice = isToolBudgetSpent ? "none" : "auto";
    if (!isToolBudgetSpent && this.definition.planningToolChoice === "required" && mustWritePlanFirst(turn, modelCallCount)) {
      toolChoice = { toolName: "write_todos" };
    }
    return {
      instructions: await this.resolveInstructions(turn),
      // The plan note comes after the summary, so compaction never hides the plan.
      inputItems: pairToolCallsWithResults([
        ...summaryItems,
        ...readStoredInputItemsWithPlanNote(turn),
        ...unstoredToolResults,
      ]),
      tools: isToolBudgetSpent ? [] : this.listOfferedTools(turn, sourceTools),
      toolChoice,
      settings: this.definition.modelSettings ?? {},
      metadata: modelRequestMetadata(turn),
      cancellationSignal: turn.cancellationSignal,
    };
  }

  private estimateItems(turn: AgentTurnContext, items: readonly ModelInputItem[]): number {
    if (items.length === 0) {
      return 0;
    }
    return this.definition.model.estimateInputTokens({
      instructions: "",
      inputItems: items,
      tools: [],
      toolChoice: "none",
      settings: this.definition.modelSettings ?? {},
      metadata: modelRequestMetadata(turn),
      cancellationSignal: turn.cancellationSignal,
    });
  }

  /** A built-in enabled only in the session options must not share a function tool's name. */
  private requireNoBuiltInToolNameClash(turn: AgentTurnContext): void {
    for (const builtInTool of turn.enabledBuiltInTools ?? []) {
      if (this.functionToolsByName.has(builtInTool)) {
        throw new TypeError(`tool ${builtInTool} uses the name of an enabled built-in tool`);
      }
    }
  }

  private async listSourceTools(turn: AgentTurnContext): Promise<ListedSourceTools> {
    const enabledBuiltInTools: ReadonlySet<string> = turn.enabledBuiltInTools ?? new Set<string>();
    const functionToolNames = new Set(this.functionToolsByName.keys());
    const listed = new Map<string, ListedSourceTool>();
    for (const [index, source] of this.toolSources.entries()) {
      for (const tool of await this.listOneSource(index, source)) {
        refuseSourceToolName(tool.name, functionToolNames, enabledBuiltInTools, listed);
        listed.set(tool.name, { index, source, tool });
      }
    }
    return listed;
  }

  private async listOneSource(index: number, source: ToolSource): Promise<readonly SourceToolDefinition[]> {
    const listing = source.listTools();
    // A listing that outlives this call keeps running; nobody else reads its error.
    listing.catch(() => undefined);
    let timer: NodeJS.Timeout | undefined;
    const timedOut = new Promise<"timed_out">((resolve) => {
      timer = setTimeout(() => resolve("timed_out"), this.toolSourceListingTimeoutMs);
    });
    try {
      const tools = await Promise.race([listing, timedOut]);
      if (tools === "timed_out") {
        console.warn(`tool source ${index} listed nothing within ${this.toolSourceListingTimeoutMs} ms`);
        return [];
      }
      this.lastListings.set(index, tools);
      return tools;
    } catch (error: unknown) {
      console.warn(`tool source ${index} could not list its tools: ${error instanceof Error ? error.message : String(error)}`);
      return [];
    } finally {
      clearTimeout(timer);
    }
  }

  private readCachedSourceTools(): ListedSourceTools {
    const cached = new Map<string, ListedSourceTool>();
    for (const [index, source] of this.toolSources.entries()) {
      const tools = source.cachedTools?.() ?? this.lastListings.get(index) ?? [];
      for (const tool of tools) {
        if (!cached.has(tool.name)) {
          cached.set(tool.name, { index, source, tool });
        }
      }
    }
    return cached;
  }

  private routeToToolSource(
    toolCall: ToolSourceRoutedCall,
  ): { readonly source: ToolSource; readonly reference: string | undefined } | undefined {
    const envelope = readToolSourceEnvelope(toolCall.toolSourceReference);
    if (envelope !== undefined) {
      const source = this.toolSourcesByName.get(envelope.sourceName);
      if (source === undefined) {
        throw new RangeError(
          `tool source ${envelope.sourceName}, which built the call of tool ${toolCall.toolName}, is not configured on this Worker`,
        );
      }
      return { source, reference: envelope.reference };
    }
    const reference = toolCall.toolSourceReference ?? undefined;
    if (reference === undefined || this.toolSources.length === 0) {
      return undefined;
    }
    const onlySource = this.toolSources.length === 1 ? this.toolSources[0] : undefined;
    if (onlySource !== undefined) {
      // A reference the source built itself, such as in a runner subclass.
      return { source: onlySource, reference };
    }
    throw new RangeError(`cannot tell which tool source built the call of ${toolCall.toolName}`);
  }

  private listOfferedTools(turn: AgentTurnContext, sourceTools: ListedSourceTools): ModelToolDefinition[] {
    const isPlanning = turn.interactionMode === "planning";
    const enabledBuiltInTools = turn.enabledBuiltInTools ?? new Set<BuiltInTool>();
    const builtInTools = builtInToolDefinitions
      .filter((definition) => enabledBuiltInTools.has(definition.name))
      .filter((definition) => !isPlanning || definition.name === "write_todos")
      .map((definition) => ({
        name: definition.name,
        description: definition.description,
        parameters: definition.parameters,
      }));
    if (isPlanning) {
      return builtInTools;
    }
    const functionTools = [...this.functionToolsByName.values()].map((tool) => ({
      name: tool.name,
      description: tool.description,
      parameters: tool.parameters,
    }));
    const offeredSourceTools = [...sourceTools.values()].map(({ tool }) => ({
      name: tool.name,
      description: tool.description,
      parameters: tool.inputSchema,
    }));
    return [...functionTools, ...offeredSourceTools, ...builtInTools];
  }

  /** A session policy wins; otherwise a function tool's own effect and approval apply, as in the Python core. */
  private resolveModelToolPolicy(turn: AgentTurnContext, toolName: string): ResolvedToolPolicy {
    const tool = this.functionToolsByName.get(toolName);
    const isBuiltIn = (turn.enabledBuiltInTools as ReadonlySet<string> | undefined)?.has(toolName) === true;
    if (tool === undefined && this.toolSources.length > 0 && !isBuiltIn) {
      return unlistedSourceToolPolicy;
    }
    const sessionPolicies = turn.toolPolicies ?? this.definition.toolPolicies ?? {};
    if (Object.hasOwn(sessionPolicies, toolName)) {
      return turn.resolveToolPolicy(toolName);
    }
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
    const resolved = await instructions({
      flowId: turn.flowId,
      turnId: turn.turnId,
      attempt: turn.attempt,
      interactionMode: turn.interactionMode ?? "chat",
      plan: turn.plan ?? null,
    });
    if (typeof resolved !== "string") {
      throw new TypeError(`the instructions of agent ${this.definition.name} must resolve to a string`);
    }
    return resolved;
  }
}

/** A planning turn without a draft drafts one first; feedback on a draft may only answer. */
function mustWritePlanFirst(turn: AgentTurnContext, modelCallCount: number): boolean {
  return (
    turn.interactionMode === "planning" &&
    modelCallCount === 0 &&
    turn.enabledBuiltInTools?.has("write_todos") === true &&
    turn.plan?.status !== "draft"
  );
}

function readStoredInputItemsWithPlanNote(turn: AgentTurnContext): ModelInputItem[] {
  const items = turn.messages.map(readStoredModelInputItem);
  const planNote = buildPlanNote(turn.plan, turn.interactionMode);
  if (planNote === undefined) {
    return items;
  }
  // Before the turn's messages, so a plan change rewrites only the turn's tail of the input.
  const turnStart = turn.messages.findIndex((message) => message.turnId === turn.turnId);
  items.splice(turnStart === -1 ? items.length : turnStart, 0, { type: "user_text", text: planNote });
  return items;
}

/** Results of the turn's final records that no stored `tool_result` message holds yet, in deferral order. */
function buildUnstoredToolResults(turn: AgentTurnContext): ModelToolResultItem[] {
  const turnMessages = turn.messages.filter((message) => message.turnId === turn.turnId);
  const storedResultIds = new Set(
    turnMessages.filter((message) => message.role === "tool").map((message) => message.toolCallId),
  );
  // A call summarized away was summarized with its result, so only shown calls can lack one.
  const shownCallIds =
    (turn.contextSummary ?? null) === null
      ? undefined
      : new Set(
          turnMessages
            .map(readStoredModelInputItem)
            .flatMap((item) => (item.type === "model_output" ? item.toolCalls : []))
            .map((toolCall) => toolCall.toolCallId),
        );
  return [...turn.toolCalls]
    .sort((left, right) => left.deferralOrder - right.deferralOrder)
    .filter((record) => shownCallIds === undefined || shownCallIds.has(record.toolCallId))
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

/** Each index that may start the kept items, ascending; never the last item's index. */
function findCompactionCuts(items: readonly ModelInputItem[]): number[] {
  const cuts: number[] = [];
  const openCallIds = new Set<string>();
  for (let index = 0; index < items.length - 1; index += 1) {
    const item = items[index];
    const following = items[index + 1];
    if (item === undefined || following === undefined) {
      continue;
    }
    if (item.type === "model_output") {
      for (const toolCall of item.toolCalls) {
        openCallIds.add(toolCall.toolCallId);
      }
    } else if (item.type === "tool_result") {
      openCallIds.delete(item.toolCallId);
    } else {
      // User text closes calls left open, as pairToolCallsWithResults does.
      openCallIds.clear();
    }
    if (openCallIds.size > 0 || following.type === "tool_result") {
      continue;
    }
    // Consecutive output items are one model response, which must stay whole.
    if (item.type === "model_output" && following.type === "model_output") {
      continue;
    }
    cuts.push(index + 1);
  }
  return cuts;
}

function modelRequestMetadata(turn: AgentTurnContext): ModelRequestMetadata {
  return {
    flowId: turn.flowId,
    turnId: turn.turnId,
    stepExecutionId: turn.stepExecutionId ?? "",
    attempt: turn.attempt,
  };
}

function refuseSourceToolName(
  toolName: string,
  functionToolNames: ReadonlySet<string>,
  builtInTools: ReadonlySet<string>,
  otherSourceTools: ReadonlyMap<string, unknown>,
): void {
  if (builtInTools.has(toolName)) {
    throw new TypeError(`tool ${toolName} uses the name of an enabled built-in tool`);
  }
  if (functionToolNames.has(toolName)) {
    throw new TypeError(`tool ${toolName} of a tool source uses the name of a function tool`);
  }
  if (otherSourceTools.has(toolName)) {
    throw new TypeError(`two tool sources list a tool named ${toolName}`);
  }
}

function refuseDeclaredToolNameCollisions(
  toolSources: readonly ToolSource[],
  functionToolNames: ReadonlySet<string>,
  builtInTools: ReadonlySet<string>,
): void {
  const declared = new Map<string, ToolSource>();
  for (const source of toolSources) {
    const names = [...(source.declaredToolNames?.() ?? [])].sort();
    for (const toolName of names) {
      refuseSourceToolName(toolName, functionToolNames, builtInTools, declared);
    }
    for (const toolName of names) {
      declared.set(toolName, source);
    }
  }
}

/** Error of a call to a tool that no function tool or source listing offers; the same text as the Python core. */
export function buildUnknownToolError(toolName: string): string {
  return `unknown tool ${toolName}`;
}

/** The runner's `toolSourceReference`: the source's name and its own reference, as JSON text. */
export function writeToolSourceEnvelope(sourceName: string, reference: string | undefined): string {
  return JSON.stringify({ [toolSourceNameKey]: sourceName, [toolSourceReferenceKey]: reference ?? null });
}

export function readToolSourceEnvelope(
  toolSourceReference: string | undefined,
): { readonly sourceName: string; readonly reference: string | undefined } | undefined {
  if (toolSourceReference === undefined || toolSourceReference === null) {
    return undefined;
  }
  let envelope: unknown;
  try {
    envelope = JSON.parse(toolSourceReference);
  } catch {
    return undefined;
  }
  if (typeof envelope !== "object" || envelope === null || Array.isArray(envelope)) {
    return undefined;
  }
  const sourceName = (envelope as Record<string, unknown>)[toolSourceNameKey];
  const reference = (envelope as Record<string, unknown>)[toolSourceReferenceKey];
  if (typeof sourceName !== "string") {
    return undefined;
  }
  if (reference !== null && reference !== undefined && typeof reference !== "string") {
    return undefined;
  }
  return { sourceName, reference: reference ?? undefined };
}

interface ToolSourceRoutedCall {
  readonly toolName: string;
  readonly toolSourceReference?: string;
}

/** The record as the source built it: its own reference instead of the runner's envelope. */
function withToolSourceReference<Call extends ToolSourceRoutedCall>(toolCall: Call, reference: string | undefined): Call {
  const { toolSourceReference: _envelope, ...rest } = toolCall;
  return (reference === undefined ? rest : { ...rest, toolSourceReference: reference }) as Call;
}
