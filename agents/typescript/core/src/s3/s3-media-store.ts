// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import {
  GetObjectCommand,
  HeadObjectCommand,
  PutObjectCommand,
  S3Client,
  S3ServiceException,
} from "@aws-sdk/client-s3";

import {
  MediaStoreError,
  buildImageReference,
  requireShortImageUris,
  requireStoreImageReference,
  verifyImageBytes,
  type MediaStore,
} from "../agent-media.js";
import type { AgentImageReference } from "../agent-session-values.js";

const notFoundErrorCodes: ReadonlySet<string> = new Set(["NotFound", "NoSuchKey", "404"]);
// 412 means the object exists; 409 means a concurrent conditional write of it is in flight.
const alreadyExistsErrorCodes: ReadonlySet<string> = new Set([
  "PreconditionFailed",
  "ConditionalRequestConflict",
  "412",
  "409",
]);
const conditionalWriteUnsupportedErrorCodes: ReadonlySet<string> = new Set(["NotImplemented", "501"]);

/**
 * Credentials an {@link S3MediaStore} signs requests with instead of the standard AWS credential chain.
 * @since 0.5.0
 */
export interface S3MediaStoreCredentials {
  /** The access key ID. */
  readonly accessKeyId: string;
  /** The secret access key. The store never logs it. */
  readonly secretAccessKey: string;
  /** The session token of temporary credentials; omit it for long-lived keys. */
  readonly sessionToken?: string;
}

/**
 * Where an {@link S3MediaStore} keeps images and how it reaches S3.
 * @since 0.5.0
 */
export interface S3MediaStoreOptions {
  /** The bucket name. It must be non-empty and contain no `/`. */
  readonly bucket: string;
  /**
   * Key prefix of every object, such as `"agents/media"`. Leading and trailing `/` characters are dropped. Omitted or
   * empty keeps objects at `sha256/<sha256>`.
   */
  readonly prefix?: string;
  /** Endpoint of an S3-compatible service, such as `"http://127.0.0.1:9000"`; omit it for AWS S3. */
  readonly endpoint?: string;
  /** AWS region; omit it for the standard AWS configuration, such as `AWS_REGION`. */
  readonly region?: string;
  /** Whether to address the bucket in the URL path instead of the host name. Many S3-compatible services need it. */
  readonly forcePathStyle?: boolean;
  /**
   * Credentials to sign requests with; omit them, the default, for the standard AWS credential chain. Pass them only
   * for a bucket whose keys are not secret, such as a local development bucket with fixed loopback credentials.
   */
  readonly credentials?: S3MediaStoreCredentials;
}

/**
 * Keeps image bytes in an S3 bucket, one object per distinct image.
 *
 * The object key is `<prefix>/sha256/<sha256>`, and a reference's `uri` is `s3://<bucket>/<prefix>/sha256/<sha256>`.
 * `putImage` uploads with `If-None-Match: *` and the image's `Content-Type`, and treats "already exists" as success, so
 * concurrent and retried uploads of the same bytes write one object. A service that refuses conditional writes gets a
 * `HEAD` first and a plain `PUT` when the object is missing; then two concurrent uploads may both write, so
 * deduplication holds for the key and its final content, not for every write. `getImage` reads only objects under
 * this store's bucket and prefix, refuses an object whose `Content-Length` differs from the reference, reads at most
 * one byte more than `sizeBytes`, and checks the bytes with `verifyImageBytes`. The AWS SDK reads credentials from the
 * standard AWS chain unless {@link S3MediaStoreOptions.credentials} gives them. Never put credentials in session
 * settings or Flow input.
 *
 * Session deletion does not delete objects. A runner reads a session's images again whenever a model call replays
 * them, so keep every object at least as long as any session that references it can still run a turn or replay its
 * history. A lifecycle rule that expires objects sooner makes those turns fail with `MediaStoreError`. Call
 * {@link close} when the store is no longer needed, so its connections do not keep the process alive.
 *
 * @example
 * ```ts
 * import { S3MediaStore } from "@superdurable/dex-agents/s3";
 *
 * const store = S3MediaStore.fromStoreUri("s3://agent-media/sessions");
 * const reference = await store.putImage(screenshotPng);
 * // reference.uri === "s3://agent-media/sessions/sha256/9f2c..."
 * store.close();
 *
 * // A local development bucket with fixed loopback credentials.
 * const local = new S3MediaStore({
 *   bucket: "agent-media",
 *   endpoint: "http://127.0.0.1:9000",
 *   region: "us-east-1",
 *   forcePathStyle: true,
 *   credentials: { accessKeyId: "s3local", secretAccessKey: "s3local" },
 * });
 * ```
 * @since 0.5.0
 */
export class S3MediaStore implements MediaStore {
  /** The location that every reference of this store starts with: `"s3://<bucket>/<prefix>"`, or without a prefix `"s3://<bucket>"`. */
  public readonly storeUri: string;
  private readonly bucket: string;
  private readonly prefix: string;
  private readonly client: S3Client;
  private supportsConditionalWrites = true;

  /**
   * Creates a store; it makes no request until the first upload or read.
   *
   * @param options - The bucket, prefix, and connection settings.
   * @throws {TypeError} When `options.bucket` is empty or contains `/`.
   * @throws {RangeError} When the bucket and prefix make image URIs longer than `maxImageUriLength`.
   */
  public constructor(options: S3MediaStoreOptions) {
    if (options.bucket === "" || options.bucket.includes("/")) {
      throw new TypeError("S3MediaStore bucket must be non-empty and contain no /");
    }
    this.bucket = options.bucket;
    this.prefix = (options.prefix ?? "").replace(/^\/+|\/+$/g, "");
    this.storeUri = this.prefix === "" ? `s3://${this.bucket}` : `s3://${this.bucket}/${this.prefix}`;
    requireShortImageUris(this.storeUri);
    this.client = new S3Client({
      ...(options.endpoint === undefined ? {} : { endpoint: options.endpoint }),
      ...(options.region === undefined ? {} : { region: options.region }),
      ...(options.credentials === undefined ? {} : { credentials: { ...options.credentials } }),
      forcePathStyle: options.forcePathStyle ?? false,
    });
  }

  /**
   * Creates a store from `s3://<bucket>` or `s3://<bucket>/<prefix>`.
   *
   * @param storeUri - The bucket and optional prefix as an S3 URI.
   * @param options - The connection settings and credentials.
   * @returns The store.
   * @throws {TypeError} When `storeUri` does not start with `s3://` or names no bucket.
   * @throws {RangeError} When the bucket and prefix make image URIs longer than `maxImageUriLength`.
   */
  public static fromStoreUri(
    storeUri: string,
    options: Omit<S3MediaStoreOptions, "bucket" | "prefix"> = {},
  ): S3MediaStore {
    if (!storeUri.startsWith("s3://")) {
      throw new TypeError(`S3 media store URI must start with s3://, got ${JSON.stringify(storeUri)}`);
    }
    const location = storeUri.slice("s3://".length);
    const slash = location.indexOf("/");
    const bucket = slash === -1 ? location : location.slice(0, slash);
    const prefix = slash === -1 ? "" : location.slice(slash + 1);
    return new S3MediaStore({ ...options, bucket, prefix });
  }

  /**
   * Returns the object key of the image with this SHA-256.
   *
   * @param sha256 - SHA-256 of the bytes as 64 lowercase hex digits.
   * @returns `"<prefix>/sha256/<sha256>"`, or `"sha256/<sha256>"` without a prefix.
   */
  public objectKey(sha256: string): string {
    return this.prefix === "" ? `sha256/${sha256}` : `${this.prefix}/sha256/${sha256}`;
  }

  /**
   * Stores one image unless the bucket already has it, and returns its reference.
   *
   * @param data - PNG, JPEG, GIF, or WebP bytes, at most `maxImageBytes`.
   * @returns The reference.
   * @throws {ImageValidationError} When the bytes are not a valid image for `buildImageReference`. Nothing is uploaded.
   * @throws {S3ServiceException} When S3 refuses the request, such as for missing credentials or permissions.
   * Retrying is safe.
   */
  public async putImage(data: Uint8Array): Promise<AgentImageReference> {
    const reference = buildImageReference(data, this.storeUri);
    const input = {
      Bucket: this.bucket,
      Key: this.objectKey(reference.sha256),
      Body: data,
      ContentType: reference.mimeType,
    };
    if (this.supportsConditionalWrites) {
      try {
        await this.client.send(new PutObjectCommand({ ...input, IfNoneMatch: "*" }));
        return reference;
      } catch (error) {
        if (hasErrorCode(error, alreadyExistsErrorCodes)) {
          return reference;
        }
        if (!hasErrorCode(error, conditionalWriteUnsupportedErrorCodes)) {
          throw error;
        }
        this.supportsConditionalWrites = false;
      }
    }
    if (!(await this.hasObject(input.Key))) {
      await this.client.send(new PutObjectCommand(input));
    }
    return reference;
  }

  /**
   * Returns the bytes of one image in this store.
   *
   * @param reference - A reference under this store's bucket and prefix.
   * @returns The exact bytes, checked with `verifyImageBytes`.
   * @throws {ImageValidationError} When the reference's fields are invalid.
   * @throws {MediaStoreError} When the reference is outside this store's bucket and prefix, the object is missing, or
   * its bytes do not match.
   * @throws {S3ServiceException} When S3 refuses the request for another reason.
   */
  public async getImage(reference: AgentImageReference): Promise<Uint8Array> {
    requireStoreImageReference(reference, this.storeUri);
    let response;
    try {
      response = await this.client.send(
        new GetObjectCommand({ Bucket: this.bucket, Key: this.objectKey(reference.sha256) }),
      );
    } catch (error) {
      if (hasErrorCode(error, notFoundErrorCodes)) {
        throw new MediaStoreError(`image ${reference.uri} is not in the media store`, { cause: error });
      }
      throw error;
    }
    const mismatch = (): MediaStoreError =>
      new MediaStoreError(`image ${reference.uri} does not match its sha256 and size`);
    const body: unknown = response.Body;
    if (body === undefined) {
      throw new MediaStoreError(`image ${reference.uri} has no body in the media store`);
    }
    if (response.ContentLength !== undefined && response.ContentLength !== reference.sizeBytes) {
      discardBody(body);
      throw mismatch();
    }
    // One byte more than expected tells an oversized body apart without reading all of it.
    const data = await readAtMost(body, reference.sizeBytes + 1);
    if (data.length > reference.sizeBytes) {
      throw mismatch();
    }
    return verifyImageBytes(reference, data);
  }

  /** Closes the store's S3 connections. Do not use the store afterwards. */
  public close(): void {
    this.client.destroy();
  }

  private async hasObject(key: string): Promise<boolean> {
    try {
      await this.client.send(new HeadObjectCommand({ Bucket: this.bucket, Key: key }));
      return true;
    } catch (error) {
      if (hasErrorCode(error, notFoundErrorCodes)) {
        return false;
      }
      throw error;
    }
  }
}

/** Reads a response body up to `limit` bytes, then stops and discards the rest. */
async function readAtMost(body: unknown, limit: number): Promise<Uint8Array> {
  if (isAsyncIterable(body)) {
    const chunks: Uint8Array[] = [];
    let total = 0;
    // Leaving the loop early destroys a Node stream, so the rest is never downloaded.
    for await (const chunk of body) {
      const bytes = typeof chunk === "string" ? Buffer.from(chunk) : (chunk as Uint8Array);
      chunks.push(bytes.subarray(0, Math.max(0, limit - total)));
      total += bytes.length;
      if (total >= limit) {
        break;
      }
    }
    return new Uint8Array(Buffer.concat(chunks));
  }
  const bytes = await (body as { transformToByteArray(): Promise<Uint8Array> }).transformToByteArray();
  return bytes.subarray(0, limit);
}

function isAsyncIterable(value: unknown): value is AsyncIterable<unknown> {
  return typeof value === "object" && value !== null && Symbol.asyncIterator in value;
}

function discardBody(body: unknown): void {
  const destroyable = body as { destroy?: () => void };
  if (typeof destroyable.destroy === "function") {
    destroyable.destroy();
  }
}

function hasErrorCode(error: unknown, codes: ReadonlySet<string>): boolean {
  return (
    error instanceof S3ServiceException &&
    (codes.has(error.name) || codes.has(String(error.$metadata.httpStatusCode ?? "")))
  );
}
