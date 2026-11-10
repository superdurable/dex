/*
 * Copyright (c) 2022-2026 Super Durable, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

import {
  Usage,
  type AgentInputItem,
  type Model,
  type ModelRequest,
  type ModelResponse,
  type StreamEvent,
  type protocol,
} from "@openai/agents";

import { decideOrderSupportReply } from "./order-support-script.js";

const scriptedUsage = { requests: 1, inputTokens: 10, outputTokens: 5, totalTokens: 15 };

/**
 * An OpenAI Agents SDK `Model` that follows the order-support script from its input alone, so the OpenAI Agents
 * example runs without a model key. It reads the last user message and the function results after it.
 */
export class ScriptedOrderSupportOpenAIModel implements Model {
  public async getResponse(request: ModelRequest): Promise<ModelResponse> {
    return { usage: new Usage(scriptedUsage), output: replyToInput(request.input), responseId: "scripted-response" };
  }

  public async *getStreamedResponse(request: ModelRequest): AsyncIterable<StreamEvent> {
    const output = replyToInput(request.input);
    yield { type: "response_started" };
    for (const item of output) {
      if (item.type === "message") {
        for (const part of item.content) {
          if (part.type === "output_text") {
            yield { type: "output_text_delta", delta: part.text };
          }
        }
      }
    }
    yield { type: "response_done", response: { id: "scripted-response", usage: scriptedUsage, output } };
  }
}

function replyToInput(input: string | AgentInputItem[]): protocol.OutputModelItem[] {
  const items: AgentInputItem[] = typeof input === "string" ? [{ type: "message", role: "user", content: input }] : input;
  const lastUserIndex = items.map((item) => ("role" in item ? item.role : undefined)).lastIndexOf("user");
  const toolOutputs: string[] = [];
  for (const item of items.slice(lastUserIndex + 1)) {
    if (item.type === "function_call_result") {
      toolOutputs.push(readFunctionCallOutput(item.output));
    }
  }
  const lastUser = items[lastUserIndex];
  const userText = lastUser === undefined ? "" : readText(lastUser);
  const reply = decideOrderSupportReply(userText, toolOutputs);
  if (reply.kind === "call_tool") {
    const callId = `call_${lastUserIndex}_${reply.toolName}`;
    return [
      {
        type: "function_call",
        id: `fc_${callId}`,
        callId,
        name: reply.toolName,
        arguments: JSON.stringify(reply.arguments),
        status: "completed",
      },
    ];
  }
  return [
    {
      type: "message",
      id: "msg_scripted",
      role: "assistant",
      status: "completed",
      content: [{ type: "output_text", text: reply.text }],
    },
  ];
}

function readText(item: AgentInputItem): string {
  if (!("content" in item)) {
    return "";
  }
  const content: unknown = item.content;
  if (typeof content === "string") {
    return content;
  }
  return Array.isArray(content)
    ? content.map((part: { type?: string; text?: string }) => (part.type === "input_text" ? part.text ?? "" : "")).join("")
    : "";
}

function readFunctionCallOutput(output: unknown): string {
  if (typeof output === "string") {
    return output;
  }
  const text = (output as { text?: unknown } | null)?.text;
  return typeof text === "string" ? text : JSON.stringify(output);
}
