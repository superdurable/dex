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
  describeToolCallResultForModel,
  isFinalToolCallStatus,
  type AgentFrameworkRunner,
  type AgentSessionFlow,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolExecution,
  type FrameworkMessage,
  type ToolExecutionResult,
} from "../../src/index.js";
import { appendMarkerLine, waitForMarkerFile } from "../../src/testing/index.js";

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
  | { readonly kind: "call_tool"; readonly toolName: string };

interface ScriptedRunState {
  readonly turnItems: readonly ScriptedItem[];
  readonly pendingToolCallId: string;
}

/**
 * A deterministic framework runner with a scripted model, so tests need no model key.
 *
 * User commands:
 * - `call <tool>`: the model calls `<tool>` once, then answers `<tool> -> <result>`.
 * - `history`: the model answers with every earlier user and assistant text it was replayed.
 * - `slow`: the runner records its attempt in `turn-attempts.log` and waits for `turn.release`.
 * - `slow stream`: like `slow`, after streaming partial text tagged with the attempt.
 * - anything else: the model answers `echo: <text>`.
 *
 * Tools append `<flowId> <toolCallId> ...` to `<tool>.log` in the marker directory on every real run.
 * `charge_card` then waits for `charge_card.release`.
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
      const record = turn.toolCalls.find((toolCall) => toolCall.toolCallId === runState.pendingToolCallId);
      if (record === undefined || !isFinalToolCallStatus(record.status)) {
        throw new Error(`resumed turn ${turn.turnId} without a final record for ${runState.pendingToolCallId}`);
      }
      turnItems.push({
        kind: "tool_result",
        toolCallId: record.toolCallId,
        toolName: record.toolName,
        output: describeToolCallResultForModel(record),
      });
    }
    const userText = lastUserText(history);
    if (userText === "slow" || userText === "slow stream") {
      if (userText === "slow stream") {
        turn.events.writeTextDelta(`partial text of attempt ${turn.attempt}`);
      }
      appendMarkerLine(
        join(this.markerDirectory, "turn-attempts.log"),
        `${turn.flowId} ${turn.turnId} attempt ${turn.attempt}`,
      );
      await waitForMarkerFile(join(this.markerDirectory, "turn.release"), {
        description: "turn release marker",
        timeoutMs: 120_000,
        signal: turn.cancellationSignal,
      });
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
      const toolCallId = `${turn.turnId}-${action.toolName}`;
      const toolArguments = JSON.stringify({ requested_by: userText });
      turnItems.push({ kind: "tool_call", toolCallId, toolName: action.toolName, arguments: toolArguments });
      const policy = turn.resolveToolPolicy(action.toolName);
      if (!policy.isDeferred) {
        turn.events.writeToolCallStarted(toolCallId, action.toolName);
        const output = this.runReadTool(turn.flowId, toolCallId, action.toolName);
        turn.events.writeToolCallFinished(toolCallId, action.toolName, "succeeded");
        turnItems.push({ kind: "tool_result", toolCallId, toolName: action.toolName, output });
        continue;
      }
      const runState: ScriptedRunState = { turnItems, pendingToolCallId: toolCallId };
      return {
        kind: "suspended",
        frameworkRunState: JSON.stringify(runState),
        deferredToolCalls: [
          {
            toolCallId,
            toolName: action.toolName,
            arguments: toolArguments,
            effect: policy.effect,
            requiresApproval: policy.requiresApproval,
          },
        ],
        usage: { modelCalls, inputTokens: 10 * modelCalls, outputTokens: 5 * modelCalls, cachedInputTokens: 0, reasoningTokens: 0 },
      };
    }
  }

  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    appendMarkerLine(
      join(this.markerDirectory, `${call.toolName}.log`),
      `${call.flowId} ${call.toolCallId} attempt ${call.attempt}`,
    );
    switch (call.toolName) {
      case "send_email":
        return { output: `email sent for ${call.idempotencyKey}` };
      case "charge_card":
        await waitForMarkerFile(join(this.markerDirectory, "charge_card.release"), {
          description: "charge_card release marker",
          timeoutMs: 120_000,
          signal: call.cancellationSignal,
        });
        return { output: "card charged" };
      case "refund_order":
        return { output: "refund issued" };
      case "delete_account":
        throw new ToolExecutionError("account deletion is disabled");
      default:
        throw new ToolExecutionError(`unknown tool ${call.toolName}`);
    }
  }

  private runReadTool(flowId: string, toolCallId: string, toolName: string): string {
    appendMarkerLine(join(this.markerDirectory, `${toolName}.log`), `${flowId} ${toolCallId} inline`);
    if (toolName === "get_weather") {
      return "sunny in Paris";
    }
    throw new ToolExecutionError(`unknown read tool ${toolName}`);
  }
}

/** Creates the scripted agent Flow; tests and the Worker child build it identically. */
export function createScriptedAgentFlow(
  markerDirectory: string,
  flowOptions: { readonly flowType?: string; readonly interruptDeadlineMs?: number } = {},
): AgentSessionFlow {
  return createAgentSessionFlow(new ScriptedAgentRunner(markerDirectory), {
    flowType: flowOptions.flowType ?? scriptedAgentFlowType,
    ...(flowOptions.interruptDeadlineMs === undefined
      ? {}
      : { interruptDeadlineMs: flowOptions.interruptDeadlineMs }),
    toolPolicies: {
      get_weather: { effect: "read" },
      charge_card: { effect: "non_idempotent_write" },
      refund_order: { effect: "idempotent_write" },
    },
  });
}

function decideScriptedModelAction(
  history: readonly ScriptedItem[],
  turnItems: readonly ScriptedItem[],
): ScriptedModelAction {
  const userText = lastUserText(history);
  if (userText.startsWith("call ")) {
    const toolName = userText.slice("call ".length).trim();
    const result = turnItems.find(
      (item): item is Extract<ScriptedItem, { kind: "tool_result" }> =>
        item.kind === "tool_result" && item.toolName === toolName,
    );
    if (result === undefined) {
      return { kind: "call_tool", toolName };
    }
    return { kind: "answer", text: `${toolName} -> ${result.output}` };
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
