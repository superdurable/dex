// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import type { AgentInputItem, CallModelInputFilter, ModelInputData, protocol } from "@openai/agents";
import { loadImages, type AgentImageReference, type MediaStore } from "@superdurable/dex-agents";

// A stored input_image part's image is the reference's uri; the model input filter swaps in a data URL.
const imageDetail = "auto";
// A reference URI ends in /sha256/<sha256>; other image URLs are the application's own.
const placeholderPattern = /\/sha256\/[0-9a-f]{64}$/;
const partListKeys = ["content", "output"] as const;

type InputImagePart = { readonly type: "input_image"; readonly image?: unknown };

/**
 * Builds the user message item for user text and its images.
 *
 * @param text - User message text.
 * @param images - The message's images, in order.
 * @returns A user message whose content is the text part, then one `input_image` part per image.
 */
export function buildUserImageMessageItem(text: string, images: readonly AgentImageReference[]): AgentInputItem {
  return {
    type: "message",
    role: "user",
    content: [
      { type: "input_text", text },
      ...images.map((image) => ({ type: "input_image" as const, image: image.uri, detail: imageDetail })),
    ],
  };
}

/**
 * Builds a tool result that the SDK turns into a `function_call_result` with image parts.
 *
 * @param text - The recorded output text.
 * @param images - The record's images, in order.
 * @returns A text output followed by one image output per image.
 */
export function buildToolImageOutputs(
  text: string,
  images: readonly AgentImageReference[],
): (protocol.ToolOutputText | protocol.ToolOutputImage)[] {
  return [
    { type: "text", text },
    ...images.map((image): protocol.ToolOutputImage => ({ type: "image", image: image.uri, detail: imageDetail })),
  ];
}

/**
 * Reports whether a tool result output lists any `input_image` part.
 *
 * @param output - A `function_call_result` output.
 * @returns Whether the output is a list with an image part.
 */
export function hasInputImageParts(output: unknown): boolean {
  return Array.isArray(output) && output.some(isInputImagePart);
}

/**
 * Creates the `callModelInputFilter` of one model call. It runs the application's filter first, then gives the
 * model a copy of the input whose image placeholders are `data:` URLs of their bytes. A store read failure throws,
 * which fails the `RunModel` attempt.
 *
 * @param mediaStore - The store that holds the images.
 * @param references - Every image the turn may show, such as `collectTurnImages(turn)`.
 * @param applicationFilter - The run configuration's own filter, if any.
 * @returns The filter for one run.
 */
export function createImageBytesInputFilter(
  mediaStore: MediaStore,
  references: readonly AgentImageReference[],
  applicationFilter: CallModelInputFilter | undefined,
): CallModelInputFilter {
  const referencesByUri = new Map(references.map((reference) => [reference.uri, reference] as const));
  return async (args) => {
    const modelData: ModelInputData = applicationFilter === undefined ? args.modelData : await applicationFilter(args);
    const uris = [...new Set(modelData.input.flatMap(listPlaceholderUris))];
    if (uris.length === 0) {
      return modelData;
    }
    const imageReferences = uris.map((uri) => {
      const reference = referencesByUri.get(uri);
      if (reference === undefined) {
        throw new Error(`the model input shows image ${uri}, which no message or tool call of the session references`);
      }
      return reference;
    });
    const contents = await loadImages(mediaStore, imageReferences);
    const dataUrls = new Map(
      imageReferences.map((reference) => {
        const data = contents.get(reference.sha256);
        if (data === undefined) {
          throw new Error(`the media store returned no bytes for image ${reference.uri}`);
        }
        const base64 = Buffer.from(data.buffer, data.byteOffset, data.byteLength).toString("base64");
        return [reference.uri, `data:${reference.mimeType};base64,${base64}`] as const;
      }),
    );
    // Changed items are new objects, so the run state keeps its placeholders.
    return { ...modelData, input: modelData.input.map((item) => replacePlaceholders(item, dataUrls)) };
  };
}

function listPlaceholderUris(item: AgentInputItem): string[] {
  const record = item as Readonly<Record<string, unknown>>;
  return partListKeys.flatMap((key) => {
    const parts = record[key];
    return Array.isArray(parts) ? parts.filter(isPlaceholderPart).map((part) => part.image) : [];
  });
}

function replacePlaceholders(item: AgentInputItem, dataUrls: ReadonlyMap<string, string>): AgentInputItem {
  const record = item as Readonly<Record<string, unknown>>;
  let replaced: Record<string, unknown> | undefined;
  for (const key of partListKeys) {
    const parts = record[key];
    if (Array.isArray(parts) && parts.some(isPlaceholderPart)) {
      replaced ??= { ...record };
      replaced[key] = parts.map((part: unknown) =>
        isPlaceholderPart(part) ? { ...part, image: dataUrls.get(part.image) } : part,
      );
    }
  }
  return (replaced ?? item) as AgentInputItem;
}

function isInputImagePart(part: unknown): part is InputImagePart {
  return typeof part === "object" && part !== null && (part as { type?: unknown }).type === "input_image";
}

function isPlaceholderPart(part: unknown): part is InputImagePart & { readonly image: string } {
  return (
    isInputImagePart(part) &&
    typeof part.image === "string" &&
    !part.image.startsWith("data:") &&
    placeholderPattern.test(part.image)
  );
}
