// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  jsonSchema,
  stepCountIs,
  streamText,
  type AssistantModelMessage,
  type LanguageModel,
  type LanguageModelUsage,
  type ModelMessage,
  type Tool,
  type ToolChoice,
  type ToolModelMessage,
  type ToolSet,
} from "ai";

import { emptyAgentUsage, type AgentUsage } from "../agent-session-values.js";
import type {
  ModelClient,
  ModelInputItem,
  ModelOutputItem,
  ModelOutputWriter,
  ModelRequest,
  ModelResponse,
  ModelToolCall,
  ModelToolChoice,
} from "../stepwise/model-client.js";

/** API name recorded on every output item of this client. */
export const aiSdkModelClientApi = "ai-sdk";

type StreamTextOptions = Parameters<typeof streamText>[0];
type ReasoningEffort = NonNullable<StreamTextOptions["reasoning"]>;
type ProviderOptions = NonNullable<StreamTextOptions["providerOptions"]>;

const reasoningEfforts: readonly ReasoningEffort[] = [
  "provider-default",
  "none",
  "minimal",
  "low",
  "medium",
  "high",
  "xhigh",
  "max",
];

/** Options of {@link createAiSdkModelClient}. */
export interface AiSdkModelClientOptions {
  /** HTTP headers sent with every model request. Defaults to none. */
  readonly headers?: Readonly<Record<string, string>>;
}

/**
 * Creates a Stepwise {@link ModelClient} for any AI SDK language model.
 *
 * Each `callModel` makes exactly one model call: one `streamText` call with `stopWhen: stepCountIs(1)`,
 * `maxRetries: 0`, and the agent's tools as definitions without `execute`. The AI SDK therefore runs no
 * tool loop, no tool, and no retry; a retry is a `RunModel` attempt. Text and reasoning deltas stream to
 * `AgentEvents` as they arrive.
 *
 * The client stores each response as the AI SDK assistant `ModelMessage`, as JSON text, with every
 * text, reasoning, tool call, and file part and its `providerOptions`, which hold provider signatures and
 * encrypted reasoning, exactly as the AI SDK returned them. It replays that message unchanged to a model
 * of the same provider, and only its text and tool calls to any other model, so a session can change
 * model between turns. Tool results reach the model as text. For a model ID string, the string itself
 * names the provider, so a model switch between two strings replays text only.
 *
 * It requires the optional peer dependency `ai`, version 7.0.159 or a later 7.x release, and uses only
 * its public API; install it next to the provider package.
 *
 * @example
 * ```ts
 * import { anthropic } from "@ai-sdk/anthropic";
 * import { createAiSdkModelClient } from "@superdurable/dex-agents/ai-sdk";
 *
 * const model = createAiSdkModelClient(anthropic("claude-sonnet-4-5"));
 * const flow = createStepwiseAgentSessionFlow({ name: "support", instructions, model, tools }, "SupportAgent");
 * ```
 * @param model - Any AI SDK `LanguageModel`: a provider model or a model ID string.
 * @param options - Request headers.
 * @returns The model client.
 */
export function createAiSdkModelClient(model: LanguageModel, options: AiSdkModelClientOptions = {}): ModelClient {
  return new AiSdkModelClient(model, options);
}

class AiSdkModelClient implements ModelClient {
  private readonly provider: string;

  public constructor(
    private readonly model: LanguageModel,
    private readonly options: AiSdkModelClientOptions,
  ) {
    this.provider = typeof model === "string" ? model : model.provider;
  }

  public async callModel(request: ModelRequest, output: ModelOutputWriter): Promise<ModelResponse> {
    const settings = request.settings;
    const tools: ToolSet = {};
    for (const definition of request.tools) {
      const tool: Tool = { description: definition.description, inputSchema: jsonSchema(definition.parameters) };
      tools[definition.name] = tool;
    }
    let streamError: { readonly error: unknown } | undefined;
    const result = streamText({
      model: this.model,
      ...(request.instructions.trim() === "" ? {} : { system: request.instructions }),
      messages: request.inputItems.reduce<ModelMessage[]>((messages, item) => this.appendInputItem(messages, item), []),
      ...(request.tools.length === 0 ? {} : { tools, toolChoice: toAiSdkToolChoice(request.toolChoice) }),
      ...(settings.temperature === undefined ? {} : { temperature: settings.temperature }),
      ...(settings.maxOutputTokens === undefined ? {} : { maxOutputTokens: settings.maxOutputTokens }),
      ...(settings.reasoningEffort === undefined ? {} : { reasoning: toReasoningEffort(settings.reasoningEffort) }),
      ...(settings.providerOptions === undefined
        ? {}
        : { providerOptions: settings.providerOptions as ProviderOptions }),
      ...(this.options.headers === undefined ? {} : { headers: { ...this.options.headers } }),
      maxRetries: 0,
      stopWhen: stepCountIs(1),
      abortSignal: request.cancellationSignal,
      onError: ({ error }) => {
        streamError ??= { error };
      },
    });
    for await (const part of result.fullStream) {
      switch (part.type) {
        case "text-delta":
          output.writeTextDelta(part.text);
          break;
        case "reasoning-delta":
          output.writeReasoningDelta(part.text);
          break;
        case "error":
          streamError ??= { error: part.error };
          break;
        case "abort":
          request.cancellationSignal.throwIfAborted();
          throw new Error(`the AI SDK aborted the model call: ${part.reason ?? "no reason given"}`);
        default:
          break;
      }
    }
    if (streamError !== undefined) {
      throw streamError.error;
    }
    const [step] = await result.steps;
    if (step === undefined) {
      throw new Error("the AI SDK model call returned no step");
    }
    const toolCalls = step.content.flatMap((part): ModelToolCall[] =>
      part.type === "tool-call" && part.providerExecuted !== true
        ? [{ toolCallId: part.toolCallId, toolName: part.toolName, arguments: serializeToolInput(part) }]
        : [],
    );
    const assistantMessage = (await result.responseMessages).find(
      (message): message is AssistantModelMessage => message.role === "assistant",
    );
    const outputItems: ModelOutputItem[] =
      assistantMessage === undefined
        ? []
        : [
            {
              type: "model_output",
              provider: this.provider,
              api: aiSdkModelClientApi,
              item: JSON.stringify(assistantMessage),
              text: step.text === "" ? null : step.text,
              toolCalls,
            },
          ];
    return {
      outputItems,
      text: step.text,
      toolCalls,
      usage: toAgentUsage(step.usage),
      responseModel: step.response.modelId,
      finishReason: step.finishReason,
    };
  }

  public estimateInputTokens(request: ModelRequest): number {
    const characters =
      request.instructions.length + JSON.stringify(request.inputItems).length + JSON.stringify(request.tools).length;
    return Math.ceil(characters / 4);
  }

  private appendInputItem(messages: ModelMessage[], item: ModelInputItem): ModelMessage[] {
    switch (item.type) {
      case "user_text":
        messages.push({ role: "user", content: [{ type: "text", text: item.text }] });
        return messages;
      case "model_output": {
        const message = this.replayModelOutput(item);
        if (typeof message.content === "string" ? message.content !== "" : message.content.length > 0) {
          messages.push(message);
        }
        return messages;
      }
      case "tool_result": {
        const part: ToolModelMessage["content"][number] = {
          type: "tool-result",
          toolCallId: item.toolCallId,
          toolName: item.toolName,
          output: { type: "text", value: item.output },
        };
        const lastMessage = messages.at(-1);
        if (lastMessage?.role === "tool") {
          lastMessage.content.push(part);
        } else {
          messages.push({ role: "tool", content: [part] });
        }
        return messages;
      }
    }
  }

  /** Replays the stored message unchanged to the same provider; other providers get text and tool calls. */
  private replayModelOutput(item: ModelOutputItem): AssistantModelMessage {
    if (item.api === aiSdkModelClientApi && item.provider === this.provider) {
      const message = JSON.parse(item.item) as AssistantModelMessage;
      if (message?.role !== "assistant") {
        throw new TypeError("a stored AI SDK model output is not an assistant message");
      }
      return message;
    }
    const content: Exclude<AssistantModelMessage["content"], string> = [];
    if (item.text !== null && item.text !== "") {
      content.push({ type: "text", text: item.text });
    }
    for (const toolCall of item.toolCalls) {
      content.push({
        type: "tool-call",
        toolCallId: toolCall.toolCallId,
        toolName: toolCall.toolName,
        input: parseToolInput(toolCall.arguments),
      });
    }
    return { role: "assistant", content };
  }
}

/** The model's arguments as JSON text; an invalid call keeps its raw text, so the tool Step reports it. */
function serializeToolInput(part: { readonly input: unknown; readonly invalid?: boolean | undefined }): string {
  if (part.invalid === true && typeof part.input === "string") {
    return part.input;
  }
  return JSON.stringify(part.input ?? {});
}

function toAiSdkToolChoice(toolChoice: ModelToolChoice): ToolChoice<ToolSet> {
  return typeof toolChoice === "string" ? toolChoice : { type: "tool", toolName: toolChoice.toolName };
}

function toReasoningEffort(reasoningEffort: string): ReasoningEffort {
  if (!(reasoningEfforts as readonly string[]).includes(reasoningEffort)) {
    throw new TypeError(`the AI SDK supports reasoning efforts ${reasoningEfforts.join(", ")}, not ${reasoningEffort}`);
  }
  return reasoningEffort as ReasoningEffort;
}

/** Tool call input as JSON; text that is not a JSON object is replayed as an empty object, as the AI SDK does. */
function parseToolInput(argumentsText: string): unknown {
  try {
    const parsed: unknown = JSON.parse(argumentsText);
    return typeof parsed === "object" && parsed !== null && !Array.isArray(parsed) ? parsed : {};
  } catch {
    return {};
  }
}

function toAgentUsage(usage: LanguageModelUsage): AgentUsage {
  return {
    ...emptyAgentUsage(),
    modelCalls: 1,
    inputTokens: usage.inputTokens ?? 0,
    outputTokens: usage.outputTokens ?? 0,
    cachedInputTokens: usage.inputTokenDetails.cacheReadTokens ?? 0,
    reasoningTokens: usage.outputTokenDetails.reasoningTokens ?? 0,
  };
}
