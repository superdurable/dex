// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  asSchema,
  generateText,
  jsonSchema,
  streamText,
  tool,
  type FinishReason,
  type Instructions,
  type JSONSchema7,
  type LanguageModel,
  type LanguageModelCallOptions,
  type LanguageModelUsage,
  type ModelMessage,
  type StepResult,
  type StopCondition,
  type Tool,
  type ToolChoice,
  type ToolResultPart,
  type ToolSet,
} from "ai";

import {
  ToolExecutionError,
  addAgentUsage,
  buildContextSummaryNote,
  buildContextSummaryPrompt,
  contextSummaryInstructions,
  emptyAgentUsage,
  estimateStoredInputTokens,
  estimateTextTokens,
  selectTurnBoundaryCutoff,
  isFinalToolCallStatus,
  toolEffectFromMcpToolAnnotations,
  type AgentEventWriter,
  type AgentFrameworkRunner,
  type AgentImageReference,
  type AgentToolCall,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type AgentUsage,
  type ContextSummaryRequest,
  type ContextSummaryResult,
  type DeferredToolCall,
  type DeferredToolExecution,
  type FrameworkMessage,
  type ListedSourceTool,
  type McpToolAnnotations,
  type MediaStore,
  type ModelCallMetadata,
  type ResolvedToolPolicy,
  type SourceToolDefinition,
  type ToolCallStepOptions,
  type ToolEffect,
  type ToolExecutionResult,
  type ToolPolicy,
  type ToolSourceRouter,
} from "@superdurable/dex-agents";

import { runInAgentToolExecutionScope } from "./agent-tool-execution-scope.js";
import { toGenAIProviderName } from "./gen-ai-provider-names.js";
import { replaceImagePlaceholders } from "./vercel-ai-model-images.js";
import {
  addToolResultImages,
  appendToolResultParts,
  createRecordedToolResultPart,
  isModelMessage,
  parseRecordedJson,
  parseStoredModelMessage,
  serializeJsonValue,
  serializeToolOutput,
  toFrameworkMessage,
  toUserFrameworkMessageWithImages,
} from "./vercel-ai-model-messages.js";

/** Settings of `streamText` that the runner passes through unchanged on every model call. */
export interface VercelAiStreamTextSettings {
  readonly toolChoice: ToolChoice<ToolSet> | undefined;
  readonly providerOptions: Parameters<typeof streamText>[0]["providerOptions"];
  readonly maxRetries: number | undefined;
  readonly headers: Readonly<Record<string, string | undefined>> | undefined;
  readonly callSettings: LanguageModelCallOptions;
}

/** Agent definition after the factory validated it. */
export interface ResolvedVercelAiAgentDefinition {
  readonly model: LanguageModel;
  readonly summaryModel: LanguageModel | undefined;
  readonly instructions: Instructions | undefined;
  readonly tools: Readonly<Record<string, Tool>>;
  readonly toolPolicies: Readonly<Record<string, ToolPolicy>>;
  readonly toolSources: ToolSourceRouter;
  readonly shouldTrustMcpToolAnnotations: boolean;
  readonly stopConditions: readonly StopCondition<ToolSet>[];
  readonly streamTextSettings: VercelAiStreamTextSettings;
}

const frameworkRunStateVersion = 1;
// The AI SDK runs tool calls only for these finish reasons.
const toolExecutionFinishReasons: ReadonlySet<FinishReason> = new Set(["stop", "tool-calls"]);
const toolInputCallbackNames = ["onInputStart", "onInputDelta", "onInputAvailable"] as const;

/** The turn between two model calls, stored in `FrameworkRunState`. */
interface VercelAiFrameworkRunState {
  readonly version: typeof frameworkRunStateVersion;
  /** Response messages this turn produced so far, after its user message. */
  readonly turnMessages: readonly ModelMessage[];
  /** Tool calls of the last model response that wait for recorded results, in model order. */
  readonly requestedToolCallIds: readonly string[];
  /** Every model step of the turn so far, for the agent's stop conditions. */
  readonly modelSteps: readonly RecordedModelStep[];
}

/** JSON summary of one AI SDK step that stop conditions read back as a `StepResult`. */
interface RecordedModelStep {
  readonly stepNumber: number;
  readonly finishReason: FinishReason;
  readonly text: string;
  readonly toolCalls: readonly RecordedToolCall[];
  readonly toolResults: readonly RecordedToolResult[];
  readonly usage: LanguageModelUsage;
}

interface RecordedToolCall {
  readonly type: "tool-call";
  readonly toolCallId: string;
  readonly toolName: string;
  readonly input: unknown;
  readonly providerExecuted?: boolean;
  readonly dynamic?: boolean;
  readonly invalid?: boolean;
}

interface RecordedToolResult {
  readonly type: "tool-result";
  readonly toolCallId: string;
  readonly toolName: string;
  readonly input: unknown;
  readonly output: unknown;
}

interface ModelCallTools {
  readonly tools: Record<string, Tool>;
  readonly policies: ReadonlyMap<string, ResolvedToolPolicy>;
  readonly sourceTools: ReadonlyMap<string, ListedSourceTool>;
}

interface ModelCallResult {
  readonly responseMessages: readonly ModelMessage[];
  readonly steps: readonly StepResult<ToolSet>[];
  readonly usage: AgentUsage;
}

/**
 * Runs one `streamText` step per `RunModel`; every tool lacks `execute`, so tool calls come back to Dex.
 * It implements the compaction methods, so with `AgentSessionOptions.compaction` the session compacts at turn
 * boundaries and the summary enters as the first message. Tool source tools are listed in every `RunModel` and
 * offered as JSON Schema tools without `execute`; their calls run through the source in their tool Steps.
 * With a media store it shows images to the model; without one the session refuses images.
 */
export class VercelAiAgentRunner implements AgentFrameworkRunner {
  /**
   * Stores user text and its images as one user message whose image parts hold references, not bytes.
   *
   * It is present only when the runner has a media store, so a session without one refuses images. Each model call
   * reads the bytes from the store and sends every image as a `file` part after the text, in reference order.
   *
   * @param text - The user message text.
   * @param images - The message's images, uploaded to the runner's media store.
   * @returns The `"user"` message the session stores as the turn's first message.
   * @since 0.5.0
   */
  public readonly buildUserMessageWithImages?: (
    text: string,
    images: readonly AgentImageReference[],
  ) => FrameworkMessage;
  private fixedInputTokens: number | undefined;

  /**
   * Creates a runner for one resolved agent definition. Most applications call `createVercelAiAgentSessionFlow`.
   *
   * @param agent - The resolved agent: model, instructions, tools, and tool sources.
   * @param mediaStore - The store that holds the session's images. With it, the runner accepts user messages with
   * images and shows them and tool result images to the model, reading the bytes for each model call; stored
   * messages hold only references. Omit it to refuse images. Since 0.5.0.
   *
   * Changed in 0.5.0: takes `mediaStore`.
   */
  public constructor(
    private readonly agent: ResolvedVercelAiAgentDefinition,
    private readonly mediaStore: MediaStore | undefined = undefined,
  ) {
    if (mediaStore !== undefined) {
      this.buildUserMessageWithImages = toUserFrameworkMessageWithImages;
    }
  }

  public buildUserMessage(text: string): FrameworkMessage {
    return toFrameworkMessage({ role: "user", content: text });
  }

  /**
   * Estimates the next model input: every stored message as stored, the summary, the run state of a resumed call,
   * the instructions, and the tool definitions.
   * @param turn - The context of the next model call.
   * @returns The estimated input tokens.
   * @since 0.3.0
   */
  public estimateInputTokens(turn: AgentTurnContext): number {
    this.fixedInputTokens ??= estimateAgentPromptTokens(this.agent);
    const sourceToolTokens = estimateSourceToolTokens(this.agent.toolSources.readCachedSourceTools());
    return estimateStoredInputTokens(turn, this.fixedInputTokens + sourceToolTokens);
  }

  /**
   * Chooses the last message to summarize, at a boundary between turns; a resumed model call never compacts.
   * @param turn - The context of the model call that crossed the threshold.
   * @param retainedTokenBudget - Tokens that the kept messages may use.
   * @returns What `selectTurnBoundaryCutoff` returns.
   * @since 0.3.0
   */
  public selectCompactionCutoff(turn: AgentTurnContext, retainedTokenBudget: number): number | null {
    return selectTurnBoundaryCutoff(turn, retainedTokenBudget);
  }

  /**
   * Writes a new summary with one `generateText` call of the summary model, without tools.
   * @param request - The previous summary and the messages to summarize.
   * @returns The summary text and the usage of the call.
   * @throws {Error} When the model returns no text, or any model error.
   * @since 0.3.0
   */
  public async summarizeContext(request: ContextSummaryRequest): Promise<ContextSummaryResult> {
    const settings = this.agent.streamTextSettings;
    const result = await generateText({
      ...settings.callSettings,
      model: this.agent.summaryModel ?? this.agent.model,
      instructions: contextSummaryInstructions,
      prompt: buildContextSummaryPrompt(request),
      ...(settings.providerOptions === undefined ? {} : { providerOptions: settings.providerOptions }),
      ...(settings.maxRetries === undefined ? {} : { maxRetries: settings.maxRetries }),
      ...(settings.headers === undefined ? {} : { headers: { ...settings.headers } }),
      abortSignal: request.cancellationSignal,
    });
    const text = result.text.trim();
    if (text === "") {
      throw new Error("the summary model call returned no text");
    }
    return {
      text,
      usage: result.steps.reduce((usage, step) => addAgentUsage(usage, toAgentUsage(step.usage)), emptyAgentUsage()),
      ...createModelCallMetadata(result.steps),
    };
  }

  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    const history = turn.messages.map(parseStoredModelMessage);
    if (turn.contextSummary) {
      history.unshift({ role: "user", content: buildContextSummaryNote(turn.contextSummary) });
    }
    let turnMessages: ModelMessage[] = [];
    let modelSteps: RecordedModelStep[] = [];
    if (turn.frameworkRunState !== null) {
      const runState = parseFrameworkRunState(turn.frameworkRunState);
      const requestedToolNames = readRequestedToolNames(turn, runState);
      const records = runState.requestedToolCallIds.map((toolCallId) =>
        this.readFinalRecord(turn, toolCallId, requestedToolNames.get(toolCallId)),
      );
      const recordedResults = await Promise.all(records.map((record) => this.createRecordedToolResult(record)));
      turnMessages = appendToolResultParts(runState.turnMessages, recordedResults);
      modelSteps = addRecordedToolResults(runState.modelSteps, records);
      // As in the AI SDK loop, stop conditions run once the step's tool calls have results.
      if (await this.isAgentStopConditionMet(modelSteps)) {
        return { kind: "completed", newMessages: turnMessages.map(toFrameworkMessage), usage: emptyAgentUsage() };
      }
    }
    const modelCallTools = await this.createModelCallTools(turn);
    // A copy with image bytes goes to the model; turnMessages keep the placeholders that are stored.
    const modelInput =
      this.mediaStore === undefined
        ? [...history, ...turnMessages]
        : await replaceImagePlaceholders([...history, ...turnMessages], this.mediaStore);
    const modelCall = await runInAgentToolExecutionScope(
      { flowId: turn.flowId, turnId: turn.turnId, attempt: turn.attempt, idempotencyKey: null, events: turn.events },
      () => this.streamModelCall(turn, modelInput, modelCallTools, modelSteps),
    );
    turnMessages = [...turnMessages, ...modelCall.responseMessages];
    modelSteps = [...modelSteps, ...modelCall.steps.map((step) => recordModelStep(step, modelSteps.length))];
    const modelCallMetadata = createModelCallMetadata(modelCall.steps);
    const finalStep = modelCall.steps.at(-1);
    const requestedToolCalls = finalStep === undefined ? [] : findUnansweredToolCalls(finalStep);
    if (requestedToolCalls.length === 0 || finalStep === undefined) {
      return {
        kind: "completed",
        newMessages: turnMessages.map(toFrameworkMessage),
        usage: modelCall.usage,
        ...modelCallMetadata,
      };
    }
    if (!toolExecutionFinishReasons.has(finalStep.finishReason)) {
      const notRunResults = requestedToolCalls.map((toolCall) =>
        createNotRunToolResult(toolCall, finalStep.finishReason),
      );
      turnMessages = appendToolResultParts(turnMessages, notRunResults);
      return {
        kind: "completed",
        newMessages: turnMessages.map(toFrameworkMessage),
        usage: modelCall.usage,
        ...modelCallMetadata,
      };
    }
    const runState: VercelAiFrameworkRunState = {
      version: frameworkRunStateVersion,
      turnMessages,
      requestedToolCallIds: requestedToolCalls.map((toolCall) => toolCall.toolCallId),
      modelSteps,
    };
    return {
      kind: "tool_calls_requested",
      frameworkRunState: serializeJsonValue(runState),
      toolCalls: requestedToolCalls.map((toolCall) => this.createDeferredToolCall(turn, toolCall, modelCallTools)),
      usage: modelCall.usage,
      ...modelCallMetadata,
    };
  }

  /**
   * Runs one deferred call in its tool Step. A call that a tool source built goes to that source with the whole
   * execution, including the Step context and the heartbeat checkpoint. Any other call runs the AI SDK tool's
   * `execute` once.
   * @param call - The approved call.
   * @returns The source's result, or the tool output as text.
   * @throws {ToolExecutionError} When the agent has no such tool, the tool or its source throws it, or the source
   * asks the user a question, which this runner cannot ask.
   * @throws {RangeError} When a source built the call but is not configured on this Worker, so the effect decides
   * the outcome.
   * @throws Any other error of the tool or the source, which fails the Step attempt.
   */
  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const toolCall = call.toolCall;
    if (this.agent.toolSources.isSourceToolCall(toolCall)) {
      return this.agent.toolSources.executeDeferredToolCallForResult(call);
    }
    const tool = this.findTool(toolCall.toolName);
    if (tool?.execute === undefined) {
      throw new ToolExecutionError(`the agent has no tool named ${toolCall.toolName}`);
    }
    const execute = tool.execute;
    const input: unknown = JSON.parse(toolCall.arguments);
    const output = await runInAgentToolExecutionScope(
      {
        flowId: call.flowId,
        turnId: toolCall.turnId,
        attempt: call.attempt,
        idempotencyKey: call.idempotencyKey,
        events: call.events,
      },
      () =>
        readFinalToolOutput(
          execute(input, {
            toolCallId: toolCall.toolCallId,
            messages: [],
            abortSignal: call.cancellationSignal,
            context: undefined,
          }),
        ),
    );
    return { output: serializeToolOutput(output) };
  }

  /**
   * Returns a tool source's timeout and retry policy for one call's tool Step, such as a connector operation's.
   * AI SDK tools keep the tool Steps' defaults.
   * @param toolCall - The record whose tool Step starts.
   * @returns The source's options, or `undefined` for the defaults.
   * @throws {RangeError} When a source built the call but is not configured on this Worker.
   * @since 0.4.0
   */
  public toolCallStepOptions(toolCall: AgentToolCall): ToolCallStepOptions | undefined {
    return this.agent.toolSources.toolCallStepOptions(toolCall);
  }

  private readFinalRecord(
    turn: AgentTurnContext,
    toolCallId: string,
    requestedToolName: string | undefined,
  ): AgentToolCall {
    const record = turn.recordedToolCall(toolCallId);
    if (record === undefined || !isFinalToolCallStatus(record.status)) {
      throw new Error(`turn ${turn.turnId} resumed without a final record for tool call ${toolCallId}`);
    }
    if (record.toolName !== requestedToolName) {
      throw new Error(
        `turn ${turn.turnId} resumed tool call ${toolCallId} of ${String(requestedToolName)} with the record of ` +
          `tool ${record.toolName}`,
      );
    }
    return record;
  }

  private async createRecordedToolResult(record: AgentToolCall): Promise<ToolResultPart> {
    const part = await this.createRecordedToolResultWithoutImages(record);
    // Without a media store the model cannot see images, so the result stays as before.
    return this.mediaStore === undefined ? part : addToolResultImages(part, record.images ?? []);
  }

  private async createRecordedToolResultWithoutImages(record: AgentToolCall): Promise<ToolResultPart> {
    // A source's output is text, such as a truncated connector result, so the model reads it unchanged.
    if (record.status === "succeeded" && this.agent.toolSources.isSourceToolCall(record)) {
      return {
        type: "tool-result",
        toolCallId: record.toolCallId,
        toolName: record.toolName,
        output: { type: "text", value: record.output ?? "" },
      };
    }
    return createRecordedToolResultPart(record, this.findTool(record.toolName));
  }

  private findTool(toolName: string): Tool | undefined {
    return Object.hasOwn(this.agent.tools, toolName) ? this.agent.tools[toolName] : undefined;
  }

  private async createModelCallTools(turn: AgentTurnContext): Promise<ModelCallTools> {
    const tools: Record<string, Tool> = {};
    const policies = new Map<string, ResolvedToolPolicy>();
    for (const [toolName, agentTool] of Object.entries(this.agent.tools)) {
      if (isProviderExecutedTool(agentTool)) {
        tools[toolName] = agentTool;
        continue;
      }
      policies.set(toolName, this.resolveTurnToolPolicy(turn, toolName, agentTool));
      tools[toolName] = withoutLocalExecution(agentTool);
    }
    const sourceTools = await this.agent.toolSources.listSourceTools();
    for (const [toolName, listed] of sourceTools) {
      tools[toolName] = createSourceTool(listed.tool);
    }
    return { tools, policies, sourceTools };
  }

  private resolveTurnToolPolicy(turn: AgentTurnContext, toolName: string, tool: Tool): ResolvedToolPolicy {
    const policy = turn.resolveToolPolicy(toolName, this.readFrameworkToolEffect(tool));
    const explicitPolicy = Object.hasOwn(this.agent.toolPolicies, toolName)
      ? this.agent.toolPolicies[toolName]
      : undefined;
    if (tool.needsApproval === true && explicitPolicy?.requiresApproval === undefined) {
      return { effect: policy.effect, requiresApproval: true };
    }
    return policy;
  }

  private readFrameworkToolEffect(tool: Tool): ToolEffect | undefined {
    if (!this.agent.shouldTrustMcpToolAnnotations) {
      return undefined;
    }
    const annotations = tool.metadata?.["annotations"];
    if (typeof annotations !== "object" || annotations === null || Array.isArray(annotations)) {
      return undefined;
    }
    return toolEffectFromMcpToolAnnotations(annotations as McpToolAnnotations);
  }

  private createDeferredToolCall(
    turn: AgentTurnContext,
    toolCall: RecordedToolCall,
    modelCallTools: ModelCallTools,
  ): DeferredToolCall {
    const toolArguments = serializeJsonValue(toolCall.input ?? null);
    const listedSourceTool = modelCallTools.sourceTools.get(toolCall.toolName);
    if (listedSourceTool !== undefined) {
      // The source decides the effect, the approval, and the write order; AI SDK settings never apply.
      return this.agent.toolSources.buildDeferredToolCall(turn, listedSourceTool, {
        toolCallId: toolCall.toolCallId,
        arguments: toolArguments,
      });
    }
    return {
      toolCallId: toolCall.toolCallId,
      toolName: toolCall.toolName,
      arguments: toolArguments,
      policy: modelCallTools.policies.get(toolCall.toolName) ?? turn.resolveToolPolicy(toolCall.toolName),
    };
  }

  private async isAgentStopConditionMet(modelSteps: readonly RecordedModelStep[]): Promise<boolean> {
    return isAnyStopConditionMet(this.agent.stopConditions, modelSteps.map(toStopConditionStep));
  }

  private async streamModelCall(
    turn: AgentTurnContext,
    messages: ModelMessage[],
    modelCallTools: ModelCallTools,
    previousModelSteps: readonly RecordedModelStep[],
  ): Promise<ModelCallResult> {
    const settings = this.agent.streamTextSettings;
    const previousSteps = previousModelSteps.map(toStopConditionStep);
    let streamError: { readonly error: unknown } | undefined;
    const result = streamText({
      ...settings.callSettings,
      model: this.agent.model,
      ...(this.agent.instructions === undefined ? {} : { instructions: this.agent.instructions }),
      messages,
      tools: modelCallTools.tools,
      // The AI SDK checks this only when every call of a step has a result, such as invalid calls.
      stopWhen: ({ steps }) => isAnyStopConditionMet(this.agent.stopConditions, [...previousSteps, ...steps]),
      ...(settings.toolChoice === undefined ? {} : { toolChoice: settings.toolChoice }),
      ...(settings.providerOptions === undefined ? {} : { providerOptions: settings.providerOptions }),
      ...(settings.maxRetries === undefined ? {} : { maxRetries: settings.maxRetries }),
      ...(settings.headers === undefined ? {} : { headers: { ...settings.headers } }),
      abortSignal: turn.cancellationSignal,
      // Dex tool policies own approval, so AI SDK approval settings never pause the loop.
      toolApproval: () => "not-applicable",
      onError: ({ error }) => {
        streamError ??= { error };
      },
    });
    for await (const part of result.stream) {
      switch (part.type) {
        case "text-delta":
          turn.events.writeTextDelta(part.text);
          break;
        case "reasoning-delta":
          turn.events.writeReasoningDelta(part.text);
          break;
        case "finish-step":
          writeStepFinishedEvent(turn.events, part.finishReason, part.usage);
          break;
        case "error":
          streamError ??= { error: part.error };
          break;
        case "abort":
          turn.cancellationSignal.throwIfAborted();
          throw new Error(`the AI SDK aborted turn ${turn.turnId}: ${part.reason ?? "no reason given"}`);
        default:
          break;
      }
    }
    if (streamError !== undefined) {
      throw streamError.error;
    }
    const steps = await result.steps;
    return {
      responseMessages: await result.responseMessages,
      steps,
      usage: steps.reduce((usage, step) => addAgentUsage(usage, toAgentUsage(step.usage)), emptyAgentUsage()),
    };
  }
}

function estimateAgentPromptTokens(agent: ResolvedVercelAiAgentDefinition): number {
  const parts: string[] = [agent.instructions === undefined ? "" : JSON.stringify(agent.instructions)];
  for (const [toolName, tool] of Object.entries(agent.tools)) {
    parts.push(toolName, typeof tool.description === "string" ? tool.description : "");
    try {
      const schema = asSchema(tool.inputSchema).jsonSchema;
      // A schema that resolves later is not counted; reservedInputTokens covers it.
      parts.push(typeof (schema as PromiseLike<unknown>).then === "function" ? "" : JSON.stringify(schema));
    } catch {
      // Estimating must never fail a session; the name and description still count.
    }
  }
  return parts.reduce((tokens, part) => tokens + estimateTextTokens(part), 0);
}

function estimateSourceToolTokens(sourceTools: ReadonlyMap<string, ListedSourceTool>): number {
  let tokens = 0;
  for (const { tool: definition } of sourceTools.values()) {
    tokens += estimateTextTokens(`${definition.name} ${definition.description} ${JSON.stringify(definition.inputSchema)}`);
  }
  return tokens;
}

/** A tool source's tool as the model sees it. It has no `execute`, so its calls always come back to Dex. */
function createSourceTool(definition: SourceToolDefinition): Tool {
  // Without a validator the AI SDK accepts any JSON arguments; the source checks them in the tool Step.
  return tool({
    description: definition.description,
    inputSchema: jsonSchema(definition.inputSchema as JSONSchema7),
  });
}

/** Returns a copy of a tool without `execute` and without input callbacks, which would rerun with the model call. */
function withoutLocalExecution(tool: Tool): Tool {
  const { execute, ...toolWithoutExecute } = tool;
  const deferredTool: Record<string, unknown> = { ...toolWithoutExecute };
  for (const callbackName of toolInputCallbackNames) {
    delete deferredTool[callbackName];
  }
  return deferredTool as Tool;
}

function findUnansweredToolCalls(step: StepResult<ToolSet>): RecordedToolCall[] {
  const answeredToolCallIds = new Set(
    step.content.flatMap((part) =>
      part.type === "tool-result" || part.type === "tool-error" ? [part.toolCallId] : [],
    ),
  );
  return step.content.flatMap((part) =>
    part.type === "tool-call" &&
    part.providerExecuted !== true &&
    part.invalid !== true &&
    !answeredToolCallIds.has(part.toolCallId)
      ? [{ type: "tool-call", toolCallId: part.toolCallId, toolName: part.toolName, input: part.input }]
      : [],
  );
}

function createNotRunToolResult(toolCall: RecordedToolCall, finishReason: FinishReason): ToolResultPart {
  return {
    type: "tool-result",
    toolCallId: toolCall.toolCallId,
    toolName: toolCall.toolName,
    output: {
      type: "error-text",
      value: `The tool call was not run: the model response ended with finish reason ${finishReason}.`,
    },
  };
}

function recordModelStep(step: StepResult<ToolSet>, previousStepCount: number): RecordedModelStep {
  return {
    stepNumber: previousStepCount + step.stepNumber,
    finishReason: step.finishReason,
    text: step.text,
    toolCalls: step.toolCalls.map((toolCall) => ({
      type: "tool-call",
      toolCallId: toolCall.toolCallId,
      toolName: toolCall.toolName,
      input: toolCall.input,
      ...(toolCall.providerExecuted === undefined ? {} : { providerExecuted: toolCall.providerExecuted }),
      ...(toolCall.dynamic === undefined ? {} : { dynamic: toolCall.dynamic }),
      ...(toolCall.invalid === undefined ? {} : { invalid: toolCall.invalid }),
    })),
    toolResults: step.toolResults.map((toolResult) => ({
      type: "tool-result",
      toolCallId: toolResult.toolCallId,
      toolName: toolResult.toolName,
      input: toolResult.input,
      output: toolResult.output,
    })),
    usage: step.usage,
  };
}

function addRecordedToolResults(
  modelSteps: readonly RecordedModelStep[],
  records: readonly AgentToolCall[],
): RecordedModelStep[] {
  const lastStep = modelSteps.at(-1);
  if (lastStep === undefined) {
    return [];
  }
  const succeededResults: RecordedToolResult[] = records
    .filter((record) => record.status === "succeeded")
    .map((record) => ({
      type: "tool-result",
      toolCallId: record.toolCallId,
      toolName: record.toolName,
      input: parseRecordedJson(record.arguments),
      output: parseRecordedJson(record.output ?? "null"),
    }));
  return [...modelSteps.slice(0, -1), { ...lastStep, toolResults: [...lastStep.toolResults, ...succeededResults] }];
}

/** Rebuilds the `StepResult` fields that stop conditions such as `isStepCount` and `hasToolCall` read. */
function toStopConditionStep(step: RecordedModelStep): StepResult<ToolSet> {
  const stopConditionStep = {
    stepNumber: step.stepNumber,
    finishReason: step.finishReason,
    text: step.text,
    toolCalls: step.toolCalls,
    toolResults: step.toolResults,
    content: [
      ...(step.text === "" ? [] : [{ type: "text", text: step.text }]),
      ...step.toolCalls,
      ...step.toolResults,
    ],
    usage: step.usage,
  };
  return stopConditionStep as unknown as StepResult<ToolSet>;
}

async function isAnyStopConditionMet(
  stopConditions: readonly StopCondition<ToolSet>[],
  steps: StepResult<ToolSet>[],
): Promise<boolean> {
  const results = await Promise.all(stopConditions.map((stopCondition) => stopCondition({ steps })));
  return results.some((isMet) => isMet);
}

/** Each requested call's tool name from the run state's tool-call parts; refuses a call requested twice. */
function readRequestedToolNames(
  turn: AgentTurnContext,
  runState: VercelAiFrameworkRunState,
): ReadonlyMap<string, string> {
  if (new Set(runState.requestedToolCallIds).size !== runState.requestedToolCallIds.length) {
    throw new Error(`turn ${turn.turnId} resumed with a tool call requested more than once`);
  }
  const toolNames = new Map<string, string>();
  for (const message of runState.turnMessages) {
    if (message.role !== "assistant" || typeof message.content === "string") {
      continue;
    }
    for (const part of message.content) {
      if (part.type === "tool-call") {
        toolNames.set(part.toolCallId, part.toolName);
      }
    }
  }
  return toolNames;
}

function parseFrameworkRunState(text: string): VercelAiFrameworkRunState {
  const parsed = JSON.parse(text) as Partial<VercelAiFrameworkRunState>;
  if (
    parsed.version !== frameworkRunStateVersion ||
    !Array.isArray(parsed.turnMessages) ||
    !parsed.turnMessages.every(isModelMessage) ||
    !Array.isArray(parsed.requestedToolCallIds) ||
    !parsed.requestedToolCallIds.every((toolCallId) => typeof toolCallId === "string") ||
    !Array.isArray(parsed.modelSteps)
  ) {
    throw new TypeError("FrameworkRunState is not a Vercel AI SDK run state of this adapter");
  }
  return parsed as VercelAiFrameworkRunState;
}

async function readFinalToolOutput(result: unknown): Promise<unknown> {
  if (isAsyncIterable(result)) {
    let lastOutput: unknown;
    for await (const output of result) {
      lastOutput = output;
    }
    return lastOutput;
  }
  return await result;
}

function isAsyncIterable(value: unknown): value is AsyncIterable<unknown> {
  return (
    typeof value === "object" &&
    value !== null &&
    typeof (value as { [Symbol.asyncIterator]?: unknown })[Symbol.asyncIterator] === "function"
  );
}

export function isProviderExecutedTool(tool: Tool): boolean {
  return tool.type === "provider" && tool.isProviderExecuted === true;
}

/**
 * Reads `modelCallMetadata` from the AI SDK steps of one `RunModel`. It reports no response ID: the AI SDK
 * generates one when the provider sends none, and its public API does not say which it is.
 */
function createModelCallMetadata(steps: readonly StepResult<ToolSet>[]): {
  readonly modelCallMetadata?: ModelCallMetadata;
} {
  const [firstStep] = steps;
  if (firstStep === undefined) {
    return {};
  }
  const requestModel = readValueSharedByEveryStep(steps, (step) => step.model.modelId);
  const providerName = readValueSharedByEveryStep(steps, (step) => toGenAIProviderName(step.model.provider));
  // The AI SDK fills a missing response model with the requested one, so only a different one is the provider's.
  const responseModel = readValueSharedByEveryStep(steps, (step) =>
    step.response.modelId === step.model.modelId ? undefined : step.response.modelId,
  );
  return {
    modelCallMetadata: {
      ...(requestModel === undefined ? {} : { requestModel }),
      ...(providerName === undefined ? {} : { providerName }),
      ...(responseModel === undefined ? {} : { responseModel }),
      // A finish reason describes one response, so several model calls report none.
      ...(steps.length === 1 ? { finishReasons: [firstStep.finishReason] } : {}),
    },
  };
}

function readValueSharedByEveryStep(
  steps: readonly StepResult<ToolSet>[],
  readValue: (step: StepResult<ToolSet>) => string | undefined,
): string | undefined {
  const values = new Set(steps.map(readValue));
  return values.size === 1 ? [...values][0] : undefined;
}

function toAgentUsage(usage: LanguageModelUsage): AgentUsage {
  return {
    modelCalls: 1,
    inputTokens: usage.inputTokens ?? 0,
    outputTokens: usage.outputTokens ?? 0,
    cachedInputTokens: usage.inputTokenDetails.cacheReadTokens ?? 0,
    reasoningTokens: usage.outputTokenDetails.reasoningTokens ?? 0,
  };
}

function writeStepFinishedEvent(events: AgentEventWriter, finishReason: string, usage: LanguageModelUsage): void {
  const stepUsage = toAgentUsage(usage);
  events.writeEvent("vercel_ai.step_finished", {
    finish_reason: finishReason,
    usage: {
      model_calls: stepUsage.modelCalls,
      input_tokens: stepUsage.inputTokens,
      output_tokens: stepUsage.outputTokens,
      cached_input_tokens: stepUsage.cachedInputTokens,
      reasoning_tokens: stepUsage.reasoningTokens,
    },
  });
}
