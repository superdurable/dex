// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

// Image content of MCP tool results, stored in a media store as image references.

import type { CallToolResult } from "@modelcontextprotocol/sdk/types.js";

import { ToolExecutionError, type ToolExecutionResult } from "../agent-framework-runner.js";
import { ImageValidationError, maxImagesPerMessage, type MediaStore } from "../agent-media.js";
import type { AgentImageReference } from "../agent-session-values.js";
import { serializeToolOutputJson } from "./mcp-resource-and-prompt-tools.js";

// Canonical padded base64, the same check as the Python core's.
const base64Pattern = /^(?:[A-Za-z0-9+/]{4})*(?:[A-Za-z0-9+/]{2}==|[A-Za-z0-9+/]{3}=)?$/;

type ImageBlock = Extract<CallToolResult["content"][number], { type: "image" }>;

/** Reports whether a result carries at least one `image` content block. */
export function hasImageContent(result: CallToolResult): boolean {
  return result.content.some((block) => block.type === "image");
}

/**
 * Uploads a successful result's image blocks and returns the tool output with their references.
 *
 * Each image block becomes `[image <n>]` in the output text, `n` counting from 1 in `images` order. The caller
 * passes only a result with image blocks.
 * @throws {ToolExecutionError} When the result has more than {@link maxImagesPerMessage} images, or an image is not
 * valid base64 or not an image the store accepts.
 */
export async function storeToolResultImages(
  result: CallToolResult,
  store: MediaStore,
  toolName: string,
): Promise<ToolExecutionResult> {
  const imageCount = result.content.filter((block) => block.type === "image").length;
  if (imageCount > maxImagesPerMessage) {
    throw new ToolExecutionError(
      `MCP tool ${toolName} returned ${imageCount} images; a tool result carries at most ${maxImagesPerMessage}`,
    );
  }
  const references: AgentImageReference[] = [];
  const parts: string[] = [];
  for (const block of result.content) {
    if (block.type === "text") {
      parts.push(block.text);
    } else if (block.type === "image") {
      references.push(await storeImage(block, store, toolName, references.length + 1));
      parts.push(`[image ${references.length}]`);
    } else {
      parts.push(serializeToolOutputJson(block));
    }
  }
  return { output: parts.join("\n"), images: references };
}

async function storeImage(
  block: ImageBlock,
  store: MediaStore,
  toolName: string,
  imageNumber: number,
): Promise<AgentImageReference> {
  if (typeof block.data !== "string" || !base64Pattern.test(block.data)) {
    throw new ToolExecutionError(`MCP tool ${toolName} returned image ${imageNumber}, which is not valid base64`);
  }
  try {
    return await store.putImage(new Uint8Array(Buffer.from(block.data, "base64")));
  } catch (error: unknown) {
    if (error instanceof ImageValidationError) {
      throw new ToolExecutionError(
        `MCP tool ${toolName} returned image ${imageNumber}, which is not a supported image: ${error.message}`,
      );
    }
    throw error;
  }
}
