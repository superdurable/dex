// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentUsage } from "../agent-session-values.js";
import { serializeJsonWithSpacedSeparators } from "./json-schema-validation.js";
import type {
  ModelClient,
  ModelOutputItem,
  ModelOutputWriter,
  ModelRequest,
  ModelResponse,
  ModelToolCall,
  ModelToolResultItem,
} from "./model-client.js";

/** {@link ModelOutputItem.provider} of every item {@link ScriptedModelClient} returns. */
export const scriptedModelProvider = "dex-scripted";
/** {@link ModelOutputItem.api} of every item {@link ScriptedModelClient} returns. */
export const scriptedModelApi = "scripted";

/** Default number of latest requests {@link ScriptedModelClient.requests} keeps. */
export const defaultMaxRecordedRequests = 100;

/** One tool call of a {@link ScriptedModelReply}. */
export interface ScriptedToolCall {
  /** Tool name. */
  readonly toolName: string;
  /** Arguments as JSON text, or an object encoded as JSON. Defaults to `{}`. */
  readonly arguments?: string | Readonly<Record<string, unknown>>;
  /** Tool call ID. Defaults to `<stepExecutionId>-<index>`, which is unique in the session. */
  readonly toolCallId?: string;
}

/** What one scripted model call returns. */
export interface ScriptedModelReply {
  /** Visible text, streamed as one `text_delta`. Defaults to none. */
  readonly text?: string;
  /** Tool calls, in order. Defaults to none. */
  readonly toolCalls?: readonly ScriptedToolCall[];
  /** Reasoning summary, streamed as one `reasoning_delta` and stored as a reasoning item. Defaults to none. */
  readonly reasoning?: string | null;
  /** Usage of the call. Defaults to one model call with 10 input and 5 output tokens. */
  readonly usage?: AgentUsage;
}

/** The script: a function from the whole request to the reply. */
export type ScriptedModelResponder = (request: ModelRequest) => ScriptedModelReply | Promise<ScriptedModelReply>;

/**
 * A built-in agent loop {@link ModelClient} that answers every model call from a script, for demos and tests without a
 * model key. It mirrors the Python core's `ScriptedModelClient`.
 *
 * Use it as `AgentDefinition.model` to run a built-in loop agent with no provider account: a local demo
 * mode of an application, a tutorial, or an integration test. It is a supported part of
 * `@superdurable/dex-agents/builtin-loop` and needs no optional peer. `@superdurable/dex-agents/testing`
 * exports the same class.
 *
 * The script is a function of the whole {@link ModelRequest}, so it can read the user text, with
 * {@link ScriptedModelClient.userTexts}, and the tool results the runner replays, with
 * {@link ScriptedModelClient.toolResults}, and it can call any offered tool, including built-in and tool
 * source tools. Keep it deterministic: a Worker that crashes mid-call reruns it in a new process. Its output
 * items use {@link scriptedModelProvider} and {@link scriptedModelApi}, so a session can switch to a real
 * model later; that model sees the scripted replies as text.
 *
 * @example
 * ```ts
 * const model = new ScriptedModelClient((request) =>
 *   ScriptedModelClient.toolResults(request).length > 0
 *     ? { text: "done" }
 *     : { toolCalls: [{ toolName: "lookup_order", arguments: { order_id: "7" } }] });
 * const flow = createBuiltinAgentSessionFlow(
 *   { name: "demo", instructions: "Help.", model, tools: [lookupOrder] }, "DemoAgent");
 * ```
 */
export class ScriptedModelClient implements ModelClient {
  private readonly receivedRequests: ModelRequest[] = [];
  private readonly maxRecordedRequests: number;

  /**
   * Creates a client around a script.
   * @param respond - Function that returns the reply for a request.
   * @param options - `maxRecordedRequests`: how many of the latest requests {@link requests} keeps, so a
   * long-running demo Worker does not keep every transcript. `0` records none. Defaults to
   * {@link defaultMaxRecordedRequests}.
   * @throws {RangeError} When `maxRecordedRequests` is not a non-negative integer.
   */
  public constructor(
    private readonly respond: ScriptedModelResponder,
    options: { readonly maxRecordedRequests?: number } = {},
  ) {
    this.maxRecordedRequests = options.maxRecordedRequests ?? defaultMaxRecordedRequests;
    if (!Number.isSafeInteger(this.maxRecordedRequests) || this.maxRecordedRequests < 0) {
      throw new RangeError("maxRecordedRequests must be a non-negative integer");
    }
  }

  /** The latest requests this client received in this process, up to `maxRecordedRequests`, oldest first. */
  public get requests(): readonly ModelRequest[] {
    return [...this.receivedRequests];
  }

  /**
   * Runs the script for one request and streams its reply.
   * @param request - The request.
   * @param output - Writer for text and reasoning deltas.
   * @returns One reasoning item, one message item, and one item per tool call, for the parts the reply has.
   */
  public async callModel(request: ModelRequest, output: ModelOutputWriter): Promise<ModelResponse> {
    this.receivedRequests.push(request);
    if (this.receivedRequests.length > this.maxRecordedRequests) {
      this.receivedRequests.splice(0, this.receivedRequests.length - this.maxRecordedRequests);
    }
    const reply = await this.respond(request);
    const text = reply.text ?? "";
    const items: ModelOutputItem[] = [];
    if (reply.reasoning !== undefined && reply.reasoning !== null) {
      output.writeReasoningDelta(reply.reasoning);
      items.push(createScriptedItem({ type: "reasoning", summary: reply.reasoning }, null, []));
    }
    if (text !== "") {
      output.writeTextDelta(text);
      items.push(createScriptedItem({ type: "message", text }, text, []));
    }
    const toolCalls: ModelToolCall[] = [];
    for (const [index, scripted] of (reply.toolCalls ?? []).entries()) {
      const toolCall: ModelToolCall = {
        toolCallId: scripted.toolCallId ?? `${request.metadata.stepExecutionId}-${index}`,
        toolName: scripted.toolName,
        arguments:
          typeof scripted.arguments === "string"
            ? scripted.arguments
            : serializeJsonWithSpacedSeparators(scripted.arguments ?? {}),
      };
      toolCalls.push(toolCall);
      items.push(
        createScriptedItem(
          { type: "function_call", call_id: toolCall.toolCallId, name: toolCall.toolName, arguments: toolCall.arguments },
          null,
          [toolCall],
        ),
      );
    }
    return {
      outputItems: items,
      text,
      toolCalls,
      usage: reply.usage ?? { modelCalls: 1, inputTokens: 10, outputTokens: 5, cachedInputTokens: 0, reasoningTokens: 0 },
      responseModel: "scripted",
      finishReason: toolCalls.length > 0 ? "tool_calls" : "stop",
    };
  }

  /**
   * Estimates four characters per token over the request text.
   * @param request - The request.
   * @returns The estimate.
   */
  public estimateInputTokens(request: ModelRequest): number {
    let characters = request.instructions.length;
    for (const item of request.inputItems) {
      characters += (item.type === "model_output" ? item.item : item.type === "user_text" ? item.text : item.output)
        .length;
    }
    return Math.floor(characters / 4);
  }

  /**
   * Returns the text of every user item in a request, oldest first.
   * @param request - The request.
   * @returns User texts, including Dex notes stored as user text.
   */
  public static userTexts(request: ModelRequest): string[] {
    return request.inputItems.flatMap((item) => (item.type === "user_text" ? [item.text] : []));
  }

  /**
   * Returns the tool results after the last user item of a request.
   *
   * A continuation message from `decideTurnContinuation` is a user item too. After one, this returns only the
   * results that follow it, so a script that decides from these results must also read the earlier ones itself.
   * @param request - The request.
   * @returns The tool results after the last user item, oldest first.
   */
  public static toolResults(request: ModelRequest): ModelToolResultItem[] {
    let results: ModelToolResultItem[] = [];
    for (const item of request.inputItems) {
      if (item.type === "user_text") {
        results = [];
      } else if (item.type === "tool_result") {
        results.push(item);
      }
    }
    return results;
  }
}

function createScriptedItem(
  item: Readonly<Record<string, unknown>>,
  text: string | null,
  toolCalls: readonly ModelToolCall[],
): ModelOutputItem {
  return {
    type: "model_output",
    provider: scriptedModelProvider,
    api: scriptedModelApi,
    item: JSON.stringify(item),
    text,
    toolCalls,
  };
}
