// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createHash } from "node:crypto";

import type { AgentTurnContext } from "./agent-framework-runner.js";
import type { AgentImageReference } from "./agent-session-values.js";

/**
 * Largest image a media store accepts, in bytes. Its base64 text is 5,000,000 bytes, which fits the per-image limits
 * of Amazon Bedrock (5 MB of base64) and the Claude API (10 MB of base64).
 * @since 0.5.0
 */
export const maxImageBytes = 3_750_000;

/**
 * Most images one user message or one tool result may carry.
 * @since 0.5.0
 */
export const maxImagesPerMessage = 20;

/**
 * Image types a media store accepts, detected from the bytes.
 * @since 0.5.0
 */
export const supportedImageMimeTypes: readonly string[] = ["image/png", "image/jpeg", "image/gif", "image/webp"];

/**
 * Longest {@link AgentImageReference.uri} in bytes as UTF-8, the same unit in every core; S3 object keys are at most
 * 1024 bytes.
 * @since 0.5.0
 */
export const maxImageUriLength = 1024;

const sha256Pattern = /^[0-9a-f]{64}$/;
const imageUriPattern = /^[a-z][a-z0-9+.-]*:\/\/\S+\/sha256\/[0-9a-f]{64}$/;
const pngSignature = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
const pngHeaderChunk = [0x00, 0x00, 0x00, 0x0d, 0x49, 0x48, 0x44, 0x52];
const webpChunkTypes: readonly string[] = ["VP8 ", "VP8L", "VP8X"];
const formatNames: Readonly<Record<string, string>> = {
  "image/png": "PNG",
  "image/jpeg": "JPEG",
  "image/gif": "GIF",
  "image/webp": "WebP",
};

/**
 * Image bytes or an image reference that Dex agents refuse.
 *
 * `putImage` throws it for bytes that are empty, larger than {@link maxImageBytes}, or not PNG, JPEG, GIF, or WebP.
 * {@link validateImageReference} and {@link validateImageReferences} throw it for a reference whose fields are
 * invalid. The message says what is wrong.
 * @since 0.5.0
 */
export class ImageValidationError extends Error {
  /**
   * @param message - What is wrong with the image or reference.
   */
  public constructor(message: string) {
    super(message);
    this.name = "ImageValidationError";
  }
}

/**
 * A media store cannot return the bytes of a valid reference.
 *
 * `getImage` throws it when the reference belongs to another store, when the store has no object for it, or when the
 * stored bytes do not match the reference's SHA-256, size, and type.
 * @since 0.5.0
 */
export class MediaStoreError extends Error {
  /**
   * @param message - Which image could not be read, and why.
   * @param options - Standard error options, such as the `cause` from the storage service.
   */
  public constructor(message: string, options?: ErrorOptions) {
    super(message, options);
    this.name = "MediaStoreError";
  }
}

/**
 * Keeps image bytes outside session state, addressed by their content.
 *
 * Implementations key every object by the SHA-256 of its bytes, under a location of their own, so `putImage` is
 * idempotent: the same bytes give the same reference and one object, however often a retried tool Step or client
 * uploads them. Use `S3MediaStore` from `@superdurable/dex-agents/s3` for an S3-compatible bucket,
 * `LocalDirectoryMediaStore` when every client and Worker runs on one machine, or {@link InMemoryMediaStore} in tests.
 * Upload with `putImage` before a reference enters a message or a tool result, and read with `getImage` or
 * {@link loadImages} when building model input.
 *
 * @example
 * ```ts
 * const reference = await store.putImage(screenshotPng);
 * return { output: "Took a screenshot.", images: [reference] };
 * ```
 * @since 0.5.0
 */
export interface MediaStore {
  /**
   * Stores one image and returns its reference.
   *
   * @param data - PNG, JPEG, GIF, or WebP bytes, at most {@link maxImageBytes}.
   * @returns The reference, whose `uri` ends in `/sha256/<sha256>`.
   * @throws {ImageValidationError} When the bytes are empty, too large, or of another type. Nothing is stored.
   * @throws The store's own error when the upload fails; uploading the same bytes again is safe.
   */
  putImage(data: Uint8Array): Promise<AgentImageReference>;
  /**
   * Returns the bytes of one image this store holds.
   *
   * @param reference - A reference that this store's `putImage` returned.
   * @returns The exact bytes, checked with {@link verifyImageBytes} against `sha256`, `sizeBytes`, and `mimeType`.
   * @throws {ImageValidationError} When the reference's fields are invalid.
   * @throws {MediaStoreError} When the reference belongs to another store, the object is missing, or its bytes do
   * not match the reference.
   * @throws The store's own error when the read fails.
   */
  getImage(reference: AgentImageReference): Promise<Uint8Array>;
}

/**
 * Keeps images in this process, for tests and single-process tools.
 *
 * References use the `memory://<name>` location. Two stores with the same name accept each other's references, but
 * each holds only what was put into it, so a Worker in another process cannot read them.
 *
 * @example
 * ```ts
 * const store = new InMemoryMediaStore();
 * const reference = await store.putImage(pngBytes);
 * assert.deepEqual(await store.getImage(reference), pngBytes);
 * ```
 * @since 0.5.0
 */
export class InMemoryMediaStore implements MediaStore {
  /** The location that every reference of this store starts with: `"memory://<name>"`. */
  public readonly storeUri: string;
  private readonly images = new Map<string, Uint8Array>();

  /**
   * Creates an empty store.
   *
   * @param name - The location name in `memory://<name>`, `"default"` when omitted. It must be non-empty and contain
   * no `/` or whitespace.
   * @throws {TypeError} When `name` is empty or contains `/` or whitespace.
   * @throws {RangeError} When `name` makes image URIs longer than {@link maxImageUriLength}.
   */
  public constructor(name = "default") {
    if (name === "" || name.includes("/") || /\s/.test(name)) {
      throw new TypeError("InMemoryMediaStore name must be non-empty, without / or whitespace");
    }
    this.storeUri = `memory://${name}`;
    requireShortImageUris(this.storeUri);
  }

  /** How many distinct images the store holds; uploading the same bytes twice counts once. */
  public get imageCount(): number {
    return this.images.size;
  }

  /**
   * Stores one image and returns its reference. See {@link MediaStore.putImage}.
   *
   * @param data - PNG, JPEG, GIF, or WebP bytes, at most {@link maxImageBytes}.
   * @returns The reference.
   * @throws {ImageValidationError} When the bytes are empty, too large, or of another type.
   */
  public async putImage(data: Uint8Array): Promise<AgentImageReference> {
    const reference = buildImageReference(data, this.storeUri);
    if (!this.images.has(reference.sha256)) {
      this.images.set(reference.sha256, Uint8Array.from(data));
    }
    return reference;
  }

  /**
   * Returns the bytes of one image. See {@link MediaStore.getImage}.
   *
   * @param reference - A reference that this store's `putImage` returned.
   * @returns A copy of the exact bytes; changing it leaves the stored image as it was.
   * @throws {ImageValidationError} When the reference's fields are invalid.
   * @throws {MediaStoreError} When the reference belongs to another store, the store does not hold the image, or its
   * bytes do not match.
   */
  public async getImage(reference: AgentImageReference): Promise<Uint8Array> {
    requireStoreImageReference(reference, this.storeUri);
    const data = this.images.get(reference.sha256);
    if (data === undefined) {
      throw new MediaStoreError(`image ${reference.uri} is not in the media store`);
    }
    return verifyImageBytes(reference, Uint8Array.from(data));
  }
}

/**
 * Returns the MIME type of image bytes, read from their structure.
 *
 * The signature picks the type, then a cheap structural check must hold; it does not decode the image. PNG needs its
 * signature followed by the `IHDR` chunk. JPEG needs SOI, a marker right after it, and EOI as the last two bytes. GIF
 * needs a `GIF87a` or `GIF89a` header and the logical screen descriptor. WebP needs a RIFF size equal to the byte count
 * minus 8 and a first chunk of `VP8 `, `VP8L`, or `VP8X`.
 *
 * @param data - Image bytes.
 * @returns `"image/png"`, `"image/jpeg"`, `"image/gif"`, or `"image/webp"`.
 * @throws {ImageValidationError} When the bytes are empty, start with no supported signature, or fail the structural
 * check of their type.
 * @since 0.5.0
 */
export function detectImageMimeType(data: Uint8Array): string {
  if (data.length === 0) {
    throw new ImageValidationError("image is empty");
  }
  const mimeType = signatureMimeType(data);
  if (mimeType === undefined) {
    throw new ImageValidationError("image is not PNG, JPEG, GIF, or WebP");
  }
  if (!isWellFormed(mimeType, data)) {
    throw new ImageValidationError(`image is not a well-formed ${formatNames[mimeType]}`);
  }
  return mimeType;
}

function signatureMimeType(data: Uint8Array): string | undefined {
  if (startsWithBytes(data, pngSignature)) {
    return "image/png";
  }
  if (startsWithBytes(data, [0xff, 0xd8, 0xff])) {
    return "image/jpeg";
  }
  if (startsWithText(data, 0, "GIF87a") || startsWithText(data, 0, "GIF89a")) {
    return "image/gif";
  }
  if (startsWithText(data, 0, "RIFF") && startsWithText(data, 8, "WEBP")) {
    return "image/webp";
  }
  return undefined;
}

function isWellFormed(mimeType: string, data: Uint8Array): boolean {
  switch (mimeType) {
    case "image/png":
      return data.length >= 33 && startsWithBytes(data.subarray(8), pngHeaderChunk);
    case "image/jpeg": {
      const marker = data[3] ?? 0;
      return data.length >= 6 && marker >= 0xc0 && marker <= 0xfe && data.at(-2) === 0xff && data.at(-1) === 0xd9;
    }
    case "image/gif":
      return data.length >= 13;
    default: {
      if (data.length < 20) {
        return false;
      }
      const riffSize = new DataView(data.buffer, data.byteOffset, data.byteLength).getUint32(4, true);
      const chunkType = String.fromCharCode(...data.subarray(12, 16));
      return riffSize + 8 === data.length && webpChunkTypes.includes(chunkType);
    }
  }
}

/**
 * Checks image bytes and returns the reference a store keeps them under.
 *
 * A {@link MediaStore} implementation calls it before it uploads: the reference names the object,
 * `imageUri(storeUri, sha256)`.
 *
 * @param data - Image bytes.
 * @param storeUri - The store's location, such as `"s3://media/agents"`.
 * @returns The reference, with the detected MIME type, SHA-256, and size.
 * @throws {ImageValidationError} When the bytes are empty, larger than {@link maxImageBytes}, or not a well-formed
 * PNG, JPEG, GIF, or WebP, or when {@link validateImageReference} refuses the reference, such as for a URI longer than
 * {@link maxImageUriLength}.
 * @since 0.5.0
 */
export function buildImageReference(data: Uint8Array, storeUri: string): AgentImageReference {
  if (data.length > maxImageBytes) {
    throw new ImageValidationError(`image is ${data.length} bytes; the limit is ${maxImageBytes} bytes`);
  }
  const mimeType = detectImageMimeType(data);
  const sha256 = createHash("sha256").update(data).digest("hex");
  const reference: AgentImageReference = {
    type: "image",
    uri: imageUri(storeUri, sha256),
    mimeType,
    sha256,
    sizeBytes: data.length,
  };
  validateImageReference(reference);
  return reference;
}

/**
 * Returns the URI of the image with this SHA-256 in a store.
 *
 * @param storeUri - The store's location; trailing `/` characters are dropped.
 * @param sha256 - SHA-256 of the bytes as 64 lowercase hex digits.
 * @returns `"<storeUri>/sha256/<sha256>"`.
 * @since 0.5.0
 */
export function imageUri(storeUri: string, sha256: string): string {
  return `${storeUri.replace(/\/+$/, "")}/sha256/${sha256}`;
}

/**
 * Checks that every image URI of a store fits {@link maxImageUriLength}.
 *
 * A {@link MediaStore} implementation calls it in its constructor, so a long location fails at once instead of on the
 * first upload.
 *
 * @param storeUri - The store's location.
 * @throws {RangeError} When `imageUri(storeUri, sha256)` is longer than {@link maxImageUriLength} bytes as UTF-8.
 * @since 0.5.0
 */
export function requireShortImageUris(storeUri: string): void {
  if (Buffer.byteLength(imageUri(storeUri, "0".repeat(64)), "utf8") > maxImageUriLength) {
    throw new RangeError(
      `media store location ${storeUri} makes image URIs longer than ${maxImageUriLength} bytes as UTF-8`,
    );
  }
}

/**
 * Checks the fields of one image reference.
 *
 * The checks run in order, and the first failure is thrown: `type` is `"image"`; `mimeType` is supported; `sha256` is
 * 64 lowercase hex digits; `sizeBytes` is from 1 to {@link maxImageBytes}; `uri` is at most
 * {@link maxImageUriLength} bytes as UTF-8, looks like `<scheme>://<location>/sha256/<sha256>`, and ends with the
 * reference's own `sha256`. It does not read the store.
 *
 * @param reference - The reference, such as one decoded from a request.
 * @throws {ImageValidationError} When a field is invalid.
 * @since 0.5.0
 */
export function validateImageReference(reference: AgentImageReference): void {
  if ((reference.type as string) !== "image") {
    throw new ImageValidationError('image reference type must be "image"');
  }
  if (!supportedImageMimeTypes.includes(reference.mimeType)) {
    throw new ImageValidationError(`image reference mime_type must be one of ${supportedImageMimeTypes.join(", ")}`);
  }
  if (typeof reference.sha256 !== "string" || !sha256Pattern.test(reference.sha256)) {
    throw new ImageValidationError("image reference sha256 must be 64 lowercase hex digits");
  }
  const size = reference.sizeBytes;
  if (!Number.isSafeInteger(size) || size < 1 || size > maxImageBytes) {
    throw new ImageValidationError(`image reference size_bytes must be from 1 to ${maxImageBytes}`);
  }
  const uri = reference.uri;
  if (typeof uri !== "string" || Buffer.byteLength(uri, "utf8") > maxImageUriLength) {
    throw new ImageValidationError(`image reference uri must be at most ${maxImageUriLength} bytes as UTF-8`);
  }
  if (!imageUriPattern.test(uri)) {
    throw new ImageValidationError("image reference uri must look like <scheme>://<location>/sha256/<sha256>");
  }
  if (!uri.endsWith(`/${reference.sha256}`)) {
    throw new ImageValidationError("image reference uri must end with its own sha256");
  }
}

/**
 * Checks the images of one user message or one tool result.
 *
 * @param references - The images, at most {@link maxImagesPerMessage}.
 * @throws {ImageValidationError} When there are too many images, or with `"images[<index>]: "` before the reason
 * when one is invalid.
 * @since 0.5.0
 */
export function validateImageReferences(references: readonly AgentImageReference[]): void {
  if (references.length > maxImagesPerMessage) {
    throw new ImageValidationError(
      `a message may carry at most ${maxImagesPerMessage} images, got ${references.length}`,
    );
  }
  references.forEach((reference, index) => {
    try {
      validateImageReference(reference);
    } catch (error) {
      if (error instanceof ImageValidationError) {
        throw new ImageValidationError(`images[${index}]: ${error.message}`);
      }
      throw error;
    }
  });
}

/**
 * Checks that a valid reference names an image of the store at `storeUri`.
 *
 * A {@link MediaStore} implementation calls it first in `getImage`, so a request cannot make the Worker read outside
 * the store's location.
 *
 * @param reference - The reference to read.
 * @param storeUri - The store's location.
 * @throws {ImageValidationError} When the reference's fields are invalid.
 * @throws {MediaStoreError} When its `uri` is not `imageUri(storeUri, sha256)`.
 * @since 0.5.0
 */
export function requireStoreImageReference(reference: AgentImageReference, storeUri: string): void {
  validateImageReference(reference);
  if (reference.uri !== imageUri(storeUri, reference.sha256)) {
    throw new MediaStoreError(`image ${reference.uri} is not in the media store ${storeUri}`);
  }
}

/**
 * Checks bytes read from a store against their reference.
 *
 * The SHA-256 binds the bytes, and {@link detectImageMimeType} must find the reference's `mimeType` in them, so a
 * reference whose type was changed after upload fails here.
 *
 * @param reference - The reference that was read.
 * @param data - The bytes the store returned.
 * @returns `data`, unchanged.
 * @throws {MediaStoreError} When the size or the SHA-256 differs, or when the bytes are not a well-formed image of
 * `mimeType`.
 * @since 0.5.0
 */
export function verifyImageBytes(reference: AgentImageReference, data: Uint8Array): Uint8Array {
  if (data.length !== reference.sizeBytes || createHash("sha256").update(data).digest("hex") !== reference.sha256) {
    throw new MediaStoreError(`image ${reference.uri} does not match its sha256 and size`);
  }
  let detected: string | undefined;
  try {
    detected = detectImageMimeType(data);
  } catch (error) {
    if (!(error instanceof ImageValidationError)) {
      throw error;
    }
  }
  if (detected !== reference.mimeType) {
    throw new MediaStoreError(`image ${reference.uri} does not hold a well-formed ${reference.mimeType}`);
  }
  return data;
}

/**
 * Reads the bytes of several images, each distinct image once.
 *
 * A runner calls it while building model input, with the references it chose from {@link collectTurnImages} or from
 * its own replayed items. The reads run concurrently.
 *
 * @example
 * ```ts
 * const images = await loadImages(store, collectTurnImages(turn));
 * const parts = (message.images ?? []).map((reference) => imagePart(reference.mimeType, images.get(reference.sha256)!));
 * ```
 * @param store - The store that holds the images.
 * @param references - The images to read, in any order, with repeats allowed.
 * @returns The bytes of each image, keyed by {@link AgentImageReference.sha256}.
 * @throws {ImageValidationError} When a reference's fields are invalid.
 * @throws {MediaStoreError} When the store cannot return an image's exact bytes.
 * @throws The store's own error when a read fails.
 * @since 0.5.0
 */
export async function loadImages(
  store: MediaStore,
  references: Iterable<AgentImageReference>,
): Promise<ReadonlyMap<string, Uint8Array>> {
  const distinct = new Map<string, AgentImageReference>();
  for (const reference of references) {
    distinct.set(reference.sha256, reference);
  }
  const contents = await Promise.all([...distinct.values()].map((reference) => store.getImage(reference)));
  return new Map([...distinct.keys()].map((sha256, index) => [sha256, contents[index] as Uint8Array]));
}

/**
 * Returns every image a model call of this turn may show, each once.
 *
 * It collects `AgentMessage.images` of `turn.messages` in sequence order, then `AgentToolCall.images` of the turn's
 * records in deferral order, keeping the first reference to each SHA-256. Whether a model call shows earlier turns'
 * images is the runner's choice; pass only the references it needs to {@link loadImages}.
 *
 * @param turn - The context of one `runModelStep` call.
 * @returns The references, without repeats.
 * @since 0.5.0
 */
export function collectTurnImages(turn: AgentTurnContext): AgentImageReference[] {
  const collected = new Map<string, AgentImageReference>();
  const keepFirst = (reference: AgentImageReference): void => {
    if (!collected.has(reference.sha256)) {
      collected.set(reference.sha256, reference);
    }
  };
  for (const message of turn.messages) {
    (message.images ?? []).forEach(keepFirst);
  }
  const records = [...turn.toolCalls].sort((left, right) => left.deferralOrder - right.deferralOrder);
  for (const record of records) {
    (record.images ?? []).forEach(keepFirst);
  }
  return [...collected.values()];
}

/** Reports whether {@link validateImageReferences} accepts the images; the RPCs answer `false` instead of throwing. */
export function areValidImageReferences(references: readonly AgentImageReference[]): boolean {
  try {
    validateImageReferences(references);
    return true;
  } catch (error) {
    if (error instanceof ImageValidationError) {
      return false;
    }
    throw error;
  }
}

/**
 * Returns the `images` field of a succeeded record from a tool's result. An invalid reference throws
 * {@link ImageValidationError}, which fails the tool Step attempt.
 */
export function toolResultImages(references: readonly AgentImageReference[] | undefined): {
  readonly images?: readonly AgentImageReference[];
} {
  if (references === undefined || references.length === 0) {
    return {};
  }
  validateImageReferences(references);
  return { images: [...references] };
}

function startsWithBytes(data: Uint8Array, signature: readonly number[]): boolean {
  return data.length >= signature.length && signature.every((byte, index) => data[index] === byte);
}

function startsWithText(data: Uint8Array, offset: number, text: string): boolean {
  return startsWithBytes(
    data.subarray(offset),
    [...text].map((character) => character.charCodeAt(0)),
  );
}
