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
  type AgentTracingOptions,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type DeferredToolCall,
  type DeferredToolExecution,
  type BuiltInTool,
  type DeferredToolOutcome,
  type FrameworkMessage,
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
  | {
      readonly kind: "call_tools";
      readonly toolCalls: readonly { readonly toolCallId: string; readonly toolName: string }[];
    };

interface ScriptedRunState {
  readonly turnItems: readonly ScriptedItem[];
  readonly pendingToolCallIds: readonly string[];
}

/**
 * A deterministic framework runner in Step mode with a scripted model, so tests need no model key.
 *
 * Each `runModelStep` makes one scripted model decision. User commands:
 * - `call <tool> [and <tool> ...][, then call <tool> [and <tool> ...] ...]`: each model call requests
 *   the next batch of tools in one response, then the model answers `<tool> -> <result>` per tool,
 *   joined with ` | `.
 * - `call ..., then repeat`: after the batches, the model requests the first batch again, with the same
 *   tool call IDs, which the session rejects.
 * - `history`: the model answers with every earlier user and assistant text it was replayed.
 * - `call ... then fail`: like `call`, but the model call after the tools throws on every attempt.
 * - `call ... then pause`: like `call`, but the model call after the tools waits at the `turn` FileGate.
 * - `slow`: the model call records its attempt in `turn.log`, then waits at the `turn` FileGate.
 * - `slow stream`: like `slow`, after streaming partial text tagged with the attempt.
 * - anything else: the model answers `echo: <text>`.
 *
 * Tools record `<flowId> <toolCallId> attempt <n>` in the `<tool>.log` InvocationLog on every real
 * run, and `<flowId> <toolName>` in `tool-runs.log` in run order. `charge_card`, `transfer_funds`,
 * `retry_payout`, `lookup_a`, and `lookup_b` then wait at a FileGate: their own, or `lookups` for the two
 * lookups. `retry_payout` then fails its first attempt.
 * `confirm_shipment` asks the user `Ship now?` through `ToolInputRequested`, then, once answered, logs
 * the answers in `confirm_shipment-answers.log` and waits at its FileGate. `confirm_return` asks
 * `Return now?` the same way without a gate, and `gated_confirm` waits at its FileGate before it asks.
 *
 * `buildUnfinishedTurnMessages` always throws, so the failure tests prove the note is still stored.
 */
export class ScriptedAgentRunner implements AgentFrameworkRunner<DeferredToolOutcome> {
  public constructor(
    private readonly markerDirectory: string,
    private readonly unfinishedTurnMessages: "throw" | "malformed" = "throw",
  ) {}

  public buildUserMessage(text: string): FrameworkMessage {
    return toFrameworkMessage({ kind: "user", text });
  }

  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
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
    const isPausedModelCall =
      (!turn.isResuming && (userText === "slow" || userText === "slow stream")) ||
      (turn.isResuming && userText.endsWith(" then pause"));
    if (isPausedModelCall) {
      if (userText === "slow stream") {
        turn.events.writeTextDelta(`partial text of attempt ${turn.attempt}`);
      }
      this.invocationLog("turn").record(`${turn.flowId} ${turn.turnId} attempt ${turn.attempt}`);
      const gate = new FileGate(this.markerDirectory, "turn");
      gate.markReached();
      await gate.waitForRelease(turn.cancellationSignal);
    }
    const usage = { modelCalls: 1, inputTokens: 10, outputTokens: 5, cachedInputTokens: 0, reasoningTokens: 0 };
    const action = decideScriptedModelAction(turn.turnId, history, turnItems);
    if (action.kind === "answer") {
      turn.events.writeTextDelta(action.text);
      turnItems.push({ kind: "assistant", text: action.text });
      return { kind: "completed", newMessages: turnItems.map(toFrameworkMessage), usage };
    }
    const toolCalls: DeferredToolCall[] = action.toolCalls.map(({ toolCallId, toolName }) => {
      // An adapter exposes an enabled built-in tool like any tool; its call carries the built-in's arguments.
      const isAskUserCall = toolName === "ask_user" && turn.enabledBuiltInTools?.has("ask_user") === true;
      const toolArguments = isAskUserCall
        ? JSON.stringify({ question: "Proceed?", choices: ["yes", "no"] })
        : JSON.stringify({ requested_by: userText });
      turnItems.push({ kind: "tool_call", toolCallId, toolName, arguments: toolArguments });
      return { toolCallId, toolName, arguments: toolArguments, policy: turn.resolveToolPolicy(toolName) };
    });
    const runState: ScriptedRunState = {
      turnItems,
      pendingToolCallIds: toolCalls.map((toolCall) => toolCall.toolCallId),
    };
    return { kind: "tool_calls_requested", frameworkRunState: JSON.stringify(runState), toolCalls, usage };
  }

  public buildUnfinishedTurnMessages(): readonly FrameworkMessage[] {
    if (this.unfinishedTurnMessages === "malformed") {
      return [{ role: "narrator" } as unknown as FrameworkMessage];
    }
    throw new Error("the scripted runner stores no messages before a turn ends");
  }

  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<DeferredToolOutcome> {
    const toolCall = call.toolCall;
    this.invocationLog(toolCall.toolName).record(
      `${call.flowId} ${toolCall.toolCallId} attempt ${call.attempt}`,
    );
    this.invocationLog("tool-runs").record(`${call.flowId} ${toolCall.toolName}`);
    switch (toolCall.toolName) {
      case "get_weather":
        return { output: "sunny in Paris" };
      case "lookup_a":
      case "lookup_b": {
        const gate = new FileGate(this.markerDirectory, "lookups");
        gate.markReached();
        await gate.waitForRelease(call.cancellationSignal);
        return { output: `${toolCall.toolName} found` };
      }
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
      case "confirm_shipment": {
        const answers = call.inputAnswers ?? [];
        if (answers.length === 0) {
          return {
            kind: "tool_input_requested",
            question: "Ship now?",
            choices: ["yes", "no"],
            allowsFreeText: false,
            continuation: `continuation-${toolCall.toolCallId}`,
          };
        }
        this.invocationLog("confirm_shipment-answers").record(
          `${call.flowId} ${answers.map((answer) => `${answer.interruptId}=${answer.answer} (${answer.question})`).join(", ")} ` +
            `continuation ${toolCall.inputContinuation}`,
        );
        const gate = new FileGate(this.markerDirectory, "confirm_shipment");
        gate.markReached();
        await gate.waitForRelease(call.cancellationSignal);
        return { output: `shipped after ${answers.at(-1)?.answer}` };
      }
      case "confirm_return":
      case "gated_confirm": {
        const answer = call.inputAnswers?.at(-1)?.answer;
        if (answer !== undefined) {
          return { output: `${toolCall.toolName} confirmed: ${answer}` };
        }
        if (toolCall.toolName === "gated_confirm") {
          const gate = new FileGate(this.markerDirectory, "gated_confirm");
          gate.markReached();
          await gate.waitForRelease(call.cancellationSignal);
        }
        return {
          kind: "tool_input_requested",
          question: toolCall.toolName === "confirm_return" ? "Return now?" : "Go ahead?",
          choices: ["yes", "no"],
          allowsFreeText: false,
          continuation: null,
        };
      }
      case "transfer_funds": {
        this.invocationLog("transfer_funds-keys").record(`${call.idempotencyKey} attempt ${call.attempt}`);
        const gate = new FileGate(this.markerDirectory, "transfer_funds");
        gate.markReached();
        await gate.waitForRelease(call.cancellationSignal);
        return { output: "funds transferred" };
      }
      case "retry_payout": {
        const gate = new FileGate(this.markerDirectory, "retry_payout");
        gate.markReached();
        await gate.waitForRelease(call.cancellationSignal);
        if (call.attempt === 1) {
          throw new Error("payout service connection lost");
        }
        return { output: "payout sent" };
      }
      case "sync_inventory":
        throw new Error("inventory service unavailable");
      case "check_stock":
        throw new Error("stock service unavailable");
      case "delete_account":
        throw new ToolExecutionError("account deletion is disabled");
      default:
        throw new ToolExecutionError(`unknown tool ${toolCall.toolName}`);
    }
  }

  private invocationLog(name: string): InvocationLog {
    return new InvocationLog(join(this.markerDirectory, `${name}.log`));
  }
}

/** Creates the scripted agent Flow; tests and the Worker child build it identically. */
export function createScriptedAgentFlow(
  markerDirectory: string,
  flowOptions: {
    readonly flowType?: string;
    readonly interruptDeadlineMs?: number;
    readonly tracing?: AgentTracingOptions;
    readonly runner?: AgentFrameworkRunner<DeferredToolOutcome>;
    readonly builtInTools?: readonly BuiltInTool[];
    readonly unfinishedTurnMessages?: "throw" | "malformed";
  } = {},
): AgentSessionFlow<AgentFrameworkRunner<DeferredToolOutcome>> {
  const runner =
    flowOptions.runner ?? new ScriptedAgentRunner(markerDirectory, flowOptions.unfinishedTurnMessages ?? "throw");
  return createAgentSessionFlow(runner, flowOptions.flowType ?? scriptedAgentFlowType, {
    ...(flowOptions.builtInTools === undefined ? {} : { builtInTools: flowOptions.builtInTools }),
    ...(flowOptions.interruptDeadlineMs === undefined
      ? {}
      : { interruptDeadlineMs: flowOptions.interruptDeadlineMs }),
    ...(flowOptions.tracing === undefined ? {} : { tracing: flowOptions.tracing }),
    toolPolicies: {
      get_weather: { effect: "read" },
      lookup_a: { effect: "read" },
      lookup_b: { effect: "read" },
      check_stock: { effect: "read" },
      charge_card: { effect: "non_idempotent_write" },
      confirm_shipment: { effect: "non_idempotent_write", requiresApproval: false },
      confirm_return: { effect: "non_idempotent_write", requiresApproval: false },
      gated_confirm: { effect: "non_idempotent_write", requiresApproval: false },
      refund_order: { effect: "idempotent_write" },
      transfer_funds: { effect: "idempotent_write" },
      retry_payout: { effect: "idempotent_write", requiresApproval: true },
      sync_inventory: { effect: "idempotent_write" },
    },
  });
}

function decideScriptedModelAction(
  turnId: string,
  history: readonly ScriptedItem[],
  turnItems: readonly ScriptedItem[],
): ScriptedModelAction {
  const userText = lastUserText(history);
  if (userText.startsWith("call ")) {
    let body = userText.slice("call ".length).replace(/ then (fail|pause)$/, "");
    const isRepeat = body.endsWith(", then repeat");
    body = body.replace(/, then repeat$/, "");
    const batches = body.split(", then call ").map((batch, batchIndex) =>
      batch.split(" and ").map((toolName) => ({
        toolName: toolName.trim(),
        toolCallId: scriptedToolCallId(turnId, batchIndex, toolName.trim()),
      })),
    );
    const resultOf = (toolCallId: string) =>
      turnItems.find(
        (item): item is Extract<ScriptedItem, { kind: "tool_result" }> =>
          item.kind === "tool_result" && item.toolCallId === toolCallId,
      );
    for (const batch of batches) {
      if (batch.some((toolCall) => resultOf(toolCall.toolCallId) === undefined)) {
        return { kind: "call_tools", toolCalls: batch };
      }
    }
    // A misbehaving model that asks for the first batch again, with the same tool call IDs.
    if (isRepeat) {
      return { kind: "call_tools", toolCalls: batches[0] ?? [] };
    }
    return {
      kind: "answer",
      text: batches
        .flat()
        .map((toolCall) => `${toolCall.toolName} -> ${resultOf(toolCall.toolCallId)?.output}`)
        .join(" | "),
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

/** Returns `<turnId>-<tool>` for the first batch and `<turnId>-b<n>-<tool>` for later ones. */
export function scriptedToolCallId(turnId: string, batchIndex: number, toolName: string): string {
  return batchIndex === 0 ? `${turnId}-${toolName}` : `${turnId}-b${batchIndex}-${toolName}`;
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
