// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// A scripted runner that takes images, the same agent as the Python core's tests/image_agent.py.
//
// Commands: `describe` loads every image of the replayed framework items and answers `saw <n> images`;
// `screenshot` asks for `take_screenshot`, which uploads a PNG and returns it as a reference; `screenshot-gate <gate>`
// waits at that gate after the upload on the tool's first attempt; `screenshot-hold <gate>` waits at that gate in the
// second model call; `bad-screenshot` asks for `return_invalid_image`, a single-attempt write whose result carries an
// invalid reference; `stall <gate>` waits at that gate, then answers. Every model call records
// `<turnId>:<model call>:<sha256>:<size>,...` in image-loads.log, and every upload records
// `upload <sha256> attempt <n>` in uploads.log.

import { deflateSync } from "node:zlib";
import { join } from "node:path";

import {
  createAgentSessionFlow,
  describeToolCallOutcome,
  loadImages,
  ToolExecutionError,
  type AgentFrameworkRunner,
  type AgentImageReference,
  type AgentSessionFlow,
  type AgentTurnContext,
  type AgentTurnOutcome,
  type AgentUsage,
  type DeferredToolExecution,
  type FrameworkMessage,
  type MediaStore,
  type ToolExecutionResult,
  type ToolPolicy,
} from "../../src/index.js";
import { FileGate, InvocationLog } from "../../src/testing/index.js";

export const imageTestAgentFlowType = "ImageAgent";
export const imageTestAgentStateDirectoryVariable = "IMAGE_AGENT_STATE_DIR";
export const imageLoadsLogName = "image-loads.log";
export const uploadsLogName = "uploads.log";
export const mediaDirectoryName = "media";

const modelCallUsage: AgentUsage = {
  modelCalls: 1,
  inputTokens: 10,
  outputTokens: 5,
  cachedInputTokens: 0,
  reasoningTokens: 0,
};
const toolPolicies: Readonly<Record<string, ToolPolicy>> = {
  take_screenshot: { effect: "read" },
  return_invalid_image: { effect: "non_idempotent_write", requiresApproval: false },
};

/** Returns the CRC-32 that PNG chunks carry; node:zlib exports one only from Node 22.2. */
function crc32(data: Buffer): number {
  let crc = 0xffffffff;
  for (const byte of data) {
    crc ^= byte;
    for (let bit = 0; bit < 8; bit += 1) {
      crc = crc & 1 ? (crc >>> 1) ^ 0xedb88320 : crc >>> 1;
    }
  }
  return (crc ^ 0xffffffff) >>> 0;
}

/** Returns the 2x2 PNG that `take_screenshot` uploads, the same bytes as the Python core's `screenshot_png`. */
export function screenshotPng(): Uint8Array {
  const chunk = (kind: string, data: Buffer): Buffer => {
    const kindBytes = Buffer.from(kind, "latin1");
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    const checksum = Buffer.alloc(4);
    checksum.writeUInt32BE(crc32(Buffer.concat([kindBytes, data])));
    return Buffer.concat([length, kindBytes, data, checksum]);
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(2, 0);
  header.writeUInt32BE(2, 4);
  header.set([8, 2, 0, 0, 0], 8);
  const rows = Buffer.from([0x00, 0xff, 0x00, 0x00, 0x00, 0xff, 0x00, 0x00, 0x00, 0x00, 0xff, 0xff, 0xff, 0xff]);
  return new Uint8Array(
    Buffer.concat([
      Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
      chunk("IHDR", header),
      chunk("IDAT", deflateSync(rows, { level: 9 })),
      chunk("IEND", Buffer.alloc(0)),
    ]),
  );
}

export class ImageTestRunner implements AgentFrameworkRunner {
  private readonly imageLoads: InvocationLog;
  private readonly uploads: InvocationLog;

  public constructor(
    private readonly stateDirectory: string,
    private readonly store: MediaStore,
  ) {
    this.imageLoads = new InvocationLog(join(stateDirectory, imageLoadsLogName));
    this.uploads = new InvocationLog(join(stateDirectory, uploadsLogName));
  }

  public buildUserMessage(text: string): FrameworkMessage {
    return this.buildUserMessageWithImages(text, []);
  }

  public buildUserMessageWithImages(text: string, images: readonly AgentImageReference[]): FrameworkMessage {
    const item = { type: "user", text, ...(images.length > 0 ? { images: images.map(imageJson) } : {}) };
    return message("user", { text, frameworkItem: JSON.stringify(item) });
  }

  public async runModelStep(turn: AgentTurnContext): Promise<AgentTurnOutcome> {
    if (turn.frameworkRunState !== null) {
      return this.continueAfterToolCall(turn, JSON.parse(turn.frameworkRunState) as RunState);
    }
    await this.loadTurnImages(turn, 1, []);
    const command = currentUserText(turn);
    if (command === "describe") {
      return answer(`saw ${replayedImages(turn).length} images`);
    }
    if (command.startsWith("stall ")) {
      const gate = new FileGate(this.stateDirectory, command.slice("stall ".length));
      gate.markReached();
      await gate.waitForRelease();
      return answer("stalled");
    }
    if (command === "bad-screenshot") {
      return requestTool(turn, command, "return_invalid_image", {});
    }
    if (command === "screenshot" || command.startsWith("screenshot-gate ") || command.startsWith("screenshot-hold ")) {
      const toolArguments = command.startsWith("screenshot-gate ") ? { gate: command.split(" ", 2)[1] } : {};
      return requestTool(turn, command, "take_screenshot", toolArguments);
    }
    throw new Error(`unknown image command ${command}`);
  }

  public async executeDeferredToolCall(call: DeferredToolExecution): Promise<ToolExecutionResult> {
    const record = call.toolCall;
    const toolArguments = JSON.parse(record.arguments) as { readonly gate?: string };
    if (record.toolName === "return_invalid_image") {
      const reference = await this.store.putImage(screenshotPng());
      return { output: "returned a bad image", images: [{ ...reference, mimeType: "image/bmp" }] };
    }
    if (record.toolName !== "take_screenshot") {
      throw new ToolExecutionError(`unknown tool ${record.toolName}`);
    }
    const reference = await this.store.putImage(screenshotPng());
    this.uploads.record(`upload ${reference.sha256} attempt ${call.attempt}`);
    if (toolArguments.gate !== undefined && call.attempt === 1) {
      const gate = new FileGate(this.stateDirectory, toolArguments.gate);
      gate.markReached();
      await gate.waitForRelease();
    }
    return { output: "took a screenshot", images: [reference] };
  }

  private async continueAfterToolCall(turn: AgentTurnContext, state: RunState): Promise<AgentTurnOutcome> {
    const record = turn.recordedToolCall(state.toolCallId);
    if (record === undefined) {
      throw new Error(`no record for tool call ${state.toolCallId}`);
    }
    const images = record.images ?? [];
    await this.loadTurnImages(turn, 2, images);
    if (state.command.startsWith("screenshot-hold ")) {
      const gate = new FileGate(this.stateDirectory, state.command.slice("screenshot-hold ".length));
      gate.markReached();
      await gate.waitForRelease();
    }
    const output = describeToolCallOutcome(record);
    const toolResult = {
      type: "tool_result",
      tool_call_id: state.toolCallId,
      output,
      ...(images.length > 0 ? { images: images.map(imageJson) } : {}),
    };
    return {
      kind: "completed",
      usage: modelCallUsage,
      newMessages: [
        message("assistant", {
          toolCallId: state.toolCallId,
          toolName: record.toolName,
          toolArguments: record.arguments,
          frameworkItem: JSON.stringify({ type: "tool_call", tool_call_id: state.toolCallId }),
        }),
        {
          ...message("tool", {
            toolCallId: state.toolCallId,
            toolName: record.toolName,
            toolOutput: output,
            frameworkItem: JSON.stringify(toolResult),
          }),
          images,
        },
        message("assistant", {
          text: `${record.toolName}: ${output}`,
          frameworkItem: JSON.stringify({ type: "assistant", text: output }),
        }),
      ],
    };
  }

  private async loadTurnImages(
    turn: AgentTurnContext,
    modelCall: number,
    extra: readonly AgentImageReference[],
  ): Promise<void> {
    const images = await loadImages(this.store, [...replayedImages(turn), ...extra]);
    const loaded = [...images].map(([sha256, data]) => `${sha256}:${data.length}`).join(",");
    this.imageLoads.record(`${turn.turnId}:${modelCall}:${loaded}`);
  }
}

export function createImageTestAgentFlow(stateDirectory: string, store: MediaStore): AgentSessionFlow<ImageTestRunner> {
  return createAgentSessionFlow(new ImageTestRunner(stateDirectory, store), imageTestAgentFlowType, { toolPolicies });
}

interface RunState {
  readonly command: string;
  readonly toolCallId: string;
}

/** The images in the stored framework items, the replay truth, in order. */
function replayedImages(turn: AgentTurnContext): AgentImageReference[] {
  return turn.messages.flatMap((stored) => {
    const item = JSON.parse(stored.frameworkItem) as { readonly images?: readonly ImageJson[] };
    return (item.images ?? []).map((image) => ({
      type: "image",
      uri: image.uri,
      mimeType: image.mime_type,
      sha256: image.sha256,
      sizeBytes: image.size_bytes,
    }));
  });
}

interface ImageJson {
  readonly type: string;
  readonly uri: string;
  readonly mime_type: string;
  readonly sha256: string;
  readonly size_bytes: number;
}

function imageJson(image: AgentImageReference): ImageJson {
  return {
    type: image.type,
    uri: image.uri,
    mime_type: image.mimeType,
    sha256: image.sha256,
    size_bytes: image.sizeBytes,
  };
}

function currentUserText(turn: AgentTurnContext): string {
  for (const stored of [...turn.messages].reverse()) {
    if (stored.role === "user" && stored.turnId === turn.turnId) {
      return (JSON.parse(stored.frameworkItem) as { readonly text: string }).text;
    }
  }
  throw new Error(`turn ${turn.turnId} has no user message`);
}

function requestTool(
  turn: AgentTurnContext,
  command: string,
  toolName: string,
  toolArguments: Readonly<Record<string, unknown>>,
): AgentTurnOutcome {
  const toolCallId = `${turn.turnId}-call-0`;
  return {
    kind: "tool_calls_requested",
    frameworkRunState: JSON.stringify({ command, toolCallId } satisfies RunState),
    toolCalls: [
      { toolCallId, toolName, arguments: JSON.stringify(toolArguments), policy: turn.resolveToolPolicy(toolName) },
    ],
    usage: modelCallUsage,
  };
}

function answer(text: string): AgentTurnOutcome {
  return {
    kind: "completed",
    usage: modelCallUsage,
    newMessages: [message("assistant", { text, frameworkItem: JSON.stringify({ type: "assistant", text }) })],
  };
}

function message(
  role: FrameworkMessage["role"],
  fields: Partial<Omit<FrameworkMessage, "role" | "frameworkItem">> & { readonly frameworkItem: string },
): FrameworkMessage {
  return {
    role,
    text: null,
    toolCallId: null,
    toolName: null,
    toolArguments: null,
    toolOutput: null,
    ...fields,
  };
}
