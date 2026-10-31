// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import {
  ToolExecutionError,
  createAgentSessionFlow,
  describeToolCallOutcome,
  isFinalToolCallStatus,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolCall,
  type DeferredToolExecution,
  type FrameworkMessage,
  type ToolExecutionResult,
} from "../../src/index.js";
import { FileGate, InvocationLog } from "../../src/testing/index.js";

/** Flow type of the scripted agent in every test. */
export const scriptedAgentFlowType = "ScriptedAgent";
/** Environment variable naming the directory where scripted tools write marker files. */
export const scriptedAgentMarkerDirectoryVariable = "SCRIPTED_AGENT_MARKER_DIRECTORY";

/**
 * One framework-native item of the scripted framework, stored as `framework_item` JSON.
 * The scripted model reads only these items, so the tests prove history replay.
 */
type ScriptedItem =
  | { readonly kind: "user"; readonly text: string }
  | { readonly kind: "assistant"; readonly text: string }
  | {
      readonly kind: "tool_call";
      readonly toolCallId: string;
      readonly toolName: string;
      readonly arguments: string;
    }
  | {
      readonly kind: "tool_result";
      readonly toolCallId: string;
      readonly toolName: string;
      readonly output: string;
    };

type ScriptedModelAction =
  | { readonly kind: "answer"; readonly text: string }
  | { readonly kind: "call_tools"; readonly toolNames: readonly string[] };

interface ScriptedRunState {
  readonly turnItems: readonly ScriptedItem[];
  readonly pendingToolCallIds: readonly string[];
}

/**
 * A deterministic framework runner with a scripted model, so tests need no model key.
 *
 * User commands:
 * - `call <tool> [and <tool> ...]`: the model calls every listed tool in one response, then answers
 *   `<tool> -> <result>` per tool, joined with ` | `.
 * - `history`: the model answers with every earlier user and assistant text it was replayed.
 * - `call ... then fail`: like `call`, but every resumed segment throws after the tools finished.
 * - `slow`: the runner records its attempt in `turn.log`, then waits at the `turn` FileGate.
 * - `slow stream`: like `slow`, after streaming partial text tagged with the attempt.
 * - anything else: the model answers `echo: <text>`.
 *
 * Tools record `<flowId> <toolCallId> ...` in the `<tool>.log` InvocationLog on every real run, and
 * deferred tools also record `<flowId> <toolName>` in `deferred-tools.log` in run order.
 * `charge_card` then waits at the `charge_card` FileGate.
 */
export class ScriptedAgentRunner implements AgentFrameworkRunner {
  public constructor(private readonly markerDirectory: string) {}

  public buildUserMessage(text: string): FrameworkMessage {
    return toFrameworkMessage({ kind: "user", text });
  }

  public async runTurn(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    const history = turn.messages.map((message) => JSON.parse(message.frameworkItem) as ScriptedItem);
    const turnItems: ScriptedItem[] = [];
    if (turn.frameworkRunState !== null) {
      const runState = JSON.parse(turn.frameworkRunState) as ScriptedRunState;
      turnItems.push(...runState.turnItems);
      for (const pendingToolCallId of runState.pendingToolCallIds) {
        const record = turn.recordedToolCall(pendingToolCallId);
        if (record === undefined || !isFinalToolCallStatus(record.status)) {
          throw new Error(`resumed turn ${turn.turnId} without a final record for ${pendingToolCallId}`);
        }
        turnItems.push({
          kind: "tool_result",
          toolCallId: record.toolCallId,
          toolName: record.toolName,
          output: describeToolCallOutcome(record),
        });
      }
    }
    const userText = lastUserText(history);
    if (turn.isResuming && userText.endsWith(" then fail")) {
      throw new Error(`scripted failure after the tool calls of ${turn.turnId}`);
    }
    if (userText === "slow" || userText === "slow stream") {
      if (userText === "slow stream") {
        turn.events.writeTextDelta(`partial text of attempt ${turn.attempt}`);
      }
      this.invocationLog("turn").record(`${turn.flowId} ${turn.turnId} attempt ${turn.attempt}`);
      const gate = new FileGate(this.markerDirectory, "turn");
      gate.markReached();
      await gate.waitForRelease(turn.cancellationSignal);
    }
    let modelCalls = 0;
    for (;;) {
      modelCalls += 1;
      const action = decideScriptedModelAction(history, turnItems);
      if (action.kind === "answer") {
        turn.events.writeTextDelta(action.text);
        turnItems.push({ kind: "assistant", text: action.text });
        return {
          kind: "completed",
          newMessages: turnItems.map(toFrameworkMessage),
          usage: { modelCalls, inputTokens: 10 * modelCalls, outputTokens: 5 * modelCalls, cachedInputTokens: 0, reasoningTokens: 0 },
        };
      }
      const deferredToolCalls: DeferredToolCall[] = [];
      for (const toolName of action.toolNames) {
        const toolCallId = `${turn.turnId}-${toolName}`;
        const toolArguments = JSON.stringify({ requested_by: userText });
        turnItems.push({ kind: "tool_call", toolCallId, toolName, arguments: toolArguments });
        const policy = turn.resolveToolPolicy(toolName);
        if (policy.shouldDefer) {
          deferredToolCalls.push({ toolCallId, toolName, arguments: toolArguments, policy });
          continue;
        }
        turn.events.writeToolCallStarted(toolCallId, toolName);
        const output = this.runReadTool(turn.flowId, toolCallId, toolName);
        turn.events.writeToolCallFinished(toolCallId, toolName, "succeeded");
        turnItems.push({ kind: "tool_result", toolCallId, toolName, output });
      }
      if (deferredToolCalls.length === 0) {
        continue;
      }
      const runState: ScriptedRunState = {
        turnItems,
        pendingToolCallIds: deferredToolCalls.map((deferredToolCall) => deferredToolCall.toolCallId),
      };
      return {
        kind: "suspended",
        frameworkRunState: JSON.stringify(runState),
        deferredToolCalls,
        usage: { modelCalls, inputTokens: 10 * modelCalls, outputTokens: 5 * modelCalls, cachedInputTokens: 0, reasoningTokens: 0 },
      };
    }
  }

  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const toolCall = call.toolCall;
    this.invocationLog(toolCall.toolName).record(
      `${call.flowId} ${toolCall.toolCallId} attempt ${call.attempt}`,
    );
    this.invocationLog("deferred-tools").record(`${call.flowId} ${toolCall.toolName}`);
    switch (toolCall.toolName) {
      case "send_email":
        return { output: `email sent for ${call.idempotencyKey}` };
      case "charge_card": {
        const gate = new FileGate(this.markerDirectory, "charge_card");
        gate.markReached();
        await gate.waitForRelease(call.cancellationSignal);
        return { output: "card charged" };
      }
      case "refund_order":
        return { output: "refund issued" };
      case "transfer_funds": {
        this.invocationLog("transfer_funds-keys").record(`${call.idempotencyKey} attempt ${call.attempt}`);
        const gate = new FileGate(this.markerDirectory, "transfer_funds");
        gate.markReached();
        await gate.waitForRelease(call.cancellationSignal);
        return { output: "funds transferred" };
      }
      case "sync_inventory":
        throw new Error("inventory service unavailable");
      case "delete_account":
        throw new ToolExecutionError("account deletion is disabled");
      default:
        throw new ToolExecutionError(`unknown tool ${toolCall.toolName}`);
    }
  }

  private runReadTool(flowId: string, toolCallId: string, toolName: string): string {
    this.invocationLog(toolName).record(`${flowId} ${toolCallId} inline`);
    if (toolName === "get_weather") {
      return "sunny in Paris";
    }
    throw new ToolExecutionError(`unknown read tool ${toolName}`);
  }

  private invocationLog(name: string): InvocationLog {
    return new InvocationLog(join(this.markerDirectory, `${name}.log`));
  }
}

/** Creates the scripted agent Flow; tests and the Worker child build it identically. */
export function createScriptedAgentFlow(
  markerDirectory: string,
  flowOptions: { readonly flowType?: string; readonly interruptDeadlineMs?: number } = {},
): AgentSessionFlow {
  return createAgentSessionFlow(new ScriptedAgentRunner(markerDirectory), flowOptions.flowType ?? scriptedAgentFlowType, {
    ...(flowOptions.interruptDeadlineMs === undefined
      ? {}
      : { interruptDeadlineMs: flowOptions.interruptDeadlineMs }),
    toolPolicies: {
      get_weather: { effect: "read" },
      charge_card: { effect: "non_idempotent_write" },
      refund_order: { effect: "idempotent_write" },
      transfer_funds: { effect: "idempotent_write" },
      sync_inventory: { effect: "idempotent_write" },
    },
  });
}

function decideScriptedModelAction(
  history: readonly ScriptedItem[],
  turnItems: readonly ScriptedItem[],
): ScriptedModelAction {
  const userText = lastUserText(history);
  if (userText.startsWith("call ")) {
    const toolNames = userText
      .slice("call ".length)
      .replace(/ then fail$/, "")
      .split(" and ")
      .map((toolName) => toolName.trim());
    const results = toolNames.map((toolName) =>
      turnItems.find(
        (item): item is Extract<ScriptedItem, { kind: "tool_result" }> =>
          item.kind === "tool_result" && item.toolName === toolName,
      ),
    );
    if (results.some((result) => result === undefined)) {
      return { kind: "call_tools", toolNames };
    }
    return {
      kind: "answer",
      text: toolNames.map((toolName, index) => `${toolName} -> ${results[index]?.output}`).join(" | "),
    };
  }
  if (userText === "history") {
    const earlierTexts = history
      .slice(0, -1)
      .flatMap((item) => (item.kind === "user" || item.kind === "assistant" ? [`${item.kind}: ${item.text}`] : []));
    return { kind: "answer", text: `history | ${earlierTexts.join(" | ")}` };
  }
  if (userText === "slow" || userText === "slow stream") {
    return { kind: "answer", text: "slow turn done" };
  }
  return { kind: "answer", text: `echo: ${userText}` };
}

function lastUserText(history: readonly ScriptedItem[]): string {
  for (let index = history.length - 1; index >= 0; index -= 1) {
    const item = history[index];
    if (item?.kind === "user") {
      return item.text;
    }
  }
  throw new Error("scripted history has no user message");
}

function toFrameworkMessage(item: ScriptedItem): FrameworkMessage {
  const frameworkItem = JSON.stringify(item);
  switch (item.kind) {
    case "user":
    case "assistant":
      return {
        role: item.kind,
        text: item.text,
        toolCallId: null,
        toolName: null,
        toolArguments: null,
        toolOutput: null,
        frameworkItem,
      };
    case "tool_call":
      return {
        role: "assistant",
        text: null,
        toolCallId: item.toolCallId,
        toolName: item.toolName,
        toolArguments: item.arguments,
        toolOutput: null,
        frameworkItem,
      };
    case "tool_result":
      return {
        role: "tool",
        text: null,
        toolCallId: item.toolCallId,
        toolName: item.toolName,
        toolArguments: null,
        toolOutput: item.output,
        frameworkItem,
      };
  }
}
