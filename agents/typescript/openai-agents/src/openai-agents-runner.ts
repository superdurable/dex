// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  Agent,
  ModelBehaviorError,
  RunContext,
  RunState,
  Runner,
  ToolInputGuardrailTripwireTriggered,
  invokeFunctionTool,
  runToolInputGuardrails,
  type AgentInputItem,
  type Model,
  type RunConfig,
  type RunStreamEvent,
  type RunToolApprovalItem,
  type StreamedRunResult,
  type protocol,
} from "@openai/agents";
import { getToolCallDisplayName } from "@openai/agents/utils";
import {
  ToolExecutionError,
  buildContextSummaryNote,
  buildContextSummaryPrompt,
  contextSummaryInstructions,
  defaultRejectionToolResult,
  describeStoredMessage,
  estimateStoredInputTokens,
  estimateTextTokens,
  isFinalToolCallStatus,
  selectTurnBoundaryCutoff,
  type AgentEventWriter,
  type AgentFrameworkRunner,
  type AgentTurnContext,
  type AgentMessage,
  type AgentTurnOutcome,
  type ContextSummaryRequest,
  type ContextSummaryResult,
  type DeferredToolCall,
  type DeferredToolExecution,
  type FrameworkMessage,
  type ModelCallMetadata,
  type ResolvedToolPolicy,
  type ToolExecutionResult,
} from "@superdurable/dex-agents";

import {
  wrapAgentGraph,
  type AgentGraphConfiguration,
  type GraphFunctionTool,
  type WrappedAgentGraph,
} from "./openai-agents-agent-graph.js";
import {
  buildUserMessageItem,
  readAgentInputItems,
  toAgentUsage,
  toFrameworkMessage,
} from "./openai-agents-items.js";
import {
  runningDeferredToolExecutionScope,
  runningModelStepScope,
} from "./openai-agents-tool-scopes.js";

type AnyAgent = Agent<any, any>;
type AnyRunState = RunState<any, AnyAgent>;
type AnyStreamedRunResult = StreamedRunResult<any, AnyAgent>;

interface OpenAIAgentsModelStepRun {
  readonly result: AnyStreamedRunResult;
  /** The agent current when `RunModel` started, then each handoff target, in order. */
  readonly modelCallAgents: readonly AnyAgent[];
  /** The `response_done` response ID of each model call, in order. */
  readonly responseIds: readonly string[];
}

const handoffEventType = "openai_agents.handoff";
// The SDK's Chat Completions model reports this response ID when the provider sent none.
const placeholderResponseId = "FAKE_ID";
const reasoningSummaryDeltaEventType = "response.reasoning_summary_text.delta";
// The text tool() returns from its default errorFunction, which hides whether the tool ran.
const defaultToolErrorOutput = "An error occurred while running the tool. Please try again.";

/** Settings of {@link OpenAIAgentsRunner}, resolved by `createOpenAIAgentsSessionFlow`. */
export interface OpenAIAgentsRunnerSettings<TContext> extends AgentGraphConfiguration {
  readonly runConfig: Partial<RunConfig>;
  readonly maxTurns: number | null | undefined;
  readonly createContext: ((flowId: string) => TContext | Promise<TContext>) | undefined;
  readonly summaryModel?: string | Model | undefined;
}

/**
 * Runs an OpenAI Agents SDK agent graph in Step mode: one model call per `RunModel` Step. It implements the
 * compaction methods: with `AgentSessionOptions.compaction` the session compacts at turn boundaries, and the
 * summary enters as the first input item of a turn's first model call. A resumed call continues from the
 * `RunState`, which holds its input, so it never compacts.
 */
export class OpenAIAgentsRunner<TContext> implements AgentFrameworkRunner {
  private readonly agentGraph: WrappedAgentGraph;
  private readonly sdkRunner: Runner;
  private fixedInputTokens: number | undefined;

  public constructor(
    startingAgent: Agent<TContext, any>,
    private readonly settings: OpenAIAgentsRunnerSettings<TContext>,
  ) {
    this.agentGraph = wrapAgentGraph(startingAgent, settings);
    this.sdkRunner = new Runner(settings.runConfig);
  }

  public buildUserMessage(text: string): FrameworkMessage {
    return toFrameworkMessage(buildUserMessageItem(text));
  }

  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    const runContext = new RunContext<TContext | undefined>(await this.createContextValue(turn.flowId));
    const unreturnedResumedToolCallIds = new Set<string>();
    const runInput =
      turn.frameworkRunState === null
        ? readTurnInputItems(turn)
        : await this.restoreRunState(turn, turn.frameworkRunState, runContext, unreturnedResumedToolCallIds);
    const modelStepRun = await runningModelStepScope.run({ turn, unreturnedResumedToolCallIds }, () =>
      this.streamModelCall(turn, runInput, runContext),
    );
    const result = modelStepRun.result;
    const usage = toAgentUsage(result.state.usage);
    const modelCallMetadata = createModelCallMetadata(modelStepRun, this.settings.runConfig.model);
    const interruptions = result.interruptions;
    if (interruptions.length === 0) {
      return { kind: "completed", newMessages: result.output.map(toFrameworkMessage), usage, ...modelCallMetadata };
    }
    const toolCalls: DeferredToolCall[] = [];
    for (const interruption of interruptions) {
      toolCalls.push(await this.toDeferredToolCall(turn, interruption, runContext));
    }
    return {
      kind: "tool_calls_requested",
      frameworkRunState: serializeRunStateWithoutContext(result.state),
      toolCalls,
      usage,
      ...modelCallMetadata,
    };
  }

  /**
   * Estimates the next model input: every stored item as stored, the summary, the `RunState` of a resumed call,
   * and the largest prompt in the agent graph, from static instructions and function tool definitions. Dynamic
   * instructions and MCP tools are not visible here; cover them with `CompactionPolicy.reservedInputTokens`.
   * @param turn - The context of the next model call.
   * @returns The estimated input tokens.
   * @since 0.3.0
   */
  public estimateInputTokens(turn: AgentTurnContext): number {
    this.fixedInputTokens ??= estimateGraphPromptTokens(this.agentGraph.startingAgent);
    return estimateStoredInputTokens(turn, this.fixedInputTokens);
  }

  /**
   * Chooses the last message to summarize, at a boundary between turns.
   * @param turn - The context of the model call that crossed the threshold.
   * @param retainedTokenBudget - Tokens that the kept messages may use.
   * @returns What `selectTurnBoundaryCutoff` returns.
   * @since 0.3.0
   */
  public selectCompactionCutoff(turn: AgentTurnContext, retainedTokenBudget: number): number | null {
    return selectTurnBoundaryCutoff(turn, retainedTokenBudget);
  }

  /**
   * Writes a new summary with one model call of a tool-less agent, on the summary model or the agent's model,
   * without guardrails. Encrypted reasoning and compaction items are left out of the prompt.
   * @param request - The previous summary and the messages to summarize.
   * @returns The summary text and the usage of the call.
   * @throws {Error} When the model returns no text, or any SDK or model error.
   * @since 0.3.0
   */
  public async summarizeContext(request: ContextSummaryRequest): Promise<ContextSummaryResult> {
    const startingAgent = this.agentGraph.startingAgent;
    const summaryModel = this.settings.summaryModel ?? this.settings.runConfig.model;
    // A tool choice without tools is an error, so the summary call keeps the other settings only.
    const { toolChoice, parallelToolCalls, ...modelSettings } = startingAgent.modelSettings;
    const summaryAgent = new Agent({
      name: `${startingAgent.name} summary`,
      instructions: contextSummaryInstructions,
      model: summaryModel ?? startingAgent.model,
      modelSettings,
    });
    const summaryRunner = new Runner({
      ...this.settings.runConfig,
      ...(summaryModel === undefined ? {} : { model: summaryModel }),
      inputGuardrails: [],
      outputGuardrails: [],
    });
    const result = await summaryRunner.run(summaryAgent, buildContextSummaryPrompt(request, describeForSummary), {
      maxTurns: 1,
      signal: request.cancellationSignal,
    });
    const text = typeof result.finalOutput === "string" ? result.finalOutput.trim() : "";
    if (text === "") {
      throw new Error("the summary model call returned no text");
    }
    return {
      text,
      usage: toAgentUsage(result.state.usage),
      ...(typeof summaryModel === "string" || typeof startingAgent.model === "string"
        ? { modelCallMetadata: { requestModel: String(summaryModel ?? startingAgent.model) } }
        : {}),
    };
  }

  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const toolCall = call.toolCall;
    const graphTool = this.agentGraph.functionTools.get(toolCall.toolName);
    if (graphTool === undefined) {
      throw new ToolExecutionError(`the agent has no function tool named ${toolCall.toolName}`);
    }
    const originalTool = graphTool.originalTool;
    const runContext = new RunContext<TContext | undefined>(await this.createContextValue(call.flowId));
    const functionCall: protocol.FunctionCallItem = {
      type: "function_call",
      callId: toolCall.toolCallId,
      name: originalTool.name,
      arguments: toolCall.arguments,
      status: "completed",
      ...(toolCall.toolName === originalTool.name
        ? {}
        : { namespace: toolCall.toolName.slice(0, -(originalTool.name.length + 1)) }),
    };
    return runningDeferredToolExecutionScope.run(call, async () => {
      await requireInputGuardrailsAllow(graphTool, runContext, functionCall);
      let output: unknown;
      try {
        output = await invokeFunctionTool({
          tool: originalTool,
          runContext,
          input: toolCall.arguments,
          details: { toolCall: functionCall, signal: call.cancellationSignal },
        });
      } catch (error: unknown) {
        // The SDK rejects invalid arguments before the tool runs, so the model may correct them.
        if (error instanceof ModelBehaviorError) {
          throw new ToolExecutionError(error.message, { cause: error });
        }
        throw error;
      }
      const outputText = typeof output === "string" ? output : JSON.stringify(output);
      if (outputText === defaultToolErrorOutput || outputText === describeDefaultTimeout(graphTool)) {
        throw new Error(
          `tool ${toolCall.toolName} failed and returned the SDK default text ${JSON.stringify(outputText)}; ` +
            "create it with errorFunction: null and throw ToolExecutionError for failures the model should see",
        );
      }
      return { output: outputText };
    });
  }

  private async restoreRunState(
    turn: AgentTurnContext,
    frameworkRunState: string,
    runContext: RunContext<TContext | undefined>,
    unreturnedResumedToolCallIds: Set<string>,
  ): Promise<AnyRunState> {
    const runState = await RunState.fromStringWithContext(
      this.agentGraph.startingAgent,
      frameworkRunState,
      runContext,
    );
    for (const interruption of runState.getInterruptions()) {
      const toolCallId = readInterruptedFunctionCall(interruption).callId;
      const record = turn.recordedToolCall(toolCallId);
      if (record === undefined || !isFinalToolCallStatus(record.status)) {
        throw new Error(`turn ${turn.turnId} resumed before tool call ${toolCallId} had a final record`);
      }
      if (record.status === "rejected") {
        runState.reject(interruption, { message: record.rejectionMessage ?? defaultRejectionToolResult });
      } else {
        // The gated tool returns the recorded result instead of running again.
        runState.approve(interruption);
        unreturnedResumedToolCallIds.add(toolCallId);
      }
    }
    return runState;
  }

  private async streamModelCall(
    turn: AgentTurnContext,
    runInput: AgentInputItem[] | AnyRunState,
    runContext: RunContext<TContext | undefined>,
  ): Promise<OpenAIAgentsModelStepRun> {
    const maxTurns = this.settings.maxTurns;
    const modelCallAgents = [Array.isArray(runInput) ? this.agentGraph.startingAgent : runInput.currentAgent];
    const responseIds: string[] = [];
    // The run also stops when a response reuses a recorded call ID, which the SDK would skip.
    const runAbort = new AbortController();
    const signal = AbortSignal.any([turn.cancellationSignal, runAbort.signal]);
    const result: AnyStreamedRunResult = await this.sdkRunner.run(
      this.agentGraph.startingAgent,
      runInput,
      {
        stream: true,
        signal,
        ...(Array.isArray(runInput) ? { context: runContext } : {}),
        ...(maxTurns === undefined ? {} : { maxTurns }),
      },
    );
    for await (const event of result) {
      writeRunStreamEvent(turn.events, event);
      if (event.type === "agent_updated_stream_event") {
        modelCallAgents.push(event.agent);
      } else if (event.type === "raw_model_stream_event" && event.data.type === "response_done") {
        responseIds.push(event.data.response.id);
      }
      const reusedToolCallId = findReusedToolCallId(turn, event);
      if (reusedToolCallId !== undefined) {
        runAbort.abort(
          new Error(
            `a model call of turn ${turn.turnId} reuses an earlier tool call ID ${reusedToolCallId}; the SDK ` +
              "would treat it as already run, so it cannot run in a Dex Step",
          ),
        );
        break;
      }
    }
    if (runAbort.signal.aborted) {
      throw runAbort.signal.reason;
    }
    await result.completed;
    // A cancelled stream ends without an error, so it must never be saved as a completed turn.
    if (result.cancelled || signal.aborted) {
      throw signal.reason ?? new Error(`the model call of turn ${turn.turnId} was cancelled`);
    }
    return { result, modelCallAgents, responseIds };
  }

  private async toDeferredToolCall(
    turn: AgentTurnContext,
    interruption: RunToolApprovalItem,
    runContext: RunContext<TContext | undefined>,
  ): Promise<DeferredToolCall> {
    const functionCall = readInterruptedFunctionCall(interruption);
    const toolName = getToolCallDisplayName(functionCall) ?? functionCall.name;
    const graphTool = this.agentGraph.functionTools.get(toolName);
    if (graphTool === undefined || !graphTool.clonedOwnerAgents.has(interruption.agent)) {
      throw new Error(
        `agent ${interruption.agent.name} asked to approve tool ${toolName}, which it does not own in ` +
          "the Dex agent graph",
      );
    }
    return {
      toolCallId: functionCall.callId,
      toolName,
      arguments: functionCall.arguments,
      policy: await this.resolveCallPolicy(turn, graphTool, functionCall, toolName, runContext),
    };
  }

  // An explicit requiresApproval wins; otherwise the tool's own needsApproval can require approval.
  private async resolveCallPolicy(
    turn: AgentTurnContext,
    graphTool: GraphFunctionTool,
    functionCall: protocol.FunctionCallItem,
    toolName: string,
    runContext: RunContext<TContext | undefined>,
  ): Promise<ResolvedToolPolicy> {
    const policy = turn.resolveToolPolicy(toolName);
    const explicitPolicy = Object.hasOwn(this.settings.toolPolicies, toolName)
      ? this.settings.toolPolicies[toolName]
      : undefined;
    if (policy.requiresApproval || explicitPolicy?.requiresApproval !== undefined) {
      return policy;
    }
    const hasNativeApproval = await graphTool.originalTool.needsApproval(
      runContext,
      parseToolArguments(functionCall.arguments),
      functionCall.callId,
    );
    return hasNativeApproval ? { effect: policy.effect, requiresApproval: true } : policy;
  }

  private async createContextValue(flowId: string): Promise<TContext | undefined> {
    const createContext = this.settings.createContext;
    return createContext === undefined ? undefined : createContext(flowId);
  }
}

async function requireInputGuardrailsAllow(
  graphTool: GraphFunctionTool,
  runContext: RunContext<unknown>,
  functionCall: protocol.FunctionCallItem,
): Promise<void> {
  let decision: Awaited<ReturnType<typeof runToolInputGuardrails>>;
  try {
    decision = await runToolInputGuardrails({
      guardrails: graphTool.originalTool.inputGuardrails ?? [],
      context: runContext,
      agent: graphTool.originalOwnerAgent,
      toolCall: functionCall,
    });
  } catch (error: unknown) {
    // A tripwire stops the call before the tool runs.
    if (error instanceof ToolInputGuardrailTripwireTriggered) {
      throw new ToolExecutionError(error.message, { cause: error });
    }
    throw error;
  }
  if (decision.type === "reject") {
    throw new ToolExecutionError(decision.message);
  }
}

/**
 * Reads `modelCallMetadata` for one `RunModel`. The SDK's `ModelResponse` names no model, provider, or finish
 * reason, so only the requested model name and the response ID are known.
 */
function createModelCallMetadata(
  modelStepRun: OpenAIAgentsModelStepRun,
  runConfigModel: RunConfig["model"],
): { readonly modelCallMetadata?: ModelCallMetadata } {
  const requestModelNames = new Set(
    modelStepRun.modelCallAgents.map((agent) => readRequestedModelName(agent, runConfigModel)),
  );
  const requestModel = requestModelNames.size === 1 ? [...requestModelNames][0] : undefined;
  // A response ID describes one response, so several model calls report none.
  const [onlyResponseId] = modelStepRun.responseIds.length === 1 ? modelStepRun.responseIds : [];
  const responseId = onlyResponseId === placeholderResponseId ? undefined : onlyResponseId;
  if (requestModel === undefined && responseId === undefined) {
    return {};
  }
  return {
    modelCallMetadata: {
      ...(requestModel === undefined ? {} : { requestModel }),
      ...(responseId === undefined ? {} : { responseId }),
    },
  };
}

// The Runner's own precedence; only a model given by name has a name the adapter can read.
function readRequestedModelName(agent: AnyAgent, runConfigModel: RunConfig["model"]): string | undefined {
  const selectedModel = agent.model ? agent.model : runConfigModel;
  return typeof selectedModel === "string" && selectedModel !== "" ? selectedModel : undefined;
}

function findReusedToolCallId(turn: AgentTurnContext, event: RunStreamEvent): string | undefined {
  if (event.type !== "raw_model_stream_event" || event.data.type !== "response_done") {
    return undefined;
  }
  for (const item of event.data.response.output) {
    if (item.type === "function_call" && turn.recordedToolCall(item.callId) !== undefined) {
      return item.callId;
    }
  }
  return undefined;
}

function describeDefaultTimeout(graphTool: GraphFunctionTool): string | undefined {
  const timeoutMs = graphTool.originalTool.timeoutMs;
  return timeoutMs === undefined ? undefined : `Tool '${graphTool.originalTool.name}' timed out after ${timeoutMs}ms.`;
}

function parseToolArguments(toolArguments: string): unknown {
  try {
    return JSON.parse(toolArguments) as unknown;
  } catch {
    // Invalid JSON is rejected when the call runs; the policy still sees the raw text.
    return toolArguments;
  }
}

function readInterruptedFunctionCall(interruption: RunToolApprovalItem): protocol.FunctionCallItem {
  const rawItem = interruption.rawItem;
  if (rawItem.type !== "function_call") {
    throw new Error(
      `the Dex adapter cannot run ${rawItem.type} approvals in a Step; only function tools can need approval`,
    );
  }
  return rawItem;
}

// The context is process-local by design, so the persisted run state never carries it.
function serializeRunStateWithoutContext(runState: AnyRunState): string {
  const runContext = runState._context;
  const contextValue = runContext.context;
  // The SDK schema requires a record here.
  runContext.context = {};
  try {
    return runState.toString();
  } finally {
    runContext.context = contextValue;
  }
}

function writeRunStreamEvent(events: AgentEventWriter, event: RunStreamEvent): void {
  switch (event.type) {
    case "raw_model_stream_event": {
      const data = event.data;
      if (data.type === "output_text_delta") {
        events.writeTextDelta(data.delta);
        return;
      }
      if (data.type === "model") {
        const rawEvent = data.event as { type?: unknown; delta?: unknown } | null | undefined;
        if (rawEvent?.type === reasoningSummaryDeltaEventType && typeof rawEvent.delta === "string") {
          events.writeReasoningDelta(rawEvent.delta);
        }
      }
      return;
    }
    case "agent_updated_stream_event":
      events.writeEvent(handoffEventType, { agent_name: event.agent.name });
      return;
    case "run_item_stream_event":
      return;
  }
}

function readTurnInputItems(turn: AgentTurnContext): AgentInputItem[] {
  const items = readAgentInputItems(turn.messages);
  if (turn.contextSummary) {
    items.unshift(buildUserMessageItem(buildContextSummaryNote(turn.contextSummary)));
  }
  return items;
}

// Reasoning and server compaction items are encrypted for the provider, not text.
const opaqueItemTypes = new Set(["reasoning", "compaction"]);

function describeForSummary(message: AgentMessage): string | null {
  if (message.role === "assistant" && message.text === null) {
    const item = JSON.parse(message.frameworkItem) as { readonly type?: unknown };
    if (typeof item.type === "string" && opaqueItemTypes.has(item.type)) {
      return null;
    }
  }
  return describeStoredMessage(message);
}

function estimateGraphPromptTokens(startingAgent: AnyAgent): number {
  const agents: AnyAgent[] = [startingAgent];
  for (const agent of agents) {
    for (const handoff of agent.handoffs) {
      const target = handoff instanceof Agent ? handoff : (handoff as { readonly agent?: unknown }).agent;
      if (target instanceof Agent && !agents.includes(target)) {
        agents.push(target);
      }
    }
  }
  return Math.max(...agents.map(estimateAgentPromptTokens));
}

function estimateAgentPromptTokens(agent: AnyAgent): number {
  const parts: string[] = [typeof agent.instructions === "string" ? agent.instructions : ""];
  parts.push(agent.handoffDescription);
  for (const tool of agent.tools) {
    parts.push(tool.name);
    if (tool.type === "function") {
      parts.push(tool.description, JSON.stringify(tool.parameters));
    }
  }
  return parts.reduce((tokens, part) => tokens + estimateTextTokens(part), 0);
}
