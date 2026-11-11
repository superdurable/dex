// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// A built-in loop agent on the AI SDK client with a scripted language model and a screenshot tool source.
//
// The model reads the last user text:
// - `describe` answers `saw <n> images`, counting the file parts of the prompt.
// - `screenshot` calls `take_screenshot`, which uploads a PNG and returns its reference, then answers
//   `screenshot taken`.
// - `screenshot-hold <gate>` does the same, but the second model call waits at that FileGate after it logs.
// - `mcp-screenshot` calls `local__take_screenshot` of an McpToolSource with the media store, whose fake stdio
//   server returns an image block, then answers `mcp screenshot taken`.
// - `screenshot-continue` calls `take_screenshot` with the text `continue after tools`, so `decideTurnContinuation`
//   continues the turn with the user text `Describe the screenshot.`, which the model answers `described`.
// - `function-screenshot` and `function-screenshot-hold <gate>` work like `screenshot` and `screenshot-hold`, but call
//   `take_function_screenshot`, a function tool that uploads the PNG through `ToolCallContext.mediaStore` and returns
//   `functionToolResult`. Without a store it returns a reference it built itself. It logs `store present` or
//   `store absent` with its attempt in `function-tool-stores.log`.
// - `function-invalid-image` calls `return_invalid_function_image`, a single-attempt write function tool that returns
//   a reference with an unsupported media type.
// - `function-plain-json` and `function-plain-json-images` call `return_plain_result_json`, a function tool that
//   returns a plain object with the fields of a `FunctionToolResult`, without or with one image reference.
// While the file `store-reads-fail` exists in the state directory, every image read fails with MediaStoreError and
// is logged as `read-failed <sha256>` in `store-reads.log`.
// Every call appends one JSON line to `model-prompts.log`: each prompt message's role and parts, with each file
// part as its media type and the SHA-256 and size of its bytes.

import { createHash } from "node:crypto";
import { existsSync } from "node:fs";
import { join } from "node:path";

import { MockLanguageModelV4 } from "ai/test";

import type {
  AgentTurnContext,
  DeferredToolCall,
  DeferredToolExecution,
  ToolExecutionResult,
  ToolTaskPoll,
  ToolTaskPollOutcome,
} from "../../src/agent-framework-runner.js";
import type { AgentSessionFlow } from "../../src/agent-session-flow.js";
import { MediaStoreError, buildImageReference, type MediaStore } from "../../src/agent-media.js";
import type { AgentImageReference } from "../../src/agent-session-values.js";
import { createAiSdkModelClient } from "../../src/ai-sdk/index.js";
import {
  createBuiltinAgentSessionFlow,
  functionTool,
  functionToolResult,
  type BuiltinLoopAgentRunner,
  type FunctionTool,
  type SourceToolDefinition,
  type ToolSource,
  type TurnContinuation,
  type TurnContinuationContext,
} from "../../src/builtin-loop/index.js";
import { McpToolSource } from "../../src/mcp/index.js";
import { FileGate, InvocationLog } from "../../src/testing/index.js";
import { fakeMcpServerModulePath, fakeMcpStateDirectoryVariable } from "./fake-mcp-server-names.js";
import { screenshotPng } from "./image-test-agent.js";

export const builtinImageAgentFlowType = "BuiltinImageAgent";
export const builtinNoStoreImageAgentFlowType = "BuiltinNoStoreImageAgent";
export const builtinImageAgentStateDirectoryVariable = "BUILTIN_IMAGE_AGENT_STATE_DIR";
export const modelPromptsLogName = "model-prompts.log";
export const screenshotToolName = "take_screenshot";
export const mcpScreenshotToolName = "local__take_screenshot";
export const functionScreenshotToolName = "take_function_screenshot";
export const invalidFunctionImageToolName = "return_invalid_function_image";
export const plainResultJsonToolName = "return_plain_result_json";
export const functionToolStoresLogName = "function-tool-stores.log";
const noStoreUri = "file:///no-media-store";
export const storeReadsFailFileName = "store-reads-fail";
export const storeReadsLogName = "store-reads.log";
export const continuationMessage = "Describe the screenshot.";
const continueAfterToolsText = "continue after tools";

type LanguageModelCallOptions = Parameters<MockLanguageModelV4["doStream"]>[0];
type LanguageModelPrompt = LanguageModelCallOptions["prompt"];
type LanguageModelStream = Awaited<ReturnType<MockLanguageModelV4["doStream"]>>["stream"];
type LanguageModelStreamPart = LanguageModelStream extends ReadableStream<infer Part> ? Part : never;

/** What the model saw of one prompt part. */
export type SeenPart =
  | { readonly type: "text"; readonly text: string }
  | { readonly type: "file"; readonly mediaType: string; readonly sha256: string; readonly size: number }
  | { readonly type: "tool-call"; readonly toolName: string }
  | { readonly type: "tool-result"; readonly toolName: string; readonly output: readonly SeenPart[] | string };

/** What the model saw of one prompt message. */
export interface SeenMessage {
  readonly role: string;
  readonly parts: readonly SeenPart[] | string;
}

/**
 * Creates the agent with a media store and the same agent without one.
 * @param stateDirectory - Directory of the log and the gates.
 * @param store - The store the screenshot tool uploads to and the first Flow reads from.
 * @returns The two Flows.
 */
export function createBuiltinImageAgentFlows(
  stateDirectory: string,
  store: MediaStore,
): AgentSessionFlow<BuiltinLoopAgentRunner>[] {
  const switchableStore = new SwitchableReadMediaStore(store, stateDirectory);
  const mcpTools = new McpToolSource(
    [
      {
        transport: "stdio",
        name: "local",
        command: process.execPath,
        args: [fakeMcpServerModulePath, "stdio"],
        env: { [fakeMcpStateDirectoryVariable]: stateDirectory },
      },
    ],
    { mediaStore: switchableStore },
  );
  const definition = {
    name: "image-agent",
    instructions: "You look at images.",
    model: createAiSdkModelClient(createImageLanguageModel(stateDirectory)),
    tools: createFunctionImageTools(stateDirectory),
    toolSources: [new ScreenshotToolSource(switchableStore), mcpTools],
    decideTurnContinuation: continueAfterTools,
  };
  return [
    createBuiltinAgentSessionFlow(definition, builtinImageAgentFlowType, { mediaStore: switchableStore }),
    createBuiltinAgentSessionFlow(definition, builtinNoStoreImageAgentFlowType),
  ];
}

/**
 * Reads the logged prompts.
 * @param stateDirectory - The agent's state directory.
 * @returns Every logged prompt, oldest first.
 */
export function readLoggedPrompts(stateDirectory: string): SeenMessage[][] {
  return new InvocationLog(join(stateDirectory, modelPromptsLogName))
    .names()
    .map((line) => JSON.parse(line) as SeenMessage[]);
}

/**
 * Describes one image as the model sees it.
 * @param data - The image bytes.
 * @param mediaType - The image's media type.
 * @returns The seen file part.
 */
export function seenImage(data: Uint8Array, mediaType: string): SeenPart {
  return { type: "file", mediaType, sha256: sha256Of(data), size: data.length };
}

/** Creates the function tools that return images through `functionToolResult`. */
function createFunctionImageTools(stateDirectory: string): FunctionTool[] {
  const stores = new InvocationLog(join(stateDirectory, functionToolStoresLogName));
  return [
    functionTool({
      name: functionScreenshotToolName,
      description: "Take a screenshot of the screen.",
      parameters: { type: "object", properties: {} },
      effect: "read",
      execute: async (_args, context) => {
        let reference: AgentImageReference;
        if (context.mediaStore === undefined) {
          stores.record(`store absent attempt ${context.attempt}`);
          reference = buildImageReference(screenshotPng(), noStoreUri);
        } else {
          stores.record(`store present attempt ${context.attempt}`);
          reference = await context.mediaStore.putImage(screenshotPng());
        }
        return functionToolResult({ output: "Took a function screenshot.", images: [reference] });
      },
    }),
    functionTool({
      name: invalidFunctionImageToolName,
      description: "Return an image reference with an unsupported media type.",
      parameters: { type: "object", properties: {} },
      effect: "non_idempotent_write",
      requiresApproval: false,
      execute: () => {
        const reference = buildImageReference(screenshotPng(), noStoreUri);
        return functionToolResult({
          output: "returned a bad image",
          images: [{ ...reference, mimeType: "image/bmp" }],
        });
      },
    }),
    functionTool<{ readonly withImages: boolean }>({
      name: plainResultJsonToolName,
      description: "Return plain JSON with the fields of a function tool result.",
      parameters: {
        type: "object",
        properties: { withImages: { type: "boolean" } },
        required: ["withImages"],
      },
      effect: "read",
      execute: (args) => plainResultJson(args.withImages),
    }),
  ];
}

/**
 * Builds the plain object that `return_plain_result_json` returns.
 * @param withImages - Whether the object lists one image reference.
 * @returns An object with the fields of a function tool result that no factory created.
 */
export function plainResultJson(withImages: boolean): Record<string, unknown> {
  return {
    kind: "function_tool_result",
    output: "data",
    images: withImages ? [buildImageReference(screenshotPng(), noStoreUri)] : [],
  };
}

/** Continues with a user message after the tools of a response that says so. */
function continueAfterTools(context: TurnContinuationContext): TurnContinuation {
  const asksToContinue = context.modelOutput.some((item) => item.text === continueAfterToolsText);
  return context.toolResults.length > 0 && asksToContinue
    ? { action: "continue", message: continuationMessage }
    : "default";
}

/** A media store whose reads fail while the test's flag file exists. */
class SwitchableReadMediaStore implements MediaStore {
  public constructor(
    private readonly store: MediaStore,
    private readonly stateDirectory: string,
  ) {}

  public putImage(data: Uint8Array): Promise<AgentImageReference> {
    return this.store.putImage(data);
  }

  public getImage(reference: AgentImageReference): Promise<Uint8Array> {
    if (existsSync(join(this.stateDirectory, storeReadsFailFileName))) {
      new InvocationLog(join(this.stateDirectory, storeReadsLogName)).record(`read-failed ${reference.sha256}`);
      return Promise.reject(new MediaStoreError(`the test made reads of ${reference.uri} fail`));
    }
    return this.store.getImage(reference);
  }
}

class ScreenshotToolSource implements ToolSource {
  public readonly sourceName = "screenshots";

  public constructor(private readonly store: MediaStore) {}

  public listTools(): Promise<readonly SourceToolDefinition[]> {
    return Promise.resolve([
      { name: screenshotToolName, description: "Take a screenshot.", inputSchema: { type: "object", properties: {} } },
    ]);
  }

  public deferredToolCall(
    turn: AgentTurnContext,
    call: { readonly toolCallId: string; readonly toolName: string; readonly arguments: string },
  ): DeferredToolCall {
    return {
      toolCallId: call.toolCallId,
      toolName: call.toolName,
      arguments: call.arguments,
      policy: turn.resolveToolPolicy(call.toolName, "read"),
    };
  }

  public async executeDeferredToolCall(_call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const reference = await this.store.putImage(screenshotPng());
    return { output: "Took a screenshot.", images: [reference] };
  }

  public pollToolTask(_poll: ToolTaskPoll): Promise<ToolTaskPollOutcome> {
    return Promise.reject(new RangeError("take_screenshot starts no tool task"));
  }
}

function createImageLanguageModel(stateDirectory: string): MockLanguageModelV4 {
  return new MockLanguageModelV4({
    provider: "scripted.images",
    modelId: "scripted-image-model",
    doStream: async (options) => ({
      stream: readableStreamFromAsyncIterable(streamImageResponse(stateDirectory, options)),
    }),
  });
}

async function* streamImageResponse(
  stateDirectory: string,
  options: LanguageModelCallOptions,
): AsyncGenerator<LanguageModelStreamPart> {
  const seen = describePrompt(options.prompt);
  new InvocationLog(join(stateDirectory, modelPromptsLogName)).record(JSON.stringify(seen));
  const lastUserIndex = options.prompt.map((message) => message.role).lastIndexOf("user");
  const lastUser = seen[lastUserIndex];
  const command =
    lastUser === undefined || typeof lastUser.parts === "string"
      ? ""
      : lastUser.parts.flatMap((part) => (part.type === "text" ? [part.text] : [])).join("");
  const hasToolResult = options.prompt.slice(lastUserIndex + 1).some((message) => message.role === "tool");
  yield { type: "stream-start", warnings: [] };
  if (command === "describe") {
    const fileCount = JSON.stringify(seen).split('"type":"file"').length - 1;
    yield* streamAnswer(`saw ${fileCount} images`);
    return;
  }
  const isFunctionScreenshot = command.startsWith("function-screenshot");
  const screenshotCommand = isFunctionScreenshot ? command.slice("function-".length) : command;
  if (screenshotCommand === "screenshot" || screenshotCommand.startsWith("screenshot-hold ")) {
    if (!hasToolResult) {
      const toolName = isFunctionScreenshot ? functionScreenshotToolName : screenshotToolName;
      yield { type: "tool-call", toolCallId: `call-${seen.length}`, toolName, input: "{}" };
      yield finishPart("tool-calls");
      return;
    }
    if (screenshotCommand.startsWith("screenshot-hold ")) {
      const gate = new FileGate(stateDirectory, screenshotCommand.slice("screenshot-hold ".length));
      gate.markReached();
      await gate.waitForRelease(options.abortSignal);
    }
    yield* streamAnswer("screenshot taken");
    return;
  }
  if (command === "function-invalid-image") {
    if (!hasToolResult) {
      yield {
        type: "tool-call",
        toolCallId: `call-${seen.length}`,
        toolName: invalidFunctionImageToolName,
        input: "{}",
      };
      yield finishPart("tool-calls");
      return;
    }
    yield* streamAnswer("invalid image returned");
    return;
  }
  if (command === "function-plain-json" || command === "function-plain-json-images") {
    if (!hasToolResult) {
      yield {
        type: "tool-call",
        toolCallId: `call-${seen.length}`,
        toolName: plainResultJsonToolName,
        input: JSON.stringify({ withImages: command === "function-plain-json-images" }),
      };
      yield finishPart("tool-calls");
      return;
    }
    yield* streamAnswer("plain json returned");
    return;
  }
  if (command === "screenshot-continue") {
    yield { type: "text-start", id: "t1" };
    yield { type: "text-delta", id: "t1", delta: continueAfterToolsText };
    yield { type: "text-end", id: "t1" };
    yield { type: "tool-call", toolCallId: `call-${seen.length}`, toolName: screenshotToolName, input: "{}" };
    yield finishPart("tool-calls");
    return;
  }
  if (command === continuationMessage) {
    yield* streamAnswer("described");
    return;
  }
  if (command === "mcp-screenshot") {
    if (!hasToolResult) {
      yield { type: "tool-call", toolCallId: `call-${seen.length}`, toolName: mcpScreenshotToolName, input: "{}" };
      yield finishPart("tool-calls");
      return;
    }
    yield* streamAnswer("mcp screenshot taken");
    return;
  }
  throw new Error(`unknown image command ${command}`);
}

function describePrompt(prompt: LanguageModelPrompt): SeenMessage[] {
  return prompt.map((message): SeenMessage => {
    if (message.role === "system") {
      return { role: message.role, parts: message.content };
    }
    return {
      role: message.role,
      parts: message.content.map((part): SeenPart => {
        switch (part.type) {
          case "text":
            return { type: "text", text: part.text };
          case "file":
            return describeFile(part.mediaType, part.data);
          case "tool-call":
            return { type: "tool-call", toolName: part.toolName };
          case "tool-result":
            return {
              type: "tool-result",
              toolName: part.toolName,
              output:
                part.output.type === "content"
                  ? part.output.value.map((item): SeenPart => {
                      if (item.type === "text") {
                        return { type: "text", text: item.text };
                      }
                      if (item.type === "file") {
                        return describeFile(item.mediaType, item.data);
                      }
                      throw new Error(`unexpected tool result part ${item.type}`);
                    })
                  : JSON.stringify(part.output),
            };
          default:
            throw new Error(`unexpected prompt part ${part.type}`);
        }
      }),
    };
  });
}

function describeFile(mediaType: string, data: unknown): SeenPart {
  const fileData = data as { readonly type?: unknown; readonly data?: unknown };
  if (fileData.type !== "data") {
    throw new Error(`a file part holds ${String(fileData.type)} data, not bytes`);
  }
  const bytes =
    typeof fileData.data === "string"
      ? new Uint8Array(Buffer.from(fileData.data, "base64"))
      : (fileData.data as Uint8Array);
  return { type: "file", mediaType, sha256: sha256Of(bytes), size: bytes.length };
}

function sha256Of(data: Uint8Array): string {
  return createHash("sha256").update(data).digest("hex");
}

function* streamAnswer(text: string): Generator<LanguageModelStreamPart> {
  yield { type: "text-start", id: "t1" };
  yield { type: "text-delta", id: "t1", delta: text };
  yield { type: "text-end", id: "t1" };
  yield finishPart("stop");
}

function finishPart(unified: "stop" | "tool-calls"): LanguageModelStreamPart {
  return {
    type: "finish",
    usage: {
      inputTokens: { total: 10, noCache: 10, cacheRead: 0, cacheWrite: 0 },
      outputTokens: { total: 5, text: 5, reasoning: 0 },
    },
    finishReason: { unified, raw: unified },
  };
}

function readableStreamFromAsyncIterable<Part>(parts: AsyncIterable<Part>): ReadableStream<Part> {
  const iterator = parts[Symbol.asyncIterator]();
  return new ReadableStream<Part>({
    async pull(controller) {
      const next = await iterator.next();
      if (next.done === true) {
        controller.close();
        return;
      }
      controller.enqueue(next.value);
    },
    async cancel() {
      await iterator.return?.();
    },
  });
}
