// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  ModelBehaviorError,
  RunContext,
  RunState,
  Runner,
  ToolInputGuardrailTripwireTriggered,
  invokeFunctionTool,
  runToolInputGuardrails,
  type Agent,
  type AgentInputItem,
  type RunConfig,
  type RunStreamEvent,
  type RunToolApprovalItem,
  type StreamedRunResult,
  type protocol,
} from "@openai/agents";
import { getToolCallDisplayName } from "@openai/agents/utils";
import {
  ToolExecutionError,
  defaultRejectionToolResult,
  isFinalToolCallStatus,
  type AgentEventWriter,
  type AgentFrameworkRunner,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolCall,
  type DeferredToolExecution,
  type FrameworkMessage,
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

const handoffEventType = "openai_agents.handoff";
const reasoningSummaryDeltaEventType = "response.reasoning_summary_text.delta";
// The text tool() returns from its default errorFunction, which hides whether the tool ran.
const defaultToolErrorOutput = "An error occurred while running the tool. Please try again.";

/** Settings of {@link OpenAIAgentsRunner}, resolved by `createOpenAIAgentsSessionFlow`. */
export interface OpenAIAgentsRunnerSettings<TContext> extends AgentGraphConfiguration {
  readonly runConfig: Partial<RunConfig>;
  readonly maxTurns: number | null | undefined;
  readonly createContext: ((flowId: string) => TContext | Promise<TContext>) | undefined;
}

/** Runs an OpenAI Agents SDK agent graph in Step mode: one model call per `RunModel` Step. */
export class OpenAIAgentsRunner<TContext> implements AgentFrameworkRunner {
  private readonly agentGraph: WrappedAgentGraph;
  private readonly sdkRunner: Runner;

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
        ? readAgentInputItems(turn.messages)
        : await this.restoreRunState(turn, turn.frameworkRunState, runContext, unreturnedResumedToolCallIds);
    const result = await runningModelStepScope.run({ turn, unreturnedResumedToolCallIds }, () =>
      this.streamModelCall(turn, runInput, runContext),
    );
    const usage = toAgentUsage(result.state.usage);
    const interruptions = result.interruptions;
    if (interruptions.length === 0) {
      return { kind: "completed", newMessages: result.output.map(toFrameworkMessage), usage };
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
  ): Promise<AnyStreamedRunResult> {
    const maxTurns = this.settings.maxTurns;
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
    return result;
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
