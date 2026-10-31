// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  streamText,
  type FinishReason,
  type Instructions,
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
  emptyAgentUsage,
  isFinalToolCallStatus,
  toolEffectFromMcpToolAnnotations,
  type AgentEventWriter,
  type AgentFrameworkRunner,
  type AgentToolCall,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type AgentUsage,
  type DeferredToolCall,
  type DeferredToolExecution,
  type FrameworkMessage,
  type McpToolAnnotations,
  type ResolvedToolPolicy,
  type ToolEffect,
  type ToolExecutionResult,
  type ToolPolicy,
} from "@superdurable/dex-agents";

import { runInAgentToolExecutionScope } from "./agent-tool-execution-scope.js";
import {
  appendToolResultParts,
  createRecordedToolResultPart,
  isModelMessage,
  parseRecordedJson,
  parseStoredModelMessage,
  serializeJsonValue,
  serializeToolOutput,
  toFrameworkMessage,
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
  readonly instructions: Instructions | undefined;
  readonly tools: Readonly<Record<string, Tool>>;
  readonly toolPolicies: Readonly<Record<string, ToolPolicy>>;
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
}

interface ModelCallResult {
  readonly responseMessages: readonly ModelMessage[];
  readonly steps: readonly StepResult<ToolSet>[];
  readonly usage: AgentUsage;
}

/** Runs one `streamText` step per `RunModel`; every tool lacks `execute`, so tool calls come back to Dex. */
export class VercelAiAgentRunner implements AgentFrameworkRunner {
  public constructor(private readonly agent: ResolvedVercelAiAgentDefinition) {}

  public buildUserMessage(text: string): FrameworkMessage {
    return toFrameworkMessage({ role: "user", content: text });
  }

  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    const history = turn.messages.map(parseStoredModelMessage);
    let turnMessages: ModelMessage[] = [];
    let modelSteps: RecordedModelStep[] = [];
    if (turn.frameworkRunState !== null) {
      const runState = parseFrameworkRunState(turn.frameworkRunState);
      const records = runState.requestedToolCallIds.map((toolCallId) => this.readFinalRecord(turn, toolCallId));
      const recordedResults = await Promise.all(records.map((record) => this.createRecordedToolResult(record)));
      turnMessages = appendToolResultParts(runState.turnMessages, recordedResults);
      modelSteps = addRecordedToolResults(runState.modelSteps, records);
      // As in the AI SDK loop, stop conditions run once the step's tool calls have results.
      if (await this.isAgentStopConditionMet(modelSteps)) {
        return { kind: "completed", newMessages: turnMessages.map(toFrameworkMessage), usage: emptyAgentUsage() };
      }
    }
    const modelCallTools = this.createModelCallTools(turn);
    const modelCall = await runInAgentToolExecutionScope(
      { flowId: turn.flowId, turnId: turn.turnId, attempt: turn.attempt, idempotencyKey: null, events: turn.events },
      () => this.streamModelCall(turn, [...history, ...turnMessages], modelCallTools, modelSteps),
    );
    turnMessages = [...turnMessages, ...modelCall.responseMessages];
    modelSteps = [...modelSteps, ...modelCall.steps.map((step) => recordModelStep(step, modelSteps.length))];
    const finalStep = modelCall.steps.at(-1);
    const requestedToolCalls = finalStep === undefined ? [] : findUnansweredToolCalls(finalStep);
    if (requestedToolCalls.length === 0 || finalStep === undefined) {
      return { kind: "completed", newMessages: turnMessages.map(toFrameworkMessage), usage: modelCall.usage };
    }
    if (!toolExecutionFinishReasons.has(finalStep.finishReason)) {
      const notRunResults = requestedToolCalls.map((toolCall) =>
        createNotRunToolResult(toolCall, finalStep.finishReason),
      );
      turnMessages = appendToolResultParts(turnMessages, notRunResults);
      return { kind: "completed", newMessages: turnMessages.map(toFrameworkMessage), usage: modelCall.usage };
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
    };
  }

  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const toolCall = call.toolCall;
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

  private readFinalRecord(turn: AgentTurnContext, toolCallId: string): AgentToolCall {
    const record = turn.recordedToolCall(toolCallId);
    if (record === undefined || !isFinalToolCallStatus(record.status)) {
      throw new Error(`turn ${turn.turnId} resumed without a final record for tool call ${toolCallId}`);
    }
    return record;
  }

  private createRecordedToolResult(record: AgentToolCall): Promise<ToolResultPart> {
    return createRecordedToolResultPart(record, this.findTool(record.toolName));
  }

  private findTool(toolName: string): Tool | undefined {
    return Object.hasOwn(this.agent.tools, toolName) ? this.agent.tools[toolName] : undefined;
  }

  private createModelCallTools(turn: AgentTurnContext): ModelCallTools {
    const tools: Record<string, Tool> = {};
    const policies = new Map<string, ResolvedToolPolicy>();
    for (const [toolName, tool] of Object.entries(this.agent.tools)) {
      if (isProviderExecutedTool(tool)) {
        tools[toolName] = tool;
        continue;
      }
      policies.set(toolName, this.resolveTurnToolPolicy(turn, toolName, tool));
      tools[toolName] = withoutLocalExecution(tool);
    }
    return { tools, policies };
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
    return {
      toolCallId: toolCall.toolCallId,
      toolName: toolCall.toolName,
      arguments: serializeJsonValue(toolCall.input ?? null),
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
