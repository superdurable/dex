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
  requireStoreImageReference,
  verifyImageBytes,
  type MediaStore,
} from "../agent-media.js";
import type { AgentImageReference } from "../agent-session-values.js";

const notFoundErrorNames: ReadonlySet<string> = new Set(["NotFound", "NoSuchKey"]);

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
}

/**
 * Keeps image bytes in an S3 bucket, one object per distinct image.
 *
 * The object key is `<prefix>/sha256/<sha256>`, and a reference's `uri` is `s3://<bucket>/<prefix>/sha256/<sha256>`.
 * `putImage` checks whether the object exists and uploads it only when it does not, with the image's `Content-Type`,
 * so a retried upload writes nothing new. `getImage` reads only objects under this store's bucket and prefix and
 * checks the bytes against the reference. The AWS SDK reads credentials from the standard AWS chain: environment
 * variables, the shared credentials file, or an instance or task role. Never put credentials in session settings or
 * Flow input.
 *
 * Session deletion does not delete objects. Expire old objects with a bucket lifecycle rule on the prefix. Call
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
 * ```
 * @since 0.5.0
 */
export class S3MediaStore implements MediaStore {
  /** The location that every reference of this store starts with: `"s3://<bucket>/<prefix>"`, or without a prefix `"s3://<bucket>"`. */
  public readonly storeUri: string;
  private readonly bucket: string;
  private readonly prefix: string;
  private readonly client: S3Client;

  /**
   * Creates a store; it makes no request until the first upload or read.
   *
   * @param options - The bucket, prefix, and connection settings.
   * @throws {TypeError} When `options.bucket` is empty or contains `/`.
   */
  public constructor(options: S3MediaStoreOptions) {
    if (options.bucket === "" || options.bucket.includes("/")) {
      throw new TypeError("S3MediaStore bucket must be non-empty and contain no /");
    }
    this.bucket = options.bucket;
    this.prefix = (options.prefix ?? "").replace(/^\/+|\/+$/g, "");
    this.storeUri = this.prefix === "" ? `s3://${this.bucket}` : `s3://${this.bucket}/${this.prefix}`;
    this.client = new S3Client({
      ...(options.endpoint === undefined ? {} : { endpoint: options.endpoint }),
      ...(options.region === undefined ? {} : { region: options.region }),
      forcePathStyle: options.forcePathStyle ?? false,
    });
  }

  /**
   * Creates a store from `s3://<bucket>` or `s3://<bucket>/<prefix>`.
   *
   * @param storeUri - The bucket and optional prefix as an S3 URI.
   * @param options - The connection settings.
   * @returns The store.
   * @throws {TypeError} When `storeUri` does not start with `s3://` or names no bucket.
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
   * @throws {ImageValidationError} When the bytes are empty, too large, or of another type. Nothing is uploaded.
   * @throws {S3ServiceException} When S3 refuses the request, such as for missing credentials or permissions.
   * Retrying is safe.
   */
  public async putImage(data: Uint8Array): Promise<AgentImageReference> {
    const reference = buildImageReference(data, this.storeUri);
    const key = this.objectKey(reference.sha256);
    if (!(await this.hasObject(key))) {
      await this.client.send(
        new PutObjectCommand({ Bucket: this.bucket, Key: key, Body: data, ContentType: reference.mimeType }),
      );
    }
    return reference;
  }

  /**
   * Returns the bytes of one image in this store.
   *
   * @param reference - A reference under this store's bucket and prefix.
   * @returns The exact bytes, checked against `sha256` and `sizeBytes`.
   * @throws {ImageValidationError} When the reference's fields are invalid.
   * @throws {MediaStoreError} When the reference is outside this store's bucket and prefix, the object is missing, or
   * its bytes do not match.
   * @throws {S3ServiceException} When S3 refuses the request for another reason.
   */
  public async getImage(reference: AgentImageReference): Promise<Uint8Array> {
    requireStoreImageReference(reference, this.storeUri);
    let body: { transformToByteArray(): Promise<Uint8Array> } | undefined;
    try {
      const response = await this.client.send(
        new GetObjectCommand({ Bucket: this.bucket, Key: this.objectKey(reference.sha256) }),
      );
      body = response.Body;
    } catch (error) {
      if (isNotFound(error)) {
        throw new MediaStoreError(`image ${reference.uri} is not in the media store`, { cause: error });
      }
      throw error;
    }
    if (body === undefined) {
      throw new MediaStoreError(`image ${reference.uri} has no body in the media store`);
    }
    return verifyImageBytes(reference, await body.transformToByteArray());
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
      if (isNotFound(error)) {
        return false;
      }
      throw error;
    }
  }
}

function isNotFound(error: unknown): boolean {
  return (
    error instanceof S3ServiceException &&
    (notFoundErrorNames.has(error.name) || error.$metadata.httpStatusCode === 404)
  );
}
