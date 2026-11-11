// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { FilePart, ModelMessage, TextPart, ToolResultPart } from "ai";

import {
  loadImages,
  maxImagesPerMessage,
  type AgentImageReference,
  type MediaStore,
} from "@superdurable/dex-agents";

// Stored image: text part `[image <uri>]` with the reference under this key; bytes replace it before streamText.
const imageReferenceProviderKey = "dexAgents";

type ToolResultContentPart = Extract<ToolResultPart["output"], { type: "content" }>["value"][number];

/** Builds the stored placeholder of one image. */
export function buildImagePlaceholderPart(reference: AgentImageReference): TextPart {
  return {
    type: "text",
    text: `[image ${reference.uri}]`,
    providerOptions: {
      [imageReferenceProviderKey]: {
        imageReference: {
          type: reference.type,
          uri: reference.uri,
          mimeType: reference.mimeType,
          sha256: reference.sha256,
          sizeBytes: reference.sizeBytes,
        },
      },
    },
  };
}

/** Returns the reference of a placeholder part, or `undefined` for any other part. */
export function readImagePlaceholder(part: { readonly type: string }): AgentImageReference | undefined {
  if (part.type !== "text") {
    return undefined;
  }
  const providerOptions = (part as { readonly providerOptions?: unknown }).providerOptions;
  const reference = readRecord(readRecord(providerOptions)?.[imageReferenceProviderKey])?.["imageReference"];
  const fields = readRecord(reference);
  if (
    fields === undefined ||
    fields["type"] !== "image" ||
    typeof fields["uri"] !== "string" ||
    typeof fields["mimeType"] !== "string" ||
    typeof fields["sha256"] !== "string" ||
    typeof fields["sizeBytes"] !== "number"
  ) {
    return undefined;
  }
  return {
    type: "image",
    uri: fields["uri"],
    mimeType: fields["mimeType"],
    sha256: fields["sha256"],
    sizeBytes: fields["sizeBytes"],
  };
}

/** Returns the images of a user message and of a tool message's results, in message order. */
export function listImagePlaceholders(message: ModelMessage): AgentImageReference[] {
  switch (message.role) {
    case "user":
      return typeof message.content === "string" ? [] : collectPlaceholders(message.content);
    case "tool":
      return message.content.flatMap((part) =>
        part.type === "tool-result" && part.output.type === "content" ? collectPlaceholders(part.output.value) : [],
      );
    default:
      return [];
  }
}

/** The display images of a stored message: each image once, at most `maxImagesPerMessage`. */
export function listDisplayImages(message: ModelMessage): AgentImageReference[] {
  const distinct = new Map<string, AgentImageReference>();
  for (const reference of listImagePlaceholders(message)) {
    if (!distinct.has(reference.sha256)) {
      distinct.set(reference.sha256, reference);
    }
  }
  // Parallel tool results share one message; the model still sees every image.
  return [...distinct.values()].slice(0, maxImagesPerMessage);
}

/** Returns copies with each placeholder replaced by a file part; the stored inputs stay unchanged. */
export async function replaceImagePlaceholders(
  messages: readonly ModelMessage[],
  store: MediaStore,
): Promise<ModelMessage[]> {
  const references = messages.flatMap(listImagePlaceholders);
  if (references.length === 0) {
    return [...messages];
  }
  const contents = await loadImages(store, references);
  const replacePart = <PART extends { readonly type: string }>(part: PART): PART | FilePart => {
    const reference = readImagePlaceholder(part);
    if (reference === undefined) {
      return part;
    }
    const data = contents.get(reference.sha256);
    if (data === undefined) {
      throw new Error(`loadImages returned no bytes for image ${reference.uri}`);
    }
    return { type: "file", mediaType: reference.mimeType, data: { type: "data", data } };
  };
  return messages.map((message): ModelMessage => {
    switch (message.role) {
      case "user":
        return typeof message.content === "string"
          ? message
          : { ...message, content: message.content.map((part) => replacePart(part)) };
      case "tool":
        return {
          ...message,
          content: message.content.map((part) =>
            part.type === "tool-result" && part.output.type === "content"
              ? {
                  ...part,
                  output: {
                    ...part.output,
                    value: part.output.value.map((item) => replacePart(item) as ToolResultContentPart),
                  },
                }
              : part,
          ),
        };
      default:
        return message;
    }
  });
}

function collectPlaceholders(parts: readonly { readonly type: string }[]): AgentImageReference[] {
  return parts.flatMap((part) => {
    const reference = readImagePlaceholder(part);
    return reference === undefined ? [] : [reference];
  });
}

function readRecord(value: unknown): Record<string, unknown> | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}
