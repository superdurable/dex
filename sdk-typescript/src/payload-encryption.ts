// Copyright (c) 2026 Super Durable, Inc.
//
// Licensed under the Sustainable Use License 1.0.
// You may not use this file except in compliance with the License.
// See the LICENSE file in the repository root.
//
// SPDX-License-Identifier: LicenseRef-Sustainable-Use-1.0

import { createCipheriv, createDecipheriv, randomBytes } from "node:crypto";
import { readFile, stat } from "node:fs/promises";
import { promisify } from "node:util";
import { gunzip, gzip } from "node:zlib";

import { PayloadEncryptionError } from "./errors.js";
import { PayloadCipher, PayloadCompression, type PayloadEnvelope } from "./gen/dex.js";

const gzipAsync = promisify(gzip);
const gunzipAsync = promisify(gunzip);

/** The object encoding of an encrypted Value: Dex `PayloadEnvelope` format, version 1. */
export const payloadEnvelopeMarker = "dex/1";
/** Every object encoding with this prefix is reserved for Dex. */
export const reservedPayloadEncodingPrefix = "dex/";
/** The prefix of an encrypted string field such as an error detail. */
export const encodedTextPrefix = "dex-encoded:";

/** Default limit of an envelope's serialized size: 16 MiB plus 64 KiB. */
export const defaultMaxPayloadEnvelopeBytes = 16_842_752;
/** Default limit of a decrypted or decompressed plaintext: 64 MiB. */
export const defaultMaxPayloadPlaintextBytes = 67_108_864;

const aesGcmKeyBytes = 32;
const aesGcmNonceBytes = 12;
const aesGcmTagBytes = 16;
const maxKeyIdBytes = 64;

/**
 * Identifies the Flow that stores a batch of payloads.
 *
 * Every {@link PayloadCodec} and {@link PayloadEncryptionKeyProvider} call receives one. A key
 * provider can use it to choose keys, for example one key per tenant. The SDK always passes the
 * Flow the values are stored in. SubFlow input uses the child Flow's type and its real Flow ID,
 * `SubFlow:{parentFlowId}-{stepExecutionId}-{index}`, so a tenant-aware provider must handle the
 * `SubFlow:` prefix.
 *
 * The context is not part of the ciphertext. Clone, fork, and time travel copy values into a new
 * run or a new Flow ID, and those copies must still decrypt.
 *
 * `flowType` may be the empty string. The SDK passes it empty when a call names only a Flow ID,
 * such as `Client.waitForFlow`, `Client.getAttributes`, `Client.stopFlow`, and a SubFlow result
 * read by its parent. The same value can therefore arrive with and without its Flow type. A key
 * provider should select and validate keys by `flowId`, and use `flowType` only as a hint.
 *
 * @example
 * ```ts
 * const provider: PayloadEncryptionKeyProvider = {
 *   async currentKeyId(context: PayloadContext): Promise<string> {
 *     return context.flowId.startsWith("tenant-b-") ? "tenant-b-k1" : "tenant-a-k1";
 *   },
 *   async key(context: PayloadContext, keyId: string): Promise<Uint8Array> {
 *     return loadTenantKey(context.flowId, keyId);
 *   },
 * };
 * ```
 * @since 1.45.16
 */
export interface PayloadContext {
  /** The Flow type that stores the payloads, or the empty string when the SDK knows only the Flow ID. */
  readonly flowType: string;
  /** The Flow ID that stores the payloads. */
  readonly flowId: string;
}

/**
 * Encrypts serialized Dex Values into envelopes and decrypts them again.
 *
 * Configure one codec as `ClientOptions.payloadCodec` and `WorkerOptions.payloadCodec`. Use the
 * built-in {@link AesGcmPayloadCodec}, or implement this interface for another scheme, such as
 * one that calls a KMS. Codecs work in batches so that a KMS codec can make one network call for
 * many values.
 *
 * A custom codec must set `cipher` to `PayloadCipher.PAYLOAD_CIPHER_CUSTOM`. It must
 * authenticate every `metadata` entry that affects decoding, or reject metadata it does not
 * authenticate. The SDK checks the envelope header, key ID, and metadata limits before it calls
 * `decode`. After `decode`, it checks the plaintext size and that the plaintext is a Dex Value.
 *
 * @example
 * ```ts
 * class KmsPayloadCodec implements PayloadCodec {
 *   public async encode(context: PayloadContext, plaintexts: readonly Uint8Array[]): Promise<PayloadEnvelope[]> {
 *     const sealed = await kms.encryptMany(context.flowId, plaintexts);
 *     return sealed.map((data) => PayloadEnvelope.create({
 *       cipher: PayloadCipher.PAYLOAD_CIPHER_CUSTOM,
 *       keyId: "kms-1",
 *       data,
 *     }));
 *   }
 *
 *   public async decode(
 *     context: PayloadContext,
 *     envelopes: readonly PayloadEnvelope[],
 *     options?: PayloadCodecDecodeOptions,
 *   ): Promise<Uint8Array[]> {
 *     return kms.decryptMany(context.flowId, envelopes.map((envelope) => envelope.data), {
 *       maxOutputBytes: options?.maxPlaintextBytes,
 *     });
 *   }
 * }
 * ```
 * @since 1.45.16
 */
export interface PayloadCodec {
  /**
   * Encrypts serialized Values.
   * @param context - The Flow that stores the values.
   * @param plaintexts - Serialized Dex Values, each a protobuf `Value` message.
   * @returns Exactly one envelope per plaintext, in order.
   * @throws {@link PayloadEncryptionError} or any other error to fail the whole batch. The SDK
   * then fails the operation that wrote the values with a new {@link PayloadEncryptionError}. That
   * error keeps only a known category, or `codec_failed`, and drops the original message and cause.
   */
  encode(context: PayloadContext, plaintexts: readonly Uint8Array[]): Promise<PayloadEnvelope[]>;

  /**
   * Decrypts envelopes.
   *
   * A codec that decompresses must stop as soon as its output exceeds
   * `options.maxPlaintextBytes`, and must not decompress the whole input first. This stops gzip
   * bombs. The SDK checks each returned plaintext against the same limit again.
   * @param context - The Flow that stores the values.
   * @param envelopes - Envelopes that passed the SDK's header checks.
   * @param options - Limits the SDK enforces; see {@link PayloadCodecDecodeOptions}.
   * @returns Exactly one serialized Dex Value per envelope, in order.
   * @throws {@link PayloadEncryptionError} or any other error to fail the whole batch. The SDK
   * replaces it with a new {@link PayloadEncryptionError}. That error keeps only a known category
   * and, when it names one of the batch's envelopes, the key ID. Any other error becomes category
   * `codec_failed`. The original message and cause are dropped.
   */
  decode(
    context: PayloadContext,
    envelopes: readonly PayloadEnvelope[],
    options?: PayloadCodecDecodeOptions,
  ): Promise<Uint8Array[]>;
}

/**
 * Limits that the SDK passes to {@link PayloadCodec.decode}.
 *
 * A custom codec that compresses must bound its decompression by `maxPlaintextBytes`. The example
 * below wraps a KMS client in your application. It gzips before encryption and passes the limit
 * to Node's `maxOutputLength`. That option stops gunzip as soon as the output grows too large.
 *
 * @example
 * ```ts
 * import { promisify } from "node:util";
 * import { gunzip, gzip } from "node:zlib";
 * import {
 *   PayloadCipher,
 *   PayloadCompression,
 *   PayloadEnvelope,
 *   type PayloadCodec,
 *   type PayloadCodecDecodeOptions,
 *   type PayloadContext,
 * } from "@superdurable/dex";
 *
 * // Your application's KMS client: encrypts and decrypts byte arrays for one tenant.
 * interface TenantKms {
 *   encrypt(flowId: string, plaintext: Uint8Array): Promise<Uint8Array>;
 *   decrypt(flowId: string, ciphertext: Uint8Array): Promise<Uint8Array>;
 * }
 *
 * const gzipAsync = promisify(gzip);
 * const gunzipAsync = promisify(gunzip);
 * const defaultMaxPlaintextBytes = 64 * 1024 * 1024;
 *
 * export class GzipKmsPayloadCodec implements PayloadCodec {
 *   public constructor(private readonly kms: TenantKms) {}
 *
 *   public async encode(context: PayloadContext, plaintexts: readonly Uint8Array[]): Promise<PayloadEnvelope[]> {
 *     return Promise.all(plaintexts.map(async (plaintext) => PayloadEnvelope.create({
 *       cipher: PayloadCipher.PAYLOAD_CIPHER_CUSTOM,
 *       keyId: "kms-1",
 *       compression: PayloadCompression.PAYLOAD_COMPRESSION_GZIP,
 *       data: await this.kms.encrypt(context.flowId, await gzipAsync(plaintext)),
 *     })));
 *   }
 *
 *   public async decode(
 *     context: PayloadContext,
 *     envelopes: readonly PayloadEnvelope[],
 *     options?: PayloadCodecDecodeOptions,
 *   ): Promise<Uint8Array[]> {
 *     const maxOutputLength = options?.maxPlaintextBytes ?? defaultMaxPlaintextBytes;
 *     return Promise.all(envelopes.map(async (envelope) => {
 *       const compressed = await this.kms.decrypt(context.flowId, envelope.data);
 *       return new Uint8Array(await gunzipAsync(compressed, { maxOutputLength }));
 *     }));
 *   }
 * }
 * ```
 * @since 1.45.16
 */
export interface PayloadCodecDecodeOptions {
  /**
   * Largest plaintext the SDK accepts, in bytes.
   *
   * It is the Client's or Worker's `maxPayloadPlaintextBytes`. Stop decompressing once the output
   * exceeds it.
   */
  readonly maxPlaintextBytes: number;
}

/**
 * Supplies the AES-256 keys of an {@link AesGcmPayloadCodec}.
 *
 * Use {@link FilePayloadEncryptionKeyProvider} for a local key file, or implement this interface
 * to read keys from a secret manager. Key IDs are visible in every envelope, so they must not
 * contain sensitive data.
 *
 * A multi-tenant provider should refuse, in `key`, a key ID that does not belong to the
 * context's tenant. Ciphertext is not bound to a Flow, so otherwise a value copied from another
 * tenant's Flow would decrypt. A refusal makes decryption fail with category `unknown_key`.
 *
 * @example
 * ```ts
 * const provider: PayloadEncryptionKeyProvider = {
 *   async currentKeyId(): Promise<string> {
 *     return "k2";
 *   },
 *   async key(_context: PayloadContext, keyId: string): Promise<Uint8Array> {
 *     const key = await secrets.read(`dex-payload-key-${keyId}`);
 *     if (key === undefined) throw new Error(`unknown key ID ${keyId}`);
 *     return key;
 *   },
 * };
 * const codec = new AesGcmPayloadCodec(provider);
 * ```
 * @since 1.45.16
 */
export interface PayloadEncryptionKeyProvider {
  /**
   * Chooses the key for new encryptions.
   * @param context - The Flow that stores the values.
   * @returns A key ID of 1 to 64 printable ASCII bytes from `!` to `~`.
   * @throws Any error to fail the encryption with category `unknown_key`. The codec discards the
   * thrown error and its message.
   */
  currentKeyId(context: PayloadContext): Promise<string>;

  /**
   * Returns the key for a key ID.
   * @param context - The Flow that stores the values.
   * @param keyId - The key ID from an envelope, or from `currentKeyId`.
   * @returns The 32-byte AES-256 key.
   * @throws Any error when the key ID is unknown or refused for this context. Decryption then
   * fails with category `unknown_key`. The codec discards the thrown error and its message, because
   * provider text could contain key material; the new error names only the category and key ID.
   */
  key(context: PayloadContext, keyId: string): Promise<Uint8Array>;
}

/**
 * Configures an {@link AesGcmPayloadCodec}.
 *
 * @example
 * ```ts
 * const codec = new AesGcmPayloadCodec(provider, {
 *   compression: PayloadCompression.PAYLOAD_COMPRESSION_GZIP,
 * });
 * ```
 * @since 1.45.16
 */
export interface AesGcmPayloadCodecOptions {
  /**
   * Compression applied before encryption. Defaults to `PayloadCompression.PAYLOAD_COMPRESSION_NONE`.
   *
   * With `PAYLOAD_COMPRESSION_GZIP`, the codec gzips each value and keeps the gzip form only when
   * it is smaller. Do not enable compression for values that mix attacker-controlled content with
   * secrets. Ciphertext length reveals how well the content compresses, as in the CRIME and BREACH
   * attacks. Decoding always follows each envelope's own compression.
   */
  readonly compression?: PayloadCompression;
  /**
   * Largest decrypted or decompressed plaintext, in bytes. Defaults to 67,108,864 (64 MiB).
   *
   * Decompression stops as soon as its output exceeds the limit, which also stops gzip bombs.
   */
  readonly maxDecompressedBytes?: number;
  /**
   * Largest `PayloadEnvelope.data`, in bytes. Defaults to 16,842,752 (16 MiB plus 64 KiB).
   *
   * Raise it together with `maxPayloadEnvelopeBytes` of the Client and Worker, for a Server whose
   * maximum Value size is larger than the default 16 MiB.
   */
  readonly maxDataBytes?: number;
}

/**
 * The built-in payload codec: AES-256-GCM with optional gzip compression.
 *
 * Each encryption uses a fresh random 12-byte nonce. `data` is the nonce, the ciphertext, and
 * the 16-byte tag. The additional authenticated data binds the format version, cipher,
 * compression, and key ID to the ciphertext, so the Server cannot change them. Encrypt at most
 * 2^32 values with one key, and rotate keys regularly: point the key provider's current key ID at
 * a new key and keep old keys so existing envelopes still decrypt.
 *
 * Keys stay in memory. Errors name only the category and key ID.
 *
 * @example
 * ```ts
 * const provider = await FilePayloadEncryptionKeyProvider.fromFile("/etc/dex/payload-keys.json");
 * const payloadCodec = new AesGcmPayloadCodec(provider);
 * const client = new Client(registry, cache, { payloadCodec });
 * const worker = new Worker(registry, cache, { payloadCodec });
 * ```
 * @since 1.45.16
 */
export class AesGcmPayloadCodec implements PayloadCodec {
  private readonly compression: PayloadCompression;
  private readonly maxDecompressedBytes: number;
  private readonly maxDataBytes: number;

  /**
   * Creates the codec.
   * @param keyProvider - Supplies the current key ID and the key for each key ID.
   * @param options - Compression and size limits; omitted uses no compression and the default limits.
   * @throws {TypeError} When `options.compression` is not a known `PayloadCompression`.
   * @throws {RangeError} When a size limit is not a positive safe integer.
   */
  public constructor(
    private readonly keyProvider: PayloadEncryptionKeyProvider,
    options: AesGcmPayloadCodecOptions = {},
  ) {
    const compression = options.compression ?? PayloadCompression.PAYLOAD_COMPRESSION_NONE;
    if (
      compression !== PayloadCompression.PAYLOAD_COMPRESSION_NONE
      && compression !== PayloadCompression.PAYLOAD_COMPRESSION_GZIP
    ) {
      throw new TypeError(`AesGcmPayloadCodec does not support compression ${String(compression)}`);
    }
    this.compression = compression;
    this.maxDecompressedBytes = requirePositiveByteLimit(
      "maxDecompressedBytes",
      options.maxDecompressedBytes ?? defaultMaxPayloadPlaintextBytes,
    );
    this.maxDataBytes = requirePositiveByteLimit(
      "maxDataBytes",
      options.maxDataBytes ?? defaultMaxPayloadEnvelopeBytes,
    );
  }

  /**
   * Encrypts serialized Values with the provider's current key.
   * @param context - The Flow that stores the values.
   * @param plaintexts - Serialized Dex Values.
   * @returns One `PAYLOAD_CIPHER_AES256_GCM` envelope per plaintext, in order.
   * @throws {@link PayloadEncryptionError} When the key ID or key is invalid, or an envelope would
   * exceed `maxDataBytes`.
   */
  public async encode(
    context: PayloadContext,
    plaintexts: readonly Uint8Array[],
  ): Promise<PayloadEnvelope[]> {
    if (plaintexts.length === 0) {
      return [];
    }
    let keyId: string;
    try {
      keyId = await this.keyProvider.currentKeyId(context);
    } catch {
      // Provider text can hold key material, so neither its message nor the error itself is kept.
      throw new PayloadEncryptionError("encode", "unknown_key", "the key provider failed to choose the current key ID");
    }
    requireValidKeyId(keyId, "encode");
    const key = await this.requireKey(context, keyId, "encode");
    const envelopes: PayloadEnvelope[] = [];
    for (const plaintext of plaintexts) {
      if (plaintext.length > this.maxDecompressedBytes) {
        throw new PayloadEncryptionError(
          "encode",
          "limit_exceeded",
          `a ${plaintext.length}-byte plaintext exceeds maxDecompressedBytes ${this.maxDecompressedBytes}`,
          keyId,
        );
      }
      let compression = PayloadCompression.PAYLOAD_COMPRESSION_NONE;
      let sealedInput: Uint8Array = plaintext;
      if (this.compression === PayloadCompression.PAYLOAD_COMPRESSION_GZIP) {
        const compressed = await gzipAsync(plaintext);
        if (compressed.length < plaintext.length) {
          compression = PayloadCompression.PAYLOAD_COMPRESSION_GZIP;
          sealedInput = compressed;
        }
      }
      const nonce = randomBytes(aesGcmNonceBytes);
      const cipher = createCipheriv("aes-256-gcm", key, nonce, { authTagLength: aesGcmTagBytes });
      cipher.setAAD(payloadEnvelopeAad(PayloadCipher.PAYLOAD_CIPHER_AES256_GCM, compression, keyId));
      const ciphertext = Buffer.concat([cipher.update(sealedInput), cipher.final()]);
      const data = Buffer.concat([nonce, ciphertext, cipher.getAuthTag()]);
      if (data.length > this.maxDataBytes) {
        throw new PayloadEncryptionError(
          "encode",
          "limit_exceeded",
          `a ${data.length}-byte envelope data exceeds maxDataBytes ${this.maxDataBytes}`,
          keyId,
        );
      }
      envelopes.push({
        cipher: PayloadCipher.PAYLOAD_CIPHER_AES256_GCM,
        keyId,
        data: new Uint8Array(data.buffer, data.byteOffset, data.byteLength),
        compression,
        metadata: {},
      });
    }
    return envelopes;
  }

  /**
   * Decrypts AES-256-GCM envelopes.
   * @param context - The Flow that stores the values.
   * @param envelopes - Envelopes whose header the SDK already checked.
   * @param options - SDK limits; decompression stops at the smaller of
   * `options.maxPlaintextBytes` and this codec's `maxDecompressedBytes`.
   * @returns One serialized Dex Value per envelope, in order.
   * @throws {@link PayloadEncryptionError} With the category of the first failing check: the
   * cipher, metadata, compression, key, data size, authentication tag, or plaintext size.
   */
  public async decode(
    context: PayloadContext,
    envelopes: readonly PayloadEnvelope[],
    options?: PayloadCodecDecodeOptions,
  ): Promise<Uint8Array[]> {
    const requestedLimit = options?.maxPlaintextBytes;
    const maxPlaintextBytes = typeof requestedLimit === "number" && Number.isSafeInteger(requestedLimit) && requestedLimit > 0
      ? Math.min(requestedLimit, this.maxDecompressedBytes)
      : this.maxDecompressedBytes;
    const keys = new Map<string, Uint8Array>();
    const plaintexts: Uint8Array[] = [];
    for (const envelope of envelopes) {
      const keyId = envelope.keyId;
      // A direct caller can pass any key ID, so error text names only a valid one.
      const shownKeyId = isValidKeyId(keyId) ? keyId : undefined;
      if (envelope.cipher !== PayloadCipher.PAYLOAD_CIPHER_AES256_GCM) {
        throw new PayloadEncryptionError(
          "decode",
          "unsupported_cipher",
          "AesGcmPayloadCodec supports only PAYLOAD_CIPHER_AES256_GCM",
          shownKeyId,
        );
      }
      if (Object.keys(envelope.metadata).length > 0) {
        throw new PayloadEncryptionError(
          "decode",
          "malformed",
          "an AES-256-GCM envelope must not have metadata",
          shownKeyId,
        );
      }
      if (
        envelope.compression !== PayloadCompression.PAYLOAD_COMPRESSION_NONE
        && envelope.compression !== PayloadCompression.PAYLOAD_COMPRESSION_GZIP
      ) {
        throw new PayloadEncryptionError(
          "decode",
          "unsupported_compression",
          "the envelope's compression is unknown",
          shownKeyId,
        );
      }
      let key = keys.get(keyId);
      if (key === undefined) {
        key = await this.requireKey(context, keyId, "decode");
        keys.set(keyId, key);
      }
      const data = envelope.data;
      if (data.length < aesGcmNonceBytes + aesGcmTagBytes) {
        throw new PayloadEncryptionError(
          "decode",
          "malformed",
          `envelope data has ${data.length} bytes, fewer than the ${aesGcmNonceBytes + aesGcmTagBytes}-byte minimum`,
          shownKeyId,
        );
      }
      if (data.length > this.maxDataBytes) {
        throw new PayloadEncryptionError(
          "decode",
          "limit_exceeded",
          `envelope data has ${data.length} bytes, more than maxDataBytes ${this.maxDataBytes}`,
          shownKeyId,
        );
      }
      let decrypted: Buffer;
      try {
        const decipher = createDecipheriv(
          "aes-256-gcm",
          key,
          data.subarray(0, aesGcmNonceBytes),
          { authTagLength: aesGcmTagBytes },
        );
        decipher.setAAD(payloadEnvelopeAad(envelope.cipher, envelope.compression, keyId));
        decipher.setAuthTag(data.subarray(data.length - aesGcmTagBytes));
        decrypted = Buffer.concat([
          decipher.update(data.subarray(aesGcmNonceBytes, data.length - aesGcmTagBytes)),
          decipher.final(),
        ]);
      } catch {
        throw new PayloadEncryptionError(
          "decode",
          "authentication_failed",
          "AES-256-GCM authentication failed",
          shownKeyId,
        );
      }
      plaintexts.push(await decompressPlaintext(decrypted, envelope.compression, shownKeyId, maxPlaintextBytes));
    }
    return plaintexts;
  }

  private async requireKey(
    context: PayloadContext,
    keyId: string,
    operation: "encode" | "decode",
  ): Promise<Uint8Array> {
    const shownKeyId = isValidKeyId(keyId) ? keyId : undefined;
    let key: Uint8Array | undefined;
    try {
      const provided: unknown = await this.keyProvider.key(context, keyId);
      // The copy reads internal slots, so a hostile key object cannot run code after this block.
      key = provided instanceof Uint8Array ? new Uint8Array(provided) : undefined;
    } catch {
      // Provider text can hold key material, so neither its message nor the error itself is kept.
      throw new PayloadEncryptionError(operation, "unknown_key", "the key provider refused the key ID", shownKeyId);
    }
    if (key === undefined || key.length !== aesGcmKeyBytes) {
      throw new PayloadEncryptionError(
        operation,
        "unknown_key",
        `the key provider returned a key that is not ${aesGcmKeyBytes} bytes`,
        shownKeyId,
      );
    }
    return key;
  }
}

/**
 * Reads AES-256 payload encryption keys from a JSON key file.
 *
 * The file has this format, shared by dexcli and the example codec servers:
 *
 * ```json
 * {
 *   "current_key_id": "k2",
 *   "keys": {
 *     "k1": "<standard base64 of 32 bytes>",
 *     "k2": "<standard base64 of 32 bytes>"
 *   }
 * }
 * ```
 *
 * `current_key_id` names the key for new encryptions and must appear in `keys`. Each key is
 * standard padded base64 of exactly 32 bytes. Keep old keys after a rotation so that envelopes
 * that name them still decrypt. Key IDs are 1 to 64 printable ASCII bytes from `!` to `~`.
 *
 * The provider reads the file once, when {@link FilePayloadEncryptionKeyProvider.fromFile} runs.
 * Restart the process to pick up a rotated file. It warns on the console when the file is
 * readable or writable by group or other users; create it with mode `0600`. It ignores the
 * {@link PayloadContext} in `key`, so values cloned into another Flow ID still decrypt.
 *
 * @example
 * ```ts
 * const provider = await FilePayloadEncryptionKeyProvider.fromFile("/etc/dex/payload-keys.json");
 * const payloadCodec = new AesGcmPayloadCodec(provider);
 * ```
 * @since 1.45.16
 */
export class FilePayloadEncryptionKeyProvider implements PayloadEncryptionKeyProvider {
  private constructor(
    private readonly currentKey: string,
    private readonly keys: ReadonlyMap<string, Uint8Array>,
  ) {}

  /**
   * Loads a key file.
   * @param path - Path of the JSON key file.
   * @returns A provider that holds the file's keys in memory.
   * @throws {Error} When the file cannot be read, is not valid JSON, has an invalid key ID, has a
   * key that is not standard padded base64 of 32 bytes, or names a current key that it does not
   * contain. The message never contains key bytes.
   */
  public static async fromFile(path: string): Promise<FilePayloadEncryptionKeyProvider> {
    const contents = await readFile(path, "utf8");
    if (process.platform !== "win32") {
      const mode = (await stat(path)).mode;
      if ((mode & 0o077) !== 0) {
        console.warn(
          `payload encryption key file ${path} is accessible by other users (mode ${(mode & 0o777).toString(8)}); `
            + "restrict it to mode 0600",
        );
      }
    }
    let parsed: unknown;
    try {
      parsed = JSON.parse(contents);
    } catch {
      throw new Error(`payload encryption key file ${path} is not valid JSON`);
    }
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) {
      throw new Error(`payload encryption key file ${path} must contain a JSON object`);
    }
    const { current_key_id: currentKeyId, keys } = parsed as Record<string, unknown>;
    if (typeof currentKeyId !== "string") {
      throw new Error(`payload encryption key file ${path} must set current_key_id to a string`);
    }
    if (typeof keys !== "object" || keys === null || Array.isArray(keys)) {
      throw new Error(`payload encryption key file ${path} must set keys to a JSON object`);
    }
    const decodedKeys = new Map<string, Uint8Array>();
    for (const [keyId, encodedKey] of Object.entries(keys)) {
      if (!isValidKeyId(keyId)) {
        throw new Error(
          `payload encryption key file ${path} has an invalid key ID ${JSON.stringify(keyId)}; `
            + "use 1 to 64 printable ASCII characters without spaces",
        );
      }
      const key = typeof encodedKey === "string" ? decodeCanonicalBase64(encodedKey) : undefined;
      if (key === undefined || key.length !== aesGcmKeyBytes) {
        throw new Error(
          `payload encryption key file ${path} key ${keyId} must be standard padded base64 of ${aesGcmKeyBytes} bytes`,
        );
      }
      decodedKeys.set(keyId, key);
    }
    if (!decodedKeys.has(currentKeyId)) {
      throw new Error(
        `payload encryption key file ${path} current_key_id ${JSON.stringify(currentKeyId)} is not in keys`,
      );
    }
    return new FilePayloadEncryptionKeyProvider(currentKeyId, decodedKeys);
  }

  /**
   * Returns the file's `current_key_id`.
   * @param _context - Ignored; every Flow uses the same current key.
   * @returns The current key ID.
   */
  public async currentKeyId(_context: PayloadContext): Promise<string> {
    return this.currentKey;
  }

  /**
   * Returns a copy of the key for a key ID.
   * @param _context - Ignored, so values cloned into another Flow ID still decrypt.
   * @param keyId - A key ID from the file.
   * @returns The 32-byte key.
   * @throws {@link PayloadEncryptionError} With category `unknown_key` when the file has no such key.
   */
  public async key(_context: PayloadContext, keyId: string): Promise<Uint8Array> {
    const key = this.keys.get(keyId);
    if (key === undefined) {
      throw new PayloadEncryptionError(
        "decode",
        "unknown_key",
        "the key ID is not in the key file",
        isValidKeyId(keyId) ? keyId : undefined,
      );
    }
    return Uint8Array.from(key);
  }
}

/**
 * Builds the additional authenticated data of a `dex/1` envelope.
 * @returns `"dex/1" 0x00 <cipher> 0x00 <compression> 0x00 <keyId>` with decimal enum numbers.
 */
export function payloadEnvelopeAad(
  cipher: PayloadCipher,
  compression: PayloadCompression,
  keyId: string,
): Buffer {
  return Buffer.from(`${payloadEnvelopeMarker}\0${cipher}\0${compression}\0${keyId}`, "latin1");
}

/**
 * Reports whether a key ID has 1 to 64 bytes, each printable ASCII from `!` to `~`.
 * @returns Whether the key ID is valid.
 */
export function isValidKeyId(keyId: string): boolean {
  if (keyId.length === 0 || keyId.length > maxKeyIdBytes) {
    return false;
  }
  for (let index = 0; index < keyId.length; index += 1) {
    const code = keyId.charCodeAt(index);
    if (code < 0x21 || code > 0x7e) {
      return false;
    }
  }
  return true;
}

/**
 * Decodes standard padded base64 that re-encodes to the identical text.
 * @returns The decoded bytes, or `undefined` when the text is not canonical base64.
 */
export function decodeCanonicalBase64(text: string): Uint8Array | undefined {
  const decoded = Buffer.from(text, "base64");
  if (decoded.toString("base64") !== text) {
    return undefined;
  }
  return new Uint8Array(decoded.buffer, decoded.byteOffset, decoded.byteLength);
}

/** Runs check 13: the plaintext, or its gzip output, stays within `maxPlaintextBytes`. */
async function decompressPlaintext(
  decrypted: Buffer,
  compression: PayloadCompression,
  keyId: string | undefined,
  maxPlaintextBytes: number,
): Promise<Uint8Array> {
  if (compression === PayloadCompression.PAYLOAD_COMPRESSION_NONE) {
    if (decrypted.length > maxPlaintextBytes) {
      throw new PayloadEncryptionError(
        "decode",
        "limit_exceeded",
        `a ${decrypted.length}-byte plaintext exceeds the ${maxPlaintextBytes}-byte limit`,
        keyId,
      );
    }
    return new Uint8Array(decrypted.buffer, decrypted.byteOffset, decrypted.byteLength);
  }
  let decompressed: Buffer;
  try {
    decompressed = await gunzipAsync(decrypted, { maxOutputLength: maxPlaintextBytes });
  } catch (failure) {
    if (failure instanceof RangeError) {
      throw new PayloadEncryptionError(
        "decode",
        "limit_exceeded",
        `gzip output exceeds the ${maxPlaintextBytes}-byte limit`,
        keyId,
      );
    }
    throw new PayloadEncryptionError("decode", "malformed", "gzip data is invalid", keyId);
  }
  return new Uint8Array(decompressed.buffer, decompressed.byteOffset, decompressed.byteLength);
}

function requireValidKeyId(keyId: string, operation: "encode" | "decode"): void {
  if (typeof keyId !== "string" || !isValidKeyId(keyId)) {
    throw new PayloadEncryptionError(
      operation,
      "malformed",
      "key ID must have 1 to 64 printable ASCII characters without spaces",
    );
  }
}

function requirePositiveByteLimit(name: string, value: number): number {
  if (!Number.isSafeInteger(value) || value <= 0) {
    throw new RangeError(`${name} must be a positive safe integer`);
  }
  return value;
}
