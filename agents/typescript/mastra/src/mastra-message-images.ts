// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AIV5Type, MastraDBMessage, MastraMessagePart } from "@mastra/core/agent/message-list";
import type { InputProcessor, ProcessLLMRequestArgs } from "@mastra/core/processors";
import { maxImagesPerMessage, type AgentImageReference } from "@superdurable/dex-agents";

// Stored image: text part `[image <uri>]` with the reference under this key; the processor swaps in bytes.
const imageReferenceKey = "dexAgents";

type LanguageModelPrompt = ProcessLLMRequestArgs["prompt"];
type LanguageModelPromptMessage = LanguageModelPrompt[number];

/** A tool result `content` output item: text, or an image placeholder with its reference. */
type ModelOutputTextItem = {
  readonly type: "text";
  readonly text: string;
  readonly providerOptions?: { readonly [key: string]: { readonly imageReference: Record<string, string | number> } };
};

/** Builds the user message part that stands for one image. */
export function buildUserImagePlaceholderPart(reference: AgentImageReference): MastraMessagePart {
  return {
    type: "text",
    text: describeImage(reference),
    providerMetadata: { [imageReferenceKey]: { imageReference: encodeImageReference(reference) } },
  };
}

/** Mastra keeps `mastra.modelOutput` on the tool invocation and sends it instead of the plain result. */
export function buildToolResultImageProviderOptions(
  outputText: string,
  images: readonly AgentImageReference[],
): NonNullable<AIV5Type.ToolResultPart["providerOptions"]> {
  const value: ModelOutputTextItem[] = [
    { type: "text", text: outputText },
    ...images.map(
      (reference): ModelOutputTextItem => ({
        type: "text",
        text: describeImage(reference),
        providerOptions: { [imageReferenceKey]: { imageReference: encodeImageReference(reference) } },
      }),
    ),
  ];
  return { mastra: { modelOutput: { type: "content", value } } };
}

/** Returns every image placeholder of stored Mastra messages, in message order. */
export function listMastraMessageImages(messages: readonly MastraDBMessage[]): AgentImageReference[] {
  return messages.flatMap((message) =>
    message.content.parts.flatMap((part) => {
      if (part.type === "text") {
        const reference = readImageReference(part.providerMetadata);
        return reference === undefined ? [] : [reference];
      }
      return part.type === "tool-invocation" ? listModelOutputImages(part.providerMetadata) : [];
    }),
  );
}

/** Returns the image placeholders of tool result messages built for the next model call. */
export function listToolResultMessageImages(messages: readonly AIV5Type.ToolModelMessage[]): AgentImageReference[] {
  return messages.flatMap((message) =>
    message.content.flatMap((part) => (part.type === "tool-result" ? listModelOutputImages(part.providerOptions) : [])),
  );
}

/** The display images of one stored message: each image once, at most `maxImagesPerMessage`. */
export function listDisplayImages(message: MastraDBMessage): AgentImageReference[] {
  const distinct = new Map<string, AgentImageReference>();
  for (const reference of listMastraMessageImages([message])) {
    if (!distinct.has(reference.sha256)) {
      distinct.set(reference.sha256, reference);
    }
  }
  // Parallel tool results share one message; the model still sees every image.
  return [...distinct.values()].slice(0, maxImagesPerMessage);
}

/** Emits AI SDK v5 shapes, which Mastra converts for the model's specification version. */
export function createImageInjectionProcessor(images: ReadonlyMap<string, Uint8Array>): InputProcessor {
  const readBase64 = (reference: AgentImageReference): string => {
    const data = images.get(reference.sha256);
    if (data === undefined) {
      throw new Error(`image ${reference.uri} was not loaded before the model call`);
    }
    return Buffer.from(data.buffer, data.byteOffset, data.byteLength).toString("base64");
  };
  return {
    id: "dex-agents-images",
    processLLMRequest: ({ prompt }) => ({
      prompt: prompt.map((message) => replacePromptImagePlaceholders(message, readBase64)),
    }),
  };
}

function replacePromptImagePlaceholders(
  message: LanguageModelPromptMessage,
  readBase64: (reference: AgentImageReference) => string,
): LanguageModelPromptMessage {
  switch (message.role) {
    case "user":
      return {
        ...message,
        content: message.content.map((part) => {
          const reference = readImageReference(part.providerOptions);
          return part.type === "text" && reference !== undefined
            ? { type: "file", mediaType: reference.mimeType, data: readBase64(reference) }
            : part;
        }),
      };
    case "tool":
      return {
        ...message,
        content: message.content.map((part) => {
          if (part.output.type !== "content") {
            return part;
          }
          const value = part.output.value.map((item) => {
            const reference = readImageReference((item as { readonly providerOptions?: unknown }).providerOptions);
            return item.type === "text" && reference !== undefined
              ? { type: "media" as const, data: readBase64(reference), mediaType: reference.mimeType }
              : item;
          });
          return { ...part, output: { ...part.output, value } };
        }),
      };
    default:
      return message;
  }
}

function listModelOutputImages(metadata: unknown): AgentImageReference[] {
  const modelOutput = readRecord(readRecord(readRecord(metadata)?.["mastra"])?.["modelOutput"]);
  const value = modelOutput?.["type"] === "content" ? modelOutput["value"] : undefined;
  if (!Array.isArray(value)) {
    return [];
  }
  return value.flatMap((item: unknown) => {
    const reference = readRecord(item)?.["type"] === "text" ? readImageReference(readRecord(item)?.["providerOptions"]) : undefined;
    return reference === undefined ? [] : [reference];
  });
}

function readImageReference(metadata: unknown): AgentImageReference | undefined {
  const fields = readRecord(readRecord(readRecord(metadata)?.[imageReferenceKey])?.["imageReference"]);
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

function encodeImageReference(reference: AgentImageReference): Record<string, string | number> {
  return {
    type: reference.type,
    uri: reference.uri,
    mimeType: reference.mimeType,
    sha256: reference.sha256,
    sizeBytes: reference.sizeBytes,
  };
}

function describeImage(reference: AgentImageReference): string {
  return `[image ${reference.uri}]`;
}

function readRecord(value: unknown): Record<string, unknown> | undefined {
  return typeof value === "object" && value !== null && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}
