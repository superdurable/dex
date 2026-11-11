// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { builtInToolDefinitions, type BuiltInTool } from "../agent-built-in-tools.js";
import {
  buildContextSummaryNote,
  buildContextSummaryOmissionNote,
  contextSummaryInstructions,
  contextSummaryRequest,
  type ContextSummaryRequest,
  type ContextSummaryResult,
} from "../agent-context-compaction.js";

export {
  buildContextSummaryNote,
  buildContextSummaryOmissionNote,
  contextSummaryInstructions,
  contextSummaryNoteHeader,
  contextSummaryRequest,
} from "../agent-context-compaction.js";
import {
  ToolExecutionError,
  type AgentFrameworkRunner,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolCall,
  type DeferredToolExecution,
  type DeferredToolOutcome,
  type ResolvedToolPolicy,
  type ToolCallStepOptions,
  type ToolTaskCancellation,
  type ToolTaskPoll,
  type ToolTaskPollOutcome,
} from "../agent-framework-runner.js";
import { loadImages, type MediaStore } from "../agent-media.js";
import { buildPlanNote } from "../agent-plans.js";
import {
  describeToolCallOutcome,
  emptyAgentUsage,
  type AgentImageReference,
  type AgentToolCall,
  type FrameworkMessage,
  type SessionSettings,
} from "../agent-session-values.js";
import type { AgentDefinition, ContinueTurn, TurnContinuation } from "./agent-definition.js";
import type { FunctionTool, ToolCallContext } from "./function-tool.js";
import { findJsonSchemaViolation, serializeJsonWithSpacedSeparators } from "./json-schema-validation.js";
import type {
  ModelClient,
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
  toBuiltinLoopFrameworkMessage,
} from "./builtin-loop-input-items.js";
import {
  ToolSourceRouter,
  defaultToolSourceListingTimeoutMs,
  readToolSourceEnvelope as readRouterToolSourceEnvelope,
  writeToolSourceEnvelope as writeRouterToolSourceEnvelope,
  type ListedSourceTool,
} from "../tool-source-router.js";

export { defaultToolSourceListingTimeoutMs } from "../tool-source-router.js";

type ListedSourceTools = ReadonlyMap<string, ListedSourceTool>;
// The runner's envelope around a source's toolSourceReference: the building source's name.
const toolSourceNameKey = "stepwise_tool_source";
// An unlisted name never reaches a source, so it fails without asking anyone to approve it.
const unlistedSourceToolPolicy: ResolvedToolPolicy = { effect: "unclassified", requiresApproval: false };

export const defaultMaxModelCallsPerTurn = 100;

/**
 * How many {@link ContinueTurn} results of {@link AgentDefinition.decideTurnContinuation} may follow answers without
 * tool calls in a row. A model response with tool calls resets the count.
 * @since 0.5.0
 */
export const maxConsecutiveTurnContinuations = 8;

/** Result of a tool call the model made after the turn's model call limit; the same text as the Python core. */
export function buildToolCallLimitReachedResult(maxModelCallsPerTurn: number): string {
  return `The tool call was not run: this turn reached its limit of ${maxModelCallsPerTurn} model calls.`;
}

/**
 * The `lastTurnError` of a turn whose `decideTurnContinuation` returned a {@link ContinueTurn} once too often in a
 * row; the same text as the Python core.
 * @param limit - {@link maxConsecutiveTurnContinuations}.
 * @returns The error text.
 */
export function buildTurnContinuationLimitError(limit: number): string {
  return `the turn reached its limit of ${limit} consecutive continuations without a tool call`;
}

/** The runner's `FrameworkRunState`; `consecutive_continuations` is absent until a turn first continues. */
interface BuiltinLoopRunState {
  readonly modelCalls: number;
  readonly consecutiveContinuations: number;
}

// The summary is not the assistant's answer, so its live text goes nowhere.
const discardedModelOutput: ModelOutputWriter = {
  writeTextDelta: () => undefined,
  writeReasoningDelta: () => undefined,
};

/**
 * The {@link AgentFrameworkRunner} of the built-in agent loop.
 *
 * {@link createBuiltinAgentSessionFlow} creates one; construct it directly only to pass it to
 * `createAgentSessionFlow` with options of your own. Each `RunModel` makes at most one model call
 * through {@link AgentDefinition.model}, from committed state only: the stored messages, the results
 * of the previous model response from the turn's final records, and the instructions. The runner
 * stores the turn's transcript as it goes through {@link ToolCallsRequested.newMessages}, so a long turn
 * never rewrites a growing run state. It supports plan mode: a `planning` turn offers only `write_todos`, and
 * every model call shows the plan with {@link buildPlanNote} before the turn's messages. It implements the
 * compaction methods, so a session with a compaction policy starts the model input with the summary. Its
 * {@link pollToolTask} passes a tool task to the {@link ToolSource} that started it; function tools start no
 * tasks. Its {@link cancelToolTask} passes a task that `cancel_turn` abandoned to that source's optional
 * `cancelToolTask`.
 *
 * With a media store, the runner shows images to the model. It then has `buildUserMessageWithImages` of
 * {@link AgentFrameworkRunner}, as an own property, so `send_user_message` accepts images: the stored `user_text` item keeps their references after the text. A tool
 * result item keeps the references of `AgentToolCall.images`. Before each model call, the runner reads the bytes of
 * every image in the request with `loadImages` and passes them in `ModelRequest.imageBytes`; the model client
 * converts each image to the provider's image part. A failed read fails that `RunModel` attempt, which retries. The
 * model call that writes a context summary sees the text of the items only. Without a store the session refuses
 * user images, and a tool result's images are stored but not shown to the model. The class declares neither member,
 * so a subclass may still define its own `buildUserMessageWithImages` or `mediaStore`; a subclass method stays in use
 * with a store.
 *
 * @example
 * ```ts
 * const runner = new BuiltinLoopAgentRunner(definition);
 * const flow = createAgentSessionFlow(runner, "SupportAgent", { builtInTools: ["ask_user"] });
 * ```
 *
 * It replaces `StepwiseAgentRunner`, which `@superdurable/dex-agents/stepwise` keeps as a deprecated alias.
 *
 * @since 0.5.0
 */
export class BuiltinLoopAgentRunner implements AgentFrameworkRunner<DeferredToolOutcome> {
  /** The agent definition, frozen at construction. */
  public readonly definition: AgentDefinition;
  /** The runner honors {@link AgentTurnContext.interactionMode}, so plan mode can be enabled. */
  public readonly supportsPlanMode = true;
  private readonly functionToolsByName: ReadonlyMap<string, FunctionTool>;
  private readonly toolSources: ToolSourceRouter;
  private readonly maxModelCallsPerTurn: number;
  // An ES private field, so it never collides with a subclass member.
  readonly #mediaStore: MediaStore | undefined;

  /**
   * Creates the runner and checks the definition.
   * @param definition - The agent definition.
   * @param mediaStore - The store that holds the session's images, such as `S3MediaStore`. With it, the runner
   * accepts user messages with images and shows them and tool result images to the model, reading the bytes for
   * each model call; stored messages hold only references. It also gets an own `buildUserMessageWithImages`, which
   * stores user text and its images as one `user_text` item that holds the references, not the bytes, unless a
   * subclass defines its own `buildUserMessageWithImages`, which then stays in use. Use the store
   * the clients and tools upload to. Omit it to refuse user images and show tool result images to the model as text
   * only. Since 0.5.0.
   *
   * Changed in 0.5.0: takes `mediaStore`.
   * @throws {TypeError} When the name, instructions, model, tools, or `decideTurnContinuation` are invalid, two tools
   * share a name, a function tool uses the name of an enabled built-in tool, `planModeEnabled` is set without
   * `write_todos`, `planningToolChoice` is neither `auto` nor `required`, or a tool source lacks a
   * {@link ToolSource} method.
   * @throws {RangeError} When `maxModelCallsPerTurn` is not a positive integer.
   */
  public constructor(definition: AgentDefinition, mediaStore: MediaStore | undefined = undefined) {
    if (typeof definition?.name !== "string" || definition.name.trim() === "") {
      throw new TypeError("AgentDefinition.name must be a non-blank string");
    }
    if (typeof definition.instructions !== "string" && typeof definition.instructions !== "function") {
      throw new TypeError("AgentDefinition.instructions must be a string or a function");
    }
    if (typeof definition.model?.callModel !== "function" || typeof definition.model.estimateInputTokens !== "function") {
      throw new TypeError("AgentDefinition.model must be a ModelClient");
    }
    if (definition.resolveModel !== undefined && typeof definition.resolveModel !== "function") {
      throw new TypeError("AgentDefinition.resolveModel must be a function");
    }
    if (definition.decideTurnContinuation !== undefined && typeof definition.decideTurnContinuation !== "function") {
      throw new TypeError("AgentDefinition.decideTurnContinuation must be a function");
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
    const toolSourceListingTimeoutMs = definition.toolSourceListingTimeoutMs ?? defaultToolSourceListingTimeoutMs;
    if (!(toolSourceListingTimeoutMs > 0)) {
      throw new RangeError("AgentDefinition.toolSourceListingTimeoutMs must be positive");
    }
    const toolSources = new ToolSourceRouter(definition.toolSources ?? [], {
      sourceNameKey: toolSourceNameKey,
      localToolNames: new Set(functionToolsByName.keys()),
      localToolDescription: "a function tool",
      builtInToolNames: enabledBuiltInTools,
      listingTimeoutMs: toolSourceListingTimeoutMs,
    });
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
    this.maxModelCallsPerTurn = maxModelCallsPerTurn;
    this.#mediaStore = mediaStore;
    if (mediaStore !== undefined) {
      // The session accepts images only from a runner with this method; the class leaves it undeclared for subclasses.
      const runner: AgentFrameworkRunner<DeferredToolOutcome> = this;
      // A subclass's own method is already on the prototype chain, and it stays in use.
      runner.buildUserMessageWithImages ??= (text: string, images: readonly AgentImageReference[]): FrameworkMessage =>
        toBuiltinLoopFrameworkMessage({ type: "user_text", text, images: [...images] });
    }
  }

  /**
   * Converts user text into a stored `user_text` item.
   * @param text - User message text.
   * @returns The message with role `user`.
   */
  public buildUserMessage(text: string): FrameworkMessage {
    return toBuiltinLoopFrameworkMessage({ type: "user_text", text });
  }

  /**
   * Makes the turn's next model call.
   * @param turn - The turn context.
   * @returns {@link TurnCompleted} with the results and final answer not stored yet, or
   * {@link ToolCallsRequested} with the previous results, this response, and its tool calls. When
   * {@link AgentDefinition.decideTurnContinuation} returns `"end"` after tool calls, a {@link TurnCompleted} with only
   * the results and zero usage, without a model call. When it continues after tool calls, its message follows the
   * results in the request and in the stored messages. When it continues after an answer, a {@link TurnContinued}
   * with the previous results, this response, and the message as user text, or a {@link TurnCompleted} with
   * {@link TurnCompleted.turnError} once {@link maxConsecutiveTurnContinuations} is exceeded.
   * @throws {TypeError} When a function tool uses the name of an enabled built-in tool, or a tool source lists a
   * name that repeats a function tool, an enabled built-in tool, or another source's tool. No model request is
   * made. Also when `decideTurnContinuation` returns another value.
   * @throws Any error of the model call, of the media store, of a tool source's `listTools`, or of
   * `decideTurnContinuation`, which retries the `RunModel` attempt.
   */
  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    this.requireNoBuiltInToolNameClash(turn);
    const runState = readBuiltinLoopRunState(turn.frameworkRunState);
    const modelCallCount = runState.modelCalls;
    const unstoredToolResults = buildUnstoredToolResults(turn);
    const unstoredItems: ModelInputItem[] = [...unstoredToolResults];
    if (unstoredToolResults.length > 0 && this.definition.decideTurnContinuation !== undefined) {
      const decision = await this.decideTurnContinuation(turn, readLatestModelOutput(turn), unstoredToolResults);
      if (decision === "end") {
        return {
          kind: "completed",
          newMessages: unstoredToolResults.map(toBuiltinLoopFrameworkMessage),
          usage: emptyAgentUsage(),
        };
      }
      if (decision !== "default") {
        unstoredItems.push({ type: "user_text", text: decision.message });
      }
    }
    const isToolBudgetSpent = modelCallCount >= this.maxModelCallsPerTurn;
    const sourceTools: ListedSourceTools =
      isToolBudgetSpent || turn.interactionMode === "planning"
        ? new Map()
        : await this.toolSources.listSourceTools(turn.enabledBuiltInTools ?? new Set<string>());
    const request = await this.buildModelRequest(turn, unstoredItems, modelCallCount, isToolBudgetSpent, sourceTools);
    const response = await this.resolveModel(turn.sessionSettings).callModel(
      await this.addImageBytes(request),
      turn.events,
    );
    requireModelResponse(response);
    const usage = { ...response.usage, modelCalls: 1 };
    const newMessages = [...unstoredItems, ...response.outputItems].map(toBuiltinLoopFrameworkMessage);
    if (response.toolCalls.length === 0) {
      const decision = await this.decideTurnContinuation(turn, response.outputItems, []);
      if (decision === "end" || decision === "default") {
        return { kind: "completed", newMessages, usage };
      }
      const consecutiveContinuations = runState.consecutiveContinuations + 1;
      if (consecutiveContinuations > maxConsecutiveTurnContinuations) {
        return {
          kind: "completed",
          newMessages,
          usage,
          turnError: buildTurnContinuationLimitError(maxConsecutiveTurnContinuations),
        };
      }
      return {
        kind: "turn_continued",
        frameworkRunState: serializeJsonWithSpacedSeparators({
          model_calls: modelCallCount + 1,
          consecutive_continuations: consecutiveContinuations,
        }),
        // The message makes the next request end with user text, as every provider requires.
        newMessages: [...newMessages, toBuiltinLoopFrameworkMessage({ type: "user_text", text: decision.message })],
        usage,
      };
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
        newMessages: [...newMessages, ...notRunResults.map(toBuiltinLoopFrameworkMessage)],
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
          return this.toolSources.buildDeferredToolCall(turn, listed, toolCall);
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
   * Runs one function tool call after converting its arguments with the tool's `prepareArguments`, when it has
   * one, and checking them against the tool's schema, or passes a call that a
   * tool source built in `runModelStep` to that source, with the source's own `toolSourceReference` and without a
   * listing. Any other name is an unknown tool and never reaches a source.
   * @param call - The deferred call.
   * @returns The output: a `string` result unchanged, any other result as JSON; the tool's
   * {@link ToolInputRequested}; or a source's outcome, such as {@link ToolTaskStarted}.
   * @throws {ToolExecutionError} For an unknown tool, arguments that are not a JSON object or break the
   * schema, a non-cancellation error or a thenable result of the tool's `prepareArguments`, or a
   * `ToolExecutionError` from the tool or its source.
   * @throws {RangeError} When a source built the call but the definition no longer has it, so the effect decides
   * the outcome.
   * @throws Any other error of the tool source.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    const toolCall = call.toolCall;
    const tool = this.functionToolsByName.get(toolCall.toolName);
    if (tool === undefined) {
      if (!this.toolSources.isSourceToolCall(toolCall)) {
        throw new ToolExecutionError(buildUnknownToolError(toolCall.toolName));
      }
      return this.toolSources.executeDeferredToolCall(call);
    }
    const toolArguments = prepareToolArguments(tool, toolCall.arguments, call.cancellationSignal);
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
      effect: toolCall.effect,
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
    return this.toolSources.pollToolTask(poll);
  }

  /**
   * Returns a tool source's options for one call's tool Step. Function tools and sources without
   * `toolCallStepOptions` keep the tool Steps' defaults.
   * @param toolCall - The record whose tool Step starts.
   * @returns The source's options, or `undefined` for the defaults.
   * @throws {RangeError} When a source built the call but the definition no longer has it; the session then uses
   * the defaults.
   * @since 0.4.0
   */
  public toolCallStepOptions(toolCall: AgentToolCall): ToolCallStepOptions | undefined {
    if (this.functionToolsByName.has(toolCall.toolName)) {
      return undefined;
    }
    return this.toolSources.toolCallStepOptions(toolCall);
  }

  /**
   * Cancels a tool source's task through the source that built the call; a source without `cancelToolTask` leaves
   * its task running.
   * @param cancellation - The abandoned call's record from `CancelToolTask`.
   * @returns A Promise that resolves once the source took the cancellation.
   * @throws {RangeError} When no tool source built the call; `CancelToolTask` retries and then records the call
   * `outcome_unknown`.
   * @throws Any error of the source.
   */
  public async cancelToolTask(cancellation: ToolTaskCancellation): Promise<void> {
    await this.toolSources.cancelToolTask(cancellation);
  }

  /**
   * Returns the results of the turn's tool calls that are not stored yet.
   * @param turn - The ended turn, with every record final.
   * @returns One `tool_result` message per unstored result, in deferral order.
   */
  public buildUnfinishedTurnMessages(turn: AgentTurnContext): readonly FrameworkMessage[] {
    return buildUnstoredToolResults(turn).map(toBuiltinLoopFrameworkMessage);
  }

  /**
   * Estimates the model input of the next `runModelStep` with the model client.
   * @param turn - The context of the next model call.
   * @returns `ModelClient.estimateInputTokens` of the request `runModelStep` would send: instructions,
   * summary, messages, unstored tool results, and tools. Tool source tools come from each source's `cachedTools`
   * when it has one, else from the runner's last listing, as in the Python core.
   */
  public async estimateInputTokens(turn: AgentTurnContext): Promise<number> {
    const modelCallCount = readBuiltinLoopRunState(turn.frameworkRunState).modelCalls;
    const isToolBudgetSpent = modelCallCount >= this.maxModelCallsPerTurn;
    const request = await this.buildModelRequest(
      turn,
      buildUnstoredToolResults(turn),
      modelCallCount,
      isToolBudgetSpent,
      this.toolSources.readCachedSourceTools(),
    );
    return this.resolveModel(turn.sessionSettings).estimateInputTokens(this.keepShownImagesOnly(request));
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
    const model = this.resolveModel(request.sessionSettings);
    const rangeItems = request.messages.map(readStoredModelInputItem);
    let omittedCount = 0;
    const budget =
      request.inputTokenBudget === undefined
        ? undefined
        : // The window also holds the summary the call writes.
          request.inputTokenBudget - (this.definition.modelSettings?.maxOutputTokens ?? 0);
    if (budget !== undefined) {
      // Estimate by parts, then leave out the oldest items up to the first cut that fits.
      const itemTokens = rangeItems.map((item) =>
        model.estimateInputTokens({
          ...this.buildSummaryRequest(request, [], 0),
          instructions: "",
          inputItems: stripImages([item]),
        }),
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
    const response = await model.callModel(
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
    // The summary call reads no image bytes, so it sees each item's text only.
    items.push(...stripImages(rangeItems), { type: "user_text", text: contextSummaryRequest });
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
    unstoredItems: readonly ModelInputItem[],
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
        ...unstoredItems,
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
    return this.resolveModel(turn.sessionSettings).estimateInputTokens({
      instructions: "",
      inputItems: this.#mediaStore === undefined ? stripImages(items) : items,
      tools: [],
      toolChoice: "none",
      settings: this.definition.modelSettings ?? {},
      metadata: modelRequestMetadata(turn),
      cancellationSignal: turn.cancellationSignal,
    });
  }

  /** Returns the request with the bytes of its images, read from the store right now. */
  private async addImageBytes(request: ModelRequest): Promise<ModelRequest> {
    if (this.#mediaStore === undefined) {
      return this.keepShownImagesOnly(request);
    }
    const references = request.inputItems.flatMap((item) =>
      item.type === "model_output" ? [] : (item.images ?? []),
    );
    if (references.length === 0) {
      return request;
    }
    return { ...request, imageBytes: await loadImages(this.#mediaStore, references) };
  }

  private keepShownImagesOnly(request: ModelRequest): ModelRequest {
    // Without a store the model sees no image, such as a tool source's.
    return this.#mediaStore === undefined ? { ...request, inputItems: stripImages(request.inputItems) } : request;
  }

  /** A built-in enabled only in the session options must not share a function tool's name. */
  private requireNoBuiltInToolNameClash(turn: AgentTurnContext): void {
    for (const builtInTool of turn.enabledBuiltInTools ?? []) {
      if (this.functionToolsByName.has(builtInTool)) {
        throw new TypeError(`tool ${builtInTool} uses the name of an enabled built-in tool`);
      }
    }
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
    if (tool === undefined && this.toolSources.hasToolSources && !isBuiltIn) {
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

  private resolveModel(sessionSettings: SessionSettings | undefined): ModelClient {
    const resolveModel = this.definition.resolveModel;
    if (resolveModel === undefined) {
      return this.definition.model;
    }
    const model = resolveModel(sessionSettings ?? {});
    if (typeof model?.callModel !== "function" || typeof model.estimateInputTokens !== "function") {
      throw new TypeError(`resolveModel of agent ${this.definition.name} must return a ModelClient`);
    }
    return model;
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
      sessionSettings: turn.sessionSettings ?? {},
    });
    if (typeof resolved !== "string") {
      throw new TypeError(`the instructions of agent ${this.definition.name} must resolve to a string`);
    }
    return resolved;
  }

  /** Returns the hook's decision for one iteration, or `"default"` without a hook. */
  private async decideTurnContinuation(
    turn: AgentTurnContext,
    modelOutput: readonly ModelOutputItem[],
    toolResults: readonly ModelToolResultItem[],
  ): Promise<TurnContinuation> {
    const decide = this.definition.decideTurnContinuation;
    if (decide === undefined) {
      return "default";
    }
    const decision: unknown = await decide({
      flowId: turn.flowId,
      turnId: turn.turnId,
      sessionSettings: turn.sessionSettings ?? {},
      modelOutput,
      toolResults,
    });
    if (decision === "end" || decision === "default") {
      return decision;
    }
    const continuation = decision as Partial<ContinueTurn> | null;
    if (
      typeof continuation === "object" &&
      continuation !== null &&
      continuation.action === "continue" &&
      typeof continuation.message === "string" &&
      continuation.message.trim() !== ""
    ) {
      return { action: "continue", message: continuation.message };
    }
    throw new TypeError(
      `decideTurnContinuation of agent ${this.definition.name} must return "end", "default", or ` +
        `{ action: "continue", message } with a non-blank message, not ${describeTurnContinuation(decision)}`,
    );
  }
}

/** Names an invalid hook result without its message text, which may hold application data. */
function describeTurnContinuation(decision: unknown): string {
  if (typeof decision === "string") {
    return JSON.stringify(decision);
  }
  if (typeof decision === "object" && decision !== null) {
    return `an object with action ${JSON.stringify((decision as { readonly action?: unknown }).action) ?? "undefined"}`;
  }
  return String(decision);
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
      ...((record.images ?? []).length === 0 ? {} : { images: record.images }),
    }));
}

function stripImages(items: readonly ModelInputItem[]): ModelInputItem[] {
  return items.map((item): ModelInputItem => {
    if (item.type === "model_output" || (item.images ?? []).length === 0) {
      return item;
    }
    const { images: _images, ...withoutImages } = item;
    return withoutImages;
  });
}

function prepareToolArguments(
  tool: FunctionTool,
  argumentsText: string,
  cancellationSignal: AbortSignal,
): Readonly<Record<string, unknown>> {
  let parsed: unknown;
  try {
    parsed = JSON.parse(argumentsText === "" ? "{}" : argumentsText);
  } catch {
    // A fixed text, because the two cores' JSON parsers word their errors differently.
    throw new ToolExecutionError(`invalid arguments for ${tool.name}: arguments are not valid JSON`);
  }
  if (tool.prepareArguments !== undefined) {
    try {
      parsed = tool.prepareArguments(parsed);
    } catch (error: unknown) {
      if (isCancellation(error, cancellationSignal)) {
        throw error;
      }
      // Any other error, so a failed conversion never retries or records outcome_unknown.
      const message = error instanceof Error ? error.message : String(error);
      throw new ToolExecutionError(`invalid arguments for ${tool.name}: ${message}`);
    }
    if (isThenable(parsed)) {
      if (parsed instanceof Promise) {
        observePromiseRejection(parsed);
      }
      throw new ToolExecutionError(`invalid arguments for ${tool.name}: argument conversion must be synchronous`);
    }
  }
  if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
    throw new ToolExecutionError(`invalid arguments for ${tool.name}: expected a JSON object`);
  }
  return parsed as Readonly<Record<string, unknown>>;
}

/** Reads `then` without calling it; a throwing `then` getter counts as a thenable, so it is refused too. */
function isThenable(value: unknown): boolean {
  if ((typeof value !== "object" || value === null) && typeof value !== "function") {
    return false;
  }
  try {
    return typeof (value as { readonly then?: unknown }).then === "function";
  } catch {
    return true;
  }
}

/** Observes a refused Promise's rejection through the native `then`, so a subclass's own `then` never runs. */
function observePromiseRejection(promise: Promise<unknown>): void {
  try {
    void Promise.prototype.then.call(promise, undefined, () => undefined);
  } catch {
    // A subclass constructor may throw; the result is refused either way.
  }
}

function isCancellation(error: unknown, cancellationSignal: AbortSignal): boolean {
  if (cancellationSignal.aborted && error === cancellationSignal.reason) {
    return true;
  }
  return (error as { readonly name?: unknown } | null)?.name === "AbortError";
}

function readBuiltinLoopRunState(frameworkRunState: string | null): BuiltinLoopRunState {
  if (frameworkRunState === null) {
    return { modelCalls: 0, consecutiveContinuations: 0 };
  }
  const parsed = JSON.parse(frameworkRunState) as {
    readonly model_calls?: unknown;
    readonly consecutive_continuations?: unknown;
  };
  const consecutiveContinuations = parsed.consecutive_continuations ?? 0;
  if (
    typeof parsed.model_calls !== "number" ||
    !Number.isSafeInteger(parsed.model_calls) ||
    typeof consecutiveContinuations !== "number" ||
    !Number.isSafeInteger(consecutiveContinuations)
  ) {
    throw new TypeError("FrameworkRunState is not a built-in loop run state");
  }
  return { modelCalls: parsed.model_calls, consecutiveContinuations };
}

/** The output items of the turn's latest model response: the turn's trailing stored `model_output` items. */
function readLatestModelOutput(turn: AgentTurnContext): ModelOutputItem[] {
  const items = turn.messages.filter((message) => message.turnId === turn.turnId).map(readStoredModelInputItem);
  let start = items.length;
  while (start > 0 && items[start - 1]?.type === "model_output") {
    start -= 1;
  }
  return items.slice(start) as ModelOutputItem[];
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

/** Error of a call to a tool that no function tool or source listing offers; the same text as the Python core. */
export function buildUnknownToolError(toolName: string): string {
  return `unknown tool ${toolName}`;
}

/** The runner's `toolSourceReference`: the source's name and its own reference, as JSON text. */
export function writeToolSourceEnvelope(sourceName: string, reference: string | undefined): string {
  return writeRouterToolSourceEnvelope(toolSourceNameKey, sourceName, reference);
}

export function readToolSourceEnvelope(
  toolSourceReference: string | undefined,
): { readonly sourceName: string; readonly reference: string | undefined } | undefined {
  return readRouterToolSourceEnvelope(toolSourceNameKey, toolSourceReference);
}
