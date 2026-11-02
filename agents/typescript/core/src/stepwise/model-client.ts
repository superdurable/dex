// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentUsage } from "../agent-session-values.js";

/**
 * User text in a model input, stored for a user message and for Dex notes such as the turn-failure note.
 * Wire shape: `{"type":"user_text","text"}`.
 */
export interface ModelUserTextItem {
  /** Discriminator. Wire field: `type`. */
  readonly type: "user_text";
  /** The text. Wire field: `text`. */
  readonly text: string;
}

/** One tool call of a model response, in provider-neutral form. */
export interface ModelToolCall {
  /** Provider tool call ID. Wire field: `tool_call_id`. */
  readonly toolCallId: string;
  /** Tool name. Wire field: `tool_name`. */
  readonly toolName: string;
  /** Tool arguments as JSON text, exactly as the model produced them. Wire field: `arguments`. */
  readonly arguments: string;
}

/**
 * One output item exactly as a provider returned it, with provider-neutral projections.
 *
 * Wire shape: `{"type":"model_output","provider","api","item","text","tool_calls"}`. A client returns one
 * item per provider output item, such as one AI SDK assistant message. {@link item} is the provider's
 * JSON text, opaque to Dex, so its bytes survive storage: opaque fields such as thinking signatures and
 * encrypted reasoning stay the exact strings the provider sent. A {@link ModelClient} replays the item
 * unchanged when {@link provider} and {@link api} match its own. Otherwise it replays {@link text} and
 * {@link toolCalls} and drops the rest, such as reasoning, so a session can change model between turns.
 */
export interface ModelOutputItem {
  /** Discriminator. Wire field: `type`. */
  readonly type: "model_output";
  /** Provider that produced the item, such as `"openai.responses"`. Wire field: `provider`. */
  readonly provider: string;
  /** Provider API that produced the item, such as `"ai-sdk"`. Wire field: `api`. */
  readonly api: string;
  /** Provider JSON text of the item. Wire field: `item`. */
  readonly item: string;
  /** Visible text of the item, or `null` when it has none. Wire field: `text`. */
  readonly text: string | null;
  /** Tool calls the item asks for, in model order. Wire field: `tool_calls`. */
  readonly toolCalls: readonly ModelToolCall[];
}

/**
 * The result of one tool call in a model input, in provider-neutral form.
 * Wire shape: `{"type":"tool_result","tool_call_id","tool_name","output"}`.
 */
export interface ModelToolResultItem {
  /** Discriminator. Wire field: `type`. */
  readonly type: "tool_result";
  /** Tool call this result answers. Wire field: `tool_call_id`. */
  readonly toolCallId: string;
  /** Tool name. Wire field: `tool_name`. */
  readonly toolName: string;
  /** Model-visible result text, from `describeToolCallOutcome`. Wire field: `output`. */
  readonly output: string;
}

/**
 * One item of a model input. The Stepwise runner stores each one as the `framework_item` of an
 * `AgentMessage`, as snake_case JSON.
 */
export type ModelInputItem = ModelUserTextItem | ModelOutputItem | ModelToolResultItem;

/** Definition of one tool offered to the model. */
export interface ModelToolDefinition {
  /** Tool name. */
  readonly name: string;
  /** Tool description for the model. */
  readonly description: string;
  /** JSON Schema of the arguments, an object schema. */
  readonly parameters: Readonly<Record<string, unknown>>;
}

/**
 * How the model may use the offered tools: `auto` lets it choose, `none` forbids tool calls, `required`
 * forces at least one, and `{ toolName }` forces that tool.
 */
export type ModelToolChoice = "auto" | "none" | "required" | { readonly toolName: string };

/** Settings of every model call of an agent; a {@link ModelClient} passes them to its provider. */
export interface ModelSettings {
  /** Sampling temperature; the provider's default when omitted. */
  readonly temperature?: number;
  /** Maximum output tokens; the provider's default when omitted. */
  readonly maxOutputTokens?: number;
  /** Reasoning effort, such as `"low"` or `"high"`; the provider's default when omitted. */
  readonly reasoningEffort?: string;
  /** Provider-specific options by provider name, passed through unchanged. */
  readonly providerOptions?: Readonly<Record<string, Readonly<Record<string, unknown>>>>;
}

/** Identifies the `RunModel` attempt that makes a model call. */
export interface ModelRequestMetadata {
  /** Flow ID, which is the session ID. */
  readonly flowId: string;
  /** Turn ID. */
  readonly turnId: string;
  /** Dex Step execution ID of the `RunModel` Step. */
  readonly stepExecutionId: string;
  /** One-based attempt of the `RunModel` Step. */
  readonly attempt: number;
}

/** One model call, built by the Stepwise runner from committed session state only. */
export interface ModelRequest {
  /** System instructions for this call. */
  readonly instructions: string;
  /** The conversation, oldest first; every tool call is followed by its result. */
  readonly inputItems: readonly ModelInputItem[];
  /** Tools the model may call; empty when the call must answer. */
  readonly tools: readonly ModelToolDefinition[];
  /** How the model may use {@link tools}. */
  readonly toolChoice: ModelToolChoice;
  /** Settings from the agent definition. */
  readonly settings: ModelSettings;
  /** The `RunModel` attempt making the call. */
  readonly metadata: ModelRequestMetadata;
  /** Signal aborted when Dex cancels the attempt; pass it to the provider request. */
  readonly cancellationSignal: AbortSignal;
}

/** Result of one model call. */
export interface ModelResponse {
  /** Provider-exact output items of the response; the session stores them in order. */
  readonly outputItems: readonly ModelOutputItem[];
  /** Visible text of the response, a display projection. */
  readonly text: string;
  /** Tool calls the model requested, in model order; empty for a final answer. */
  readonly toolCalls: readonly ModelToolCall[];
  /** Usage of this one call; `modelCalls` is 1. */
  readonly usage: AgentUsage;
  /** Model that answered, as the provider reported it. */
  readonly responseModel: string;
  /** Provider finish reason, such as `"stop"` or `"tool-calls"`. */
  readonly finishReason: string;
}

/** Receives the live text of one model call; the Stepwise runner forwards it to `AgentEvents`. */
export interface ModelOutputWriter {
  /**
   * Writes visible text as a `text_delta` event.
   * @param text - Text chunk; empty chunks are ignored.
   */
  writeTextDelta(text: string): void;
  /**
   * Writes a provider-authored reasoning summary chunk as a `reasoning_delta` event.
   * @param text - Text chunk; empty chunks are ignored.
   */
  writeReasoningDelta(text: string): void;
}

/**
 * Makes one model call for the Stepwise runner.
 *
 * Use `createAiSdkModelClient` from `@superdurable/dex-agents/ai-sdk` for any AI SDK provider, or
 * implement this interface for another model API. Every client follows these rules:
 * - One request per call. The client makes no retries of its own; retries are `RunModel` attempts, so
 *   each one is visible in the Flow history.
 * - Dex owns the conversation. The client sends the whole input every time and uses no provider-side
 *   conversation state, such as a previous response ID.
 * - Provider-exact transcript. The client returns each output item as the provider returned it and
 *   replays items of its own provider and API unchanged. It never rebuilds opaque fields, such as
 *   thinking signatures or encrypted reasoning, from display text.
 * - Dex-authored items, {@link ModelUserTextItem} and {@link ModelToolResultItem}, are converted at call
 *   time. An item from another provider or API is replayed as its text and tool calls.
 *
 * @example
 * ```ts
 * const echoModel: ModelClient = {
 *   async callModel(request, output) {
 *     const text = `echo: ${request.inputItems.length} items`;
 *     output.writeTextDelta(text);
 *     return {
 *       outputItems: [{ type: "model_output", provider: "echo", api: "echo", item: JSON.stringify({ text }),
 *         text, toolCalls: [] }],
 *       text, toolCalls: [], responseModel: "echo-1", finishReason: "stop",
 *       usage: { modelCalls: 1, inputTokens: 0, outputTokens: 0, cachedInputTokens: 0, reasoningTokens: 0 },
 *     };
 *   },
 *   estimateInputTokens: (request) => JSON.stringify(request.inputItems).length / 4,
 * };
 * ```
 */
export interface ModelClient {
  /**
   * Makes exactly one model call and streams its text to `output`.
   * @param request - The call; see {@link ModelRequest}.
   * @param output - Receives text and reasoning deltas while the call streams.
   * @returns The provider-exact output items, projections, and usage of the call.
   * @throws Any error to fail the `RunModel` attempt, which Dex retries up to 3 times.
   */
  callModel(request: ModelRequest, output: ModelOutputWriter): Promise<ModelResponse>;
  /**
   * Estimates the input tokens of a request without a model call.
   * @param request - The request to measure.
   * @returns An estimate of its input tokens.
   */
  estimateInputTokens(request: ModelRequest): number;
}
