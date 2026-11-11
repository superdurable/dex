// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { readFileSync } from "node:fs";
import { join } from "node:path";

import { Agent, tool, type CallModelInputFilter } from "@openai/agents";
import {
  LocalDirectoryMediaStore,
  type AgentSessionFlow,
  type AgentTurnContext,
  type DeferredToolCall,
  type MediaStore,
  type SourceToolDefinition,
  type ToolExecutionResult,
  type ToolSource,
  type ToolTaskPollOutcome,
} from "@superdurable/dex-agents";
import { FileGate, InvocationLog } from "@superdurable/dex-agents/testing";
import { z } from "zod";

import { createOpenAIAgentsSessionFlow } from "../../src/index.js";
import { ScriptedOpenAIModel } from "./scripted-openai-model.js";

/** Flow type of the agent with a media store. */
export const imageOpenAIAgentFlowType = "ImageOpenAIAgent";
/** Environment variable naming the directory of the marker files and the media store. */
export const imageAgentMarkerDirectoryVariable = "IMAGE_AGENT_MARKER_DIRECTORY";
/** Log of the image parts the run configuration's own `callModelInputFilter` saw. */
export const inputFilterLogName = "input-filter.log";

const conformanceImages = new Map(
  (
    JSON.parse(
      readFileSync(new URL("../../../../../conformance/images.json", import.meta.url), "utf8"),
    ) as { readonly images: readonly { readonly name: string; readonly base64: string }[] }
  ).images.map((image) => [image.name, new Uint8Array(Buffer.from(image.base64, "base64"))]),
);
/** The image a test attaches to a user message. */
export const userPng = conformanceImages.get("png") as Uint8Array;
/** The image `take_screenshot` uploads and returns. */
export const screenshotJpeg = conformanceImages.get("jpeg") as Uint8Array;

const screenshotTool: SourceToolDefinition = {
  name: "take_screenshot",
  description: "Take a screenshot of the screen.",
  inputSchema: { type: "object", properties: {} },
};

/** A tool source whose one tool uploads an image and returns its reference. */
class ScreenshotToolSource implements ToolSource {
  public readonly sourceName = "screenshots";

  public constructor(
    private readonly mediaStore: MediaStore,
    private readonly markerDirectory: string,
  ) {}

  public async listTools(): Promise<readonly SourceToolDefinition[]> {
    return [screenshotTool];
  }

  public deferredToolCall(
    turn: AgentTurnContext,
    call: { readonly toolCallId: string; readonly toolName: string; readonly arguments: string },
  ): DeferredToolCall {
    return { ...call, policy: turn.resolveToolPolicy(call.toolName) };
  }

  public async executeDeferredToolCall(): Promise<ToolExecutionResult> {
    new InvocationLog(join(this.markerDirectory, "take_screenshot.log")).record("take_screenshot");
    const reference = await this.mediaStore.putImage(screenshotJpeg);
    return { output: "took a screenshot", images: [reference] };
  }

  public async pollToolTask(): Promise<ToolTaskPollOutcome> {
    throw new Error("take_screenshot starts no tool tasks");
  }
}

/** Returns the media store the test, the Worker, and the tool share. */
export function createImageMediaStore(markerDirectory: string): LocalDirectoryMediaStore {
  return new LocalDirectoryMediaStore(join(markerDirectory, "media"));
}

/**
 * Creates the agent Flow with a media store; tests and the Worker child build it identically.
 *
 * The scripted model records its requests in `model-requests.log`. `take_screenshot` comes from a tool source, and
 * `lookup_a` is a function tool that waits at the `lookups` FileGate. The run configuration's own filter records
 * `images <n> bytes <boolean>` for the image parts it saw.
 */
export function createImageOpenAIAgentFlow(markerDirectory: string): AgentSessionFlow {
  const mediaStore = createImageMediaStore(markerDirectory);
  const lookupA = tool({
    name: "lookup_a",
    description: "Look up record a.",
    parameters: z.object({ key: z.string() }),
    errorFunction: null,
    execute: async (_input, _runContext, details) => {
      const gate = new FileGate(markerDirectory, "lookups");
      gate.markReached();
      await gate.waitForRelease(details?.signal);
      return "lookup_a found";
    },
  });
  const recordModelInput: CallModelInputFilter = ({ modelData }) => {
    const images = modelData.input.flatMap((item) => {
      const record = item as { readonly content?: unknown; readonly output?: unknown };
      const parts: unknown = "content" in record ? record.content : record.output;
      return Array.isArray(parts)
        ? parts.filter((part: { type?: unknown }) => part.type === "input_image").map((part: { image?: unknown }) => part.image)
        : [];
    });
    const hasBytes = images.some((image) => typeof image === "string" && image.startsWith("data:"));
    new InvocationLog(join(markerDirectory, inputFilterLogName)).record(`images ${images.length} bytes ${hasBytes}`);
    return modelData;
  };
  const agent = new Agent({
    name: "Assistant",
    instructions: "You are the scripted assistant.",
    model: new ScriptedOpenAIModel(markerDirectory, true),
    tools: [lookupA],
  });
  return createOpenAIAgentsSessionFlow(agent, imageOpenAIAgentFlowType, {
    toolPolicies: { take_screenshot: { effect: "read" }, lookup_a: { effect: "read" } },
    runConfig: { tracingDisabled: true, callModelInputFilter: recordModelInput },
    toolSources: [new ScreenshotToolSource(mediaStore, markerDirectory)],
    mediaStore,
  });
}
