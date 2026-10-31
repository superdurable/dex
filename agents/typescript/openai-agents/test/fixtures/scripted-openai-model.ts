// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import {
  Usage,
  type AgentInputItem,
  type Model,
  type ModelRequest,
  type ModelResponse,
  type StreamEvent,
  type protocol,
} from "@openai/agents";
import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";

/** Arguments the scripted model passes to each tool it calls. */
const scriptedToolArguments: Readonly<Record<string, unknown>> = {
  get_weather: { city: "Paris" },
  send_email: { to: "ana@example.com" },
  charge_card: { amountCents: 500 },
  refund_order: { orderId: "42" },
  delete_account: { accountId: "7" },
  lookup_invoice: { invoiceId: "7" },
  lookup_a: { key: "a" },
  lookup_b: { key: "b" },
  post_update: { text: "shipped" },
  read_inventory: { sku: "sku-1" },
  "crm.update_contact": { contactId: "c-1", email: "ana@example.com" },
};

const scriptedUsage = {
  requests: 1,
  inputTokens: 10,
  outputTokens: 5,
  totalTokens: 15,
  inputTokensDetails: { cached_tokens: 2 },
  outputTokensDetails: { reasoning_tokens: 1 },
};

/** Instructions marker that makes the scripted model act as the handoff target agent. */
export const billingAgentInstructions = "You are the billing agent.";

interface ScriptedModelReply {
  readonly output: protocol.OutputModelItem[];
  readonly partialText: string | null;
  readonly reasoningSummary: string | null;
  readonly shouldWaitAtTurnGate: boolean;
}

/**
 * A deterministic OpenAI Agents SDK `Model`, so tests need no model key.
 *
 * It keeps no state: every reply depends only on the request input, so a rerun in a new Worker
 * process returns the same reply. User commands:
 * - `call <tool> [and <tool> ...]`: one response calls every listed tool that has no result yet,
 *   then the answer is `<tool> -> <result>` per tool, joined with ` | `.
 * - `call <tool> ... then fail`: like `call`, but the model call after the tool results throws.
 * - `call <tool> ... then pause`: like `call`, but the model call after the tool results first waits
 *   at the `turn` FileGate.
 * - `call <tool> ... then reuse`: like `call`, but the model call after the tool results calls the
 *   same tools again with the same call IDs and arguments.
 * - `history`: the answer lists every earlier user and assistant text it was replayed.
 * - `handoff <command>`: the starting agent hands off to the billing agent, which runs `<command>`.
 * - `think`: streams a reasoning summary before answering.
 * - `slow`: waits at the `turn` FileGate before answering.
 * - `slow stream`: like `slow`, after streaming partial text.
 * - anything else: the answer is `echo: <text>`.
 *
 * Every model call records the last user text in `model.log`, so tests count model calls.
 */
export class ScriptedOpenAIModel implements Model {
  public constructor(private readonly markerDirectory: string) {}

  public async getResponse(request: ModelRequest): Promise<ModelResponse> {
    this.recordModelCall(request);
    const reply = decideScriptedReply(request);
    await this.waitAtTurnGate(reply, request.signal);
    return { usage: new Usage(scriptedUsage), output: reply.output, responseId: "resp_scripted" };
  }

  public async *getStreamedResponse(request: ModelRequest): AsyncIterable<StreamEvent> {
    this.recordModelCall(request);
    const reply = decideScriptedReply(request);
    yield { type: "response_started" };
    if (reply.partialText !== null) {
      yield { type: "output_text_delta", delta: reply.partialText };
    }
    await this.waitAtTurnGate(reply, request.signal);
    if (reply.reasoningSummary !== null) {
      yield {
        type: "model",
        event: { type: "response.reasoning_summary_text.delta", delta: reply.reasoningSummary },
      };
    }
    for (const item of reply.output) {
      if (item.type === "message" && item.role === "assistant") {
        for (const part of item.content) {
          if (part.type === "output_text") {
            yield { type: "output_text_delta", delta: part.text };
          }
        }
      }
    }
    yield {
      type: "response_done",
      response: { id: "resp_scripted", usage: scriptedUsage, output: reply.output },
    };
  }

  private recordModelCall(request: ModelRequest): void {
    const input = typeof request.input === "string" ? [userItem(request.input)] : request.input;
    new InvocationLog(join(this.markerDirectory, "model.log")).record(readText(input[findLastUserIndex(input)]));
  }

  private async waitAtTurnGate(reply: ScriptedModelReply, signal: AbortSignal | undefined): Promise<void> {
    if (!reply.shouldWaitAtTurnGate) {
      return;
    }
    const gate = new FileGate(this.markerDirectory, "turn");
    gate.markReached();
    await gate.waitForRelease(signal);
  }
}

function decideScriptedReply(request: ModelRequest): ScriptedModelReply {
  const input = typeof request.input === "string" ? [userItem(request.input)] : request.input;
  const lastUserIndex = findLastUserIndex(input);
  const userText = readText(input[lastUserIndex]);
  const turnNumber = input.filter(isUserMessage).length;
  const turnItems = input.slice(lastUserIndex + 1);
  const isBillingAgent = request.systemInstructions?.includes(billingAgentInstructions) === true;
  let command = userText;
  const failAfterToolsSuffix = " then fail";
  const pauseAfterToolsSuffix = " then pause";
  const reuseAfterToolsSuffix = " then reuse";
  const shouldReuseAfterTools = userText.endsWith(reuseAfterToolsSuffix);
  if (shouldReuseAfterTools) {
    command = userText.slice(0, -reuseAfterToolsSuffix.length);
  }
  const shouldFailAfterTools = userText.endsWith(failAfterToolsSuffix);
  const shouldPauseAfterTools = userText.endsWith(pauseAfterToolsSuffix);
  if (shouldFailAfterTools) {
    command = userText.slice(0, -failAfterToolsSuffix.length);
  }
  if (shouldPauseAfterTools) {
    command = userText.slice(0, -pauseAfterToolsSuffix.length);
  }
  if (command.startsWith("handoff ")) {
    if (!isBillingAgent) {
      const handoffToolName = request.handoffs[0]?.toolName;
      if (handoffToolName === undefined) {
        throw new Error("the scripted starting agent has no handoff");
      }
      return toolCallsReply([{ name: handoffToolName, callId: `call_${turnNumber}_handoff`, arguments: "{}" }]);
    }
    command = command.slice("handoff ".length);
  }
  if (command.startsWith("call ")) {
    const toolNames = command.slice("call ".length).split(" and ").map((toolName) => toolName.trim());
    const results = new Map(
      turnItems.flatMap((item) =>
        item.type === "function_call_result"
          ? [[qualifyToolName(item.name, item.namespace), readFunctionCallOutput(item.output)] as const]
          : [],
      ),
    );
    const missingToolNames = toolNames.filter((toolName) => !results.has(toolName));
    if (missingToolNames.length > 0 || shouldReuseAfterTools) {
      return toolCallsReply(
        (shouldReuseAfterTools && missingToolNames.length === 0 ? toolNames : missingToolNames).map((toolName) => ({
          name: toolName,
          callId: `call_${turnNumber}_${toolName}`,
          arguments: JSON.stringify(scriptedToolArguments[toolName] ?? {}),
        })),
      );
    }
    if (shouldFailAfterTools) {
      throw new Error(`scripted failure after the tool calls of turn ${turnNumber}`);
    }
    const answer = answerReply(toolNames.map((toolName) => `${toolName} -> ${results.get(toolName)}`).join(" | "));
    return { ...answer, shouldWaitAtTurnGate: shouldPauseAfterTools };
  }
  if (command === "history") {
    const earlierTexts = input.slice(0, lastUserIndex).flatMap((item) => {
      if (item.type !== "message" && item.type !== undefined) {
        return [];
      }
      const role = "role" in item ? item.role : undefined;
      return role === "user" || role === "assistant" ? [`${role}: ${readText(item)}`] : [];
    });
    return answerReply(`history | ${earlierTexts.join(" | ")}`);
  }
  if (command === "think") {
    return {
      ...answerReply("thought it through"),
      reasoningSummary: "weighing the options",
    };
  }
  if (command === "slow" || command === "slow stream") {
    return {
      ...answerReply("slow turn done"),
      partialText: command === "slow stream" ? "partial text before the gate " : null,
      shouldWaitAtTurnGate: true,
    };
  }
  return answerReply(`echo: ${command}`);
}

function toolCallsReply(
  calls: readonly { readonly name: string; readonly callId: string; readonly arguments: string }[],
): ScriptedModelReply {
  return {
    output: calls.map((call) => {
      const namespaceSeparator = call.name.lastIndexOf(".");
      return {
        type: "function_call",
        id: `fc_${call.callId.replaceAll(".", "_")}`,
        callId: call.callId.replaceAll(".", "_"),
        name: call.name.slice(namespaceSeparator + 1),
        ...(namespaceSeparator < 0 ? {} : { namespace: call.name.slice(0, namespaceSeparator) }),
        arguments: call.arguments,
        status: "completed",
      };
    }),
    partialText: null,
    reasoningSummary: null,
    shouldWaitAtTurnGate: false,
  };
}

function answerReply(text: string): ScriptedModelReply {
  return {
    output: [
      {
        type: "message",
        id: "msg_scripted",
        role: "assistant",
        status: "completed",
        content: [{ type: "output_text", text }],
      },
    ],
    partialText: null,
    reasoningSummary: null,
    shouldWaitAtTurnGate: false,
  };
}

function qualifyToolName(name: string, namespace: string | undefined): string {
  return namespace === undefined || namespace === name ? name : `${namespace}.${name}`;
}

function userItem(text: string): AgentInputItem {
  return { type: "message", role: "user", content: text };
}

function isUserMessage(item: AgentInputItem): boolean {
  return (item.type === "message" || item.type === undefined) && "role" in item && item.role === "user";
}

function findLastUserIndex(input: readonly AgentInputItem[]): number {
  for (let index = input.length - 1; index >= 0; index -= 1) {
    const item = input[index];
    if (item !== undefined && isUserMessage(item)) {
      return index;
    }
  }
  throw new Error("the scripted model input has no user message");
}

function readText(item: AgentInputItem | undefined): string {
  if (item === undefined || !("content" in item)) {
    return "";
  }
  const content: unknown = item.content;
  if (typeof content === "string") {
    return content;
  }
  if (!Array.isArray(content)) {
    return "";
  }
  return content
    .map((part: { text?: unknown }) => (typeof part.text === "string" ? part.text : ""))
    .join("");
}

function readFunctionCallOutput(output: unknown): string {
  if (typeof output === "string") {
    return output;
  }
  if (typeof output === "object" && output !== null && "text" in output && typeof output.text === "string") {
    return output.text;
  }
  return JSON.stringify(output);
}
