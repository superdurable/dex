// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type {
  Api,
  AssistantMessage,
  Context,
  Model,
  SimpleStreamOptions,
  ThinkingLevel,
  Tool,
  Usage,
} from "@earendil-works/pi-ai";
import { streamSimple } from "@earendil-works/pi-ai/compat";
import { emptyAgentUsage, type AgentUsage } from "@superdurable/dex-agents";
import type {
  ModelClient,
  ModelOutputItem,
  ModelOutputWriter,
  ModelRequest,
  ModelRequestMetadata,
  ModelResponse,
  ModelToolChoice,
} from "@superdurable/dex-agents/builtin-loop";

import {
  encodeStoredPiAiMessage,
  isStoredThinkingDisabledInToolLoop,
  piAiModelClientApi,
  readMessageText,
  readModelToolCalls,
  readStoredModelIdentity,
  toPiAiMessages,
} from "./pi-ai-transcript-codec.js";

const thinkingLevels: readonly ThinkingLevel[] = ["minimal", "low", "medium", "high", "xhigh", "max"];
const thinkingOffEffort = "off";

/**
 * Options of {@link createPiAiModelClient}.
 *
 * @since 0.5.0
 */
export interface PiAiModelClientOptions {
  /**
   * API key sent with every model request. Omitted, pi-ai reads the provider's environment variable, such as
   * `ANTHROPIC_API_KEY`. This package does not run pi-ai's OAuth login; obtain the key yourself.
   */
  readonly apiKey?: string;
  /** HTTP headers sent with every model request, merged over pi-ai's defaults. Defaults to none. */
  readonly headers?: Readonly<Record<string, string>>;
  /**
   * Replaces the pi-ai context right before each provider request, like Pi's `prepareRequest` hook.
   *
   * Use it to add per-request information or to trim messages for one request. It runs inside the `RunModel`
   * Step and Dex never replays Step code, so it does not follow a determinism rule. It changes only this one
   * request: the session keeps its stored messages, and the next model call builds its context from them again.
   * An exception fails the `RunModel` attempt, which Dex retries like a model error. The context it receives
   * is built for this request alone, so it may change it in place.
   *
   * @param context - The pi-ai context built from the session: system prompt, messages, and tools.
   * @param metadata - The Flow, turn, Step execution, and attempt of the model call.
   * @returns The context to send.
   */
  readonly prepareRequest?: (context: Context, metadata: ModelRequestMetadata) => Context | Promise<Context>;
}

/**
 * Creates a built-in agent loop {@link ModelClient} that makes each model call with pi-ai.
 *
 * Dex runs the agent loop: each model call is one `RunModel` Step and each tool call is its own Step. pi-ai only
 * talks to the provider. Each `callModel` makes one `streamSimple` request with pi-ai's client retries off, so every
 * retry is a `RunModel` attempt in the Flow history. The exception is Bedrock (`bedrock-converse-stream`): pi-ai
 * 1.5.1 ignores `maxRetries` there, so the AWS SDK retries inside one attempt, 3 attempts by default, as the AWS SDK
 * configuration sets, such as `AWS_MAX_ATTEMPTS` or a profile's `max_attempts`. Text and thinking stream to
 * `AgentEvents` as `text_delta` and `reasoning_delta` events while the call runs.
 *
 * The client stores each response as the replay fields of pi-ai's `AssistantMessage`: the content with thinking
 * signatures; `api`, `provider`, and `model`; `stopReason`; `responseModel`, `responseId`, and
 * `providerThinkingLevel` when pi-ai set them; and the token counts of `usage`. It also stores the pi-ai version
 * that produced the response. Local telemetry, such as `timestamp`, `durationMs`, `usage.cost`, and diagnostics, is
 * not stored. The client replays that message to every later model call, whatever the model, because pi-ai
 * converts messages between providers itself: it keeps thinking for the same model and drops it for another. A
 * stored message whose shape this pi-ai cannot read fails the model call with a `TypeError` that names both pi-ai
 * versions and the field.
 *
 * Only a response that stops for tool use asks Dex to run tools. A response that ends with an error or an abort,
 * such as an HTTP 500 or a stream that broke after a complete tool call, throws, so Dex retries the `RunModel`
 * attempt and stores nothing of it.
 *
 * Model settings map as follows. `maxOutputTokens` is pi-ai's `maxTokens`. `reasoningEffort` is pi-ai's thinking
 * level, one of `minimal`, `low`, `medium`, `high`, `xhigh`, and `max`, or `off`; any other value throws a
 * `TypeError`. `providerOptions[model.provider]` is merged into pi-ai's stream options, such as
 * `{ anthropic: { thinkingBudgets: { medium: 4096 } } }`, except `reasoning`, which only `reasoningEffort` sets.
 * When another model answered earlier in the current tool loop, as after a deploy that changed the model during a
 * turn, every later call of that loop sends no thinking level, because a provider such as Anthropic rejects thinking
 * that does not start with the turn. The loop ends at the next user message, and the calls after it think again.
 *
 * pi-ai's provider-neutral tool choice is `auto` or `none`, so a request that forces a tool, such as a planning
 * turn with `planningToolChoice: "required"`, throws a `TypeError`.
 *
 * @example
 * ```ts
 * import { getModel } from "@earendil-works/pi-ai/compat";
 * import { createBuiltinAgentSessionFlow } from "@superdurable/dex-agents/builtin-loop";
 * import { createPiAiModelClient } from "@superdurable/dex-agents-pi";
 *
 * const model = createPiAiModelClient(getModel("anthropic", "claude-sonnet-4-5"), {
 *   apiKey: process.env.ANTHROPIC_API_KEY,
 * });
 * const flow = createBuiltinAgentSessionFlow({ name: "support", instructions, model, tools }, "SupportAgent");
 * ```
 * @param model - A pi-ai chat model, such as `getModel("anthropic", "claude-sonnet-4-5")`. Its `baseUrl` may
 * point at any compatible endpoint.
 * @param options - API key, headers, and the `prepareRequest` hook.
 * @returns The model client.
 * @since 0.5.0
 */
export function createPiAiModelClient(model: Model<Api>, options: PiAiModelClientOptions = {}): ModelClient {
  return new PiAiModelClient(model, options);
}

class PiAiModelClient implements ModelClient {
  public constructor(
    private readonly model: Model<Api>,
    private readonly options: PiAiModelClientOptions,
  ) {}

  public async callModel(request: ModelRequest, output: ModelOutputWriter): Promise<ModelResponse> {
    let context = this.buildContext(request);
    if (this.options.prepareRequest !== undefined) {
      context = await this.options.prepareRequest(context, request.metadata);
      if (typeof context !== "object" || context === null || !Array.isArray(context.messages)) {
        throw new TypeError("prepareRequest must return a pi-ai context with a messages array");
      }
    }
    const isThinkingDisabledInToolLoop = this.isThinkingDisabledInToolLoop(request);
    const events = streamSimple(this.model, context, this.buildStreamOptions(request, isThinkingDisabledInToolLoop));
    let finalMessage: AssistantMessage | undefined;
    let failedMessage: AssistantMessage | undefined;
    for await (const event of events) {
      switch (event.type) {
        case "text_delta":
          output.writeTextDelta(event.delta);
          break;
        case "thinking_delta":
          output.writeReasoningDelta(event.delta);
          break;
        case "done":
          finalMessage = event.message;
          break;
        case "error":
          failedMessage = event.error;
          break;
        default:
          break;
      }
    }
    if (failedMessage !== undefined) {
      request.cancellationSignal.throwIfAborted();
      throw new Error(
        `the pi-ai model call ended with ${failedMessage.stopReason}: ${failedMessage.errorMessage ?? "no error message"}`,
      );
    }
    if (finalMessage === undefined) {
      throw new Error("the pi-ai model call ended without a done event");
    }
    if (finalMessage.stopReason === "deferred") {
      throw new Error("the pi-ai model call returned a deferred response, which Dex does not request");
    }
    return this.toModelResponse(finalMessage, isThinkingDisabledInToolLoop);
  }

  public estimateInputTokens(request: ModelRequest): number {
    const characters =
      request.instructions.length + JSON.stringify(request.inputItems).length + JSON.stringify(request.tools).length;
    return Math.ceil(characters / 4);
  }

  private buildContext(request: ModelRequest): Context {
    const tools: Tool[] = request.tools.map((definition) => ({
      name: definition.name,
      description: definition.description,
      parameters: { ...definition.parameters },
    }));
    return {
      ...(request.instructions.trim() === "" ? {} : { systemPrompt: request.instructions }),
      messages: toPiAiMessages(request.inputItems),
      ...(tools.length === 0 ? {} : { tools }),
    };
  }

  private buildStreamOptions(request: ModelRequest, isThinkingDisabledInToolLoop: boolean): SimpleStreamOptions {
    const settings = request.settings;
    const thinkingLevel = this.resolveThinkingLevel(request);
    const reasoning = isThinkingDisabledInToolLoop ? undefined : thinkingLevel;
    // The thinking level comes only from reasoningEffort, so the model-change guard cannot be bypassed.
    const { reasoning: _ignoredReasoning, ...providerOptions } = (settings.providerOptions?.[this.model.provider] ??
      {}) as SimpleStreamOptions;
    return {
      ...providerOptions,
      signal: request.cancellationSignal,
      maxRetries: 0,
      ...(request.tools.length === 0 ? {} : { toolChoice: toPiAiToolChoice(request.toolChoice) }),
      ...(settings.temperature === undefined ? {} : { temperature: settings.temperature }),
      ...(settings.maxOutputTokens === undefined ? {} : { maxTokens: settings.maxOutputTokens }),
      ...(reasoning === undefined ? {} : { reasoning }),
      ...(this.options.apiKey === undefined ? {} : { apiKey: this.options.apiKey }),
      ...(this.options.headers === undefined ? {} : { headers: { ...this.options.headers } }),
    };
  }

  private resolveThinkingLevel(request: ModelRequest): ThinkingLevel | undefined {
    const reasoningEffort = request.settings.reasoningEffort;
    if (reasoningEffort === undefined || reasoningEffort === thinkingOffEffort) {
      return undefined;
    }
    if (!(thinkingLevels as readonly string[]).includes(reasoningEffort)) {
      throw new TypeError(
        `pi-ai supports reasoning efforts ${[thinkingOffEffort, ...thinkingLevels].join(", ")}, not ${reasoningEffort}`,
      );
    }
    return reasoningEffort as ThinkingLevel;
  }

  /**
   * Whether this call continues a tool loop where another model answered, whatever its thinking setting. Every
   * response stores the result, so it survives compaction and calls that do not think.
   */
  private isThinkingDisabledInToolLoop(request: ModelRequest): boolean {
    const items = request.inputItems;
    if (items.at(-1)?.type !== "tool_result") {
      return false;
    }
    const lastUserTextIndex = items.map((item) => item.type).lastIndexOf("user_text");
    const toolLoopOutputs = items
      .slice(lastUserTextIndex + 1)
      .filter((item): item is ModelOutputItem => item.type === "model_output");
    const latestOutput = [...items].reverse().find((item): item is ModelOutputItem => item.type === "model_output");
    return (
      toolLoopOutputs.some((item) => !this.isOwnModelOutput(item)) ||
      (latestOutput !== undefined && isStoredThinkingDisabledInToolLoop(latestOutput))
    );
  }

  private isOwnModelOutput(item: ModelOutputItem): boolean {
    const identity = readStoredModelIdentity(item);
    return (
      identity !== undefined &&
      identity.provider === this.model.provider &&
      identity.api === this.model.api &&
      identity.model === this.model.id
    );
  }

  private toModelResponse(message: AssistantMessage, isThinkingDisabledInToolLoop: boolean): ModelResponse {
    const text = readMessageText(message);
    const toolCalls = readModelToolCalls(message);
    const outputItem: ModelOutputItem = {
      type: "model_output",
      provider: this.model.provider,
      api: piAiModelClientApi,
      item: encodeStoredPiAiMessage(message, isThinkingDisabledInToolLoop),
      text: text === "" ? null : text,
      toolCalls,
    };
    return {
      outputItems: [outputItem],
      text,
      toolCalls,
      usage: toAgentUsage(message.usage),
      responseModel: message.responseModel ?? message.model,
      finishReason: message.stopReason,
    };
  }
}

function toPiAiToolChoice(toolChoice: ModelToolChoice): "auto" | "none" {
  if (toolChoice === "auto" || toolChoice === "none") {
    return toolChoice;
  }
  const forced = typeof toolChoice === "string" ? toolChoice : `the tool ${toolChoice.toolName}`;
  throw new TypeError(`pi-ai supports the tool choices auto and none, so it cannot force ${forced}`);
}

function toAgentUsage(usage: Usage): AgentUsage {
  return {
    ...emptyAgentUsage(),
    modelCalls: 1,
    inputTokens: usage.input + usage.cacheRead + usage.cacheWrite,
    outputTokens: usage.output,
    cachedInputTokens: usage.cacheRead,
    reasoningTokens: usage.reasoning ?? 0,
  };
}
