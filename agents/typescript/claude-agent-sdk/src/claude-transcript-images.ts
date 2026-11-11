// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createHash } from "node:crypto";

import { loadImages, type AgentImageReference, type MediaStore } from "@superdurable/dex-agents";

// Stored entries never hold image bytes. In every Claude `image` block whose `source` is
// `{"type": "base64", "media_type": ..., "data": <base64>}`, the stored `data` is
// `{"dex_image_reference": <reference fields>}` instead; every other key keeps its value and position,
// so restoring the bytes rebuilds the exact entry the CLI wrote. The encoding matches the Python adapter.
export const imageReferenceKey = "dex_image_reference";

/** Swaps image bytes for references before storing, and back before the CLI reads them. */
export class TranscriptImageCodec {
  private readonly referencesBySha256 = new Map<string, AgentImageReference>();

  public constructor(
    private readonly mediaStore: MediaStore,
    knownReferences: Iterable<AgentImageReference>,
  ) {
    for (const reference of knownReferences) {
      this.referencesBySha256.set(reference.sha256, reference);
    }
  }

  /**
   * Returns a copy of `value` whose base64 image blocks hold references. An image without a known reference is
   * uploaded, so images the CLI made itself, such as an inline tool's, are stored by reference too.
   *
   * @throws {TypeError} When an image's base64 is not canonical, so its bytes could not be restored as written.
   * @throws {ImageValidationError} When the media store refuses the image.
   */
  public async stripImageBytes(value: unknown): Promise<unknown> {
    if (Array.isArray(value)) {
      const items: unknown[] = [];
      for (const item of value) {
        items.push(await this.stripImageBytes(item));
      }
      return items;
    }
    if (!isRecord(value)) {
      return value;
    }
    const source = base64ImageSourceOf(value);
    if (source !== undefined && typeof source["data"] === "string") {
      const reference = await this.referenceFor(source["data"]);
      const strippedSource = { ...source, data: { [imageReferenceKey]: encodeImageReference(reference) } };
      return { ...value, source: strippedSource };
    }
    const copy: Record<string, unknown> = {};
    for (const [key, item] of Object.entries(value)) {
      copy[key] = await this.stripImageBytes(item);
    }
    return copy;
  }

  /**
   * Returns a copy of `value` with the bytes of every referenced image as base64.
   *
   * @throws {MediaStoreError} When the store cannot return an image's bytes.
   */
  public async restoreImageBytes<T>(value: T): Promise<T> {
    const references = imageReferencesIn(value);
    if (references.length === 0) {
      return value;
    }
    const images = await loadImages(this.mediaStore, references);
    return replaceReferences(value, images) as T;
  }

  private async referenceFor(data: string): Promise<AgentImageReference> {
    const imageBytes = Buffer.from(data, "base64");
    if (imageBytes.toString("base64") !== data) {
      throw new TypeError("the Claude Code transcript has an image in invalid or non-canonical base64");
    }
    const sha256 = createHash("sha256").update(imageBytes).digest("hex");
    let reference = this.referencesBySha256.get(sha256);
    if (reference === undefined) {
      reference = await this.mediaStore.putImage(new Uint8Array(imageBytes));
      this.referencesBySha256.set(sha256, reference);
    }
    return reference;
  }
}

/** One Claude image block of a prompt, holding the reference until the runner loads the bytes. */
export interface PromptImageBlock {
  readonly type: "image";
  readonly source: {
    readonly type: "base64";
    readonly media_type: string;
    readonly data: unknown;
  };
}

export function imageBlock(reference: AgentImageReference): PromptImageBlock {
  return {
    type: "image",
    source: { type: "base64", media_type: reference.mimeType, data: { [imageReferenceKey]: encodeImageReference(reference) } },
  };
}

/** Returns the references of the image blocks in `value`, in document order. */
export function imageReferencesIn(value: unknown): AgentImageReference[] {
  const references: AgentImageReference[] = [];
  collectReferences(value, references);
  return references;
}

// The field order of the Python adapter's dataclasses.asdict.
export function encodeImageReference(reference: AgentImageReference): Record<string, unknown> {
  return {
    uri: reference.uri,
    mime_type: reference.mimeType,
    sha256: reference.sha256,
    size_bytes: reference.sizeBytes,
    type: reference.type,
  };
}

export function decodeImageReference(fields: unknown): AgentImageReference {
  if (
    !isRecord(fields) ||
    typeof fields["uri"] !== "string" ||
    typeof fields["mime_type"] !== "string" ||
    typeof fields["sha256"] !== "string" ||
    typeof fields["size_bytes"] !== "number"
  ) {
    throw new TypeError("a stored image reference is malformed");
  }
  return {
    type: "image",
    uri: fields["uri"],
    mimeType: fields["mime_type"],
    sha256: fields["sha256"],
    sizeBytes: fields["size_bytes"],
  };
}

function collectReferences(value: unknown, references: AgentImageReference[]): void {
  if (Array.isArray(value)) {
    for (const item of value) {
      collectReferences(item, references);
    }
    return;
  }
  if (!isRecord(value)) {
    return;
  }
  const fields = storedReferenceFieldsOf(value);
  if (fields !== undefined) {
    references.push(decodeImageReference(fields));
    return;
  }
  for (const item of Object.values(value)) {
    collectReferences(item, references);
  }
}

function replaceReferences(value: unknown, images: ReadonlyMap<string, Uint8Array>): unknown {
  if (Array.isArray(value)) {
    return value.map((item) => replaceReferences(item, images));
  }
  if (!isRecord(value)) {
    return value;
  }
  const fields = storedReferenceFieldsOf(value);
  if (fields !== undefined) {
    const reference = decodeImageReference(fields);
    const imageBytes = images.get(reference.sha256);
    if (imageBytes === undefined) {
      throw new Error(`image ${reference.uri} was not loaded`);
    }
    const source = { ...(value["source"] as Record<string, unknown>), data: Buffer.from(imageBytes).toString("base64") };
    return { ...value, source };
  }
  return Object.fromEntries(Object.entries(value).map(([key, item]) => [key, replaceReferences(item, images)]));
}

function base64ImageSourceOf(block: Record<string, unknown>): Record<string, unknown> | undefined {
  if (block["type"] !== "image") {
    return undefined;
  }
  const source = block["source"];
  return isRecord(source) && source["type"] === "base64" ? source : undefined;
}

function storedReferenceFieldsOf(block: Record<string, unknown>): unknown {
  const data = base64ImageSourceOf(block)?.["data"];
  if (!isRecord(data)) {
    return undefined;
  }
  return isRecord(data[imageReferenceKey]) ? data[imageReferenceKey] : undefined;
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}
