// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { deflateSync } from "node:zlib";

import type {
  AgentTurnContext,
  DeferredToolCall,
  DeferredToolExecution,
  MediaStore,
  SourceToolDefinition,
  ToolExecutionResult,
  ToolSource,
  ToolTaskPollOutcome,
} from "@superdurable/dex-agents";

/** The PNG a user sends in the image tests: one red pixel. */
export const userImagePng = solidColorPng(0xff, 0x00, 0x00);
/** The PNG that `take_screenshot` uploads: one blue pixel. */
export const screenshotPng = solidColorPng(0x00, 0x00, 0xff);
/** Output text of `take_screenshot`. */
export const screenshotToolOutput = "took a screenshot";

/** A tool source with one `read` tool, `take_screenshot`, which uploads {@link screenshotPng} and returns it. */
export class ScreenshotToolSource implements ToolSource {
  public readonly sourceName = "test-screenshots";

  public constructor(private readonly store: MediaStore) {}

  public listTools(): Promise<readonly SourceToolDefinition[]> {
    return Promise.resolve([
      {
        name: "take_screenshot",
        description: "Take a screenshot",
        inputSchema: { type: "object", properties: { requested_by: { type: "string" } } },
      },
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
    return { output: screenshotToolOutput, images: [await this.store.putImage(screenshotPng)] };
  }

  public pollToolTask(): Promise<ToolTaskPollOutcome> {
    return Promise.reject(new Error("take_screenshot starts no tool task"));
  }
}

/** Returns a 1x1 RGB PNG of one color. */
function solidColorPng(red: number, green: number, blue: number): Uint8Array {
  const chunk = (kind: string, data: Buffer): Buffer => {
    const kindBytes = Buffer.from(kind, "latin1");
    const length = Buffer.alloc(4);
    length.writeUInt32BE(data.length);
    const checksum = Buffer.alloc(4);
    checksum.writeUInt32BE(crc32(Buffer.concat([kindBytes, data])));
    return Buffer.concat([length, kindBytes, data, checksum]);
  };
  const header = Buffer.alloc(13);
  header.writeUInt32BE(1, 0);
  header.writeUInt32BE(1, 4);
  header.set([8, 2, 0, 0, 0], 8);
  return new Uint8Array(
    Buffer.concat([
      Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
      chunk("IHDR", header),
      chunk("IDAT", deflateSync(Buffer.from([0x00, red, green, blue]))),
      chunk("IEND", Buffer.alloc(0)),
    ]),
  );
}

/** Returns the CRC-32 that PNG chunks carry. */
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
