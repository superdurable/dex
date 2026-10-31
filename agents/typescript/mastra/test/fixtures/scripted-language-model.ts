// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { join } from "node:path";

import type {
  LanguageModelV3,
  LanguageModelV3CallOptions,
  LanguageModelV3GenerateResult,
  LanguageModelV3Prompt,
  LanguageModelV3StreamPart,
  LanguageModelV3StreamResult,
  LanguageModelV3ToolResultOutput,
  LanguageModelV3Usage,
} from "@ai-sdk/provider";
import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";

/** Usage the scripted model reports for every call. */
const scriptedCallUsage: LanguageModelV3Usage = {
  inputTokens: { total: 10, noCache: 8, cacheRead: 2, cacheWrite: 0 },
  outputTokens: { total: 5, text: 4, reasoning: 1 },
};

type ScriptedModelResponse =
  | { readonly kind: "answer"; readonly text: string; readonly partialText?: string }
  | { readonly kind: "call_tools"; readonly toolNames: readonly string[]; readonly userText: string };

/**
 * An AI SDK language model (specification v3) that reads only its prompt, so tests need no model key and
 * prove that Mastra replays the stored history.
 *
 * The last user message decides the response:
 * - `call <tool> [and <tool> ...]`: calls every listed tool without a result in one response, with ID
 *   `call-<user message count>-<tool>`, then answers `<tool> -> <result>` per tool, joined with ` | `.
 * - `call ... then fail`: like `call`, but the model call fails once every tool has a result.
 * - `call ... then pause`: like `call`, but the model call after the tools waits at the `turn` FileGate.
 * - `history`: answers with every earlier user and assistant text in the prompt.
 * - `tools`: answers with the sorted names of the tools offered to the model.
 * - `slow`: records the call in `turn.log`, then waits at the `turn` FileGate before answering.
 * - `slow stream`: like `slow`, after streaming `partial text `.
 * - anything else: answers `echo: <text>`.
 *
 * Every call records `<modelId> <user text>` in `model-calls.log`, so tests count model calls.
 */
export class ScriptedLanguageModel implements LanguageModelV3 {
  public readonly specificationVersion = "v3";
  public readonly provider = "scripted";
  public readonly supportedUrls = {};

  public constructor(
    private readonly markerDirectory: string,
    public readonly modelId: string = "scripted-model",
  ) {}

  public async doGenerate(): Promise<LanguageModelV3GenerateResult> {
    throw new Error("the scripted model only streams");
  }

  public async doStream(options: LanguageModelV3CallOptions): Promise<LanguageModelV3StreamResult> {
    const userText = lastUserText(options.prompt);
    new InvocationLog(join(this.markerDirectory, "model-calls.log")).record(`${this.modelId} ${userText}`);
    const response = decideScriptedModelResponse(options);
    const isSlow =
      userText === "slow" ||
      userText === "slow stream" ||
      (userText.endsWith(" then pause") && response.kind === "answer");
    const markerDirectory = this.markerDirectory;
    return {
      stream: new ReadableStream<LanguageModelV3StreamPart>({
        async start(controller) {
          controller.enqueue({ type: "stream-start", warnings: [] });
          if (response.kind === "answer" && response.partialText !== undefined) {
            enqueueText(controller, "partial", response.partialText);
          }
          if (isSlow) {
            new InvocationLog(join(markerDirectory, "turn.log")).record(userText);
            const gate = new FileGate(markerDirectory, "turn");
            gate.markReached();
            await gate.waitForRelease(options.abortSignal);
          }
          if (response.kind === "answer") {
            enqueueText(controller, "answer", response.text);
            controller.enqueue({
              type: "finish",
              usage: scriptedCallUsage,
              finishReason: { unified: "stop", raw: "stop" },
            });
          } else {
            const userMessageCount = options.prompt.filter((message) => message.role === "user").length;
            for (const toolName of response.toolNames) {
              controller.enqueue({
                type: "tool-call",
                toolCallId: `call-${userMessageCount}-${toolName}`,
                toolName,
                // Mastra's sub-agent tools take a prompt.
                input: JSON.stringify(
                  toolName.startsWith("agent-")
                    ? { prompt: `research ${toolName}` }
                    : { requested_by: response.userText },
                ),
              });
            }
            controller.enqueue({
              type: "finish",
              usage: scriptedCallUsage,
              finishReason: { unified: "tool-calls", raw: "tool_calls" },
            });
          }
          controller.close();
        },
      }),
    };
  }
}

function decideScriptedModelResponse(options: LanguageModelV3CallOptions): ScriptedModelResponse {
  const prompt = options.prompt;
  const userText = lastUserText(prompt);
  if (userText === "tools") {
    const toolNames = (options.tools ?? []).map((tool) => tool.name).sort();
    return { kind: "answer", text: `tools: ${toolNames.join(", ")}` };
  }
  if (userText.startsWith("call ")) {
    const toolNames = userText
      .slice("call ".length)
      .replace(/ then (fail|pause)$/, "")
      .split(" and ")
      .map((toolName) => toolName.trim());
    const results = toolResultsSinceLastUserMessage(prompt);
    const missingToolNames = toolNames.filter((toolName) => !results.has(toolName));
    if (missingToolNames.length > 0) {
      return { kind: "call_tools", toolNames: missingToolNames, userText };
    }
    if (userText.endsWith(" then fail")) {
      throw new Error("scripted model failure after the tool calls ran");
    }
    return {
      kind: "answer",
      text: toolNames.map((toolName) => `${toolName} -> ${results.get(toolName)}`).join(" | "),
    };
  }
  if (userText === "history") {
    const lastUserIndex = prompt.map((message) => message.role).lastIndexOf("user");
    const earlierTexts = prompt.slice(0, lastUserIndex).flatMap((message) => {
      if (message.role !== "user" && message.role !== "assistant") {
        return [];
      }
      const text = message.content
        .flatMap((part) => (part.type === "text" ? [part.text] : []))
        .join("");
      return text === "" ? [] : [`${message.role}: ${text}`];
    });
    return { kind: "answer", text: `history | ${earlierTexts.join(" | ")}` };
  }
  if (userText === "slow") {
    return { kind: "answer", text: "slow turn done" };
  }
  if (userText === "slow stream") {
    return { kind: "answer", text: "slow turn done", partialText: "partial text " };
  }
  return { kind: "answer", text: `echo: ${userText}` };
}

function lastUserText(prompt: LanguageModelV3Prompt): string {
  for (let index = prompt.length - 1; index >= 0; index -= 1) {
    const message = prompt[index];
    if (message?.role === "user") {
      return message.content.flatMap((part) => (part.type === "text" ? [part.text] : [])).join("");
    }
  }
  throw new Error("the scripted prompt has no user message");
}

function toolResultsSinceLastUserMessage(prompt: LanguageModelV3Prompt): Map<string, string> {
  const lastUserIndex = prompt.map((message) => message.role).lastIndexOf("user");
  const results = new Map<string, string>();
  for (const message of prompt.slice(lastUserIndex + 1)) {
    if (message.role !== "tool" && message.role !== "assistant") {
      continue;
    }
    for (const part of message.content) {
      if (part.type === "tool-result") {
        results.set(part.toolName, describeToolResultOutput(part.output));
      }
    }
  }
  return results;
}

function describeToolResultOutput(output: LanguageModelV3ToolResultOutput): string {
  switch (output.type) {
    case "text":
    case "error-text":
      return output.value;
    case "execution-denied":
      return output.reason ?? "execution denied";
    default:
      return JSON.stringify(output.value);
  }
}

function enqueueText(
  controller: ReadableStreamDefaultController<LanguageModelV3StreamPart>,
  id: string,
  text: string,
): void {
  controller.enqueue({ type: "text-start", id });
  controller.enqueue({ type: "text-delta", id, delta: text });
  controller.enqueue({ type: "text-end", id });
}
